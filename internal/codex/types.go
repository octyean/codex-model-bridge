package codex

import "encoding/json"

type ResponsesRequest struct {
	Model             string          `json:"model"`
	Instructions      string          `json:"instructions"`
	Input             json.RawMessage `json:"input"`
	Tools             []ResponseTool  `json:"tools"`
	ToolChoice        any             `json:"tool_choice"`
	ParallelToolCalls bool            `json:"parallel_tool_calls"`
	Stream            bool            `json:"stream"`
	Raw               map[string]any  `json:"-"`
}

func (r *ResponsesRequest) UnmarshalJSON(data []byte) error {
	type alias ResponsesRequest
	var base alias
	if err := json.Unmarshal(data, &base); err != nil {
		return err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*r = ResponsesRequest(base)
	r.Raw = raw
	return nil
}

type ResponseTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Raw         map[string]any  `json:"-"`
}

func (t *ResponseTool) UnmarshalJSON(data []byte) error {
	type alias ResponseTool
	var base alias
	if err := json.Unmarshal(data, &base); err != nil {
		return err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*t = ResponseTool(base)
	t.Raw = raw
	return nil
}

type ResponseObject struct {
	ID        string         `json:"id"`
	Object    string         `json:"object"`
	CreatedAt int64          `json:"created_at"`
	Model     string         `json:"model"`
	Status    string         `json:"status"`
	Output    []ResponseItem `json:"output"`
	Usage     any            `json:"usage,omitempty"`
}

type ResponseItem map[string]any

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}
