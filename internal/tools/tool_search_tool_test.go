package tools

import (
	"strings"
	"testing"
)

func TestToolSearchDescriptionCompactsOneMCPBoilerplate(t *testing.T) {
	original := `# 1MCP - Model Context Protocol Proxy

You are interacting with 1MCP.

## How 1MCP Works

` + strings.Repeat("Repeated architecture explanation and generic routing advice.\n", 20) + `

## Currently Connected Servers

codegraph
context7

## Available Capabilities

` + strings.Repeat("Repeated examples and generic capabilities.\n", 20) + `

## Server-Specific Instructions

<codegraph>
Use codegraph only when indexed.
</codegraph>

<context7>
Use context7 for current library documentation.
</context7>

## Tips for Using 1MCP

More repeated generic advice.`

	description := toolSearchDescription(original)
	for _, expected := range []string{
		"Currently Connected Servers",
		"codegraph",
		"context7",
		"{server}_1mcp_{tool}",
		"Use codegraph only when indexed.",
		"Use context7 for current library documentation.",
		"Use tool_search only when the needed callable tool is not already visible.",
	} {
		if !strings.Contains(description, expected) {
			t.Fatalf("description missing %q:\n%s", expected, description)
		}
	}
	for _, removed := range []string{"How 1MCP Works", "Available Capabilities", "Repeated examples", "Tips for Using 1MCP"} {
		if strings.Contains(description, removed) {
			t.Fatalf("description retained %q:\n%s", removed, description)
		}
	}
	if len(description) >= len(original) {
		t.Fatalf("description was not compacted: original=%d compacted=%d", len(original), len(description))
	}
}

func TestToolSearchDescriptionKeepsUnknownDescriptions(t *testing.T) {
	original := "Provider-specific deferred tool lookup instructions."
	description := toolSearchDescription(original)
	if !strings.HasPrefix(description, original+"\n") {
		t.Fatalf("ordinary description changed:\n%s", description)
	}
}
