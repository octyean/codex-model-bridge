package server

import (
	"context"
	"encoding/json"
	"errors"
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
	"codex-bridge/internal/incidentlog"
	"codex-bridge/internal/optimization"
	"codex-bridge/internal/providers"
	"codex-bridge/internal/requestdump"
	"codex-bridge/internal/toollog"
	"codex-bridge/internal/tools"
	"codex-bridge/internal/transcript"
)

type Server struct {
	cfg       *config.Config
	providers map[string]providers.ChatProvider
	runtime   capabilities.Runtime
	logger    *slog.Logger
	optimizer *optimization.Tracker
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
		optimizer: optimization.NewTracker(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.health)
	mux.HandleFunc("/v1", s.v1)
	mux.HandleFunc("/v1/models", s.models)
	mux.HandleFunc("/v1/responses", s.responses)
	return mux
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": "0.4.2"})
}

func (s *Server) v1(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"object":  "codex_bridge",
		"version": "0.4.2",
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
		Data:   s.modelList(),
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
	requestSummary := incidentlog.RequestSummary(req.Raw)
	workspace := incidentlog.CodexWorkspace(req.Raw, r.Header)
	sessionID := incidentlog.CodexSessionID(req.Raw, r.Header)
	toollog.RememberRequestSession(requestID, sessionID, req.Model, modelCfg.UpstreamModel, profileName, requestSummary)
	defer toollog.ForgetRequestSession(requestID)
	s.writeSessionLog(sessionID, "codex-requests.jsonl", map[string]any{
		"event":           "codex_request",
		"request_id":      requestID,
		"model":           req.Model,
		"upstream_model":  modelCfg.UpstreamModel,
		"profile":         profileName,
		"headers":         incidentlog.Headers(r.Header),
		"request_summary": requestSummary,
		"body":            req.Raw,
	})
	dumpPath := ""
	if shouldForwardResponses(s.cfg.UpstreamProtocol(modelCfg, providerCfg), adapter) {
		responsesProvider, ok := provider.(providers.ResponsesProvider)
		if !ok {
			message := "provider does not support responses protocol: " + modelCfg.Provider
			s.writeBridgeFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, profileName, http.StatusInternalServerError, message, nil)
			writeError(w, http.StatusInternalServerError, message)
			return
		}
		s.forwardResponses(w, r, requestID, sessionID, req, modelCfg, responsesProvider, adapter, dumpPath)
		return
	}

	transcriptResult, err := transcript.ToChatMessagesWithRuntime(r.Context(), req, adapter, s.runtime, transcript.LogContext{
		RequestID:       requestID,
		Model:           req.Model,
		UpstreamModel:   modelCfg.UpstreamModel,
		Profile:         profileName,
		InputModalities: effectiveInputModalities(modelCfg, adapter),
	})
	if err != nil {
		s.writeBridgeFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, profileName, http.StatusBadRequest, err.Error(), nil)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	chatTools, toolCtx := tools.FromCodex(req.Tools, adapter)
	toolCtx.Workspace = workspace
	chatTools = append(chatTools, tools.FromAdditionalTools(transcriptResult.Items, adapter, &toolCtx)...)
	chatTools = tools.AddReadFileProxy(chatTools, &toolCtx)
	chatTools = tools.AddListFilesProxy(chatTools, &toolCtx)
	chatTools = tools.AddFileSearchProxy(chatTools, &toolCtx)
	chatTools = filterUnavailableRuntimeTools(chatTools, &toolCtx, transcriptResult.Messages)
	if s.runtime.HasSearch() && tools.HasWebSearch(req.Tools) {
		chatTools = tools.AddWebSearchProxy(chatTools, &toolCtx)
	}
	if s.writeForcedLocalToolChoice(w, requestID, req, toolCtx, adapter) {
		return
	}
	toolChoice := tools.ToolChoice(req.ToolChoice, toolCtx)
	if note := tools.SoftRequiredToolChoiceNote(toolChoice, adapter.ToolPolicy().RequiredToolChoice); note != "" {
		transcriptResult.Messages = append(transcriptResult.Messages, providers.ChatMessage{Role: "system", Content: note})
	}
	chatTools, toolChoice = tools.ApplyToolChoice(chatTools, toolChoice, adapter.ToolPolicy().RequiredToolChoice)
	responseFormat := responseFormatFromText(req.Raw)
	chatReq := providers.ChatCompletionRequest{
		Model:          modelCfg.UpstreamModel,
		Messages:       structuredOutputMessages(transcriptResult.Messages, responseFormat),
		Tools:          chatTools,
		ToolChoice:     toolChoice,
		ResponseFormat: responseFormat,
		Stream:         req.Stream,
	}
	if req.ParallelToolCalls && !toolCtx.IsEmpty() {
		enabled := !toolCtx.HasFileWriteTool() && !s.hasInternalTools(toolCtx)
		chatReq.ParallelToolCalls = &enabled
	}
	chatReq = adapter.PrepareChatRequest(chatReq)
	shape := optimization.CaptureShape(chatReq)
	stats := providers.ChatRequestStats(chatReq)
	preparedRequest := providers.PreparedChatRequest(chatReq)
	requestBodyHash := requestdump.Hash(preparedRequest)
	promptExtra := map[string]any{
		"message_count":     stats.MessageCount,
		"tool_count":        stats.ToolCount,
		"body_bytes":        stats.BodyBytes,
		"estimated_tokens":  stats.EstimatedTokens,
		"tool_choice":       toolChoice,
		"parallel_tools":    chatReq.ParallelToolCalls,
		"response_format":   chatReq.ResponseFormat,
		"stream":            chatReq.Stream,
		"prefix_hash":       shape.PrefixHash,
		"system_hash":       shape.SystemHash,
		"tools_hash":        shape.ToolsHash,
		"request_body_hash": requestBodyHash,
	}

	s.logger.Info("request_started",
		slog.String("request_id", requestID),
		slog.String("model", req.Model),
		slog.String("profile", profileName),
		slog.Bool("stream", req.Stream),
	)
	s.logger.Info("upstream_request_prepared",
		slog.String("request_id", requestID),
		slog.String("model", req.Model),
		slog.String("profile", profileName),
		slog.Int("message_count", stats.MessageCount),
		slog.Int("tool_count", stats.ToolCount),
		slog.Int("body_bytes", stats.BodyBytes),
		slog.Int("estimated_tokens", stats.EstimatedTokens),
		slog.String("prefix_hash", shape.PrefixHash),
	)
	if path, err := requestdump.Write(requestID, req.Model, profileName, preparedRequest); err != nil {
		s.logger.Warn("upstream_request_dump_failed",
			slog.String("request_id", requestID),
			slog.String("error", err.Error()),
			slog.String("env", requestdump.EnvPath),
		)
	} else if path != "" {
		dumpPath = path
		promptExtra["upstream_request_dump"] = path
		s.logger.Info("upstream_request_dumped",
			slog.String("request_id", requestID),
			slog.String("path", path),
			slog.String("body_hash", requestBodyHash),
		)
	}
	s.writePromptRequest(sessionID, requestID, req.Model, modelCfg.UpstreamModel, profileName, "initial", preparedRequest, promptExtra)
	s.writeToolCatalog(sessionID, requestID, req.Model, modelCfg.UpstreamModel, profileName, chatTools, toolCtx, toolChoice)

	if req.Stream {
		if s.hasInternalTools(toolCtx) {
			s.streamInternalToolResponse(w, r, requestID, sessionID, req, chatReq, provider, toolCtx, adapter, profileName, shape, dumpPath)
			return
		}
		s.streamResponses(w, r, requestID, sessionID, req, chatReq, provider, toolCtx, adapter, profileName, shape, dumpPath)
		return
	}
	resp, err := provider.Create(r.Context(), chatReq)
	if err != nil {
		s.logger.Error("upstream_failed", slog.String("request_id", requestID), slog.String("error", err.Error()))
		extra := map[string]any{"stream": false}
		s.writePromptFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, profileName, "initial", err.Error(), extra)
		s.writeBridgeFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, profileName, http.StatusBadGateway, err.Error(), extra)
		incidentlog.Write("upstream_error", s.incidentRecord(r, req, requestID, profileName, dumpPath, map[string]any{"error": err.Error(), "stream": false}))
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if len(resp.Choices) == 0 {
		message := "upstream returned no choices"
		extra := map[string]any{"stream": false}
		s.writePromptFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, profileName, "initial", message, extra)
		s.writeBridgeFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, profileName, http.StatusBadGateway, message, extra)
		incidentlog.Write("empty_chat_choices", s.incidentRecord(r, req, requestID, profileName, dumpPath, map[string]any{"stream": false}))
		writeError(w, http.StatusBadGateway, message)
		return
	}
	s.writePromptResponse(sessionID, requestID, req.Model, modelCfg.UpstreamModel, profileName, "initial", resp, nil)
	logCtx := toollog.OutputContext{
		RequestID:      requestID,
		Model:          req.Model,
		UpstreamModel:  modelCfg.UpstreamModel,
		Profile:        profileName,
		RequestSummary: requestSummary,
	}
	if followUp, followUpReq, ok := s.resolveInternalTools(r.Context(), provider, sessionID, chatReq, resp.Choices[0].Message, toolCtx, adapter, logCtx); ok {
		resp = followUp
		shape = optimization.CaptureShape(followUpReq)
		if len(resp.Choices) == 0 {
			message := "upstream returned no choices"
			extra := map[string]any{"stream": false, "after_internal_tool": true}
			s.writeBridgeFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, profileName, http.StatusBadGateway, message, extra)
			incidentlog.Write("empty_chat_choices", s.incidentRecord(r, req, requestID, profileName, dumpPath, map[string]any{"stream": false, "after_internal_tool": true}))
			writeError(w, http.StatusBadGateway, message)
			return
		}
	}
	if contentOnlyNeedsRetry(resp.Choices[0].Message, toolCtx) {
		retryReq := contentOnlyRetryRequest(chatReq, resp.Choices[0].Message)
		shape = optimization.CaptureShape(retryReq)
		s.writePromptRequest(sessionID, requestID, req.Model, retryReq.Model, profileName, "content_only_retry", providers.PreparedChatRequest(retryReq), map[string]any{"stream": false})
		retryResp, err := provider.Create(r.Context(), retryReq)
		if err != nil {
			s.logger.Error("content_only_retry_failed", slog.String("request_id", requestID), slog.String("error", err.Error()))
			extra := map[string]any{"stream": false, "stage": "content_only_retry"}
			s.writePromptFailure(sessionID, requestID, req.Model, retryReq.Model, profileName, "content_only_retry", err.Error(), extra)
			s.writeBridgeFailure(sessionID, requestID, req.Model, retryReq.Model, profileName, http.StatusBadGateway, err.Error(), extra)
			incidentlog.Write("content_only_retry_error", s.incidentRecord(r, req, requestID, profileName, dumpPath, map[string]any{"error": err.Error(), "stream": false}))
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		s.writePromptResponse(sessionID, requestID, req.Model, retryReq.Model, profileName, "content_only_retry", retryResp, nil)
		if len(retryResp.Choices) > 0 {
			resp = retryResp
		}
	}
	items := responseItemsFromMessage(r.Context(), resp.Choices[0].Message, toolCtx, adapter, requestID, req.Model, profileName, s.logger, s.localToolResultResolver(logCtx, toolCtx))
	items = enforceStructuredOutput(items, chatReq.ResponseFormat)
	usage := providers.NormalizeUsage(resp.Usage)
	if emptyOutput(items) {
		incidentlog.Write("empty_chat_response", s.incidentRecord(r, req, requestID, profileName, dumpPath, map[string]any{"stream": false, "output": outputSummary(items, usage)}))
	}
	s.logUsage(requestID, req.Model, profileName, adapter, shape, usage)
	responseObject := codex.ResponseObject{
		ID:        responseID(resp.ID),
		Object:    "response",
		CreatedAt: time.Now().Unix(),
		Model:     req.Model,
		Status:    "completed",
		Output:    items,
		Usage:     codexUsage(usage),
	}
	s.writeBridgeResponse(sessionID, requestID, req.Model, modelCfg.UpstreamModel, profileName, responseObject, map[string]any{"stream": false})
	writeJSON(w, http.StatusOK, responseObject)
}

