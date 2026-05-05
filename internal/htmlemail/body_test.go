package htmlemail

import (
	"html"
	"strings"
	"testing"
)

func TestNormalizeFragmentForSend_plainBulletsAndParagraphs(t *testing.T) {
	t.Parallel()
	got := NormalizeFragmentForSend("- alpha\n- beta\n\nNext paragraph.")
	if !strings.Contains(got, "<ul>") || !strings.Contains(got, "<li>") {
		t.Fatalf("expected list markup, got %q", got)
	}
	if !strings.Contains(got, "<p>") {
		t.Fatalf("expected paragraph, got %q", got)
	}
}

func TestNormalizeFragmentForSend_angleBracketLooksLikeHTMLSanitized(t *testing.T) {
	t.Parallel()
	// Angle brackets match the HTML detector; unsafe tags are stripped, remainder kept.
	got := NormalizeFragmentForSend(`Hello <world> plus <script>x</script>`)
	if strings.Contains(strings.ToLower(got), "script") {
		t.Fatalf("script should be stripped: %q", got)
	}
}

func TestNormalizeFragmentForSend_stripsScript(t *testing.T) {
	t.Parallel()
	raw := `<p>Hi</p><script>alert(1)</script><p>Bye</p>`
	got := NormalizeFragmentForSend(raw)
	if strings.Contains(strings.ToLower(got), "script") {
		t.Fatalf("script leakage: %q", got)
	}
}

func TestBuildBrandedEmailInner_ctaTable(t *testing.T) {
	t.Parallel()
	got := BuildBrandedEmailInner("<p>x</p>", "Go", "https://example.com/path")
	if !strings.Contains(got, `bgcolor="#0a0a0a"`) {
		t.Fatalf("expected branded CTA table, got %q", got)
	}
	if !strings.Contains(got, html.EscapeString("https://example.com/path")) {
		t.Fatalf("missing escaped href")
	}
}
