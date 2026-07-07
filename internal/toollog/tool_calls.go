package toollog

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
	"codex-bridge/internal/diagnostics"
	"codex-bridge/internal/incidentlog"
	"codex-bridge/internal/toolruntime"
	"codex-bridge/internal/tools"
)

const EnvToolLogPath = "CODEX_BRIDGE_TOOL_LOG"

var seenToolOutputs sync.Map
var modelToolCalls sync.Map
var logicalToolCalls sync.Map
var requestContexts sync.Map

type requestContext struct {
	SessionID     string
	Model         string
	UpstreamModel string
}

type LogicalToolCall struct {
	Name      string
	Arguments string
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
	addEntryContract(record, entry)
	if strings.TrimSpace(reasoning) != "" {
		record["reasoning"] = incidentlog.TextSummary(reasoning)
	}
	attachRequestContext(record)
	modelToolCalls.Store(callID, cloneRecord(record))
	if tools.IsNativeCommandProxyToolName(entry.Name()) {
		logicalToolCalls.Store(callID, LogicalToolCall{Name: entry.Name(), Arguments: rawArguments})
	}
	appendRecord(record)
}

func RememberRequestSession(requestID string, sessionID string, model string, upstreamModel string, profile string, requestSummary map[string]any) {
	sessionID = strings.TrimSpace(sessionID)
	upstreamModel = strings.TrimSpace(upstreamModel)
	if sessionID == "" && upstreamModel == "" {
		return
	}
	requestContexts.Store(requestID, requestContext{SessionID: sessionID, Model: model, UpstreamModel: upstreamModel})
	toolruntime.RememberRequest(toolruntime.RequestContext{
		RequestID:      requestID,
		SessionID:      sessionID,
		Model:          model,
		UpstreamModel:  upstreamModel,
		Profile:        profile,
		RequestSummary: requestSummary,
	})
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
	toolruntime.ForgetRequest(requestID)
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
	addEntryContract(record, entry)
	if modelCall := rememberedToolCall(callID); modelCall != nil {
		record["model_call"] = modelCall
	}
	appendRecord(record)
}

func ToolCallFrame(requestID string, model string, profile string, callID string, entry tools.Entry, modelArguments string, canonicalArguments string, runtimeArguments string) {
	record := map[string]any{
		"time":                time.Now().Format(time.RFC3339Nano),
		"event":               "tool_call_frame",
		"request_id":          requestID,
		"model":               model,
		"profile":             profile,
		"call_id":             callID,
		"tool":                entry.Name(),
		"kind":                entry.Kind(),
		"original_type":       entry.OriginalType(),
		"transformer":         entry.Transformer(),
		"model_arguments":     modelArguments,
		"canonical_arguments": canonicalArguments,
		"runtime_arguments":   runtimeArguments,
	}
	addEntryContract(record, entry)
	if modelCall := rememberedToolCall(callID); modelCall != nil {
		record["model_call"] = modelCall
	}
	appendRecord(record)
}

func BrokerDecision(requestID string, model string, profile string, callID string, entry tools.Entry, rawArguments string, decision toolruntime.Decision) {
	record := map[string]any{
		"time":          time.Now().Format(time.RFC3339Nano),
		"event":         "tool_broker_decision",
		"request_id":    requestID,
		"model":         model,
		"profile":       profile,
		"call_id":       callID,
		"tool":          entry.Name(),
		"kind":          entry.Kind(),
		"original_type": entry.OriginalType(),
		"raw_arguments": rawArguments,
		"action":        decision.Action,
		"reason":        decision.Reason,
		"profiled_tool": decision.Profile,
	}
	addEntryContract(record, entry)
	if decision.ProgressKey != "" {
		record["progress_key"] = decision.ProgressKey
	}
	if decision.RetryCount > 0 {
		record["retry_count"] = decision.RetryCount
	}
	if modelCall := rememberedToolCall(callID); modelCall != nil {
		record["model_call"] = modelCall
	}
	appendRecord(record)
}

