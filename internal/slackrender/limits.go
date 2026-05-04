package slackrender

import (
	"os"
	"strconv"
	"strings"
)

// Limits holds Slack Block Kit text limits expressed in Unicode scalar values (Go runes),
// matching how Slack documents "characters" for composition objects.
//
// References:
//   - https://api.slack.com/reference/block-kit/composition-objects#text
//   - Mrkdwn section text: max 3000 characters
type Limits struct {
	// MrkdwnSectionRunes is the max runes per section block text object (Slack hard max 3000).
	MrkdwnSectionRunes int
	// RichTextElementRunes is the max runes per rich_text text element inside sections/lists.
	RichTextElementRunes int
}

// DefaultLimits returns conservative caps aligned with Slack’s published maximums.
func DefaultLimits() Limits {
	return Limits{
		MrkdwnSectionRunes:   3000,
		RichTextElementRunes: 3000,
	}
}

// LimitsFromEnv starts from DefaultLimits and optionally lowers caps for testing or safety.
// SLACK_RENDER_MRKDWN_MAX_RUNES (optional): integer 256–3000; applies to both mrkdwn sections
// and rich_text text elements so behavior stays consistent.
func LimitsFromEnv() Limits {
	l := DefaultLimits()
	v := strings.TrimSpace(os.Getenv("SLACK_RENDER_MRKDWN_MAX_RUNES"))
	if v == "" {
		return l
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 256 || n > 3000 {
		return l
	}
	l.MrkdwnSectionRunes = n
	l.RichTextElementRunes = n
	return l
}
