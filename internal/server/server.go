package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/capabilities"
	"codex-bridge/internal/codex"
	"codex-bridge/internal/config"
	extcap "codex-bridge/internal/extensions/capabilities"
	"codex-bridge/internal/providers"
	"codex-bridge/internal/tools"
	"codex-bridge/internal/transcript"
)

type Server struct {
	cfg       *config.Config
	providers map[string]providers.ChatProvider
	runtime   capabilities.Runtime
	logger    *slog.Logger
}

func New(cfg *config.Config, providerClients map[string]providers.ChatProvider, logger *slog.Logger) http.Handler {
	return NewWithRuntime(cfg, providerClients, extcap.NewRuntime(cfg), logger)
}

func NewWithRuntime(cfg *config.Config, providerClients map[string]providers.ChatProvider, runtime capabilities.Runtime, logger *slog.Logger) http.Handler {
	s := &Server{
		cfg:       cfg,
		providers: providerClients,
		runtime:   runtime,
		logger:    logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.health)
	mux.HandleFunc("/v1", s.v1)
	mux.HandleFunc("/v1/models", s.models)
	mux.HandleFunc("/v1/responses", s.responses)
	return mux
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": "0.2.1"})
}

func (s *Server) v1(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"object":  "codex_bridge",
		"version": "0.2.1",
		"routes":  []string{"/v1/responses", "/v1/models"},
	})
}

func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, providers.ModelsResponse{
		Object: "list",
		Data:   s.modelList(r.Context()),
	})
}

func (s *Server) responses(w http.ResponseWriter, r *http.Request) {
	requestID := fmt.Sprintf("req_%d", time.Now().UnixNano())
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req codex.ResponsesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request json")
		return
	}
	modelCfg, ok := s.cfg.Model(req.Model)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown model: "+req.Model)
		return
	}
	providerCfg, ok := s.cfg.Provider(modelCfg.Provider)
	if !ok {
		writeError(w, http.StatusInternalServerError, "provider is not configured: "+modelCfg.Provider)
		return
	}
	profileName := s.cfg.ProfileName(modelCfg, providerCfg)
	adapter := adapters.Get(profileName)
	provider, ok := s.providers[modelCfg.Provider]
	if !ok {
		writeError(w, http.StatusInternalServerError, "provider is not available: "+modelCfg.Provider)
		return
	}
	if s.cfg.UpstreamProtocol(modelCfg, providerCfg) == "responses" {
		responsesProvider, ok := provider.(providers.ResponsesProvider)
		if !ok {
			writeError(w, http.StatusInternalServerError, "provider does not support responses protocol: "+modelCfg.Provider)
			return
		}
		s.forwardResponses(w, r, requestID, req, modelCfg, responsesProvider)
		return
	}

	transcriptResult, err := transcript.ToChatMessagesWithRuntime(r.Context(), req, adapter, s.runtime)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	chatTools, toolCtx := tools.FromCodex(req.Tools, adapter)
	chatTools = append(chatTools, tools.FromAdditionalTools(transcriptResult.Items, adapter, &toolCtx)...)
	toolChoice := tools.ToolChoice(req.ToolChoice, toolCtx)
	chatReq := providers.ChatCompletionRequest{
		Model:      modelCfg.UpstreamModel,
		Messages:   transcriptResult.Messages,
		Tools:      chatTools,
		ToolChoice: toolChoice,
		Stream:     req.Stream,
	}
	chatReq = s.addInternalTools(req, chatReq)
	if req.ParallelToolCalls && !toolCtx.IsEmpty() {
		enabled := !toolCtx.HasFileWriteTool()
		chatReq.ParallelToolCalls = &enabled
	}
	chatReq = adapter.PrepareChatRequest(chatReq)

	s.logger.Info("request_started",
		slog.String("request_id", requestID),
		slog.String("model", req.Model),
		slog.String("profile", profileName),
		slog.Bool("stream", req.Stream),
	)

	if req.Stream {
		if s.hasInternalTools(req) {
			s.streamInternalToolResponse(w, r, requestID, req, chatReq, provider, toolCtx, adapter)
			return
		}
		s.streamResponses(w, r, requestID, req, chatReq, provider, toolCtx, adapter)
		return
	}
	resp, err := provider.Create(r.Context(), chatReq)
	if err != nil {
		s.logger.Error("upstream_failed", slog.String("request_id", requestID), slog.String("error", err.Error()))
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if len(resp.Choices) == 0 {
		writeError(w, http.StatusBadGateway, "upstream returned no choices")
		return
	}
	if followUp, ok := s.resolveInternalTools(r.Context(), provider, chatReq, resp.Choices[0].Message); ok {
		resp = followUp
		if len(resp.Choices) == 0 {
			writeError(w, http.StatusBadGateway, "upstream returned no choices")
			return
		}
	}
	items := responseItemsFromMessage(resp.Choices[0].Message, toolCtx, adapter, requestID, s.logger)
	usage := providers.NormalizeUsage(resp.Usage)
	logUsage(s.logger, requestID, usage)
	writeJSON(w, http.StatusOK, codex.ResponseObject{
		ID:        responseID(resp.ID),
		Object:    "response",
		CreatedAt: time.Now().Unix(),
		Model:     req.Model,
		Status:    "completed",
		Output:    items,
		Usage:     codexUsage(usage),
	})
}

