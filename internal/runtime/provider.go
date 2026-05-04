package runtime

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type ProviderConfig struct {
	Provider          string
	Model             string
	KeySource         string
	APIKey            string
	EnableWebResearch bool
	// MaxOutputTokens caps visible generation per conversation turn (Gemini generationConfig.maxOutputTokens).
	// When zero, research_gemini.go uses defaults that assume Flash-sized budgets; Pro/thinking models may need higher limits via env.
	MaxOutputTokens        int
	MaxOutputTokensWithWeb int
}

func LoadProviderConfigFromEnv() (ProviderConfig, error) {
	provider := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		os.Getenv("INFERENCE_PROVIDER"),
		"gemini",
	)))
	if provider != "gemini" {
		return ProviderConfig{}, fmt.Errorf("unsupported inference provider %q: this implementation is gemini-only", provider)
	}

	model := strings.TrimSpace(firstNonEmpty(
		os.Getenv("GEMINI_MODEL"),
		"gemini-3-flash-preview",
	))

	byok := strings.TrimSpace(os.Getenv("BYOK_GEMINI_API_KEY"))
	defaultKey := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))

	key := defaultKey
	keySource := "default_key"
	if byok != "" {
		key = byok
		keySource = "byok_key"
	}
	if strings.TrimSpace(key) == "" {
		return ProviderConfig{}, fmt.Errorf("missing Gemini API key: set GEMINI_API_KEY (or BYOK_GEMINI_API_KEY)")
	}

	maxOut := parseIntEnvPositiveOrZero(os.Getenv("GEMINI_CONV_MAX_OUTPUT_TOKENS"))
	maxOutWeb := parseIntEnvPositiveOrZero(os.Getenv("GEMINI_CONV_MAX_OUTPUT_TOKENS_WITH_SEARCH"))

	return ProviderConfig{
		Provider:               provider,
		Model:                  model,
		KeySource:              keySource,
		APIKey:                 key,
		EnableWebResearch:      parseBoolEnv(os.Getenv("GEMINI_ENABLE_WEB_RESEARCH"), true),
		MaxOutputTokens:        maxOut,
		MaxOutputTokensWithWeb: maxOutWeb,
	}, nil
}

func parseIntEnvPositiveOrZero(raw string) int {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func parseBoolEnv(raw string, def bool) bool {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return def
	}
	switch s {
	case "1", "true", "t", "yes", "y", "on":
		return true
	case "0", "false", "f", "no", "n", "off":
		return false
	default:
		return def
	}
}
