package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
)

func DefaultConfigText(homeDir string) (string, error) {
	catalogPath := filepath.ToSlash(filepath.Join(homeDir, ".codex", "models.codex-bridge.json"))
	localToken, err := RandomLocalToken()
	if err != nil {
		return "", fmt.Errorf("generate local token: %w", err)
	}
	return fmt.Sprintf(`[server]
listen = "127.0.0.1:8787"

[codex]
model_catalog_path = "%s"
default_model = "deepseek-v4-flash"
local_token = "%s"

[model_discovery]
enabled = true
mode = "merge"

[extensions.network]
proxy_url = ""

[capabilities.search]
enabled = false
providers = ["duckduckgo_html", "jina"]
max_results = 5

[search_providers.duckduckgo_html]
type = "duckduckgo_html"

[capabilities.vision]
enabled = false
provider = "jina_vlm"
mode = "describe"

[search_providers.jina]
type = "jina"
search_base_url = "https://s.jina.ai"
reader_base_url = "https://r.jina.ai"
api_key = ""

[vision_providers.jina_vlm]
type = "openai_chat_compatible_vision"
base_url = "https://api-beta-vlm.jina.ai/v1"
api_key = "jina_xxx"
model = "jina-vlm"

[providers.deepseek]
type = "openai_compatible"
base_url = "https://api.deepseek.com"
api_key = "sk-xxx"
profile = "deepseek"
protocol = "chat_completions"

[models.deepseek-v4-flash]
display_name = "DeepSeek V4 Flash"
provider = "deepseek"
upstream_model = "deepseek-v4-flash"
context_window = 64000
supports_parallel_tool_calls = true
apply_patch_tool_type = "freeform"
`, catalogPath, localToken), nil
}

func RandomLocalToken() (string, error) {
	var data [20]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}
