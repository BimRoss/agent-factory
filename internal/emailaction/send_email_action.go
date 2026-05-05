package emailaction

import (
	"fmt"
	"regexp"
	"strings"
)

const IntentSendEmail = "send_email"

var (
	reEmail       = regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`)
	reFieldMarker = regexp.MustCompile(`(?is)\b(to|subject|title|body|instruction|body_instruction|cta|cta_text|button|cta_url|link|url)\s*:\s*`)
	// Conversational subject/title — mirrors create-google-doc natural phrasing ("subject is Foo,").
	reEmailSubjectTitleIs = regexp.MustCompile(`(?i)\b(?:subject|title)(?:\s+line)?\s+is\s+(?:"([^"]+)"|'([^']+)'|([^,;\n]+))`)
	reSlackMentionToken   = regexp.MustCompile(`<@[A-Z0-9]+>`)
	reEmailInferButton    = regexp.MustCompile(`(?i)\b(?:button|cta)\s+is\s+(?:"([^"]+)"|'([^']+)'|([^,;\n]+))`)
	reEmailInferLink      = regexp.MustCompile(`(?i)\b(?:link|url)\s+is\s+(?:<(https?://[^>|]+)(?:\|[^>]*)?>|(https?://[^\s,<]+))`)
	reEmailStripButtonCue = regexp.MustCompile(`(?i)\s*\b(?:button|cta)\s+is\s+(?:"[^"]+"|'[^']+'|[^,;\n]+)\s*,?\s*`)
	reEmailStripLinkCue   = regexp.MustCompile(`(?i)\s*\b(?:link|url)\s+is\s+(?:<https?://[^>|]+(?:\|[^>]*)?>|https?://[^\s,<]+)\s*,?\s*`)
)

// SendEmailAction mirrors employee-factory internal/emailaction.SendEmailAction.
type SendEmailAction struct {
	Intent          string `json:"intent"`
	To              string `json:"to,omitempty"`
	Subject         string `json:"subject,omitempty"`
	BodyInstruction string `json:"body_instruction,omitempty"`
	BodyText        string `json:"body_text,omitempty"`
	CTAText         string `json:"cta_text,omitempty"`
	CTAURL          string `json:"cta_url,omitempty"`
}

// ParseSendEmailAction parses a send-email command from Slack plain text.
func ParseSendEmailAction(raw string) (SendEmailAction, bool, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return SendEmailAction{}, false, nil
	}

	lower := strings.ToLower(text)
	if !looksLikeSendEmailIntent(lower) {
		return SendEmailAction{}, false, nil
	}
	action, hasFields, err := parseSendEmailFields(text)
	if err != nil {
		return SendEmailAction{}, true, err
	}
	if !hasFields {
		return SendEmailAction{}, false, nil
	}

	if action.BodyText == "" && action.BodyInstruction == "" {
		return SendEmailAction{}, true, fmt.Errorf("missing email content: include instruction:... or body:...")
	}

	return action, true, nil
}

// ParseSendEmailPatch extracts send-email fields from follow-up text without requiring an explicit
// "send email" intent phrase. Used for multi-turn gather/merge in pending create-email flows.
func ParseSendEmailPatch(raw string) (SendEmailAction, bool, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return SendEmailAction{}, false, nil
	}
	action, hasFields, err := parseSendEmailFields(text)
	if err != nil {
		return SendEmailAction{}, hasFields, err
	}
	return action, hasFields, nil
}

