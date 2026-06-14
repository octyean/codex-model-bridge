package capabilities

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	base "codex-bridge/internal/capabilities"
)

type OpenAIVisionProvider struct {
	BaseURL string
	APIKey  string
	Model   string
	client  *http.Client
}

func NewOpenAIVisionProvider(baseURL string, apiKey string, model string, client *http.Client) *OpenAIVisionProvider {
	return &OpenAIVisionProvider{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		Model:   model,
		client:  client,
	}
}

func (p *OpenAIVisionProvider) Analyze(ctx context.Context, input base.ImageInput, mode string) (base.VisionResult, error) {
	prompt := "Describe the image accurately. Include visible text if any."
	if mode == "ocr" {
		prompt = "Extract visible text from the image. If no text is visible, describe that clearly."
	}
	body := map[string]any{
		"model": p.Model,
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{
				{"type": "text", "text": prompt},
				{"type": "image_url", "image_url": imageURLPayload(input)},
			},
		}},
		"stream": false,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return base.VisionResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return base.VisionResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return base.VisionResult{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return base.VisionResult{}, fmt.Errorf("vision status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return base.VisionResult{}, err
	}
	if len(decoded.Choices) == 0 {
		return base.VisionResult{}, fmt.Errorf("vision provider returned no choices")
	}
	return base.VisionResult{Text: strings.TrimSpace(decoded.Choices[0].Message.Content)}, nil
}

func imageURLPayload(input base.ImageInput) any {
	switch input.Detail {
	case "low", "high", "auto":
		return map[string]any{"url": input.URL, "detail": input.Detail}
	default:
		return input.URL
	}
}
