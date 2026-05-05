package htmlemail

import (
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
)

var (
	reHTMLTag = regexp.MustCompile(`(?is)<[^>]+>`)

	reMarkdownStrong = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	reMarkdownItalic = regexp.MustCompile(`\*([^*\n]+)\*`)

	reUnsafeHTMLBlock                   = regexp.MustCompile(`(?is)<\s*(script|style|iframe|object|embed|form|link|meta)[^>]*>.*?<\s*/\s*[a-z0-9:_-]+\s*>`)
	reUnsafeHTMLSelfClosing             = regexp.MustCompile(`(?is)<\s*(script|style|iframe|object|embed|form|link|meta)[^>]*\/?\s*>`)
	reUnsafeHTMLOnAttrDoubleQuoted      = regexp.MustCompile(`(?is)\s+on[a-z0-9_-]+\s*=\s*"[^"]*"`)
	reUnsafeHTMLOnAttrSingleQuoted      = regexp.MustCompile(`(?is)\s+on[a-z0-9_-]+\s*=\s*'[^']*'`)
	reUnsafeHTMLOnAttrBare              = regexp.MustCompile(`(?is)\s+on[a-z0-9_-]+\s*=\s*[^\s>]+`)
	reUnsafeHTMLHrefSrcJavascriptDouble = regexp.MustCompile(`(?is)\s(href|src)\s*=\s*"[^"]*javascript:[^"]*"`)
	reUnsafeHTMLHrefSrcJavascriptSingle = regexp.MustCompile(`(?is)\s(href|src)\s*=\s*'[^']*javascript:[^']*'`)
	reUnsafeHTMLStyleAttrDoubleQuoted   = regexp.MustCompile(`(?is)\s+style\s*=\s*"[^"]*"`)
	reUnsafeHTMLStyleAttrSingleQuoted   = regexp.MustCompile(`(?is)\s+style\s*=\s*'[^']*'`)
	reUnsafeHTMLColorAttrDoubleQuoted   = regexp.MustCompile(`(?is)\s+(color|bgcolor)\s*=\s*"[^"]*"`)
	reUnsafeHTMLColorAttrSingleQuoted   = regexp.MustCompile(`(?is)\s+(color|bgcolor)\s*=\s*'[^']*'`)

	reEmailAnchorTag    = regexp.MustCompile(`(?is)<a\s+([^>]+)>(.*?)</a>`)
	reHrefInAnchorAttrs = regexp.MustCompile(`(?is)\bhref\s*=\s*("([^"]*)"|'([^']*)'|([^\s>]+))`)
)

// NormalizeFragmentForSend converts Markdown-ish plain text to HTML or sanitizes HTML fragments (employee-factory parity).
func NormalizeFragmentForSend(raw string) string {
	body := strings.TrimSpace(raw)
	if body == "" {
		return ""
	}
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	body = strings.ReplaceAll(body, "```html", "")
	body = strings.ReplaceAll(body, "```HTML", "")
	body = strings.ReplaceAll(body, "```", "")
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	if !reHTMLTag.MatchString(body) {
		return plainTextToHTML(body)
	}
	body = sanitizeHTMLFragment(body)
	if strings.TrimSpace(stripHTMLTags(body)) == "" {
		return ""
	}
	return body
}

func normalizePlainTextForCompose(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "```", "")
	lines := strings.Split(s, "\n")
	cleaned := make([]string, 0, len(lines))
	blankStreak := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			blankStreak++
			if blankStreak > 1 {
				continue
			}
			cleaned = append(cleaned, "")
			continue
		}
		blankStreak = 0
		cleaned = append(cleaned, line)
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}

func normalizeBodyParagraphs(raw string) string {
	body := normalizePlainTextForCompose(raw)
	if body == "" {
		return ""
	}
	lines := strings.Split(body, "\n")
	paragraphs := make([][]string, 0)
	current := make([]string, 0)
	flush := func() {
		if len(current) == 0 {
			return
		}
		paragraphs = append(paragraphs, append([]string(nil), current...))
		current = current[:0]
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			flush()
			continue
		}
		current = append(current, line)
	}
	flush()
	if len(paragraphs) == 0 {
		return ""
	}
	rendered := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		if shouldUnwrapHardWrappedParagraph(paragraph) {
			rendered = append(rendered, strings.Join(paragraph, " "))
			continue
		}
		rendered = append(rendered, strings.Join(paragraph, "\n"))
	}
	return strings.TrimSpace(strings.Join(rendered, "\n\n"))
}