func parseSendEmailFields(text string) (SendEmailAction, bool, error) {
	action := SendEmailAction{Intent: IntentSendEmail}
	fields, residual := parseLabeledFields(text)
	inferredSubject := inferConversationalEmailSubject(text)
	if v := strings.TrimSpace(fields["to"]); v != "" {
		action.To = v
	}
	if v := strings.TrimSpace(fields["subject"]); v != "" {
		action.Subject = v
	}
	if v := strings.TrimSpace(fields["title"]); v != "" {
		action.Subject = v
	}
	if strings.TrimSpace(action.Subject) == "" && inferredSubject != "" {
		action.Subject = inferredSubject
	}
	if v := strings.TrimSpace(fields["body"]); v != "" {
		action.BodyText = v
	}
	if v := strings.TrimSpace(fields["instruction"]); v != "" {
		action.BodyInstruction = v
	}
	if v := strings.TrimSpace(fields["body_instruction"]); v != "" {
		action.BodyInstruction = v
	}
	if v := strings.TrimSpace(fields["cta_text"]); v != "" {
		action.CTAText = v
	}
	if v := strings.TrimSpace(fields["cta"]); v != "" {
		action.CTAText = v
	}
	if v := strings.TrimSpace(fields["button"]); v != "" {
		action.CTAText = v
	}
	if v := unwrapSlackURLField(strings.TrimSpace(fields["cta_url"])); v != "" {
		action.CTAURL = v
	}
	if v := unwrapSlackURLField(strings.TrimSpace(fields["link"])); v != "" {
		action.CTAURL = v
	}
	if v := unwrapSlackURLField(strings.TrimSpace(fields["url"])); v != "" {
		action.CTAURL = v
	}

	if strings.TrimSpace(action.CTAText) == "" {
		if b := inferConversationalButton(text); b != "" {
			action.CTAText = b
		}
	}
	if strings.TrimSpace(action.CTAURL) == "" {
		if u := inferConversationalLinkURL(text); u != "" {
			action.CTAURL = unwrapSlackURLField(u)
		}
	}

	if action.To == "" {
		if matches := reEmail.FindAllString(text, -1); len(matches) > 0 {
			action.To = strings.Join(normalizeRecipientCandidates(matches), ", ")
		}
	}
	if action.To != "" {
		recipients, err := parseRecipientList(action.To)
		if err != nil {
			return SendEmailAction{}, true, err
		}
		action.To = strings.Join(recipients, ", ")
	}

	rem := normalizeResidual(stripConversationalCTAFromResidual(stripConversationalSubjectFromResidual(residual)))
	if action.BodyText == "" && action.BodyInstruction == "" && rem != "" {
		action.BodyInstruction = rem
	}

	hasAny := strings.TrimSpace(action.To) != "" ||
		strings.TrimSpace(action.Subject) != "" ||
		strings.TrimSpace(action.BodyInstruction) != "" ||
		strings.TrimSpace(action.BodyText) != "" ||
		strings.TrimSpace(action.CTAText) != "" ||
		strings.TrimSpace(action.CTAURL) != ""
	return action, hasAny, nil
}

// ParseRecipientEmails splits and validates comma/semicolon-separated recipient tokens.
func ParseRecipientEmails(raw string) ([]string, error) {
	return parseRecipientList(raw)
}

func parseRecipientList(raw string) ([]string, error) {
	parts := splitRecipientList(raw)
	if len(parts) == 0 {
		return nil, fmt.Errorf("missing recipient email")
	}
	if len(parts) == 1 {
		alias := normalizeRecipientAlias(parts[0])
		if alias != "" {
			return []string{alias}, nil
		}
	}
	seen := map[string]struct{}{}
	recipients := make([]string, 0, len(parts))
	invalid := make([]string, 0)
	for _, part := range parts {
		matches := reEmail.FindAllString(part, -1)
		if len(matches) == 0 {
			invalid = append(invalid, strings.TrimSpace(part))
			continue
		}
		for _, match := range normalizeRecipientCandidates(matches) {
			if _, exists := seen[match]; exists {
				continue
			}
			seen[match] = struct{}{}
			recipients = append(recipients, match)
		}
	}
	if len(invalid) > 0 {
		return nil, fmt.Errorf("invalid recipient email(s): %s", strings.Join(invalid, ", "))
	}
	if len(recipients) == 0 {
		return nil, fmt.Errorf("missing recipient email")
	}
	return recipients, nil
}

func normalizeRecipientAlias(raw string) string {
	alias := strings.ToLower(strings.TrimSpace(raw))
	switch alias {
	case "me", "myself", "self":
		return alias
	default:
		return ""
	}
}

