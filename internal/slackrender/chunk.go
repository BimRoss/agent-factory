package slackrender

import (
	"strings"
	"unicode"
)

// ChunkMrkdwnSectionText splits prose into segments that respect Slack’s per-field character
// limit for mrkdwn section text. Splitting uses rune indices so UTF-8 is never torn mid-codepoint
// (byte slicing at a fixed offset can corrupt text and truncate the rest of a block in clients).
// When a segment must be shortened, we prefer paragraph breaks, single newlines, then whitespace
// boundaries so sentences are not cut mid-word when possible.
func ChunkMrkdwnSectionText(s string, maxRunes int) []string {
	return chunkRunesPreferBreaks(strings.TrimSpace(s), maxRunes)
}

// chunkRunesPreferBreaks splits s into rune-length–bounded chunks with the same boundary rules.
func chunkRunesPreferBreaks(s string, maxRunes int) []string {
	if maxRunes <= 0 {
		maxRunes = 3000
	}
	rs := []rune(s)
	if len(rs) == 0 {
		return nil
	}
	if len(rs) <= maxRunes {
		return []string{s}
	}

	var out []string
	start := 0
	for start < len(rs) {
		remain := len(rs) - start
		if remain <= maxRunes {
			seg := strings.TrimSpace(string(rs[start:]))
			if seg != "" {
				out = append(out, seg)
			}
			break
		}

		winEnd := start + maxRunes
		window := rs[start:winEnd]
		splitLen := pickRuneSplitLength(window, maxRunes)
		if splitLen <= 0 {
			splitLen = len(window)
		}

		seg := strings.TrimSpace(string(rs[start : start+splitLen]))
		if seg != "" {
			out = append(out, seg)
		}
		start += splitLen

		for start < len(rs) && unicode.IsSpace(rs[start]) {
			start++
		}
	}
	return out
}

// pickRuneSplitLength returns how many runes from the start of window belong to the first chunk.
func pickRuneSplitLength(window []rune, maxRunes int) int {
	n := len(window)
	if n == 0 {
		return 0
	}
	minSplit := maxRunes / 2

	for i := n - 2; i >= minSplit && i >= 0; i-- {
		if i+1 < n && window[i] == '\n' && window[i+1] == '\n' {
			return i + 2
		}
	}
	for i := n - 1; i >= minSplit; i-- {
		if window[i] == '\n' {
			return i + 1
		}
	}
	for i := n - 1; i >= minSplit; i-- {
		if unicode.IsSpace(window[i]) {
			return i + 1
		}
	}
	return n
}
