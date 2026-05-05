package runtime

import (
	"strings"
	"testing"
)

func TestFormatHumansTermsAcceptThankYou_OnboardingPitch(t *testing.T) {
	s := FormatHumansTermsAcceptThankYou("")
	if !strings.Contains(s, "#onboarding") {
		t.Fatalf("missing onboarding: %q", s)
	}
	if !strings.Contains(s, "#ideas") {
		t.Fatalf("missing ideas channel: %q", s)
	}
}

func TestFormatHumansTermsAcceptThankYou_WithEpilogue(t *testing.T) {
	s := FormatHumansTermsAcceptThankYou("Created: <#C123|grant-llc>")
	if !strings.Contains(s, "Created:") || !strings.Contains(s, "#onboarding") {
		t.Fatalf("unexpected: %q", s)
	}
}
