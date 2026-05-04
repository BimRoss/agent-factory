package slackrender

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestChunkMrkdwnSectionText_chunksAreValidUTF8(t *testing.T) {
	// Multi-byte runes (no splitting mid-codepoint).
	part := "répété "
	s := strings.Repeat(part, 900)
	chunks := ChunkMrkdwnSectionText(s, 3000)
	for _, c := range chunks {
		if !utf8.ValidString(c) {
			t.Fatalf("invalid UTF-8 in chunk: %q", c)
		}
	}
	var total int
	for _, c := range chunks {
		total += utf8.RuneCountInString(c)
	}
	if total < utf8.RuneCountInString(s)-200 {
		t.Fatalf("rune count dropped unexpectedly: orig=%d sum=%d",
			utf8.RuneCountInString(s), total)
	}
}

func TestChunkMrkdwnSectionText_splitsLongLineAtWordBoundary(t *testing.T) {
	word := strings.Repeat("x", 100)
	s := word + " " + word + " " + word
	chunks := ChunkMrkdwnSectionText(s, 150)
	if len(chunks) < 2 {
		t.Fatalf("expected split into multiple chunks, got %d", len(chunks))
	}
	for _, c := range chunks {
		if utf8.RuneCountInString(c) > 150 {
			t.Fatalf("chunk over limit: %d runes", utf8.RuneCountInString(c))
		}
	}
}

func TestChunkMrkdwnSectionText_preservesLongParagraph(t *testing.T) {
	var b strings.Builder
	for b.Len() < 4500 {
		b.WriteString("The quick brown fox jumps. ")
	}
	s := strings.TrimSpace(b.String())
	chunks := ChunkMrkdwnSectionText(s, 3000)
	var total int
	for _, c := range chunks {
		total += utf8.RuneCountInString(c)
	}
	if total < utf8.RuneCountInString(s)-50 {
		t.Fatalf("lost too much text: orig=%d rebuilt~=%d chunks=%d",
			utf8.RuneCountInString(s), total, len(chunks))
	}
}
