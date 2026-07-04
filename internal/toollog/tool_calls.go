package toollog

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"sync"
	"time"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
	"codex-bridge/internal/diagnostics"
	"codex-bridge/internal/incidentlog"
	"codex-bridge/internal/tools"
)

const EnvToolLogPath = "CODEX_BRIDGE_TOOL_LOG"

var seenToolOutputs sync.Map
var modelToolCalls sync.Map
var requestContexts sync.Map

type requestContext struct {
	SessionID     string
	Model         string
	UpstreamModel string
}

type OutputContext struct {
	RequestID      string
	Model          string
	UpstreamModel  string
	Profile        string
	RequestSummary map[string]any
}

func ToolCall(requestID string, model string, profile string, callID string, entry tools.Entry, rawArguments string, reasoning string) {
	record := map[string]any{
		"time":          time.Now().Format(time.RFC3339Nano),
		"event":         "model_tool_call",
		"request_id":    requestID,
		"model":         model,
		"profile":       profile,
		"call_id":       callID,
		"tool":          entry.Name(),
		"kind":          entry.Kind(),
		"original_type": entry.OriginalType(),
		"raw_arguments": rawArguments,
	}
	if strings.TrimSpace(reasoning) != "" {
		record["reasoning"] = incidentlog.TextSummary(reasoning)
	}
	attachRequestContext(record)
	modelToolCalls.Store(callID, cloneRecord(record))
	appendRecord(record)
}

func RememberRequestSession(requestID string, sessionID string, model string, upstreamModel string, profile string, requestSummary map[string]any) {
	sessionID = strings.TrimSpace(sessionID)
	upstreamModel = strings.TrimSpace(upstreamModel)
	if sessionID == "" && upstreamModel == "" {
		return
	}
	requestContexts.Store(requestID, requestContext{SessionID: sessionID, Model: model, UpstreamModel: upstreamModel})
	path := ConfiguredPath()
	if path == "" || sessionID == "" {
		return
	}
	record := map[string]any{
		"time":             time.Now().Format(time.RFC3339Nano),
		"event":            "request_started",
		"request_id":       requestID,
		"codex_session_id": sessionID,
		"model":            model,
		"upstream_model":   upstreamModel,
		"profile":          profile,
	}
	if len(requestSummary) > 0 {
		record["request_summary"] = requestSummary
	}
	diagnostics.WriteSessionIndex(path, record)
	diagnostics.WriteSessionRecord(path, sessionID, "requests.jsonl", record)
}

func ForgetRequestSession(requestID string) {
	requestContexts.Delete(requestID)
}

func PatchToolCall(requestID string, callID string, entry tools.Entry, rawArguments string, item codex.ResponseItem) {
	if !isPatchWriteKind(entry.Kind()) {
		return
	}
	appendRecord(map[string]any{
		"time":          time.Now().Format(time.RFC3339Nano),
		"request_id":    requestID,
		"call_id":       callID,
		"tool":          entry.Name(),
		"kind":          entry.Kind(),
		"original_type": entry.OriginalType(),
		"raw_arguments": rawArguments,
		"item":          item,
	})
}

func ConfiguredPath() string {
	return strings.TrimSpace(os.Getenv(EnvToolLogPath))
}

func CheckConfiguredPath() (string, error) {
	path := ConfiguredPath()
	if path == "" {
		return "", nil
	}
	return diagnostics.CheckJSONL(path)
}

func BlockedToolRewrite(requestID string, model string, profile string, callID string, entry tools.Entry, rawArguments string, rewrittenArguments string) {
	record := map[string]any{
		"time":                time.Now().Format(time.RFC3339Nano),
		"event":               "tool_call_rewritten",
		"request_id":          requestID,
		"model":               model,
		"profile":             profile,
		"call_id":             callID,
		"tool":                entry.Name(),
		"kind":                entry.Kind(),
		"original_type":       entry.OriginalType(),
		"raw_arguments":       rawArguments,
		"rewritten_arguments": rewrittenArguments,
		"reason":              "shell_file_mutation_blocked",
	}
	if modelCall := rememberedToolCall(callID); modelCall != nil {
		record["model_call"] = modelCall
	}
	appendRecord(record)
	incidentlog.Write("tool_call_rewritten", record)
}

func ToolCallRerouted(requestID string, model string, profile string, callID string, entry tools.Entry, rawArguments string, targetTool string, targetArguments string, reason string) {
	record := map[string]any{
		"time":             time.Now().Format(time.RFC3339Nano),
		"event":            "tool_call_rerouted",
		"request_id":       requestID,
		"model":            model,
		"profile":          profile,
		"call_id":          callID,
		"tool":             entry.Name(),
		"kind":             entry.Kind(),
		"original_type":    entry.OriginalType(),
		"raw_arguments":    rawArguments,
		"target_tool":      targetTool,
		"target_arguments": targetArguments,
		"reason":           reason,
	}
	if modelCall := rememberedToolCall(callID); modelCall != nil {
		record["model_call"] = modelCall
	}
	appendRecord(record)
}

