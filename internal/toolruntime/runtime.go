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

	CapabilityRead     = "read"
	CapabilityWrite    = "write"
	CapabilityExecute  = "execute"
	CapabilityInteract = "interact"
	CapabilityStatus   = "status"
	CapabilityUnknown  = "unknown"

	sessionStateTTL  = 24 * time.Hour
	maxSessionStates = 1024
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

type sessionState struct {
	NoProgressFailures map[string]int
	LastSeen           time.Time
}

var (
	mu              sync.Mutex
	requestContexts = map[string]RequestContext{}
	requestPlanned  = map[string]map[string]int{}
	callProfiles    = map[string]map[string]Profile{}
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
		now := time.Now()
		ensureSessionLocked(ctx.SessionID, now)
		pruneSessionsLocked(now)
	}
}

func ForgetRequest(requestID string) {
	mu.Lock()
	defer mu.Unlock()
	delete(requestContexts, requestID)
	delete(requestPlanned, requestID)
	delete(callProfiles, requestID)
}

func Decide(ctx CallContext) Decision {
	profile := ProfileTool(ctx.Tool)
	rememberCallProfile(ctx.RequestID, ctx.CallID, profile)
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
	profile, ok := callProfile(ctx.RequestID, ctx.CallID)
	if !ok {
		tool := ctx.Tool
		if strings.TrimSpace(ctx.ModelCallTool) != "" {
			tool.Name = ctx.ModelCallTool
		}
		profile = ProfileTool(tool)
	}
	ok, category := outputStatus(ctx)
	progress := ok
	if category == "status_ack" {
		progress = false
	}
	outcome := Outcome{
		OK:          ok,
		Category:    category,
		Progress:    progress,
		OutputHash:  hashText(ctx.RawOutput),
		ProgressKey: profile.Signature,
	}
	recordAttempt(ctx, profile, outcome)
	return outcome
}

