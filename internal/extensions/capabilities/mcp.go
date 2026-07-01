package capabilities

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	base "codex-bridge/internal/capabilities"
)

type MCPProvider struct {
	ServerURL     string
	Authorization string
	SearchTool    string
	ReadTool      string
	sessionID     string
	initialized   bool
	mu            sync.Mutex
	client        *http.Client
}

func NewMCPProvider(serverURL string, authorization string, searchTool string, readTool string, client *http.Client) *MCPProvider {
	return &MCPProvider{
		ServerURL:     serverURL,
		Authorization: authorization,
		SearchTool:    defaultString(searchTool, "search_web"),
		ReadTool:      defaultString(readTool, "read_url"),
		client:        client,
	}
}

func (p *MCPProvider) Search(ctx context.Context, query string, maxResults int) (base.SearchResult, error) {
	text, err := p.callTool(ctx, p.SearchTool, map[string]any{"query": query, "max_results": maxResults})
	if err != nil {
		return base.SearchResult{}, err
	}
	return base.SearchResult{Query: query, RawText: trimText(text, 12000)}, nil
}

func (p *MCPProvider) Read(ctx context.Context, targetURL string) (string, error) {
	text, err := p.callTool(ctx, p.ReadTool, map[string]any{"url": targetURL})
	if err != nil {
		return "", err
	}
	return trimText(text, 12000), nil
}

func (p *MCPProvider) callTool(ctx context.Context, name string, args map[string]any) (string, error) {
	if err := p.initialize(ctx); err != nil {
		return "", err
	}
	resp, err := p.post(ctx, map[string]any{
		"jsonrpc": "2.0",
		"id":      time.Now().UnixNano(),
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": args},
	})
	if err != nil {
		return "", err
	}
	if resp.Error != nil {
		return "", fmt.Errorf("mcp tool error: %s", resp.Error.Message)
	}
	return mcpContentText(resp.Result), nil
}

func (p *MCPProvider) initialize(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.initialized {
		return nil
	}
	resp, err := p.post(ctx, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "codex-bridge", "version": "0.2.14"},
		},
	})
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("mcp initialize error: %s", resp.Error.Message)
	}
	p.initialized = true
	return nil
}

type mcpResponse struct {
	Result map[string]any `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (p *MCPProvider) post(ctx context.Context, payload map[string]any) (mcpResponse, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return mcpResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.ServerURL, bytes.NewReader(data))
	if err != nil {
		return mcpResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if p.Authorization != "" {
		req.Header.Set("Authorization", p.Authorization)
	}
	if p.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", p.sessionID)
	}
	httpResp, err := p.client.Do(req)
	if err != nil {
		return mcpResponse{}, err
	}
	defer httpResp.Body.Close()
	if sessionID := httpResp.Header.Get("Mcp-Session-Id"); sessionID != "" {
		p.sessionID = sessionID
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
		return mcpResponse{}, fmt.Errorf("mcp status %d: %s", httpResp.StatusCode, strings.TrimSpace(string(body)))
	}
	return decodeMCPResponse(httpResp)
}

func decodeMCPResponse(resp *http.Response) (mcpResponse, error) {
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			var out mcpResponse
			if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &out); err != nil {
				return mcpResponse{}, err
			}
			return out, nil
		}
		return mcpResponse{}, scanner.Err()
	}
	var out mcpResponse
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

func mcpContentText(result map[string]any) string {
	rawContent, ok := result["content"].([]any)
	if !ok {
		data, _ := json.Marshal(result)
		return string(data)
	}
	parts := make([]string, 0, len(rawContent))
	for _, item := range rawContent {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := obj["text"].(string); ok {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}
