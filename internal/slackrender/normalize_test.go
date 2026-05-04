package slackrender

import (
	"strings"
	"testing"
)

func TestNormalizeModelTextToSlackMrkdwn(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"plain", "plain"},
		{"**bold**", "*bold*"},
		{"pre **bold** post", "pre *bold* post"},
		{"**a** and **b**", "*a* and *b*"},
		{"__italic__", "_italic_"},
	}
	for _, tt := range tests {
		got := NormalizeModelTextToSlackMrkdwn(tt.in)
		if got != tt.want {
			t.Errorf("NormalizeModelTextToSlackMrkdwn(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNormalizeMarkdownListLinesToSlackBullets(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"no list\nhere", "no list\nhere"},
		{"- one\n- two", "• one\n• two"},
		{"  - nested\n  - two", "  • nested\n  • two"},
		{"+ plus\n+side", "• plus\n+side"}, // '+' must be followed by space/tab
		{"* bold opener*", "* bold opener*"},
		{"* item one\n* item two", "• item one\n• item two"},
		{"*   spaced item", "• spaced item"},
		{"• already\n- converted", "• already\n• converted"},
		{"Intro\n\n- first\n\nMore\n\n* second kind", "Intro\n\n• first\n\nMore\n\n• second kind"},
	}
	for _, tt := range tests {
		got := NormalizeMarkdownListLinesToSlackBullets(tt.in)
		if got != tt.want {
			t.Errorf("NormalizeMarkdownListLinesToSlackBullets(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestPlainNotificationFallback(t *testing.T) {
	s := strings.Repeat("x", 400)
	got := PlainNotificationFallback(s)
	if len(got) != 300 {
		t.Fatalf("expected length 300, got %d", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected ellipsis truncation, got %q", got)
	}
}