func effectiveInputModalities(modelCfg config.ModelConfig, adapter adapters.Adapter) []string {
	if len(modelCfg.InputModalities) > 0 {
		return adapters.NormalizeInputModalities(modelCfg.InputModalities)
	}
	return adapters.NormalizeInputModalities(adapter.Capabilities().InputModalities)
}

func shouldForwardResponses(protocol string, adapter adapters.Adapter) bool {
	return protocol == "responses" && adapter.Name() == adapters.OpenAIName
}

func (s *Server) forwardResponses(w http.ResponseWriter, r *http.Request, requestID string, sessionID string, req codex.ResponsesRequest, modelCfg config.ModelConfig, provider providers.ResponsesProvider, adapter adapters.Adapter, dumpPath string) {
	upstreamReq := cloneResponseRequest(req.Raw)
	upstreamReq["model"] = modelCfg.UpstreamModel
	upstreamReq = adapter.PrepareResponseRequest(upstreamReq)
	requestBodyHash := requestdump.Hash(upstreamReq)
	promptExtra := map[string]any{
		"stream":            req.Stream,
		"request_body_hash": requestBodyHash,
	}
	if path, err := requestdump.Write(requestID, req.Model, adapter.Name(), upstreamReq); err != nil {
		s.logger.Warn("upstream_request_dump_failed",
			slog.String("request_id", requestID),
			slog.String("error", err.Error()),
			slog.String("env", requestdump.EnvPath),
		)
	} else if path != "" {
		dumpPath = path
		promptExtra["upstream_request_dump"] = path
		s.logger.Info("upstream_request_dumped",
			slog.String("request_id", requestID),
			slog.String("path", path),
			slog.String("body_hash", requestBodyHash),
		)
	}
	s.writePromptRequest(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), "native_responses", upstreamReq, promptExtra)
	if req.Stream {
		stream, err := provider.StreamResponse(r.Context(), upstreamReq)
		if err != nil {
			if requestCanceled(r, err) {
				return
			}
			s.logger.Error("upstream_response_stream_failed", slog.String("request_id", requestID), slog.String("error", err.Error()))
			extra := map[string]any{"stream": true}
			s.writePromptFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), "native_responses", err.Error(), extra)
			s.writeBridgeFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), http.StatusBadGateway, err.Error(), extra)
			incidentlog.Write("upstream_response_stream_error", s.incidentRecord(r, req, requestID, adapter.Name(), dumpPath, map[string]any{"error": err.Error(), "stream": true}))
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writer := codex.NewSSEWriter(w)
		streamSeq := 0
		for event := range stream {
			if event.Err != nil {
				if requestCanceled(r, event.Err) {
					return
				}
				extra := map[string]any{"stream": true}
				s.writePromptFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), "native_responses", event.Err.Error(), extra)
				s.writeBridgeFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), http.StatusBadGateway, event.Err.Error(), extra)
				incidentlog.Write("upstream_response_stream_event_error", s.incidentRecord(r, req, requestID, adapter.Name(), dumpPath, map[string]any{"error": event.Err.Error(), "stream": true}))
				_ = writer.Event(map[string]any{
					"type": "response.failed",
					"response": map[string]any{
						"error": map[string]any{"message": event.Err.Error(), "type": "server_error"},
					},
				})
				return
			}
			if event.Done {
				s.writePromptResponse(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), "native_responses", map[string]any{
					"stream":      true,
					"event_count": streamSeq,
				}, nil)
				return
			}
			streamSeq++
			s.writePromptStreamEvent(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), "native_responses", streamSeq, event.Data)
			replaceResponseModel(event.Data, req.Model)
			_ = writer.Event(event.Data)
		}
		return
	}
	resp, err := provider.CreateResponse(r.Context(), upstreamReq)
	if err != nil {
		s.logger.Error("upstream_response_failed", slog.String("request_id", requestID), slog.String("error", err.Error()))
		extra := map[string]any{"stream": false}
		s.writePromptFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), "native_responses", err.Error(), extra)
		s.writeBridgeFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), http.StatusBadGateway, err.Error(), extra)
		incidentlog.Write("upstream_response_error", s.incidentRecord(r, req, requestID, adapter.Name(), dumpPath, map[string]any{"error": err.Error(), "stream": false}))
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	replaceResponseModel(resp, req.Model)
	s.writePromptResponse(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), "native_responses", resp, nil)
	if nativeResponseEmpty(resp) {
		incidentlog.Write("empty_native_response", s.incidentRecord(r, req, requestID, adapter.Name(), dumpPath, map[string]any{"stream": false}))
	}
	s.writeBridgeResponse(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), resp, map[string]any{"stream": false, "native_responses": true})
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) streamResponses(w http.ResponseWriter, r *http.Request, requestID string, sessionID string, req codex.ResponsesRequest, chatReq providers.ChatCompletionRequest, provider providers.ChatProvider, toolCtx tools.Context, adapter adapters.Adapter, profile string, shape optimization.Shape, dumpPath string) {
	writer := codex.NewSSEWriter(w)
	respID := "resp_" + requestID
	startedAt := time.Now()
	createdAt := time.Now().Unix()
	_ = writer.Event(responseCreatedEvent(respID, createdAt, req.Model))
	_ = writer.Event(responseInProgressEvent(respID, createdAt, req.Model))

	type streamResult struct {
		stream <-chan providers.StreamEvent
		err    error
	}
	streamReady := make(chan streamResult, 1)
	go func() {
		stream, err := provider.Stream(r.Context(), chatReq)
		streamReady <- streamResult{stream: stream, err: err}
	}()
	var stream <-chan providers.StreamEvent
	heartbeat := time.NewTicker(3 * time.Second)
	defer heartbeat.Stop()
	select {
	case result := <-streamReady:
		if result.err != nil {
			if requestCanceled(r, result.err) {
				return
			}
			s.logger.Error("upstream_stream_failed", slog.String("request_id", requestID), slog.String("error", result.err.Error()))
			extra := map[string]any{"stream": true}
			s.writePromptFailure(sessionID, requestID, req.Model, chatReq.Model, profile, "initial", result.err.Error(), extra)
			s.writeBridgeFailure(sessionID, requestID, req.Model, chatReq.Model, profile, http.StatusBadGateway, result.err.Error(), extra)
			incidentlog.Write("upstream_stream_error", s.incidentRecord(r, req, requestID, profile, dumpPath, map[string]any{"error": result.err.Error(), "stream": true}))
			_ = writer.Event(map[string]any{
				"type": "response.failed",
				"response": map[string]any{
					"id":    respID,
					"error": map[string]any{"message": result.err.Error(), "type": "server_error"},
				},
			})
			return
		}
		stream = result.stream
		s.logger.Info("upstream_stream_opened",
			slog.String("request_id", requestID),
			slog.Int64("elapsed_ms", time.Since(startedAt).Milliseconds()),
		)
	case <-r.Context().Done():
		s.logger.Warn("request_canceled_before_upstream_stream", slog.String("request_id", requestID), slog.String("error", r.Context().Err().Error()))
		return
	case <-heartbeat.C:
		_ = writer.Event(responseInProgressEvent(respID, createdAt, req.Model))
		for {
			select {
			case result := <-streamReady:
				if result.err != nil {
					if requestCanceled(r, result.err) {
						return
					}
					s.logger.Error("upstream_stream_failed", slog.String("request_id", requestID), slog.String("error", result.err.Error()))
					extra := map[string]any{"stream": true}
					s.writePromptFailure(sessionID, requestID, req.Model, chatReq.Model, profile, "initial", result.err.Error(), extra)
					s.writeBridgeFailure(sessionID, requestID, req.Model, chatReq.Model, profile, http.StatusBadGateway, result.err.Error(), extra)
					incidentlog.Write("upstream_stream_error", s.incidentRecord(r, req, requestID, profile, dumpPath, map[string]any{"error": result.err.Error(), "stream": true}))
					_ = writer.Event(map[string]any{
						"type": "response.failed",
						"response": map[string]any{
							"id":    respID,
							"error": map[string]any{"message": result.err.Error(), "type": "server_error"},
						},
					})
					return
				}
				stream = result.stream
				s.logger.Info("upstream_stream_opened",
					slog.String("request_id", requestID),
					slog.Int64("elapsed_ms", time.Since(startedAt).Milliseconds()),
				)
				goto streamOpened
			case <-r.Context().Done():
				s.logger.Warn("request_canceled_before_upstream_stream", slog.String("request_id", requestID), slog.String("error", r.Context().Err().Error()))
				return
			case <-heartbeat.C:
				_ = writer.Event(responseInProgressEvent(respID, createdAt, req.Model))
			}
		}
	}

streamOpened:
	logCtx := toollog.OutputContext{
		RequestID:      requestID,
		Model:          req.Model,
		UpstreamModel:  chatReq.Model,
		Profile:        profile,
		RequestSummary: incidentlog.RequestSummary(req.Raw),
	}
	state := newStreamState(r.Context(), toolCtx, adapter, requestID, req.Model, profile, s.logger, s.localToolResultResolver(logCtx, toolCtx))
	emitStreamEvents := toolCtx.IsEmpty() && !titleOnlyResponseFormat(chatReq.ResponseFormat)
	var usage providers.NormalizedUsage
	firstChunk := true
	streamSeq := 0
streamLoop:
	for {
		select {
		case event, ok := <-stream:
			if !ok {
				break streamLoop
			}
			if event.Err != nil {
				if requestCanceled(r, event.Err) {
					return
				}
				extra := map[string]any{"stream": true}
				s.writePromptFailure(sessionID, requestID, req.Model, chatReq.Model, profile, "initial", event.Err.Error(), extra)
				s.writeBridgeFailure(sessionID, requestID, req.Model, chatReq.Model, profile, http.StatusBadGateway, event.Err.Error(), extra)
				incidentlog.Write("upstream_stream_event_error", s.incidentRecord(r, req, requestID, profile, dumpPath, map[string]any{"error": event.Err.Error(), "stream": true}))
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
				break streamLoop
			}
			streamSeq++
			s.writePromptStreamEvent(sessionID, requestID, req.Model, chatReq.Model, profile, "initial", streamSeq, event.Chunk)
			if firstChunk {
				firstChunk = false
				s.logger.Info("upstream_stream_first_chunk",
					slog.String("request_id", requestID),
					slog.Int64("elapsed_ms", time.Since(startedAt).Milliseconds()),
				)
			}
			if event.Chunk.Usage != nil {
				usage = providers.NormalizeUsage(event.Chunk.Usage)
			}
			for _, out := range state.AddChunk(event.Chunk) {
				if !emitStreamEvents {
					continue
				}
				_ = writer.Event(out)
				state.eventsEmitted = true
			}
		case <-r.Context().Done():
			s.logger.Warn("request_canceled_during_upstream_stream", slog.String("request_id", requestID), slog.String("error", r.Context().Err().Error()))
			return
		case <-heartbeat.C:
			_ = writer.Event(responseInProgressEvent(respID, createdAt, req.Model))
		}
	}
	s.writePromptResponse(sessionID, requestID, req.Model, chatReq.Model, profile, "initial", map[string]any{
		"stream":      true,
		"chunk_count": streamSeq,
		"message":     chatMessageFromStreamState(state),
		"usage":       usage,
	}, nil)
	if contentOnlyNeedsRetry(chatMessageFromStreamState(state), toolCtx) {
		retryReq := contentOnlyRetryRequest(chatReq, chatMessageFromStreamState(state))
		shape = optimization.CaptureShape(retryReq)
		s.writePromptRequest(sessionID, requestID, req.Model, retryReq.Model, profile, "content_only_retry", providers.PreparedChatRequest(retryReq), map[string]any{"stream": true})
		retryState, retryUsage, err := s.streamVisibleMessage(r, writer, respID, createdAt, retryReq, provider, toolCtx, adapter, requestID, sessionID, req.Model, profile, "content_only_retry", false, s.localToolResultResolver(logCtx, toolCtx), false)
		if err != nil {
			if requestCanceled(r, err) {
				return
			}
			extra := map[string]any{"stream": true, "stage": "content_only_retry"}
			s.writeBridgeFailure(sessionID, requestID, req.Model, retryReq.Model, profile, http.StatusBadGateway, err.Error(), extra)
			incidentlog.Write("content_only_retry_error", s.incidentRecord(r, req, requestID, profile, dumpPath, map[string]any{"error": err.Error(), "stream": true}))
			_ = writer.Event(map[string]any{
				"type": "response.failed",
				"response": map[string]any{
					"id":    respID,
					"error": map[string]any{"message": err.Error(), "type": "server_error"},
				},
			})
			return
		}
		usage = addUsage(usage, retryUsage)
		state = retryState
	}
	items := state.Done()
	items = enforceStructuredOutput(items, chatReq.ResponseFormat)
	if emptyOutput(items) {
		incidentlog.Write("empty_stream_response", s.incidentRecord(r, req, requestID, profile, dumpPath, map[string]any{"stream": true, "output": outputSummary(items, usage)}))
	}
	for i, item := range items {
		alreadyAdded := state.eventsEmitted && ((item["id"] == state.textItemID && state.textAdded) || (item["id"] == state.reasoningItemID && state.reasoningAdded))
		for _, event := range outputDoneEvents(item, i, alreadyAdded) {
			_ = writer.Event(event)
		}
	}
	responseCompleted := map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id": respID, "object": "response", "created_at": time.Now().Unix(), "model": req.Model, "status": "completed", "output": items,
			"usage": codexUsage(usage),
		},
	}
	_ = writer.Event(responseCompleted)
	s.writeBridgeResponse(sessionID, requestID, req.Model, chatReq.Model, profile, responseCompleted["response"], map[string]any{"stream": true})
	s.logUsage(requestID, req.Model, profile, adapter, shape, usage)
	s.logger.Info("request_completed", slog.String("request_id", requestID), slog.String("status", "completed"), slog.Int("tool_call_count", state.ToolCallCount()))
}

