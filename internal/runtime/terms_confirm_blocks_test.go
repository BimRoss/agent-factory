package runtime

import (
	"strings"
	"testing"
)

func TestTermsConfirmationRoundTrip(t *testing.T) {
	a := TermsSkillConfirmationAction{
		Decision:      skillConfirmationDecisionCancel,
		Channel:       "C0ABCDEF",
		RequestUserID: "U011ABCDE",
		ThreadTS:      "1777957154.827629",
	}
	raw := encodeTermsSkillConfirmationValue(a)
	got, ok := decodeTermsSkillConfirmationValue(raw)
	if !ok {
		t.Fatal("decode failed")
	}
	if got != a {
		t.Fatalf("round trip mismatch: %+v vs %+v", got, a)
	}
}

func TestHumansTermsThreadSummaryMrkdwnUsesLinkedTermsWord(t *testing.T) {
	s := HumansTermsThreadSummaryMrkdwn("https://example.com")
	if !strings.Contains(s, "https://example.com/terms") || !strings.Contains(s, "*terms*") {
		t.Fatalf("unexpected summary %q", s)
	}
}
