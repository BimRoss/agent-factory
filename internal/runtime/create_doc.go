package runtime

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
)

var (
	reCreateDocTitle      = regexp.MustCompile(`(?i)\b(?:title|subject)\s*:\s*([^;\n]+)`)
	reCreateDocTitled     = regexp.MustCompile(`(?i)\b(?:title(?:\s+it)?|titled|called|named)\s+(?:"([^"]+)"|'([^']+)'|([^;\n]+))`)
	reCreateDocBody       = regexp.MustCompile(`(?i)\b(?:body|content)\s*:\s*([^;\n]+)`)
	reCreateDocPages      = regexp.MustCompile(`(?i)\b(\d+)\s*pages?\b`)
	reCreateDocLikelyMail = regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`)

	reCreateDocEditorCue         = regexp.MustCompile(`(?i)\b(?:editors?|share with|grant (?:editor )?access|add as editor)\b`)
	reCreateDocCommenterCue      = regexp.MustCompile(`(?i)\b(?:commenters?|grant commenter access|add as commenter)\b`)
	reCreateDocViewerCue         = regexp.MustCompile(`(?i)\b(?:viewers?|read-only|grant viewer access|add as viewer)\b`)
	reCreateDocSentence          = regexp.MustCompile(`(?:;|!|\?|\.(?:\s+|$))`)
	reCreateDocSlackMentionToken = regexp.MustCompile(`<@[A-Z0-9]+>`)
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
	if email, source, err := injectCreateDocRequesterEditor(ctx, &req, task, resolveCreateDocRequesterEmail); err != nil {
		if strings.TrimSpace(task.HumanUserID) != "" {
			log.Printf("create-google-doc: requester editor lookup skipped user=%s err=%v", strings.TrimSpace(task.HumanUserID), err)
		}
	} else {
		log.Printf("create-google-doc: requester editor added user=%s email=%s source=%s", strings.TrimSpace(task.HumanUserID), strings.TrimSpace(email), strings.TrimSpace(source))
	}
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
		if inferred, err := generateCreateDocTitle(ctx, e.provider, req, task, threadCtx); err != nil {
			log.Printf("create-google-doc: title generation failed, using fallback title: %v", err)
		} else {
			req.Title = inferred
		}
	}
	if strings.TrimSpace(req.Title) == "" {
		if fromBody := inferCreateDocTitleFromBody(req.Body); fromBody != "" {
			req.Title = fromBody
		}
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
		OutputID:     fmt.Sprintf("%s-create-google-doc", task.ID),
		FallbackText: fmt.Sprintf("Created Google Doc: %s", strings.TrimSpace(createRes.URL)),
		FinalSummary: "create-google-doc completed",
		Transport:    "slack",
	}, nil
}

func parseCreateDocRequest(raw string) createDocRequest {
	text := strings.TrimSpace(raw)
	title := extractDocField(reCreateDocTitle, text)
	if title == "" {
		title = inferCreateDocTitle(text)
	}
	req := createDocRequest{
		Title:       title,
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
		return "", fmt.Errorf("create-google-doc: draft generation failed: %w", err)
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return "", fmt.Errorf("create-google-doc: draft generation returned empty body")
	}
	return body, nil
}

func generateCreateDocTitle(ctx context.Context, provider ProviderConfig, req createDocRequest, task Task, threadContext string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	instruction := strings.TrimSpace(req.Instruction)
	if instruction == "" {
		instruction = "Create a useful summary document from the thread context."
	}
	system := strings.TrimSpace(`
You create concise, descriptive Google Doc titles for Joanne.
Return plain text only: title text with no quotes, no markdown, and no trailing period.
Keep it under 10 words.
`)
	user := fmt.Sprintf(
		"Employee: %s\nSlack request:\n%s\n\nThread context (may be empty):\n%s\n\nDraft body preview:\n%s\n\nProduce one concise, specific title.",
		strings.TrimSpace(task.OwnerEmployeeID),
		instruction,
		truncateRunes(strings.TrimSpace(threadContext), 6000),
		truncateRunes(strings.TrimSpace(req.Body), 1800),
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
			"temperature":      0.2,
			"maxOutputTokens":  80,
			"topP":             0.9,
			"responseMimeType": "text/plain",
		},
	}
	raw, _, err := runGeminiGenerate(ctx, provider, requestBody)
	if err != nil {
		return "", fmt.Errorf("create-google-doc: title generation failed: %w", err)
	}
	return sanitizeGeneratedCreateDocTitle(raw), nil
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

func inferCreateDocTitle(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	m := reCreateDocTitled.FindStringSubmatch(text)
	if len(m) == 0 {
		return ""
	}
	candidates := []string{}
	for i := 1; i < len(m); i++ {
		candidates = append(candidates, m[i])
	}
	for _, raw := range candidates {
		title := sanitizeInferredTitle(raw)
		if title != "" {
			return title
		}
	}
	return ""
}

func sanitizeInferredTitle(raw string) string {
	title := strings.TrimSpace(raw)
	if title == "" {
		return ""
	}
	title = reCreateDocSlackMentionToken.ReplaceAllString(title, "")
	lower := strings.ToLower(title)
	for _, marker := range []string{
		" summarizing ",
		" summary of ",
		" about ",
		" regarding ",
		" with ",
		" for ",
		" - ",
		" -- ",
	} {
		if idx := strings.Index(lower, marker); idx > 0 {
			title = strings.TrimSpace(title[:idx])
			lower = strings.ToLower(title)
		}
	}
	title = strings.TrimSpace(strings.Trim(title, `"'`))
	title = strings.Join(strings.Fields(title), " ")
	if len(title) > 120 {
		title = strings.TrimSpace(title[:120])
	}
	return title
}

func sanitizeGeneratedCreateDocTitle(raw string) string {
	title := strings.TrimSpace(raw)
	if title == "" {
		return ""
	}
	title = strings.ReplaceAll(title, "\n", " ")
	title = strings.ReplaceAll(title, "\r", " ")
	title = strings.TrimSuffix(title, ".")
	title = strings.TrimSpace(strings.Trim(title, `"'`))
	title = strings.Join(strings.Fields(title), " ")
	if len(title) > 120 {
		title = strings.TrimSpace(title[:120])
	}
	return title
}

func inferCreateDocTitleFromBody(body string) string {
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		candidate := strings.TrimSpace(line)
		candidate = strings.TrimLeft(candidate, "#*- ")
		candidate = sanitizeGeneratedCreateDocTitle(candidate)
		if candidate == "" {
			continue
		}
		if len(candidate) < 4 {
			continue
		}
		if strings.Contains(strings.ToLower(candidate), "google doc") {
			continue
		}
		return candidate
	}
	return ""
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
