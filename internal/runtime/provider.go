package runtime

import (
	"fmt"
	"os"
	"strings"
)

type ProviderConfig struct {
	Provider          string
	Model             string
	KeySource         string
	APIKey            string
	EnableWebResearch bool
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
		"gemini-2.5-pro",
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

	return ProviderConfig{
		Provider:          provider,
		Model:             model,
		KeySource:         keySource,
		APIKey:            key,
		EnableWebResearch: parseBoolEnv(os.Getenv("GEMINI_ENABLE_WEB_RESEARCH"), true),
	}, nil
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
