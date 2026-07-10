package config

import "codex-bridge/internal/adapters"

const (
	ExecutionModeNativeResponses    = "native_responses"
	ExecutionModeProjectedResponses = "projected_responses"
	ExecutionModeChatCompletions    = "chat_completions"
)

type ModelExecutionPlan struct {
	Mode                              string
	Protocol                          string
	Profile                           string
	SupportsResponsesOptions          bool
	SupportsResponsesStructuredOutput bool
}

func (cfg *Config) ExecutionPlan(model ModelConfig, provider ProviderConfig) ModelExecutionPlan {
	profile := cfg.ProfileName(model, provider)
	protocol := cfg.UpstreamProtocol(model, provider)
	verified, verifiedOK := cfg.VerifiedCapability(model, provider)
	if model.ExecutionMode == "" && (provider.Protocol == "" || provider.Protocol == "auto") && verifiedOK {
		protocol = verified.RecommendedProtocol
	}
	plan := ModelExecutionPlan{
		Mode:     ExecutionModeChatCompletions,
		Protocol: protocol,
		Profile:  profile,
	}
	if model.ExecutionMode != "" {
		plan.Mode = model.ExecutionMode
		plan.Protocol = protocolForExecutionMode(model.ExecutionMode)
		plan.SupportsResponsesOptions = supportsResponsesOptions(model, profile, plan.Mode, verified, verifiedOK)
		plan.SupportsResponsesStructuredOutput = supportsResponsesStructuredOutput(model, profile, plan.Mode, verified, verifiedOK)
		return plan
	}
	if protocol != "responses" {
		return plan
	}
	if profile == adapters.OpenAIName {
		plan.Mode = ExecutionModeNativeResponses
	} else {
		plan.Mode = ExecutionModeProjectedResponses
	}
	plan.SupportsResponsesOptions = supportsResponsesOptions(model, profile, plan.Mode, verified, verifiedOK)
	plan.SupportsResponsesStructuredOutput = supportsResponsesStructuredOutput(model, profile, plan.Mode, verified, verifiedOK)
	return plan
}

func protocolForExecutionMode(mode string) string {
	if mode == ExecutionModeChatCompletions {
		return "chat_completions"
	}
	return "responses"
}

func supportsResponsesOptions(model ModelConfig, profile string, mode string, verified VerifiedModelCapability, verifiedOK bool) bool {
	if mode == ExecutionModeChatCompletions {
		return false
	}
	if model.SupportsResponsesOptions != nil {
		return *model.SupportsResponsesOptions
	}
	if verifiedOK {
		return verified.ResponsesOptionsOK
	}
	return profile == adapters.OpenAIName || profile == adapters.KimiName || profile == adapters.MimoName
}

func supportsResponsesStructuredOutput(model ModelConfig, profile string, mode string, verified VerifiedModelCapability, verifiedOK bool) bool {
	if mode == ExecutionModeChatCompletions {
		return false
	}
	if model.SupportsResponsesStructuredOutput != nil {
		return *model.SupportsResponsesStructuredOutput
	}
	if verifiedOK {
		return verified.ResponsesStructuredOutputOK
	}
	return profile == adapters.OpenAIName || profile == adapters.KimiName
}