func requestCanceled(r *http.Request, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	ctxErr := r.Context().Err()
	return ctxErr != nil && (errors.Is(err, ctxErr) || errors.Is(ctxErr, context.Canceled) || errors.Is(ctxErr, context.DeadlineExceeded))
}

func (s *Server) incidentRecord(r *http.Request, req codex.ResponsesRequest, requestID string, profile string, dumpPath string, extra map[string]any) map[string]any {
	record := map[string]any{
		"request_id":      requestID,
		"model":           req.Model,
		"profile":         profile,
		"headers":         incidentlog.Headers(r.Header),
		"request_summary": incidentlog.RequestSummary(req.Raw),
		"tool_names":      responseToolNames(req.Tools),
		"tool_choice":     req.ToolChoice,
	}
	if dumpPath != "" {
		record["upstream_request_dump"] = dumpPath
	}
	for key, value := range extra {
		record[key] = value
	}
	return record
}

func responseToolNames(responseTools []codex.ResponseTool) []string {
	names := make([]string, 0, len(responseTools))
	for _, tool := range responseTools {
		if tool.Name != "" {
			names = append(names, tool.Name)
			continue
		}
		if tool.Raw != nil {
			if name, ok := tool.Raw["name"].(string); ok && name != "" {
				names = append(names, name)
			}
		}
	}
	return names
}