func splitRecipientList(raw string) []string {
	cleaned := strings.NewReplacer("\n", ";", "\r", ";").Replace(strings.TrimSpace(raw))
	if cleaned == "" {
		return nil
	}
	fields := strings.FieldsFunc(cleaned, func(r rune) bool {
		return r == ',' || r == ';'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if part := strings.TrimSpace(field); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func normalizeRecipientCandidates(matches []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		candidate := strings.ToLower(strings.TrimSpace(match))
		if candidate == "" {
			continue
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func looksLikeSendEmailIntent(lower string) bool {
	switch {
	case strings.Contains(lower, "send email"):
		return true
	case strings.Contains(lower, "email me"):
		return true
	case strings.Contains(lower, "please email"):
		return true
	case strings.HasPrefix(lower, "email "):
		return true
	case strings.Contains(lower, "draft email"):
		return true
	case strings.Contains(lower, "send an email"):
		return true
	case strings.Contains(lower, "subject is "), strings.Contains(lower, "subject line is "):
		return true
	case strings.Contains(lower, "title is "), strings.Contains(lower, "title line is "):
		return true
	case strings.Contains(lower, "button is "), strings.Contains(lower, "cta is "):
		return true
	case strings.Contains(lower, "link is "), strings.Contains(lower, "url is "):
		return true
	case strings.Contains(lower, "email ") && (strings.Contains(lower, "body:") || strings.Contains(lower, "title:") || strings.Contains(lower, "subject:")):
		return true
	default:
		return false
	}
}

func parseLabeledFields(s string) (map[string]string, string) {
	matches := reFieldMarker.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return map[string]string{}, s
	}
	fields := map[string]string{}
	consumed := make([][2]int, 0, len(matches))
	for i, m := range matches {
		if len(m) < 4 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(s[m[2]:m[3]]))
		valueStart := m[1]
		valueEnd := len(s)
		if i+1 < len(matches) {
			valueEnd = matches[i+1][0]
		}
		value := cleanFieldValue(s[valueStart:valueEnd])
		if value != "" {
			fields[key] = value
		}
		consumed = append(consumed, [2]int{m[0], valueEnd})
	}
	residual := removeRanges(s, consumed)
	return fields, residual
}

func cleanFieldValue(raw string) string {
	value := strings.TrimSpace(raw)
	value = strings.Trim(value, " \t\r\n;,")
	value = strings.Trim(value, `"'`)
	return strings.TrimSpace(value)
}

func removeRanges(s string, ranges [][2]int) string {
	if len(ranges) == 0 {
		return s
	}
	var builder strings.Builder
	last := 0
	for _, rg := range ranges {
		start, end := rg[0], rg[1]
		if start < last {
			start = last
		}
		if start > len(s) {
			start = len(s)
		}
		if end > len(s) {
			end = len(s)
		}
		if start > last {
			builder.WriteString(s[last:start])
		}
		if builder.Len() > 0 {
			builder.WriteString(" ")
		}
		last = end
	}
	if last < len(s) {
		builder.WriteString(s[last:])
	}
	return builder.String()
}

func unwrapSlackURLField(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "<http://") || strings.HasPrefix(s, "<https://") {
		close := strings.Index(s, ">")
		if close > 0 {
			inner := strings.TrimSpace(s[1:close])
			if pipe := strings.Index(inner, "|"); pipe >= 0 {
				inner = strings.TrimSpace(inner[:pipe])
			}
			return inner
		}
	}
	return s
}

func inferConversationalButton(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	m := reEmailInferButton.FindStringSubmatch(raw)
	if len(m) < 4 {
		return ""
	}
	for i := 1; i <= 3; i++ {
		if s := sanitizeInferredEmailSubject(m[i]); s != "" {
			return s
		}
	}
	return ""
}

func inferConversationalLinkURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	m := reEmailInferLink.FindStringSubmatch(raw)
	if len(m) < 3 {
		return ""
	}
	if u := strings.TrimSpace(m[1]); u != "" {
		return u
	}
	return strings.TrimSpace(m[2])
}

func stripConversationalCTAFromResidual(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	out := reEmailStripLinkCue.ReplaceAllString(s, " ")
	out = reEmailStripButtonCue.ReplaceAllString(out, " ")
	return strings.TrimSpace(strings.Join(strings.Fields(out), " "))
}

func inferConversationalEmailSubject(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	m := reEmailSubjectTitleIs.FindStringSubmatch(raw)
	if len(m) < 4 {
		return ""
	}
	for i := 1; i <= 3; i++ {
		s := sanitizeInferredEmailSubject(m[i])
		if s != "" {
			return s
		}
	}
	return ""
}

func sanitizeInferredEmailSubject(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = reSlackMentionToken.ReplaceAllString(s, "")
	s = strings.TrimSpace(strings.Trim(s, `"'`))
	lower := strings.ToLower(s)
	for _, marker := range []string{
		" with ",
		" regarding ",
		" about ",
		" describing ",
	} {
		if idx := strings.Index(lower, marker); idx > 0 {
			s = strings.TrimSpace(s[:idx])
			lower = strings.ToLower(s)
		}
	}
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		s = strings.TrimSpace(s[:160])
	}
	return s
}

func stripConversationalSubjectFromResidual(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	out := reEmailSubjectTitleIs.ReplaceAllString(s, " ")
	return strings.TrimSpace(strings.Join(strings.Fields(out), " "))
}

func normalizeResidual(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	intentRe := regexp.MustCompile(`(?i)\b(send an email|send email|draft email)\b`)
	s = strings.TrimSpace(intentRe.ReplaceAllString(s, ""))
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(strings.Trim(s, "-:"))
	s = strings.Join(strings.Fields(s), " ")
	return s
}
