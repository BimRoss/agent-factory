package runtime

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	reCreateDocTitle      = regexp.MustCompile(`(?i)\b(?:title|subject)\s*:\s*([^;\n]+)`)
	reCreateDocBody       = regexp.MustCompile(`(?i)\b(?:body|content)\s*:\s*([^;\n]+)`)
	reCreateDocPages      = regexp.MustCompile(`(?i)\b(\d+)\s*pages?\b`)
	reCreateDocLikelyMail = regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`)

	reCreateDocEditorCue    = regexp.MustCompile(`(?i)\b(?:editors?|share with|grant (?:editor )?access|add as editor)\b`)
	reCreateDocCommenterCue = regexp.MustCompile(`(?i)\b(?:commenters?|grant commenter access|add as commenter)\b`)
	reCreateDocViewerCue    = regexp.MustCompile(`(?i)\b(?:viewers?|read-only|grant viewer access|add as viewer)\b`)
	reCreateDocSentence     = regexp.MustCompile(`(?:;|!|\?|\.(?:\s+|$))`)
)

type createDocRequest struct {
	Title       string
	Body        string
	Instruction string
	Pages       int
	Editors     []string
	Commenters  []string
	Viewers     []string
}

func (e *Engine) runCreateDoc(ctx context.Context, task Task) (RenderPayload, error) {
	cfg := LoadGoogleDocsConfigForEmployee(task.OwnerEmployeeID)
	if err := cfg.Validate(task.OwnerEmployeeID); err != nil {
		return RenderPayload{}, err
	}

	req := parseCreateDocRequest(task.RequestText)
	threadCtx := ""
	if e.threadContext != nil {
		threadCtx = strings.TrimSpace(e.threadContext(ctx, task))
	}
	if strings.TrimSpace(req.Body) == "" {
		body, err := generateCreateDocBody(ctx, e.provider, req, task, threadCtx)
		if err != nil {
			return RenderPayload{}, err
		}
		req.Body = body
	}
	if strings.TrimSpace(req.Title) == "" {
		req.Title = defaultCreateDocTitle(task)
	}

	client, err := NewGoogleDocsClient(cfg)
	if err != nil {
		return RenderPayload{}, err
	}
	createRes, err := client.Create(ctx, GoogleDocsCreateInput{
		Title: req.Title,
		Body:  req.Body,
	})
	if err != nil {
		return RenderPayload{}, err
	}
	for _, email := range dedupeEmails(req.Editors) {
		if err := client.GrantEditor(ctx, createRes.DocumentID, email); err != nil {
			return RenderPayload{}, err
		}
	}
	for _, email := range subtractEmails(dedupeEmails(req.Commenters), req.Editors) {
		if err := client.GrantCommenter(ctx, createRes.DocumentID, email); err != nil {
			return RenderPayload{}, err
		}
	}
	viewers := subtractEmails(dedupeEmails(req.Viewers), req.Editors)
	viewers = subtractEmails(viewers, req.Commenters)
	for _, email := range viewers {
		if err := client.GrantViewer(ctx, createRes.DocumentID, email); err != nil {
			return RenderPayload{}, err
		}
	}

	return RenderPayload{
		OutputID:     fmt.Sprintf("%s-create-doc", task.ID),
		FallbackText: fmt.Sprintf("Created Google Doc: %s", strings.TrimSpace(createRes.URL)),
		FinalSummary: "create-doc completed",
		Transport:    "slack",
	}, nil
}

func parseCreateDocRequest(raw string) createDocRequest {
	text := strings.TrimSpace(raw)
	req := createDocRequest{
		Title:       extractDocField(reCreateDocTitle, text),
		Body:        extractDocField(reCreateDocBody, text),
		Instruction: strings.TrimSpace(text),
		Pages:       extractPageCount(text),
	}
	req.Editors = inferEmailsByCue(text, reCreateDocEditorCue, reCreateDocCommenterCue, reCreateDocViewerCue)
	req.Commenters = inferEmailsByCue(text, reCreateDocCommenterCue, reCreateDocEditorCue, reCreateDocViewerCue)
	req.Viewers = inferEmailsByCue(text, reCreateDocViewerCue, reCreateDocEditorCue, reCreateDocCommenterCue)
	return req
}

func generateCreateDocBody(ctx context.Context, provider ProviderConfig, req createDocRequest, task Task, threadContext string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	pageTarget := req.Pages
	if pageTarget <= 0 {
		pageTarget = 2
	}
	wordTarget := pageTarget * 500
	if wordTarget < 600 {
		wordTarget = 600
	}

	instruction := strings.TrimSpace(req.Instruction)
	if instruction == "" {
		instruction = "Summarize the thread context into a useful document."
	}
	system := strings.TrimSpace(`
