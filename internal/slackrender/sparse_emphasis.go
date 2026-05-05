package slackrender

import (
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Logic mirrors our Slack formatting emphasis behavior across runtimes.

var sparseSlackMrkdwnBold = regexp.MustCompile(`\*[^*\n]+\*`)

var reWhatsTheFirstQ = regexp.MustCompile(`(?i)(What(?:'s| is)) the first ([^.?\n]{2,70})\?`)

func sparseMrkdwnEmphasisEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("SLACK_SPARSE_MRKDWN_EMPHASIS")))
	switch v {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// ApplySparseMrkdwnEmphasis adds a few Slack *bold* spans on plain model prose when structural patterns match.
func ApplySparseMrkdwnEmphasis(s string) string {
	s = strings.TrimSpace(s)
	if !sparseMrkdwnEmphasisEnabled() || s == "" {
		return s
	}
	if strings.Contains(s, "```") || strings.Contains(s, "<http") {
		return s
	}
	if sparseSlackMrkdwnBold.MatchString(s) {
		return s
	}
	const maxAdds = 5
	adds := 0
	s = applyWhatsTheFirstEmphasis(s, &adds, maxAdds)
	if adds < maxAdds {
		s = applyAreWeAlternativesEmphasis(s, &adds, maxAdds)
	}
	return strings.TrimSpace(s)
}

func applyWhatsTheFirstEmphasis(s string, adds *int, maxAdds int) string {
	if *adds >= maxAdds {
		return s
	}
	m := reWhatsTheFirstQ.FindStringSubmatchIndex(s)
	if m == nil {
		return s
	}
	inner := strings.TrimSpace(s[m[4]:m[5]])
	if !eligibleEmphasisPhrase(inner) {
		return s
	}
	replacement := s[m[2]:m[3]] + " the first *" + inner + "*?"
	out := s[:m[0]] + replacement + s[m[1]:]
	*adds++
	return out
}

func applyAreWeAlternativesEmphasis(s string, adds *int, maxAdds int) string {
	if *adds >= maxAdds {
		return s
	}
	lower := strings.ToLower(s)
	const needle = "are we "
	idx := strings.Index(lower, needle)
	if idx < 0 {
		return s
	}
	innerStart := idx + len(needle)
	rest := s[innerStart:]
	q := strings.Index(rest, "?")
	if q <= 0 {
		return s
	}
	inner := strings.TrimSpace(rest[:q])
	suffix := rest[q:]
	if strings.Contains(inner, "?") {
		return s
	}
	parts := splitAreWeAlternativeInner(inner)
	if len(parts) < 2 {
		return s
	}
	var bolded []string
	for _, p := range parts {
		if *adds >= maxAdds {
			return s
		}
		p = strings.TrimSpace(p)
		if strings.HasPrefix(strings.ToLower(p), "or ") {
			p = strings.TrimSpace(p[len("or "):])
		}
		if !eligibleEmphasisPhrase(p) {
			return s
		}
		bolded = append(bolded, "*"+p+"*")
		*adds++
	}
	rebuilt := joinAreWeAlternatives(bolded)
	return s[:idx+len(needle)] + rebuilt + suffix
}

func splitAreWeAlternativeInner(inner string) []string {
	raw := strings.Split(inner, ", ")
	if len(raw) < 2 {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil
		}
		out = append(out, p)
	}
	return out
}

func joinAreWeAlternatives(segs []string) string {
	if len(segs) == 0 {
		return ""
	}
	if len(segs) == 1 {
		return segs[0]
	}
	if len(segs) == 2 {
		return segs[0] + " or " + segs[1]
	}
	var b strings.Builder
	for i, seg := range segs {
		if i > 0 {
			if i == len(segs)-1 {
				b.WriteString(", or ")
			} else {
				b.WriteString(", ")
			}
		}
		b.WriteString(seg)
	}
	return b.String()
}

func eligibleEmphasisPhrase(p string) bool {
	p = strings.TrimSpace(p)
	if p == "" {
		return false
	}
	if strings.ContainsAny(p, "<>&`*") {
		return false
	}
	n := utf8.RuneCountInString(p)
	if n < 3 || n > 88 {
		return false
	}
	low := strings.ToLower(p)
	switch low {
	case "the", "a", "an", "it", "this", "that":
		return false
	}
	return true
}
