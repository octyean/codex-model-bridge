package adapters

import (
	"encoding/json"
	"sort"
	"strings"

	"codex-bridge/internal/providers"
)

const (
	DefaultName  = "default"
	DeepSeekName = "deepseek"
	MimoName     = "mimo"
)

type Capabilities struct {
	InputModalities             []string
	SupportsImageDetailOriginal bool
	SupportsSearchTool          bool
	ExperimentalSupportedTools  []string
}

type Adapter interface {
	Name() string
	Capabilities() Capabilities
	PrepareChatRequest(providers.ChatCompletionRequest) providers.ChatCompletionRequest
	CustomToolDescription(tool ToolDescriptor) string
	NormalizeCustomInput(name string, input string) string
	FormatToolOutput(tool ToolDescriptor, output string) string
}

type ToolDescriptor struct {
	Name         string
	Kind         string
	InputMode    string
	SideEffect   string
	OriginalType string
	Description  string
	Raw          map[string]any
}

func DefaultToolOutput(_ ToolDescriptor, output string) string {
	return output
}

func ForcedToolName(toolChoice any) string {
	obj, ok := toolChoice.(map[string]any)
	if !ok {
		return ""
	}
	toolType, _ := obj["type"].(string)
	if toolType != "function" {
		return ""
	}
	if name, _ := obj["name"].(string); name != "" {
		return name
	}
	function, ok := obj["function"].(map[string]any)
	if !ok {
		return ""
	}
	name, _ := function["name"].(string)
	return name
}

func Normalize(name string) string {
	value := strings.TrimSpace(strings.ToLower(name))
	if value == "" {
		return DefaultName
	}
	return value
}

func Known(name string) bool {
	_, ok := registry[Normalize(name)]
	return ok
}

func Get(name string) Adapter {
	if adapter, ok := registry[Normalize(name)]; ok {
		return adapter
	}
	return registry[DefaultName]
}

func HasImageInput(caps Capabilities) bool {
	for _, modality := range caps.InputModalities {
		if strings.EqualFold(strings.TrimSpace(modality), "image") {
			return true
		}
	}
	return false
}

func NormalizeInputModalities(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		modality := strings.ToLower(strings.TrimSpace(value))
		if modality == "" || seen[modality] {
			continue
		}
		seen[modality] = true
		out = append(out, modality)
	}
	if len(out) == 0 {
		return []string{"text"}
	}
	return out
}

func normalizeApplyPatchInput(input string) string {
	text := strings.ReplaceAll(input, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		if len(lines) >= 2 {
			if strings.HasPrefix(strings.TrimSpace(lines[0]), "```") {
				lines = lines[1:]
			}
			if strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
				lines = lines[:len(lines)-1]
			}
			text = strings.TrimSpace(strings.Join(lines, "\n"))
		}
	}
	return text
}

func canonicalJSON(value any) string {
	data, err := json.Marshal(canonicalValue(value))
	if err != nil {
		return ""
	}
	return string(data)
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

var registry = map[string]Adapter{
	DefaultName:  defaultAdapter{},
	DeepSeekName: deepSeekAdapter{},
	MimoName:     mimoAdapter{},
}