You write high-quality Google Doc content for Joanne.
Return plain text only, no markdown code fences.
Use headings and clear sections where useful.
`)
	user := fmt.Sprintf(
		"Employee: %s\nSlack request:\n%s\n\nThread context:\n%s\n\nWrite a complete document body of about %d words (target %d pages).",
		strings.TrimSpace(task.OwnerEmployeeID),
		instruction,
		truncateRunes(strings.TrimSpace(threadContext), 12000),
		wordTarget,
		pageTarget,
	)
	requestBody := map[string]any{
		"systemInstruction": map[string]any{
			"parts": []any{map[string]any{"text": system}},
		},
		"contents": []any{
			map[string]any{
				"parts": []any{map[string]any{"text": user}},
			},
		},
		"generationConfig": map[string]any{
			"temperature":      0.35,
			"maxOutputTokens":  8192,
			"topP":             0.9,
			"responseMimeType": "text/plain",
		},
	}
	body, _, err := runGeminiGenerate(ctx, provider, requestBody)
	if err != nil {
		return "", fmt.Errorf("create-doc: draft generation failed: %w", err)
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return "", fmt.Errorf("create-doc: draft generation returned empty body")
	}
	return body, nil
}

func defaultCreateDocTitle(task Task) string {
	date := time.Now().UTC().Format("2006-01-02")
	return fmt.Sprintf("Joanne Summary %s", date)
}

func extractDocField(re *regexp.Regexp, text string) string {
	if re == nil || strings.TrimSpace(text) == "" {
		return ""
	}
	m := re.FindStringSubmatch(text)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(strings.Trim(m[1], `"'`))
}

func extractPageCount(text string) int {
	m := reCreateDocPages.FindStringSubmatch(text)
	if len(m) < 2 {
		return 0
	}
	n := 0
	for _, r := range m[1] {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	if n < 0 {
		return 0
	}
	return n
}

func inferEmailsByCue(raw string, primary *regexp.Regexp, secondary ...*regexp.Regexp) []string {
	text := strings.TrimSpace(raw)
	if text == "" || primary == nil || !primary.MatchString(text) {
		return nil
	}
	clauses := reCreateDocSentence.Split(text, -1)
	out := make([]string, 0)
	for _, clause := range clauses {
		clause = strings.TrimSpace(clause)
		if clause == "" || !primary.MatchString(clause) {
			continue
		}
		startEnd := primary.FindAllStringIndex(clause, -1)
		nextStarts := make([][]int, 0, len(startEnd))
		nextStarts = append(nextStarts, startEnd...)
		for _, sec := range secondary {
			if sec == nil {
				continue
			}
			nextStarts = append(nextStarts, sec.FindAllStringIndex(clause, -1)...)
		}
		for _, m := range startEnd {
			if len(m) < 2 {
				continue
			}
			segmentStart := m[0]
			segmentEnd := len(clause)
			for _, n := range nextStarts {
				if len(n) < 1 || n[0] <= segmentStart {
					continue
				}
				if n[0] < segmentEnd {
					segmentEnd = n[0]
				}
			}
			out = append(out, reCreateDocLikelyMail.FindAllString(clause[segmentStart:segmentEnd], -1)...)
		}
	}
	return dedupeEmails(out)
}

func dedupeEmails(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, raw := range in {
		email := strings.ToLower(strings.TrimSpace(raw))
		if email == "" {
			continue
		}
		if _, ok := seen[email]; ok {
			continue
		}
		seen[email] = struct{}{}
		out = append(out, email)
	}
	return out
}

func subtractEmails(source, remove []string) []string {
	if len(source) == 0 {
		return nil
	}
	rm := map[string]struct{}{}
	for _, raw := range remove {
		key := strings.ToLower(strings.TrimSpace(raw))
		if key != "" {
			rm[key] = struct{}{}
		}
	}
	out := make([]string, 0, len(source))
	for _, raw := range source {
		key := strings.ToLower(strings.TrimSpace(raw))
		if key == "" {
			continue
		}
		if _, exists := rm[key]; exists {
			continue
		}
		out = append(out, key)
	}
	return out
}