func emptyOutput(items []codex.ResponseItem) bool {
	if len(items) == 0 {
		return true
	}
	for _, item := range items {
		switch item["type"] {
		case "message":
			if strings.TrimSpace(messageOutputText(item)) != "" {
				return false
			}
		case "reasoning":
			if text, _ := item["reasoning_content"].(string); strings.TrimSpace(text) != "" {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func outputSummary(items []codex.ResponseItem, usage providers.NormalizedUsage) map[string]any {
	summary := map[string]any{
		"item_count":      len(items),
		"has_text":        false,
		"text_chars":      0,
		"reasoning_chars": 0,
		"tool_call_count": 0,
		"usage":           usage,
	}
	for _, item := range items {
		switch item["type"] {
		case "message":
			text := messageOutputText(item)
			if text != "" {
				summary["has_text"] = true
				summary["text_chars"] = summary["text_chars"].(int) + len([]rune(text))
			}
		case "reasoning":
			text, _ := item["reasoning_content"].(string)
			summary["reasoning_chars"] = summary["reasoning_chars"].(int) + len([]rune(text))
		default:
			summary["tool_call_count"] = summary["tool_call_count"].(int) + 1
		}
	}
	return summary
}

func nativeResponseEmpty(resp map[string]any) bool {
	output, ok := resp["output"].([]any)
	return ok && len(output) == 0
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

func responseFormatFromText(raw map[string]any) any {
	text, ok := raw["text"].(map[string]any)
	if !ok {
		return nil
	}
	format, ok := text["format"].(map[string]any)
	if !ok || format["type"] != "json_schema" || format["schema"] == nil {
		return nil
	}
	name, _ := format["name"].(string)
	if name == "" {
		name = "codex_output_schema"
	}
	jsonSchema := map[string]any{
		"name":   name,
		"schema": format["schema"],
	}
	if strict, ok := format["strict"]; ok {
		jsonSchema["strict"] = strict
	}
	return map[string]any{
		"type":        "json_schema",
		"json_schema": jsonSchema,
	}
}

const structuredOutputNote = `CHAT_STRUCTURED_OUTPUT
The final assistant content must be one valid JSON object matching the requested response_format schema.
Use exactly the JSON property names defined in the schema. Do not invent or rename keys.
Do not include markdown, code fences, explanations, metadata, or any text outside the JSON object.`

func structuredOutputMessages(messages []providers.ChatMessage, responseFormat any) []providers.ChatMessage {
	if responseFormat == nil || hasStructuredOutputNote(messages) {
		return messages
	}
	return append([]providers.ChatMessage{{Role: "system", Content: structuredOutputInstruction(responseFormat)}}, messages...)
}

func structuredOutputInstruction(responseFormat any) string {
	schema := responseFormatSchema(responseFormat)
	if schema == "" {
		return structuredOutputNote
	}
	return structuredOutputNote + "\nJSON schema:\n" + schema
}

func responseFormatSchema(responseFormat any) string {
	format, ok := responseFormat.(map[string]any)
	if !ok {
		return ""
	}
	jsonSchema, ok := format["json_schema"].(map[string]any)
	if !ok {
		return ""
	}
	data, err := json.Marshal(jsonSchema["schema"])
	if err != nil {
		return ""
	}
	return string(data)
}

func enforceStructuredOutput(items []codex.ResponseItem, responseFormat any) []codex.ResponseItem {
	if !titleOnlyResponseFormat(responseFormat) {
		return items
	}
	for _, item := range items {
		if item["type"] != "message" {
			continue
		}
		text := strings.TrimSpace(messageOutputText(item))
		if text == "" {
			return items
		}
		if title, ok := titleFromJSON(text); ok {
			setMessageOutputText(item, titleJSON(title))
			return items
		}
		setMessageOutputText(item, titleJSON(text))
		return items
	}
	return items
}

func titleOnlyResponseFormat(responseFormat any) bool {
	format, ok := responseFormat.(map[string]any)
	if !ok {
		return false
	}
	jsonSchema, ok := format["json_schema"].(map[string]any)
	if !ok {
		return false
	}
	schema, ok := jsonSchema["schema"].(map[string]any)
	if !ok || schema["type"] != "object" {
		return false
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok || len(properties) != 1 {
		return false
	}
	_, ok = properties["title"]
	return ok && requiresTitle(schema["required"])
}

func requiresTitle(value any) bool {
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if item == "title" {
			return true
		}
	}
	return false
}

func titleFromJSON(text string) (string, bool) {
	var obj map[string]string
	if err := json.Unmarshal([]byte(text), &obj); err != nil {
		return "", false
	}
	title := cleanTitle(obj["title"])
	return title, title != ""
}

func titleJSON(title string) string {
	data, _ := json.Marshal(map[string]string{"title": cleanTitle(title)})
	return string(data)
}

func cleanTitle(title string) string {
	title = strings.TrimSpace(title)
	title = strings.TrimPrefix(title, "```json")
	title = strings.TrimPrefix(title, "```")
	title = strings.TrimSuffix(strings.TrimSpace(title), "```")
	title = strings.TrimSpace(title)
	var obj map[string]string
	if err := json.Unmarshal([]byte(title), &obj); err == nil {
		if inner := strings.TrimSpace(obj["title"]); inner != "" {
			return inner
		}
	}
	return title
}

func setMessageOutputText(item codex.ResponseItem, text string) {
	content, _ := item["content"].([]map[string]string)
	if len(content) == 0 {
		return
	}
	content[0]["text"] = text
}

func hasStructuredOutputNote(messages []providers.ChatMessage) bool {
	for _, message := range messages {
		if message.Role != "system" {
			continue
		}
		if text, ok := message.Content.(string); ok && strings.Contains(text, "CHAT_STRUCTURED_OUTPUT") {
			return true
		}
	}
	return false
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

func (s *Server) logUsage(requestID string, model string, profile string, adapter adapters.Adapter, shape optimization.Shape, usage providers.NormalizedUsage) {
	if usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.TotalTokens == 0 {
		return
	}
	attrs := []slog.Attr{
		slog.String("request_id", requestID),
		slog.String("model", model),
		slog.String("profile", profile),
		slog.Int("input_tokens", usage.InputTokens),
		slog.Int("cached_input_tokens", usage.CachedInputTokens),
		slog.Int("fresh_input_tokens", usage.FreshInputTokens),
		slog.Int("output_tokens", usage.OutputTokens),
		slog.Int("reasoning_tokens", usage.ReasoningTokens),
		slog.Int("total_tokens", usage.TotalTokens),
	}
	if adapters.OptimizationOptions(adapter).CacheDiagnostics {
		diagnostics := s.optimizer.Observe(model+"|"+profile, shape, usage)
		attrs = append(attrs, optimization.LogAttrs(diagnostics)...)
	}
	s.logger.LogAttrs(context.Background(), slog.LevelInfo, "upstream_usage", attrs...)
}
