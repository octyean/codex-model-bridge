package toolruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DecisionAllow = "allow"
	DecisionStop  = "stop"

	CapabilityRead    = "read"
	CapabilityWrite   = "write"
	CapabilityExecute = "execute"
	CapabilityUnknown = "unknown"
)

type ToolInfo struct {
	Name         string
	Kind         string
	OriginalType string
	Description  string
	SideEffect   string
	Arguments    string
}

type RequestContext struct {
	RequestID      string
	SessionID      string
	Model          string
	UpstreamModel  string
	Profile        string
	RequestSummary map[string]any
}

type CallContext struct {
	RequestID          string
	Model              string
	UpstreamModel      string
	Profile            string
	CallID             string
	Tool               ToolInfo
	CanReturnLocalText bool
}

type OutputContext struct {
	RequestID     string
	CallID        string
	Tool          ToolInfo
	ModelCallTool string
	RawOutput     string
	ToolFailed    bool
	FailureKind   string
}

type Profile struct {
	Tool          string
	ToolKey       string
	Capability    string
	ArgumentsHash string
	Signature     string
	ReadOnly      bool
	Risk          string
	Source        []string
}

type Decision struct {
	Action       string
	Reason       string
	Profile      Profile
	ProgressKey  string
	LocalText    string
	RetryCount   int
	ShouldRecord bool
}

type Outcome struct {
	OK          bool
	Category    string
	Progress    bool
	OutputHash  string
	ProgressKey string
}

type attempt struct {
	Time          string
	RequestID     string
	CallID        string
	Tool          string
	Capability    string
	ArgumentsHash string
	OK            bool
	Category      string
	Progress      bool
	OutputHash    string
}

type sessionState struct {
	Attempts map[string][]attempt
}

var (
	mu              sync.Mutex
	requestContexts = map[string]RequestContext{}
	requestPlanned  = map[string]map[string]int{}
	sessions        = map[string]*sessionState{}
)