func shouldUnwrapHardWrappedParagraph(lines []string) bool {
	if len(lines) < 3 {
		return false
	}
	shortLines := 0
	structuredLines := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if isStructuredMetadataLine(trimmed) {
			structuredLines++
		}
		if len(trimmed) <= 58 {
			shortLines++
		}
	}
	if structuredLines*100/len(lines) >= 60 {
		return false
	}
	return shortLines*100/len(lines) >= 70
}

func isStructuredMetadataLine(line string) bool {
	line = strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(line, "- "),
		strings.HasPrefix(line, "* "),
		strings.HasPrefix(line, "• "),
		strings.HasPrefix(line, "> "),
		strings.HasPrefix(line, "1. "),
		strings.HasPrefix(line, "2. "),
		strings.HasPrefix(line, "3. "),
		strings.HasPrefix(line, "4. "),
		strings.HasPrefix(line, "5. "):
		return true
	default:
		return false
	}
}

func plainTextToHTML(raw string) string {
	plain := normalizeBodyParagraphs(raw)
	if plain == "" {
		return ""
	}
	paragraphs := strings.Split(plain, "\n\n")
	out := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		lines := strings.Split(strings.TrimSpace(paragraph), "\n")
		filtered := make([]string, 0, len(lines))
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" {
				filtered = append(filtered, line)
			}
		}
		if len(filtered) == 0 {
			continue
		}
		if allBulletLines(filtered) {
			items := make([]string, 0, len(filtered))
			for _, line := range filtered {
				item := strings.TrimSpace(strings.TrimPrefix(line, "- "))
				item = strings.TrimSpace(strings.TrimPrefix(item, "* "))
				item = strings.TrimSpace(strings.TrimPrefix(item, "• "))
				if item == "" {
					continue
				}
				items = append(items, "<li>"+html.EscapeString(item)+"</li>")
			}
			if len(items) > 0 {
				out = append(out, "<ul>"+strings.Join(items, "")+"</ul>")
				continue
			}
		}
		out = append(out, "<p>"+html.EscapeString(strings.Join(filtered, " "))+"</p>")
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func allBulletLines(lines []string) bool {
	if len(lines) == 0 {
		return false
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "- ") && !strings.HasPrefix(trimmed, "* ") && !strings.HasPrefix(trimmed, "• ") {
			return false
		}
	}
	return true
}

func sanitizeHTMLFragment(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = reMarkdownStrong.ReplaceAllString(s, "<strong>$1</strong>")
	s = reMarkdownItalic.ReplaceAllString(s, "<em>$1</em>")
	s = reUnsafeHTMLBlock.ReplaceAllString(s, "")
	s = reUnsafeHTMLSelfClosing.ReplaceAllString(s, "")
	s = reUnsafeHTMLOnAttrDoubleQuoted.ReplaceAllString(s, "")
	s = reUnsafeHTMLOnAttrSingleQuoted.ReplaceAllString(s, "")
	s = reUnsafeHTMLOnAttrBare.ReplaceAllString(s, "")
	s = reUnsafeHTMLHrefSrcJavascriptDouble.ReplaceAllString(s, ` $1="#"`)
	s = reUnsafeHTMLHrefSrcJavascriptSingle.ReplaceAllString(s, ` $1='#'`)
	s = reUnsafeHTMLStyleAttrDoubleQuoted.ReplaceAllString(s, "")
	s = reUnsafeHTMLStyleAttrSingleQuoted.ReplaceAllString(s, "")
	s = reUnsafeHTMLColorAttrDoubleQuoted.ReplaceAllString(s, "")
	s = reUnsafeHTMLColorAttrSingleQuoted.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return s
}

func stripHTMLTags(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	return strings.TrimSpace(reHTMLTag.ReplaceAllString(raw, " "))
}

// ValidateCTAURL ensures the primary button URL is http(s) with a host (employee-factory parity).
func ValidateCTAURL(raw string) error {
	ctaURL := strings.TrimSpace(raw)
	if ctaURL == "" {
		return fmt.Errorf("missing CTA URL")
	}
	parsed, err := url.Parse(ctaURL)
	if err != nil {
		return err
	}
	if parsed == nil || parsed.Host == "" {
		return fmt.Errorf("CTA URL must include a host")
	}
	switch strings.ToLower(strings.TrimSpace(parsed.Scheme)) {
	case "http", "https":
		return nil
	default:
		return fmt.Errorf("CTA URL must use http or https")
	}
}