func (s *Server) forwardResponses(w http.ResponseWriter, r *http.Request, requestID string, req codex.ResponsesRequest, modelCfg config.ModelConfig, provider providers.ResponsesProvider) {
	upstreamReq := cloneResponseRequest(req.Raw)
	upstreamReq["model"] = modelCfg.UpstreamModel
	if req.Stream {
		stream, err := provider.StreamResponse(r.Context(), upstreamReq)
		if err != nil {
			s.logger.Error("upstream_response_stream_failed", slog.String("request_id", requestID), slog.String("error", err.Error()))
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writer := codex.NewSSEWriter(w)
		for event := range stream {
			if event.Err != nil {
				_ = writer.Event(map[string]any{
					"type": "response.failed",
					"response": map[string]any{
						"error": map[string]any{"message": event.Err.Error(), "type": "server_error"},
					},
				})
				return
			}
			if event.Done {
				return
			}
			replaceResponseModel(event.Data, req.Model)
			_ = writer.Event(event.Data)
		}
		return
	}
	resp, err := provider.CreateResponse(r.Context(), upstreamReq)
	if err != nil {
		s.logger.Error("upstream_response_failed", slog.String("request_id", requestID), slog.String("error", err.Error()))
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	replaceResponseModel(resp, req.Model)
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) streamResponses(w http.ResponseWriter, r *http.Request, requestID string, req codex.ResponsesRequest, chatReq providers.ChatCompletionRequest, provider providers.ChatProvider, toolCtx tools.Context, adapter adapters.Adapter) {
	stream, err := provider.Stream(r.Context(), chatReq)
	if err != nil {
		s.logger.Error("upstream_stream_failed", slog.String("request_id", requestID), slog.String("error", err.Error()))
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writer := codex.NewSSEWriter(w)
	respID := "resp_" + requestID
	_ = writer.Event(map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id": respID, "object": "response", "created_at": time.Now().Unix(), "model": req.Model, "status": "in_progress", "output": []any{},
		},
	})
	_ = writer.Event(map[string]any{
		"type": "response.in_progress",
		"response": map[string]any{
			"id": respID, "object": "response", "created_at": time.Now().Unix(), "model": req.Model, "status": "in_progress", "output": []any{},
		},
	})

	state := newStreamState(toolCtx, adapter, requestID, s.logger)
	var usage providers.NormalizedUsage
	for event := range stream {
		if event.Err != nil {
			_ = writer.Event(map[string]any{
				"type": "response.failed",
				"response": map[string]any{
					"id":    respID,
					"error": map[string]any{"message": event.Err.Error(), "type": "server_error"},
				},
			})
			return
		}
		if event.Done {
			break
		}
		if event.Chunk.Usage != nil {
			usage = providers.NormalizeUsage(event.Chunk.Usage)
		}
		for _, out := range state.AddChunk(event.Chunk) {
			_ = writer.Event(out)
		}
	}
	items := state.Done()
	for _, item := range items {
		for _, event := range toolDoneEvents(item) {
			_ = writer.Event(event)
		}
	}
	_ = writer.Event(map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id": respID, "object": "response", "created_at": time.Now().Unix(), "model": req.Model, "status": "completed", "output": items,
			"usage": codexUsage(usage),
		},
	})
	logUsage(s.logger, requestID, usage)
	s.logger.Info("request_completed", slog.String("request_id", requestID), slog.String("status", "completed"), slog.Int("tool_call_count", state.ToolCallCount()))
}

func (s *Server) authorized(r *http.Request) bool {
	want := strings.TrimSpace(s.cfg.Codex.LocalToken)
	if want == "" {
		return true
	}
	got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	return got == want
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, codex.ErrorResponse{
		Error: codex.ErrorBody{Message: message, Type: "invalid_request_error"},
	})
}

func responseID(id string) string {
	if id != "" {
		return id
	}
	return fmt.Sprintf("resp_%d", time.Now().UnixNano())
}

func cloneResponseRequest(raw map[string]any) map[string]any {
	out := make(map[string]any, len(raw))
	for key, value := range raw {
		out[key] = value
	}
	return out
}

func replaceResponseModel(value map[string]any, model string) {
	if _, ok := value["model"]; ok {
		value["model"] = model
	}
	if response, ok := value["response"].(map[string]any); ok {
		if _, exists := response["model"]; exists {
			response["model"] = model
		}
	}
}

func codexUsage(usage providers.NormalizedUsage) map[string]any {
	return map[string]any{
		"input_tokens": usage.InputTokens,
		"input_tokens_details": map[string]any{
			"cached_tokens": usage.CachedInputTokens,
		},
		"output_tokens": usage.OutputTokens,
		"output_tokens_details": map[string]any{
			"reasoning_tokens": usage.ReasoningTokens,
		},
		"total_tokens": usage.TotalTokens,
	}
}

func logUsage(logger *slog.Logger, requestID string, usage providers.NormalizedUsage) {
	if usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.TotalTokens == 0 {
		return
	}
	logger.Info("upstream_usage",
		slog.String("request_id", requestID),
		slog.Int("input_tokens", usage.InputTokens),
		slog.Int("cached_input_tokens", usage.CachedInputTokens),
		slog.Int("fresh_input_tokens", usage.FreshInputTokens),
		slog.Int("output_tokens", usage.OutputTokens),
		slog.Int("reasoning_tokens", usage.ReasoningTokens),
		slog.Int("total_tokens", usage.TotalTokens),
	)
}