func RememberRequest(ctx RequestContext) {
	if strings.TrimSpace(ctx.RequestID) == "" {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	requestContexts[ctx.RequestID] = ctx
	requestPlanned[ctx.RequestID] = map[string]int{}
	if strings.TrimSpace(ctx.SessionID) != "" {
		ensureSessionLocked(ctx.SessionID)
	}
}

func ForgetRequest(requestID string) {
	mu.Lock()
	defer mu.Unlock()
	delete(requestContexts, requestID)
	delete(requestPlanned, requestID)
}

func Decide(ctx CallContext) Decision {
	profile := ProfileTool(ctx.Tool)
	decision := Decision{
		Action:       DecisionAllow,
		Reason:       "runtime_signature_allowed",
		Profile:      profile,
		ProgressKey:  profile.Signature,
		ShouldRecord: true,
	}
	if !ctx.CanReturnLocalText {
		decision.Reason = "runtime_signature_allowed_without_local_result_tool"
		return decision
	}
	reqCtx, ok := requestContext(ctx.RequestID)
	if !ok || reqCtx.SessionID == "" {
		decision.Reason = "runtime_signature_allowed_without_session"
		if duplicatePlanned(ctx.RequestID, profile.Signature) {
			decision.Action = DecisionStop
			decision.Reason = "same_tool_signature_already_requested_in_response"
			decision.RetryCount = 1
			decision.LocalText = stopText(profile, 1)
			return decision
		}
		markPlanned(ctx.RequestID, profile.Signature)
		return decision
	}
	retries := noProgressRetries(reqCtx.SessionID, profile.Signature)
	if retries == 0 {
		if duplicatePlanned(ctx.RequestID, profile.Signature) {
			decision.Action = DecisionStop
			decision.Reason = "same_tool_signature_already_requested_in_response"
			decision.RetryCount = 1
			decision.LocalText = stopText(profile, 1)
			return decision
		}
		markPlanned(ctx.RequestID, profile.Signature)
		return decision
	}
	decision.Action = DecisionStop
	decision.Reason = "same_tool_signature_failed_without_progress"
	decision.RetryCount = retries
	decision.LocalText = stopText(profile, retries)
	return decision
}

func ObserveOutput(ctx OutputContext) Outcome {
	tool := ctx.Tool
	if strings.TrimSpace(ctx.ModelCallTool) != "" {
		tool.Name = ctx.ModelCallTool
	}
	profile := ProfileTool(tool)
	ok, category := outputStatus(ctx)
	outcome := Outcome{
		OK:          ok,
		Category:    category,
		Progress:    ok,
		OutputHash:  hashText(ctx.RawOutput),
		ProgressKey: profile.Signature,
	}
	recordAttempt(ctx, profile, outcome)
	return outcome
}

func ProfileTool(tool ToolInfo) Profile {
	args := parseArguments(tool.Arguments)
	capability, readOnly, risk, source := capabilityFor(tool)
	toolName := strings.TrimSpace(tool.Name)
	toolKey := canonicalToolName(toolName)
	argumentsHash := hashText(canonicalJSON(args))
	signature := strings.Join([]string{
		toolKey,
		capability,
		argumentsHash,
	}, "|")
	return Profile{
		Tool:          toolName,
		ToolKey:       toolKey,
		Capability:    capability,
		ArgumentsHash: argumentsHash,
		Signature:     signature,
		ReadOnly:      readOnly,
		Risk:          risk,
		Source:        source,
	}
}

func canonicalToolName(name string) string {
	name = strings.TrimSpace(name)
	if left, right, ok := strings.Cut(name, "__"); ok && strings.HasPrefix(left, "mcp_") && strings.TrimSpace(right) != "" {
		return strings.TrimSpace(right)
	}
	return name
}

func capabilityFor(tool ToolInfo) (string, bool, string, []string) {
	switch tool.SideEffect {
	case "write_files":
		return CapabilityWrite, false, "high", []string{"side_effect"}
	case "execute":
		return CapabilityExecute, false, "high", []string{"side_effect"}
	case "read":
		return CapabilityRead, true, "low", []string{"side_effect"}
	}
	switch tool.Kind {
	case "patch", "text_editor_patch":
		return CapabilityWrite, false, "high", []string{"kind"}
	case "shell":
		return CapabilityExecute, false, "high", []string{"kind"}
	}
	return CapabilityUnknown, true, "unknown", []string{"fallback"}
}

func requestContext(requestID string) (RequestContext, bool) {
	mu.Lock()
	defer mu.Unlock()
	ctx, ok := requestContexts[requestID]
	return ctx, ok
}

func recordAttempt(ctx OutputContext, profile Profile, outcome Outcome) {
	reqCtx, ok := requestContext(ctx.RequestID)
	if !ok || reqCtx.SessionID == "" || profile.Signature == "" {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	state := ensureSessionLocked(reqCtx.SessionID)
	state.Attempts[profile.Signature] = append(state.Attempts[profile.Signature], attempt{
		Time:          time.Now().Format(time.RFC3339Nano),
		RequestID:     ctx.RequestID,
		CallID:        ctx.CallID,
		Tool:          profile.Tool,
		Capability:    profile.Capability,
		ArgumentsHash: profile.ArgumentsHash,
		OK:            outcome.OK,
		Category:      outcome.Category,
		Progress:      outcome.Progress,
		OutputHash:    outcome.OutputHash,
	})
}

func noProgressRetries(sessionID string, signature string) int {
	if signature == "" {
		return 0
	}
	mu.Lock()
	defer mu.Unlock()
	state := sessions[sessionID]
	if state == nil {
		return 0
	}
	count := 0
	for _, attempt := range state.Attempts[signature] {
		if !attempt.OK && !attempt.Progress {
			count++
		}
	}
	return count
}

func duplicatePlanned(requestID string, signature string) bool {
	if requestID == "" || signature == "" {
		return false
	}
	mu.Lock()
	defer mu.Unlock()
	return requestPlanned[requestID][signature] > 0
}

func markPlanned(requestID string, signature string) {
	if requestID == "" || signature == "" {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	planned := requestPlanned[requestID]
	if planned == nil {
		planned = map[string]int{}
		requestPlanned[requestID] = planned
	}
	planned[signature]++
}

func ensureSessionLocked(sessionID string) *sessionState {
	state := sessions[sessionID]
	if state == nil {
		state = &sessionState{Attempts: map[string][]attempt{}}
		sessions[sessionID] = state
	}
	return state
}

func parseArguments(arguments string) map[string]any {
	var out map[string]any
	if err := json.Unmarshal([]byte(arguments), &out); err == nil {
		return out
	}
	return map[string]any{}
}

func outputStatus(ctx OutputContext) (bool, string) {
	if strings.TrimSpace(ctx.RawOutput) == "" {
		return false, "empty_output"
	}
	if ctx.ToolFailed {
		if strings.TrimSpace(ctx.FailureKind) != "" {
			return false, ctx.FailureKind
		}
		return false, "tool_failure"
	}
	if code, ok := shellExitCode(ctx.RawOutput); ok && code != 0 {
		return false, "nonzero_exit"
	}
	if failedByStructuredPayload(ctx.RawOutput) {
		return false, "structured_failure"
	}
	return true, "success"
}

func shellExitCode(output string) (int, bool) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"Exit code:", "Process exited with code"} {
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			code, err := strconv.Atoi(value)
			return code, err == nil
		}
	}
	return 0, false
}

func failedByStructuredPayload(output string) bool {
	for _, candidate := range jsonCandidates(output) {
		var value any
		if err := json.Unmarshal([]byte(candidate), &value); err == nil && structuredFailure(value) {
			return true
		}
	}
	return false
}

func jsonCandidates(output string) []string {
	trimmed := strings.TrimSpace(output)
	candidates := []string{trimmed}
	if _, body, ok := strings.Cut(trimmed, "Output:"); ok {
		candidates = append(candidates, strings.TrimSpace(body))
	}
	return candidates
}

func structuredFailure(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for _, key := range []string{"success", "ok"} {
			if b, ok := v[key].(bool); ok && !b {
				return true
			}
		}
		if b, ok := v["isError"].(bool); ok && b {
			return true
		}
		if result, ok := v["result"].(string); ok {
			var nested any
			if err := json.Unmarshal([]byte(result), &nested); err == nil {
				return structuredFailure(nested)
			}
		}
		for _, item := range v {
			if structuredFailure(item) {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if structuredFailure(item) {
				return true
			}
		}
	}
	return false
}

func stopText(profile Profile, retryCount int) string {
	return strings.Join([]string{
		"TOOL_RUNTIME_NO_PROGRESS",
		"tool_runtime_state: stopped_repeated_no_progress_tool_signature",
		"tool: " + profile.Tool,
		"tool_key: " + profile.ToolKey,
		"capability: " + profile.Capability,
		"arguments_hash: " + profile.ArgumentsHash,
		"retry_count: " + jsonNumber(retryCount),
		"required_next_action: stop_retrying_the_same_tool_signature_and_choose_a_materially_different_action",
	}, "\n")
}

func canonicalJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:8])
}

func jsonNumber(value int) string {
	data, _ := json.Marshal(value)
	return string(data)
}