func normalizeURLRefForCTADedupe(u *url.URL) string {
	if u == nil {
		return ""
	}
	nu := *u
	nu.Fragment = ""
	nu.RawFragment = ""
	host := nu.Hostname()
	port := nu.Port()
	if host != "" {
		if port != "" {
			nu.Host = strings.ToLower(host) + ":" + port
		} else {
			nu.Host = strings.ToLower(host)
		}
	}
	nu.Path = strings.TrimSuffix(nu.Path, "/")
	return nu.String()
}

func extractHrefFromAnchorAttributes(attrs string) string {
	m := reHrefInAnchorAttrs.FindStringSubmatch(attrs)
	if len(m) < 5 {
		return ""
	}
	var raw string
	switch {
	case strings.TrimSpace(m[2]) != "":
		raw = m[2]
	case strings.TrimSpace(m[3]) != "":
		raw = m[3]
	case strings.TrimSpace(m[4]) != "":
		raw = m[4]
	default:
		return ""
	}
	return html.UnescapeString(strings.TrimSpace(raw))
}

// StripAnchorsMatchingCTAURL removes <a> tags whose href matches the primary CTA URL so the branded button is not duplicated.
func StripAnchorsMatchingCTAURL(bodyHTML, ctaURL string) string {
	bodyHTML = strings.TrimSpace(bodyHTML)
	ctaURL = strings.TrimSpace(ctaURL)
	if bodyHTML == "" || ctaURL == "" {
		return bodyHTML
	}
	target, err := url.Parse(ctaURL)
	if err != nil || target == nil || target.Host == "" {
		return bodyHTML
	}
	targetNorm := normalizeURLRefForCTADedupe(target)

	return reEmailAnchorTag.ReplaceAllStringFunc(bodyHTML, func(match string) string {
		parts := reEmailAnchorTag.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}
		attr := parts[1]
		href := extractHrefFromAnchorAttributes(attr)
		if href == "" {
			return match
		}
		hrefParsed, err := url.Parse(href)
		if err != nil || hrefParsed == nil {
			return match
		}
		var resolved *url.URL
		if strings.TrimSpace(hrefParsed.Host) == "" {
			resolved = target.ResolveReference(hrefParsed)
		} else {
			resolved = hrefParsed
		}
		if normalizeURLRefForCTADedupe(resolved) == targetNorm {
			return ""
		}
		return match
	})
}

// BuildBrandedEmailInner wraps a normalized HTML fragment in the transactional layout and optional primary CTA button (employee-factory parity).
func BuildBrandedEmailInner(bodyHTML, ctaText, ctaURL string) string {
	bodyHTML = NormalizeFragmentForSend(bodyHTML)
	ctaText = strings.TrimSpace(ctaText)
	ctaURL = strings.TrimSpace(ctaURL)
	if bodyHTML == "" {
		return ""
	}
	innerNoCTA := func() string {
		return strings.TrimSpace(fmt.Sprintf(
			`<div style="margin:0;padding:0;background-color:#ffffff;color:#111111;">
<div style="max-width:640px;margin:0 auto;padding:32px 24px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif;line-height:1.6;color:#111111;background-color:#ffffff;">
%s
</div>
</div>`,
			bodyHTML,
		))
	}
	if ctaText == "" && ctaURL == "" {
		return innerNoCTA()
	}
	if ctaText == "" || ctaURL == "" {
		return innerNoCTA()
	}
	if err := ValidateCTAURL(ctaURL); err != nil {
		return innerNoCTA()
	}
	return strings.TrimSpace(fmt.Sprintf(
		`<div style="margin:0;padding:0;background-color:#ffffff;color:#111111;">
<div style="max-width:640px;margin:0 auto;padding:32px 24px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif;line-height:1.6;color:#111111;background-color:#ffffff;">
%s
<table role="presentation" border="0" cellspacing="0" cellpadding="0" style="border-collapse:collapse;margin-top:28px;">
<tr>
<td align="left" bgcolor="#0a0a0a" style="mso-line-height-rule:exactly;background-color:#0a0a0a;border-radius:10px;mso-padding-alt:12px 22px;">
<a href="%s" target="_blank" rel="noopener noreferrer" style="display:inline-block;padding:12px 22px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif;font-size:16px;font-weight:600;color:#FEFEFE !important;-webkit-text-fill-color:#FEFEFE;background-color:#0a0a0a;text-decoration:none;border-radius:10px;border:1px solid #0a0a0a;">%s</a>
</td>
</tr>
</table>
</div>
</div>`,
		bodyHTML,
		html.EscapeString(ctaURL),
		html.EscapeString(ctaText),
	))
}
