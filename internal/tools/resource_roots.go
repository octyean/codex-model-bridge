package tools

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"codex-bridge/internal/providers"
)

func ResourceRootsFromMessages(messages []providers.ChatMessage) map[string]string {
	roots := map[string]string{}
	for _, message := range messages {
		for alias, path := range resourceRootsFromText(chatMessageText(message.Content)) {
			roots[alias] = path
		}
	}
	if len(roots) == 0 {
		return nil
	}
	return roots
}

func (ctx Context) ResolveLocalResourcePath(value string, allowWorkspaceRelative bool) string {
	path := resolveLocalResourcePath(value, allowWorkspaceRelative)
	if path == "" || filepath.IsAbs(path) || ctx.Workspace == "" {
		return path
	}
	return filepath.Clean(filepath.Join(ctx.Workspace, path))
}

func resolveLocalResourcePath(value string, allowWorkspaceRelative bool) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if path, ok := strings.CutPrefix(value, "file://"); ok {
		value = path
		if unescaped, err := url.PathUnescape(value); err == nil {
			value = unescaped
		}
	}
	if strings.HasPrefix(value, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			value = filepath.Join(home, value[2:])
		}
	}
	if strings.HasPrefix(value, "$HOME/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			value = filepath.Join(home, value[len("$HOME/"):])
		}
	}
	if filepath.IsAbs(value) || strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../") {
		return filepath.Clean(value)
	}
	if allowWorkspaceRelative && !strings.Contains(value, "://") {
		return value
	}
	return ""
}

func ExpandResourceRootAliases(messages []providers.ChatMessage) []providers.ChatMessage {
	roots := ResourceRootsFromMessages(messages)
	if len(roots) == 0 {
		return messages
	}
	for i := range messages {
		messages[i].Content = expandResourceRootAliasesInContent(messages[i].Content, roots)
	}
	return messages
}

func expandResourceRootAliasesInContent(content any, roots map[string]string) any {
	switch value := content.(type) {
	case string:
		return expandResourceRootAliasesInText(value, roots)
	case []map[string]any:
		for i := range value {
			if text, ok := value[i]["text"].(string); ok {
				value[i]["text"] = expandResourceRootAliasesInText(text, roots)
			}
		}
		return value
	case []any:
		for _, item := range value {
			if obj, ok := item.(map[string]any); ok {
				if text, ok := obj["text"].(string); ok {
					obj["text"] = expandResourceRootAliasesInText(text, roots)
				}
			}
		}
		return value
	default:
		return content
	}
}

func expandResourceRootAliasesInText(text string, roots map[string]string) string {
	text = strings.ReplaceAll(text, "Skill bodies live on disk at the listed paths after expanding the matching alias from `### Skill roots`.", "Skill bodies live on disk at the listed paths.")
	text = strings.ReplaceAll(text, "short path", "file path")
	text = strings.ReplaceAll(text, "short `path`", "`path`")
	text = strings.ReplaceAll(text, "that can be expanded into an absolute path using the skill roots table", "as an absolute path")
	text = strings.ReplaceAll(text, "after expanding the matching alias from `### Skill roots`", "at the listed path")
	text = strings.ReplaceAll(text, "expand the listed `path` with the matching alias from `### Skill roots`, then open", "open the listed `path`")
	text = strings.ReplaceAll(text, "open the listed `path` and read its `SKILL.md` completely", "open and read the listed `path` completely")
	for alias, root := range roots {
		root = strings.TrimRight(root, "/")
		text = strings.ReplaceAll(text, "`"+alias+"` = `"+root+"`", "`"+root+"`")
		text = strings.ReplaceAll(text, alias+"/", root+"/")
	}
	return text
}

func resourceRootsFromText(text string) map[string]string {
	roots := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- `") {
			continue
		}
		rest := strings.TrimPrefix(line, "- `")
		alias, rest, ok := strings.Cut(rest, "`")
		if !ok {
			continue
		}
		_, rest, ok = strings.Cut(rest, "= `")
		if !ok {
			continue
		}
		path, _, ok := strings.Cut(rest, "`")
		if ok && alias != "" && path != "" {
			roots[alias] = path
		}
	}
	return roots
}

func chatMessageText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		var b strings.Builder
		for _, item := range value {
			if obj, ok := item.(map[string]any); ok {
				if text, ok := obj["text"].(string); ok {
					b.WriteString(text)
					b.WriteByte('\n')
				}
			}
		}
		return b.String()
	case []map[string]any:
		var b strings.Builder
		for _, obj := range value {
			if text, ok := obj["text"].(string); ok {
				b.WriteString(text)
				b.WriteByte('\n')
			}
		}
		return b.String()
	default:
		data, _ := json.Marshal(content)
		return string(data)
	}
}