func ToolOutput(ctx OutputContext, callID string, descriptor adapters.ToolDescriptor, rawArguments string, rawOutput string, formattedOutput string) {
	modelCall := takeRememberedToolCall(callID)
	if !shouldLogToolOutput(descriptor, rawArguments, rawOutput) {
		return
	}
	if seenToolOutput(callID, rawOutput) {
		return
	}
	failureKind := adapters.ClassifyPatchFailure(rawOutput)
	if failureKind == adapters.PatchFailureNone {
		failureKind = adapters.PatchFailureKind(adapters.ClassifyToolFailureWithArguments(descriptor, rawArguments, rawOutput))
	}
	if failureKind == adapters.PatchFailureNone && descriptor.Kind == tools.KindWebSearch && isWebSearchFailure(rawOutput) {
		failureKind = adapters.PatchFailureKind("web_search_failed")
	}
	record := map[string]any{
		"time":             time.Now().Format(time.RFC3339Nano),
		"event":            "tool_output",
		"call_id":          callID,
		"tool":             descriptor.Name,
		"kind":             descriptor.Kind,
		"original_type":    descriptor.OriginalType,
		"raw_arguments":    rawArguments,
		"failure_kind":     failureKind,
		"raw_output":       rawOutput,
		"formatted_output": formattedOutput,
	}
	if modelCall != nil {
		record["model_call"] = modelCall
	}
	if ctx.RequestID != "" {
		record["request_id"] = ctx.RequestID
	}
	if ctx.Model != "" {
		record["model"] = ctx.Model
	}
	if ctx.UpstreamModel != "" {
		record["upstream_model"] = ctx.UpstreamModel
	}
	if ctx.Profile != "" {
		record["profile"] = ctx.Profile
	}
	if len(ctx.RequestSummary) > 0 {
		record["request_summary"] = ctx.RequestSummary
	}
	appendRecord(record)
	if shouldWriteIncident(failureKind) {
		incidentlog.Write("tool_error", record)
	}
}

func shouldWriteIncident(failureKind adapters.PatchFailureKind) bool {
	return failureKind != "" && failureKind != adapters.PatchFailureKind(adapters.ToolFailureMCPResourcesEmpty)
}

func shouldLogToolOutput(descriptor adapters.ToolDescriptor, rawArguments string, rawOutput string) bool {
	return isPatchWriteKind(descriptor.Kind) || descriptor.Kind == tools.KindWebSearch || adapters.ClassifyToolFailureWithArguments(descriptor, rawArguments, rawOutput) != adapters.ToolFailureNone
}

func isPatchWriteKind(kind string) bool {
	return kind == tools.KindPatch || kind == tools.KindTextEditor
}

func seenToolOutput(callID string, rawOutput string) bool {
	key := ConfiguredPath() + ":" + callID + ":" + outputHash(rawOutput)
	if _, loaded := seenToolOutputs.LoadOrStore(key, true); loaded {
		return true
	}
	return false
}

func outputHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func rememberedToolCall(callID string) map[string]any {
	value, ok := modelToolCalls.Load(callID)
	if !ok {
		return nil
	}
	record, _ := value.(map[string]any)
	return cloneRecord(record)
}

func takeRememberedToolCall(callID string) map[string]any {
	value, ok := modelToolCalls.LoadAndDelete(callID)
	if !ok {
		return nil
	}
	record, _ := value.(map[string]any)
	return cloneRecord(record)
}

func cloneRecord(record map[string]any) map[string]any {
	if record == nil {
		return nil
	}
	out := make(map[string]any, len(record))
	for key, value := range record {
		out[key] = value
	}
	return out
}

func isWebSearchFailure(output string) bool {
	text := strings.TrimSpace(output)
	return strings.HasPrefix(text, "Search failed:") || strings.HasPrefix(text, "Search read failed:")
}

func appendRecord(record map[string]any) {
	path := ConfiguredPath()
	if path == "" {
		return
	}
	attachRequestContext(record)
	if sessionID := recordSessionID(record); sessionID != "" {
		diagnostics.WriteSessionRecord(path, sessionID, "tool-calls.jsonl", record)
	}
	diagnostics.WriteJSONL(path, record)
}

func attachRequestContext(record map[string]any) {
	if record == nil {
		return
	}
	requestID, _ := record["request_id"].(string)
	if requestID == "" {
		return
	}
	value, ok := requestContexts.Load(requestID)
	if !ok {
		return
	}
	ctx, _ := value.(requestContext)
	if recordSessionID(record) == "" && ctx.SessionID != "" {
		record["codex_session_id"] = ctx.SessionID
	}
	if _, ok := record["model"]; !ok {
		if ctx.Model != "" {
			record["model"] = ctx.Model
		}
	}
	if _, ok := record["upstream_model"]; !ok {
		if ctx.UpstreamModel != "" {
			record["upstream_model"] = ctx.UpstreamModel
		}
	}
}

func recordSessionID(record map[string]any) string {
	if value, _ := record["codex_session_id"].(string); strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	if value, _ := record["session_id"].(string); strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return ""
}
