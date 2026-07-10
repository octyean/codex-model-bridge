package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"codex-bridge/internal/codex"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func enforceStructuredOutput(items []codex.ResponseItem, responseFormat any) []codex.ResponseItem {
	_ = normalizeStructuredOutputItems(items, responseFormat)
	return items
}

func normalizeStructuredOutputItems(items []codex.ResponseItem, responseFormat any) error {
	schemaValue, ok := structuredOutputSchema(responseFormat)
	if !ok {
		return nil
	}
	for _, item := range items {
		if item["type"] != "message" {
			continue
		}
		text := strings.TrimSpace(messageOutputText(item))
		if text == "" {
			return fmt.Errorf("structured output message is empty")
		}
		value, err := decodeStructuredOutput(text)
		if err != nil && titleOnlyResponseFormat(responseFormat) {
			value = map[string]any{"title": cleanTitle(text)}
			err = nil
		}
		if err != nil {
			return fmt.Errorf("structured output is not valid JSON: %w", err)
		}
		if err := validateStructuredOutput(schemaValue, value); err != nil {
			return fmt.Errorf("structured output does not match schema: %w", err)
		}
		normalized, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode structured output: %w", err)
		}
		setMessageOutputText(item, string(normalized))
		return nil
	}
	return fmt.Errorf("structured output response has no assistant message")
}

func structuredOutputSchema(responseFormat any) (any, bool) {
	format, ok := responseFormat.(map[string]any)
	if !ok || format["type"] != "json_schema" {
		return nil, false
	}
	jsonSchema, ok := format["json_schema"].(map[string]any)
	if !ok || jsonSchema["schema"] == nil {
		return nil, false
	}
	return jsonSchema["schema"], true
}

func validateStructuredOutput(schemaValue any, value any) error {
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	if err := compiler.AddResource("schema.json", schemaValue); err != nil {
		return fmt.Errorf("load schema: %w", err)
	}
	schema, err := compiler.Compile("schema.json")
	if err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}
	return schema.Validate(value)
}

func decodeStructuredOutput(text string) (any, error) {
	text = stripJSONFence(text)
	decoder := json.NewDecoder(bytes.NewBufferString(text))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, err
	}
	return value, nil
}

func stripJSONFence(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "```") {
		return text
	}
	firstLineEnd := strings.IndexByte(text, '\n')
	if firstLineEnd < 0 {
		return text
	}
	text = strings.TrimSpace(text[firstLineEnd+1:])
	if strings.HasSuffix(text, "```") {
		text = strings.TrimSpace(strings.TrimSuffix(text, "```"))
	}
	return text
}

func titleOnlyResponseFormat(responseFormat any) bool {
	schemaValue, ok := structuredOutputSchema(responseFormat)
	if !ok {
		return false
	}
	schema, ok := schemaValue.(map[string]any)
	if !ok || schema["type"] != "object" {
		return false
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok || len(properties) != 1 {
		return false
	}
	_, ok = properties["title"]
	return ok && requiresTitle(schema["required"])
}

func requiresTitle(value any) bool {
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if item == "title" {
			return true
		}
	}
	return false
}

func cleanTitle(title string) string {
	title = stripJSONFence(title)
	var obj map[string]string
	if err := json.Unmarshal([]byte(title), &obj); err == nil {
		if inner := strings.TrimSpace(obj["title"]); inner != "" {
			return inner
		}
	}
	return strings.TrimSpace(title)
}
