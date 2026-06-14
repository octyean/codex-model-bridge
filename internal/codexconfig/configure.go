package codexconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Settings struct {
	CodexHome           string
	ProviderName        string
	ProviderDisplayName string
	BaseURL             string
	ModelCatalogPath    string
	DefaultModel        string
	BearerToken         string
}

type Result struct {
	ConfigPath string
	BackupPath string
}

func Configure(settings Settings) (Result, error) {
	settings = withDefaults(settings)
	configPath := filepath.Join(settings.CodexHome, "config.toml")
	if err := os.MkdirAll(settings.CodexHome, 0o700); err != nil {
		return Result{}, fmt.Errorf("create codex home: %w", err)
	}
	before, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return Result{}, fmt.Errorf("read codex config: %w", err)
	}
	backupPath := ""
	if len(before) > 0 {
		backupPath = configPath + ".bak-" + time.Now().Format("20060102150405")
		if err := os.WriteFile(backupPath, before, 0o600); err != nil {
			return Result{}, fmt.Errorf("write codex config backup: %w", err)
		}
	}
	after := updateConfigText(string(before), settings)
	if err := os.WriteFile(configPath, []byte(after), 0o600); err != nil {
		return Result{}, fmt.Errorf("write codex config: %w", err)
	}
	return Result{ConfigPath: configPath, BackupPath: backupPath}, nil
}

func withDefaults(settings Settings) Settings {
	if strings.TrimSpace(settings.CodexHome) == "" {
		settings.CodexHome = os.Getenv("CODEX_HOME")
	}
	if strings.TrimSpace(settings.CodexHome) == "" {
		if home, err := os.UserHomeDir(); err == nil {
			settings.CodexHome = filepath.Join(home, ".codex")
		}
	}
	if strings.TrimSpace(settings.ProviderDisplayName) == "" {
		settings.ProviderDisplayName = "Codex Bridge"
	}
	return settings
}

func updateConfigText(input string, settings Settings) string {
	lines := splitLines(input)
	settings.ProviderName = effectiveProviderName(lines, settings.ProviderName)
	if !hasTopLevelValue(lines, "model_provider") && !hasTopLevelValue(lines, "model") {
		lines = setTopLevelValue(lines, "model_provider", settings.ProviderName)
		lines = setTopLevelValue(lines, "model", settings.DefaultModel)
	}
	lines = setTopLevelValue(lines, "model_catalog_json", settings.ModelCatalogPath)
	lines = setProviderTable(lines, settings)
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
}

func effectiveProviderName(lines []string, configured string) string {
	if name := strings.TrimSpace(configured); name != "" {
		return name
	}
	if name := topLevelStringValue(lines, "model_provider"); name != "" {
		return name
	}
	return "codex_bridge"
}

func splitLines(input string) []string {
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.TrimRight(input, "\n")
	if input == "" {
		return nil
	}
	return strings.Split(input, "\n")
}

func setTopLevelValue(lines []string, key string, value string) []string {
	line := key + " = " + strconv.Quote(value)
	tableIndex := len(lines)
	inTopLevel := true
	for i, current := range lines {
		trimmed := strings.TrimSpace(current)
		if inTopLevel && isTableHeader(trimmed) {
			tableIndex = i
			inTopLevel = false
		}
		if inTopLevel && keyOf(trimmed) == key {
			lines[i] = line
			return lines
		}
	}
	return insertAt(lines, tableIndex, line)
}

func hasTopLevelValue(lines []string, key string) bool {
	for _, current := range lines {
		trimmed := strings.TrimSpace(current)
		if isTableHeader(trimmed) {
			return false
		}
		if keyOf(trimmed) == key {
			return true
		}
	}
	return false
}

func topLevelStringValue(lines []string, key string) string {
	for _, current := range lines {
		trimmed := strings.TrimSpace(current)
		if isTableHeader(trimmed) {
			return ""
		}
		if keyOf(trimmed) != key {
			continue
		}
		index := strings.Index(trimmed, "=")
		if index < 0 {
			return ""
		}
		value := strings.TrimSpace(trimmed[index+1:])
		unquoted, err := strconv.Unquote(value)
		if err == nil {
			return strings.TrimSpace(unquoted)
		}
		return strings.TrimSpace(value)
	}
	return ""
}

func setProviderTable(lines []string, settings Settings) []string {
	header := "[model_providers." + settings.ProviderName + "]"
	values := map[string]string{
		"name":                      settings.ProviderDisplayName,
		"base_url":                  settings.BaseURL,
		"wire_api":                  "responses",
		"experimental_bearer_token": settings.BearerToken,
	}
	start := -1
	end := len(lines)
	for i, current := range lines {
		trimmed := strings.TrimSpace(current)
		if trimmed == header {
			start = i
			continue
		}
		if start >= 0 && i > start && isTableHeader(trimmed) {
			end = i
			break
		}
	}
	if start < 0 {
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, header)
		start = len(lines) - 1
		end = len(lines)
	}
	for i := start + 1; i < end; {
		if keyOf(strings.TrimSpace(lines[i])) == "requires_openai_auth" {
			lines = append(lines[:i], lines[i+1:]...)
			end--
			continue
		}
		i++
	}
	order := []string{"name", "base_url", "wire_api", "experimental_bearer_token"}
	for _, key := range order {
		line := key + " = " + strconv.Quote(values[key])
		updated := false
		for i := start + 1; i < end; i++ {
			if keyOf(strings.TrimSpace(lines[i])) == key {
				lines[i] = line
				updated = true
				break
			}
		}
		if !updated {
			lines = insertAt(lines, end, line)
			end++
		}
	}
	return lines
}

func insertAt(lines []string, index int, value string) []string {
	lines = append(lines, "")
	copy(lines[index+1:], lines[index:])
	lines[index] = value
	return lines
}

func keyOf(line string) string {
	if line == "" || strings.HasPrefix(line, "#") {
		return ""
	}
	index := strings.Index(line, "=")
	if index < 0 {
		return ""
	}
	return strings.TrimSpace(line[:index])
}

func isTableHeader(line string) bool {
	return strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]")
}