func rememberCallProfile(requestID string, callID string, profile Profile) {
	if strings.TrimSpace(requestID) == "" || strings.TrimSpace(callID) == "" || profile.Signature == "" {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	profiles := callProfiles[requestID]
	if profiles == nil {
		profiles = map[string]Profile{}
		callProfiles[requestID] = profiles
	}
	profiles[callID] = profile
}

func callProfile(requestID string, callID string) (Profile, bool) {
	if strings.TrimSpace(requestID) == "" || strings.TrimSpace(callID) == "" {
		return Profile{}, false
	}
	mu.Lock()
	defer mu.Unlock()
	profiles := callProfiles[requestID]
	profile, ok := profiles[callID]
	if ok {
		delete(profiles, callID)
		if len(profiles) == 0 {
			delete(callProfiles, requestID)
		}
	}
	return profile, ok
}

func ProfileTool(tool ToolInfo) Profile {
	capability, readOnly, risk, source := capabilityFor(tool)
	toolName := strings.TrimSpace(tool.Name)
	toolKey := canonicalToolName(toolName)
	argumentsHash := hashText(canonicalArguments(tool.Arguments))
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
	return strings.TrimSpace(name)
}

func capabilityFor(tool ToolInfo) (string, bool, string, []string) {
	if tool.Kind == "text_editor_patch" {
		return CapabilityWrite, false, "high", []string{"kind"}
	}
	switch tool.SideEffect {
	case "write_files":
		return CapabilityWrite, false, "high", []string{"side_effect"}
	case "execute":
		return CapabilityExecute, false, "high", []string{"side_effect"}
	case "status":
		return CapabilityStatus, true, "low", []string{"side_effect"}
	case "read":
		return CapabilityRead, true, "low", []string{"side_effect"}
	}
	switch tool.Kind {
	case "patch":
		return CapabilityWrite, false, "high", []string{"kind"}
	case "shell":
		return CapabilityExecute, false, "high", []string{"kind"}
	}
	if capability, readOnly, risk, ok := capabilityFromText(tool.Name, tool.Description); ok {
		return capability, readOnly, risk, []string{"heuristic"}
	}
	return CapabilityUnknown, true, "unknown", []string{"fallback"}
}

func capabilityFromText(name string, description string) (string, bool, string, bool) {
	text := strings.ToLower(strings.TrimSpace(name + " " + description))
	switch {
	case containsAny(text, "write", "edit", "patch", "create", "delete", "remove", "update", "upload"):
		return CapabilityWrite, false, "high", true
	case containsAny(text, "exec", "shell", "command", "terminal", "run "):
		return CapabilityExecute, false, "high", true
	case containsAny(text, "click", "type", "press", "navigate", "scroll"):
		return CapabilityInteract, false, "medium", true
	case containsAny(text, "search", "extract", "read", "list", "fetch", "snapshot", "inspect", "get "):
		return CapabilityRead, true, "low", true
	}
	return "", true, "", false
}

func containsAny(text string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
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
	now := time.Now()
	state := ensureSessionLocked(reqCtx.SessionID, now)
	if outcome.Progress {
		clear(state.NoProgressFailures)
		return
	}
	if !outcome.OK {
		state.NoProgressFailures[profile.Signature]++
	}
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
	state.LastSeen = time.Now()
	return state.NoProgressFailures[signature]
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

func ensureSessionLocked(sessionID string, now time.Time) *sessionState {
	state := sessions[sessionID]
	if state == nil {
		state = &sessionState{NoProgressFailures: map[string]int{}}
		sessions[sessionID] = state
	}
	state.LastSeen = now
	return state
}

func pruneSessionsLocked(now time.Time) {
	cutoff := now.Add(-sessionStateTTL)
	for sessionID, state := range sessions {
		if state.LastSeen.Before(cutoff) {
			delete(sessions, sessionID)
		}
	}
	for len(sessions) > maxSessionStates {
		oldestID := ""
		var oldest time.Time
		for sessionID, state := range sessions {
			if oldestID == "" || state.LastSeen.Before(oldest) {
				oldestID = sessionID
				oldest = state.LastSeen
			}
		}
		delete(sessions, oldestID)
	}
}

func canonicalArguments(arguments string) string {
	var value any
	if err := json.Unmarshal([]byte(arguments), &value); err == nil {
		return canonicalJSON(value)
	}
	return arguments
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
	if toolBoundaryFailureOutput(ctx.RawOutput) {
		return false, "tool_unavailable"
	}
	if noProgressOutput(ctx.RawOutput) {
		return false, "no_progress"
	}
	if code, ok := shellExitCode(ctx.RawOutput); ok && code != 0 {
		return false, "nonzero_exit"
	}
	if failedByStructuredPayload(ctx.RawOutput) {
		return false, "structured_failure"
	}
	if ctx.Tool.SideEffect == "status" {
		return true, "status_ack"
	}
	return true, "success"
}

func toolBoundaryFailureOutput(output string) bool {
	text := strings.ToLower(output)
	return strings.Contains(text, " is not allowed because ") ||
		strings.Contains(text, "not allowed because you do not support") ||
		strings.Contains(text, "tool is unavailable")
}

func noProgressOutput(output string) bool {
	text := strings.ToLower(output)
	return strings.Contains(text, "file_edit_state: not_modified") ||
		strings.Contains(text, "text_editor_no_progress") ||
		strings.Contains(text, "text_editor_create_target_already_exists") ||
		strings.Contains(text, "text_editor_move_target_same_as_source") ||
		strings.Contains(text, "text_editor_move_target_is_directory")
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
	obj, ok := value.(map[string]any)
	if !ok {
		return false
	}
	if value, ok := boolField(obj, "success"); ok && !value && hasFailureDetail(obj) {
		return true
	}
	if value, ok := boolField(obj, "ok"); ok && !value && hasFailureDetail(obj) {
		return true
	}
	if value, ok := boolField(obj, "isError"); ok && value {
		return true
	}
	if value, ok := boolField(obj, "is_error"); ok && value {
		return true
	}
	for _, key := range []string{"result", "error"} {
		nested, ok := obj[key].(string)
		if !ok {
			continue
		}
		var nestedValue any
		if err := json.Unmarshal([]byte(strings.TrimSpace(nested)), &nestedValue); err == nil && structuredFailure(nestedValue) {
			return true
		}
	}
	return false
}

func boolField(obj map[string]any, key string) (bool, bool) {
	value, ok := obj[key].(bool)
	return value, ok
}

func hasFailureDetail(obj map[string]any) bool {
	for _, key := range []string{"error", "message", "reason", "stderr", "code"} {
		if value, ok := obj[key]; ok && value != nil {
			return true
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
