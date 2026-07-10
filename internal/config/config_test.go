package config

import (
	"reflect"
	"testing"
)

func TestReasoningLevelsForModelAddsMaxForGPT56Upstream(t *testing.T) {
	levels := reasoningLevelsForModel(ModelConfig{UpstreamModel: "gpt-5.6-sol"}, true)
	efforts := make([]string, 0, len(levels))
	for _, level := range levels {
		efforts = append(efforts, level.Effort)
	}

	want := []string{"low", "medium", "high", "xhigh", "max"}
	if !reflect.DeepEqual(efforts, want) {
		t.Fatalf("reasoning efforts = %v, want %v", efforts, want)
	}
}

func TestReasoningLevelsForModelKeepsMaxOffNonGPT56Models(t *testing.T) {
	levels := reasoningLevelsForModel(ModelConfig{UpstreamModel: "gpt-5.4-mini"}, true)
	efforts := make([]string, 0, len(levels))
	for _, level := range levels {
		efforts = append(efforts, level.Effort)
	}

	want := []string{"low", "medium", "high", "xhigh"}
	if !reflect.DeepEqual(efforts, want) {
		t.Fatalf("reasoning efforts = %v, want %v", efforts, want)
	}
}
