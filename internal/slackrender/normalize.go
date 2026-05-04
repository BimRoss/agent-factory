package slackrender

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var reDoubleAsteriskBold = regexp.MustCompile(`(?s)\*\*(.+?)\*\*`)
var reDoubleUnderscoreItalic = regexp.MustCompile(`(?s)__(.+?)__`)

// NormalizeModelTextToSlackMrkdwn maps common model markdown to Slack-flavored mrkdwn.
// Slack uses *single* asterisks for bold, not ** (which shows literally in the client).
func NormalizeModelTextToSlackMrkdwn(s string) string {
	if s == "" {
		return s
	}
	s = reDoubleAsteriskBold.ReplaceAllString(s, `*$1*`)
	s = reDoubleUnderscoreItalic.ReplaceAllString(s, `_${1}_`)
	return s
}

// NormalizeMarkdownListLinesToSlackBullets converts common Markdown unordered list
// markers at line start (-, +, or * with following whitespace) to the Unicode bullet
// Slack renders consistently in mrkdwn section text. Model output often uses "*   item"
// which is easy to misread next to Slack bold (*word*); normalizing to "• item" avoids that.
func NormalizeMarkdownListLinesToSlackBullets(s string) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = normalizeOneMarkdownListLine(line)
	}
	return strings.Join(lines, "\n")
}

func normalizeOneMarkdownListLine(line string) string {
	// Leading whitespace only (spaces/tabs) — preserve for nested-style indent.
	i := 0
	for i < len(line) {
		r, w := utf8.DecodeRuneInString(line[i:])
		if r != ' ' && r != '\t' {
			break
		}
		i += w
	}
	if i >= len(line) {
		return line
	}
	indent := line[:i]
	rest := line[i:]

	// Already Slack / Unicode bullet
	if strings.HasPrefix(rest, "• ") {
		return line
	}

	switch rest[0] {
	case '-', '+':
		return convertHyphenOrPlusListLine(indent, rest)
	case '*':
		return convertAsteriskMarkdownBulletLine(indent, rest)
	default:
		return line
	}
}

func convertHyphenOrPlusListLine(indent, rest string) string {
	if len(rest) < 2 {
		return indent + rest
	}
	if rest[1] != ' ' && rest[1] != '\t' {
		return indent + rest
	}
	j := 1
	for j < len(rest) && (rest[j] == ' ' || rest[j] == '\t') {
		j++
	}
	content := strings.TrimSpace(rest[j:])
	return indent + "• " + content
}

func convertAsteriskMarkdownBulletLine(indent, rest string) string {
	// Bold/italic in mrkdwn: *word* pairs asterisks; "* item *tail*" has multiple *.
	// Only treat as a Markdown bullet when this line has exactly one '*' — the list marker.
	if strings.Count(rest, "*") != 1 {
		return indent + rest
	}
	// List items: "* item" or "*   item" — require whitespace after *.
	if len(rest) < 2 {
		return indent + rest
	}
	j := 1
	for j < len(rest) && (rest[j] == ' ' || rest[j] == '\t') {
		j++
	}
	if j == 1 {
		return indent + rest
	}
	content := strings.TrimSpace(rest[j:])
	return indent + "• " + content
}

// PlainNotificationFallback is a short, unformatted string for chat.postMessage "text"
// (push notifications and clients that do not render blocks).
func PlainNotificationFallback(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Strip mrkdwn noise lightly for the fallback line.
	s = reDoubleAsteriskBold.ReplaceAllString(s, "$1")
	s = reDoubleUnderscoreItalic.ReplaceAllString(s, "$1")
	if len(s) > 300 {
		return s[:297] + "…"
	}
	return s
}
