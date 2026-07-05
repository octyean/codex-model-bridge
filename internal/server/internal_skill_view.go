package server

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"codex-bridge/internal/providers"
	"codex-bridge/internal/tools"
)

func isSkillViewTool(entry tools.Entry) bool {
	return entry.OriginalName() == "skill_view" || strings.HasSuffix(entry.Name(), "__skill_view")
}

func skillViewOutput(messages []providers.ChatMessage, arguments string) (string, bool) {
	var args struct {
		Name     string `json:"name"`
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", false
	}
	if strings.TrimSpace(args.Name) == "" {
		return "", false
	}
	skillPath, alreadyLoaded, ok := skillPathFromMessages(messages, args.Name)
	if !ok {
		return "", false
	}
	if strings.TrimSpace(args.FilePath) == "" {
		if alreadyLoaded {
			return "SKILL_ALREADY_IN_CONTEXT\nname: " + args.Name + "\nsource: existing <skill> block in conversation", true
		}
		return localFileReadOutput(skillPath), true
	}
	path, ok := skillLinkedFilePath(skillPath, args.FilePath)
	if !ok {
		return "SKILL_VIEW_LOCAL_FILE_REJECTED\nname: " + args.Name + "\nfile_path: " + args.FilePath, true
	}
	return localFileReadOutput(path), true
}

func skillPathFromMessages(messages []providers.ChatMessage, name string) (string, bool, bool) {
	var all strings.Builder
	for _, message := range messages {
		text := messageContentText(message.Content)
		if path, ok := skillPathFromText(text, name); ok {
			return path, true, true
		}
		all.WriteString(text)
		all.WriteByte('\n')
	}
	if path, ok := skillPathFromSkillList(all.String(), name); ok {
		return path, false, true
	}
	return "", false, false
}

func skillPathFromText(text string, name string) (string, bool) {
	for {
		start := strings.Index(text, "<skill>")
		if start < 0 {
			return "", false
		}
		text = text[start+len("<skill>"):]
		end := strings.Index(text, "</skill>")
		block := text
		if end >= 0 {
			block = text[:end]
		}
		if strings.TrimSpace(tagText(block, "name")) == name {
			path := strings.TrimSpace(tagText(block, "path"))
			return path, path != ""
		}
		if end < 0 {
			return "", false
		}
		text = text[end+len("</skill>"):]
	}
}

func tagText(text string, tag string) string {
	startTag := "<" + tag + ">"
	endTag := "</" + tag + ">"
	start := strings.Index(text, startTag)
	if start < 0 {
		return ""
	}
	text = text[start+len(startTag):]
	end := strings.Index(text, endTag)
	if end < 0 {
		return ""
	}
	return text[:end]
}

func skillPathFromSkillList(text string, name string) (string, bool) {
	roots := skillRootsFromText(text)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- "+name+": ") {
			continue
		}
		ref, ok := skillFileRef(line)
		if !ok {
			return "", false
		}
		return resolveSkillFileRef(ref, roots)
	}
	return "", false
}

func skillRootsFromText(text string) map[string]string {
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

func skillFileRef(line string) (string, bool) {
	start := strings.LastIndex(line, "(file:")
	if start < 0 {
		return "", false
	}
	rest := strings.TrimSpace(line[start+len("(file:"):])
	end := strings.Index(rest, ")")
	if end < 0 {
		return "", false
	}
	ref := strings.TrimSpace(rest[:end])
	return ref, ref != ""
}

func resolveSkillFileRef(ref string, roots map[string]string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", false
	}
	if filepath.IsAbs(ref) {
		return filepath.Clean(ref), true
	}
	alias, rel, ok := strings.Cut(ref, "/")
	if !ok {
		return "", false
	}
	root := roots[alias]
	if root == "" {
		return "", false
	}
	return filepath.Clean(filepath.Join(root, rel)), true
}

func skillLinkedFilePath(skillPath string, filePath string) (string, bool) {
	root := filepath.Dir(resolveWorkspacePath(skillPath, ""))
	candidate := filepath.Clean(filePath)
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Clean(filepath.Join(root, filePath))
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return candidate, true
}

func messageContentText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		var b strings.Builder
		for _, item := range value {
			if obj, ok := item.(map[string]any); ok {
				if text, ok := obj["text"].(string); ok {
					b.WriteString(text)
				}
			}
		}
		return b.String()
	case []map[string]any:
		var b strings.Builder
		for _, obj := range value {
			if text, ok := obj["text"].(string); ok {
				b.WriteString(text)
			}
		}
		return b.String()
	default:
		return ""
	}
}
