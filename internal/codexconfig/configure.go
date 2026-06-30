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
	AuthCommand         string
	AuthArgs            []string
	AuthConfigPath      string
	AuthTimeoutMS       int
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
	if strings.TrimSpace(settings.ProviderName) == "" {
		settings.ProviderName = "codex_bridge"
	}
	if settings.AuthTimeoutMS == 0 {
		settings.AuthTimeoutMS = 5000
	}
	return settings
}

func updateConfigText(input string, settings Settings) string {
	lines := splitLines(input)
	if !hasTopLevelValue(lines, "model_provider") && !hasTopLevelValue(lines, "model") {
		lines = setTopLevelValue(lines, "model_provider", settings.ProviderName)
		lines = setTopLevelValue(lines, "model", settings.DefaultModel)
	} else if currentProvider := topLevelValue(lines, "model_provider"); currentProvider != "" && settings.ProviderName == "codex_bridge" && hasProviderTable(lines, currentProvider) {
		settings.ProviderName = currentProvider
	}
	lines = setTopLevelValue(lines, "model_catalog_json", settings.ModelCatalogPath)
	lines = setProviderTable(lines, settings)
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
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
	return topLevelValue(lines, key) != ""
}

func topLevelValue(lines []string, key string) string {
	for _, current := range lines {
		trimmed := strings.TrimSpace(current)
		if isTableHeader(trimmed) {
			return ""
		}
		if keyOf(trimmed) == key {
			return stringValueOf(trimmed)
		}
	}
	return ""
}

func setProviderTable(lines []string, settings Settings) []string {
	header := "[model_providers." + settings.ProviderName + "]"
	values := map[string]string{
		"base_url": settings.BaseURL,
		"wire_api": "responses",
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
		values["name"] = settings.ProviderDisplayName
	}
	for i := start + 1; i < end; {
		switch keyOf(strings.TrimSpace(lines[i])) {
		case "requires_openai_auth", "experimental_bearer_token", "env_key":
			lines = append(lines[:i], lines[i+1:]...)
			end--
			continue
		}
		i++
	}
	if _, ok := values["name"]; !ok && !providerKeyExists(lines, start, end, "name") {
		values["name"] = settings.ProviderDisplayName
	}
	order := []string{"name", "base_url", "wire_api"}
	for _, key := range order {
		if _, ok := values[key]; !ok {
			continue
		}
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
	lines = setAuthTable(lines, settings.ProviderName, settings, end)
	return lines
}

func providerKeyExists(lines []string, start int, end int, key string) bool {
	for i := start + 1; i < end; i++ {
		if keyOf(strings.TrimSpace(lines[i])) == key {
			return true
		}
	}
	return false
}

func hasProviderTable(lines []string, providerName string) bool {
	header := "[model_providers." + providerName + "]"
	for _, line := range lines {
		if strings.TrimSpace(line) == header {
			return true
		}
	}
	return false
}

func setAuthTable(lines []string, providerName string, settings Settings, after int) []string {
	command := strings.TrimSpace(settings.AuthCommand)
	configPath := strings.TrimSpace(settings.AuthConfigPath)
	if command == "" || configPath == "" {
		return lines
	}
	header := "[model_providers." + providerName + ".auth]"
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
		insert := after
		if insert == len(lines) && len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
			insert++
		} else if insert < len(lines) && strings.TrimSpace(lines[insert]) != "" {
			lines = insertAt(lines, insert, "")
			insert++
		}
		lines = insertAt(lines, insert, header)
		start = insert
		end = start + 1
	}
	values := map[string]string{
		"command":             command,
		"timeout_ms":          strconv.Itoa(settings.AuthTimeoutMS),
		"refresh_interval_ms": "0",
	}
	args := settings.AuthArgs
	if len(args) == 0 {
		args = []string{"auth", "token", "--config", configPath}
	}
	argsLine := "args = " + tomlStringArray(args)
	for _, key := range []string{"command"} {
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
	for _, item := range []struct {
		key  string
		line string
	}{
		{"args", argsLine},
		{"timeout_ms", "timeout_ms = " + values["timeout_ms"]},
		{"refresh_interval_ms", "refresh_interval_ms = " + values["refresh_interval_ms"]},
	} {
		updated := false
		for i := start + 1; i < end; i++ {
			if keyOf(strings.TrimSpace(lines[i])) == item.key {
				lines[i] = item.line
				updated = true
				break
			}
		}
		if !updated {
			lines = insertAt(lines, end, item.line)
			end++
		}
	}
	return lines
}

func tomlStringArray(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, strconv.Quote(value))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
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

func stringValueOf(line string) string {
	index := strings.Index(line, "=")
	if index < 0 {
		return ""
	}
	value, err := strconv.Unquote(strings.TrimSpace(line[index+1:]))
	if err != nil {
		return ""
	}
	return value
}

func isTableHeader(line string) bool {
	return strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]")
}
