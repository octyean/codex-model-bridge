package optimization

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"codex-bridge/internal/providers"
)

type Options struct {
	StabilizeTools   bool
	CacheDiagnostics bool
}

type Shape struct {
	SystemHash       string
	ToolsHash        string
	PrefixHash       string
	ToolSchemaTokens int
	MessageCount     int
	ToolCount        int
}

type Diagnostics struct {
	Shape                Shape
	PrefixChanged        bool
	PrefixChangeReasons  []string
	CacheHitTokens       int
	CacheMissTokens      int
	CacheHitRatePermille int
}

type Tracker struct {
	mu     sync.Mutex
	shapes map[string]Shape
}

func NewTracker() *Tracker {
	return &Tracker{shapes: map[string]Shape{}}
}

func PrepareRequest(req providers.ChatCompletionRequest, opts Options) providers.ChatCompletionRequest {
	if opts.StabilizeTools {
		req.Tools = StabilizeTools(req.Tools)
	}
	return req
}

func StabilizeTools(tools []providers.ChatTool) []providers.ChatTool {
	out := append([]providers.ChatTool(nil), tools...)
	for i := range out {
		out[i].Function.Parameters = CanonicalJSONRaw(out[i].Function.Parameters)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := out[i].Function
		right := out[j].Function
		if left.Name != right.Name {
			return left.Name < right.Name
		}
		if left.Description != right.Description {
			return left.Description < right.Description
		}
		if string(left.Parameters) != string(right.Parameters) {
			return string(left.Parameters) < string(right.Parameters)
		}
		return out[i].Type < out[j].Type
	})
	return out
}

func CanonicalJSONRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || !json.Valid(raw) {
		return raw
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return raw
	}
	data, err := json.Marshal(canonicalValue(value))
	if err != nil {
		return raw
	}
	return json.RawMessage(data)
}

func CaptureShape(req providers.ChatCompletionRequest) Shape {
	systemText := systemPrompt(req.Messages)
	tools := StabilizeTools(req.Tools)
	toolsJSON, _ := json.Marshal(tools)
	return Shape{
		SystemHash:       shortHash(systemText),
		ToolsHash:        shortHash(string(toolsJSON)),
		PrefixHash:       shortHash(map[string]any{"system": systemText, "tools": string(toolsJSON)}),
		ToolSchemaTokens: estimateTokens(string(toolsJSON)),
		MessageCount:     len(req.Messages),
		ToolCount:        len(req.Tools),
	}
}

func (t *Tracker) Observe(key string, shape Shape, usage providers.NormalizedUsage) Diagnostics {
	t.mu.Lock()
	defer t.mu.Unlock()
	prev, ok := t.shapes[key]
	t.shapes[key] = shape
	reasons := changeReasons(prev, shape, ok)
	return Diagnostics{
		Shape:                shape,
		PrefixChanged:        len(reasons) > 0,
		PrefixChangeReasons:  reasons,
		CacheHitTokens:       usage.CachedInputTokens,
		CacheMissTokens:      usage.FreshInputTokens,
		CacheHitRatePermille: cacheHitRatePermille(usage.CachedInputTokens, usage.FreshInputTokens),
	}
}

func LogAttrs(d Diagnostics) []slog.Attr {
	attrs := []slog.Attr{
		slog.String("prefix_hash", d.Shape.PrefixHash),
		slog.String("system_hash", d.Shape.SystemHash),
		slog.String("tools_hash", d.Shape.ToolsHash),
		slog.Int("tool_schema_tokens", d.Shape.ToolSchemaTokens),
		slog.Int("message_count", d.Shape.MessageCount),
		slog.Int("tool_count", d.Shape.ToolCount),
		slog.Bool("prefix_changed", d.PrefixChanged),
		slog.Int("cache_hit_rate_permille", d.CacheHitRatePermille),
	}
	if len(d.PrefixChangeReasons) > 0 {
		attrs = append(attrs, slog.String("prefix_change_reasons", strings.Join(d.PrefixChangeReasons, ",")))
	}
	return attrs
}

func changeReasons(prev Shape, cur Shape, hasPrev bool) []string {
	if !hasPrev {
		return nil
	}
	var reasons []string
	if prev.SystemHash != cur.SystemHash {
		reasons = append(reasons, "system")
	}
	if prev.ToolsHash != cur.ToolsHash {
		reasons = append(reasons, "tools")
	}
	return reasons
}

func systemPrompt(messages []providers.ChatMessage) string {
	var b strings.Builder
	for _, message := range messages {
		if message.Role != "system" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(contentText(message.Content))
	}
	return b.String()
}

func contentText(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		data, err := json.Marshal(canonicalValue(v))
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(data)
	}
}

func canonicalValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(v))
		for _, key := range keys {
			out[key] = canonicalValue(v[key])
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, canonicalValue(item))
		}
		return out
	default:
		return value
	}
}

func shortHash(value any) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:8])
}

func estimateTokens(text string) int {
	if text == "" {
		return 0
	}
	return len(text) / 4
}

func cacheHitRatePermille(hit int, miss int) int {
	total := hit + miss
	if total <= 0 {
		return 0
	}
	return hit * 1000 / total
}
