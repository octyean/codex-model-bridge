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

const (
	EnvToolLogPath = "CODEX_BRIDGE_TOOL_LOG"

	toolStateTTL        = 24 * time.Hour
	maxToolStateEntries = 8192
)

type rememberedStateEntry struct {
	Value    any
	LastSeen time.Time
}

var (
	toolStateMu      sync.Mutex
	seenToolOutputs  = map[string]rememberedStateEntry{}
	modelToolCalls   = map[string]rememberedStateEntry{}
	logicalToolCalls = map[string]rememberedStateEntry{}
	requestContexts  sync.Map
)

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
	rememberState(modelToolCalls, scopedCallKey(requestStateScope(requestID), callID), cloneRecord(record))
	if tools.IsNativeCommandProxyToolName(entry.Name()) {
		rememberState(logicalToolCalls, scopedCallKey(requestSessionScope(requestID), callID), LogicalToolCall{Name: entry.Name(), Arguments: rawArguments})
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
	pruneRememberedState(time.Now())
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
	if modelCall := rememberedToolCall(requestID, callID); modelCall != nil {
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
	if modelCall := rememberedToolCall(requestID, callID); modelCall != nil {
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
	if modelCall := rememberedToolCall(requestID, callID); modelCall != nil {
		record["model_call"] = modelCall
	}
	appendRecord(record)
}

func ToolOutput(ctx OutputContext, callID string, descriptor adapters.ToolDescriptor, rawArguments string, rawOutput string, formattedOutput string) {
	modelCall := takeRememberedToolCall(ctx.RequestID, callID)
	if seenToolOutput(ctx.RequestID, callID, rawOutput) {
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
		"time":            time.Now().Format(time.RFC3339Nano),
		"event":           "tool_output",
		"call_id":         callID,
		"tool":            descriptor.Name,
		"kind":            descriptor.Kind,
		"original_type":   descriptor.OriginalType,
		"raw_arguments":   rawArguments,
		"failure_kind":    failureKind,
		"runtime_outcome": outcome,
	}
	level := diagnosticLevel(failureKind, outcome)
	record["diagnostic_level"] = level
	if level == "ok" {
		record["raw_output_summary"] = diagnostics.TextSummary(rawOutput, 1200)
		if formattedOutput != rawOutput {
			record["formatted_output_summary"] = diagnostics.TextSummary(formattedOutput, 1200)
		}
	} else {
		record["raw_output"] = rawOutput
		record["formatted_output"] = formattedOutput
	}
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
	out := cloneRecord(record)
	out["event"] = "tool_recovery"
	WriteRecovery(recordSessionID(out), out)
}

func WriteRecovery(sessionID string, record map[string]any) {
	path := ConfiguredPath()
	if path == "" || record == nil {
		return
	}
	out := cloneRecord(record)
	if _, ok := out["time"]; !ok {
		out["time"] = time.Now().Format(time.RFC3339Nano)
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = recordSessionID(out)
	}
	if sessionID != "" {
		if recordSessionID(out) == "" {
			out["codex_session_id"] = sessionID
		}
	}
	diagnostics.WriteGlobalJSONL(filepath.Join(filepath.Dir(path), "recoveries.jsonl"), out)
	if sessionID != "" {
		diagnostics.WriteSessionRecord(path, sessionID, "recoveries.jsonl", out)
	}
}

func isPatchWriteKind(kind string) bool {
	return kind == tools.KindPatch || kind == tools.KindTextEditor
}

func seenToolOutput(requestID string, callID string, rawOutput string) bool {
	key := scopedCallKey(requestStateScope(requestID), callID)
	if key == "" {
		return false
	}
	key += "\x00" + outputHash(rawOutput)
	now := time.Now()
	toolStateMu.Lock()
	defer toolStateMu.Unlock()
	if entry, ok := seenToolOutputs[key]; ok && !entry.LastSeen.Before(now.Add(-toolStateTTL)) {
		entry.LastSeen = now
		seenToolOutputs[key] = entry
		return true
	}
	seenToolOutputs[key] = rememberedStateEntry{LastSeen: now}
	pruneRememberedMapLocked(seenToolOutputs, now)
	return false
}

func outputHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func rememberedToolCall(requestID string, callID string) map[string]any {
	value, ok := loadRememberedState(modelToolCalls, scopedCallKey(requestStateScope(requestID), callID), false)
	if !ok {
		return nil
	}
	record, _ := value.(map[string]any)
	return cloneRecord(record)
}

func takeRememberedToolCall(requestID string, callID string) map[string]any {
	value, ok := loadRememberedState(modelToolCalls, scopedCallKey(requestStateScope(requestID), callID), true)
	if !ok {
		return nil
	}
	record, _ := value.(map[string]any)
	return cloneRecord(record)
}

func RememberedLogicalToolCall(sessionID string, callID string) (LogicalToolCall, bool) {
	value, ok := loadRememberedState(logicalToolCalls, scopedCallKey(sessionStateScope(sessionID), callID), false)
	if !ok {
		return LogicalToolCall{}, false
	}
	call, _ := value.(LogicalToolCall)
	if call.Name == "" {
		return LogicalToolCall{}, false
	}
	return call, true
}

func rememberState(values map[string]rememberedStateEntry, key string, value any) {
	if key == "" {
		return
	}
	now := time.Now()
	toolStateMu.Lock()
	defer toolStateMu.Unlock()
	values[key] = rememberedStateEntry{Value: value, LastSeen: now}
	pruneRememberedMapLocked(values, now)
}

func loadRememberedState(values map[string]rememberedStateEntry, key string, remove bool) (any, bool) {
	if key == "" {
		return nil, false
	}
	now := time.Now()
	toolStateMu.Lock()
	defer toolStateMu.Unlock()
	entry, ok := values[key]
	if !ok {
		return nil, false
	}
	if entry.LastSeen.Before(now.Add(-toolStateTTL)) {
		delete(values, key)
		return nil, false
	}
	if remove {
		delete(values, key)
	} else {
		entry.LastSeen = now
		values[key] = entry
	}
	return entry.Value, true
}

func pruneRememberedState(now time.Time) {
	toolStateMu.Lock()
	defer toolStateMu.Unlock()
	pruneRememberedMapLocked(seenToolOutputs, now)
	pruneRememberedMapLocked(modelToolCalls, now)
	pruneRememberedMapLocked(logicalToolCalls, now)
}

func pruneRememberedMapLocked(values map[string]rememberedStateEntry, now time.Time) {
	cutoff := now.Add(-toolStateTTL)
	for key, entry := range values {
		if entry.LastSeen.Before(cutoff) {
			delete(values, key)
		}
	}
	for len(values) > maxToolStateEntries {
		oldestKey := ""
		var oldest time.Time
		for key, entry := range values {
			if oldestKey == "" || entry.LastSeen.Before(oldest) {
				oldestKey = key
				oldest = entry.LastSeen
			}
		}
		delete(values, oldestKey)
	}
}

func requestStateScope(requestID string) string {
	if sessionID := requestSessionID(requestID); sessionID != "" {
		return sessionStateScope(sessionID)
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return ""
	}
	return "request:" + requestID
}

func requestSessionScope(requestID string) string {
	return sessionStateScope(requestSessionID(requestID))
}

func requestSessionID(requestID string) string {
	value, ok := requestContexts.Load(requestID)
	if !ok {
		return ""
	}
	ctx, _ := value.(requestContext)
	return strings.TrimSpace(ctx.SessionID)
}

func sessionStateScope(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	return "session:" + sessionID
}

func scopedCallKey(scope string, callID string) string {
	scope = strings.TrimSpace(scope)
	callID = strings.TrimSpace(callID)
	if scope == "" || callID == "" {
		return ""
	}
	return scope + "\x00" + callID
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
	diagnostics.WriteGlobalJSONL(path, record)
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