func ToolOutput(ctx OutputContext, callID string, descriptor adapters.ToolDescriptor, rawArguments string, rawOutput string, formattedOutput string) {
	modelCall := takeRememberedToolCall(callID)
	if seenToolOutput(callID, rawOutput) {
		return
	}
	failureKind := adapters.PatchFailureNone
	if isPatchWriteKind(descriptor.Kind) {
		failureKind = adapters.ClassifyPatchFailure(rawOutput)
	}
	if failureKind == adapters.PatchFailureNone {
		failureKind = adapters.PatchFailureKind(adapters.ClassifyToolFailureWithArguments(descriptor, rawArguments, rawOutput))
	}
	outcome := toolruntime.ObserveOutput(toolruntime.OutputContext{
		RequestID: ctx.RequestID,
		CallID:    callID,
		Tool: toolruntime.ToolInfo{
			Name:         descriptor.Name,
			Kind:         descriptor.Kind,
			OriginalType: descriptor.OriginalType,
			Description:  descriptor.Description,
			SideEffect:   descriptor.SideEffect,
			Arguments:    rawArguments,
		},
		ModelCallTool: modelCallToolName(modelCall),
		RawOutput:     rawOutput,
		ToolFailed:    failureKind != adapters.PatchFailureNone,
		FailureKind:   string(failureKind),
	})
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
		"runtime_outcome":  outcome,
	}
	level := diagnosticLevel(failureKind, outcome)
	record["diagnostic_level"] = level
	if modelCall != nil {
		record["model_call"] = modelCall
		if namespace, _ := modelCall["namespace"].(string); strings.TrimSpace(namespace) != "" {
			record["namespace"] = namespace
		}
		if originalName, _ := modelCall["original_name"].(string); strings.TrimSpace(originalName) != "" {
			record["original_name"] = originalName
		}
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
	if level == "recoverable" {
		writeRecovery(record)
	}
	if level == "incident" || level == "fatal" {
		incidentlog.Write("tool_error", record)
	}
}

func diagnosticLevel(failureKind adapters.PatchFailureKind, outcome toolruntime.Outcome) string {
	category := strings.TrimSpace(string(failureKind))
	if category == "" {
		category = strings.TrimSpace(outcome.Category)
	}
	if category == "" || outcome.OK {
		return "ok"
	}
	switch category {
	case "already_applied",
		"context_mismatch",
		"invalid_arguments",
		"malformed_patch",
		"invalid_hunk",
		"read_file_operation",
		"no_progress",
		"path_error",
		"nonzero_exit",
		"structured_failure",
		"schema_validation_error",
		"mcp_resource_local_identifier",
		"mcp_resource_server_unknown",
		"mcp_resource_unlisted_identifier",
		"mcp_resource_read_failed",
		"mcp_resources_empty",
		"tool_search_empty",
		"file_search_empty",
		"file_search_failed",
		"local_file_read_failed":
		return "recoverable"
	case "permission_or_sandbox",
		"tool_unavailable",
		"runtime_no_progress",
		"tool_execution_error":
		return "incident"
	default:
		return "incident"
	}
}

func writeRecovery(record map[string]any) {
	path := ConfiguredPath()
	if path == "" {
		return
	}
	out := cloneRecord(record)
	out["event"] = "tool_recovery"
	out["time"] = time.Now().Format(time.RFC3339Nano)
	diagnostics.WriteJSONL(filepath.Join(filepath.Dir(path), "recoveries.jsonl"), out)
	if sessionID := recordSessionID(out); sessionID != "" {
		diagnostics.WriteSessionRecord(path, sessionID, "recoveries.jsonl", out)
	}
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

func RememberedLogicalToolCall(callID string) (LogicalToolCall, bool) {
	value, ok := logicalToolCalls.Load(callID)
	if !ok {
		return LogicalToolCall{}, false
	}
	call, _ := value.(LogicalToolCall)
	if call.Name == "" {
		return LogicalToolCall{}, false
	}
	return call, true
}

func modelCallToolName(record map[string]any) string {
	if record == nil {
		return ""
	}
	if value, _ := record["tool"].(string); strings.TrimSpace(value) != "" {
		return value
	}
	return ""
}

func addEntryContract(record map[string]any, entry tools.Entry) {
	if record == nil {
		return
	}
	record["contract_id"] = entry.ContractID()
	record["argument_mode"] = entry.ArgumentMode
	record["schema_quality"] = entry.SchemaQuality
	if entry.Namespace != "" {
		record["namespace"] = entry.Namespace
		record["original_name"] = entry.OriginalName()
	}
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
