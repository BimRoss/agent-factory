package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var (
	reCreateIssuePhrase = regexp.MustCompile(`(?i)\b(create|open|file|write)\b(?:\s+(?:a|an))?\s+(?:github\s+)?issue\b`)
	reSlackMentionToken = regexp.MustCompile(`(?i)<@[A-Z0-9]+>|@[a-z0-9._-]+`)
	reWhitespace        = regexp.MustCompile(`\s+`)
)

type createIssueDraft struct {
	Title string
	Body  string
	Repo  string
}

type githubIssueCreateAPIRequest struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type githubIssueCreateAPIResponse struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
}

type issueSynthJSON struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

func (e *Engine) runCreateIssue(task Task) (RenderPayload, error) {
	ctx := context.Background()
	cfg := LoadGitHubConfigForEmployee(task.OwnerEmployeeID)
	if cfg.Token == "" {
		return RenderPayload{}, fmt.Errorf("create-issue: set GITHUB_TOKEN or %s_GITHUB_TOKEN", strings.ToUpper(strings.TrimSpace(task.OwnerEmployeeID)))
	}

	defaultRepo := cfg.FullRepo()
	if r := extractNamedField(task.RequestText, "repo"); strings.TrimSpace(r) != "" {
		defaultRepo = normalizeOwnerRepoString(r, defaultRepo)
	}

	threadCtx := ""
	if e.threadContext != nil {
		threadCtx = strings.TrimSpace(e.threadContext(ctx, task))
	}

	raw := strings.TrimSpace(task.RequestText)
	draft, explicitTitle, explicitBody := inferCreateIssueDraft(raw, task.HumanUserID, threadCtx, defaultRepo)
	draft = synthesizeCreateIssueDraftGemini(ctx, e.provider, draft, raw, task.HumanUserID, threadCtx, explicitTitle, explicitBody)

	if needsCreateIssueFollowUp(draft, raw, threadCtx) {
		q, err := geminiCreateIssueFollowUpQuestion(ctx, e.provider, raw, threadCtx, draft)
		if err != nil || strings.TrimSpace(q) == "" {
			q = "I need a clear title and what to change—what should this GitHub issue say in one line?"
		}
		return RenderPayload{
			OutputID:     fmt.Sprintf("%s-create-issue", task.ID),
			FallbackText: strings.TrimSpace(q),
			FinalSummary: "create-issue awaiting detail",
			Transport:    "slack",
		}, nil
	}

	url, num, err := postGitHubIssue(ctx, cfg.Token, draft, task.HumanUserID)
	if err != nil {
		return RenderPayload{}, err
	}
	text := fmt.Sprintf("Created GitHub issue #%d — %s", num, url)
	return RenderPayload{
		OutputID:     fmt.Sprintf("%s-create-issue", task.ID),
		FallbackText: text,
		FinalSummary: "create-issue completed",
		Transport:    "slack",
	}, nil
}

func needsCreateIssueFollowUp(d createIssueDraft, raw, threadCtx string) bool {
	plain := normalizeIssueComplaint(raw)
	if plain == "" || plain == "Issue requested from Slack conversation." {
		if strings.TrimSpace(threadCtx) == "" {
			return true
		}
	}
	t := strings.TrimSpace(d.Title)
	b := strings.TrimSpace(d.Body)
	if t == "" || len([]rune(t)) < 4 {
		return true
	}
	if len([]rune(b)) < 24 {
		return true
	}
	return false
}

func inferCreateIssueDraft(rawText, requestUserID, threadContext, repo string) (createIssueDraft, bool, bool) {
	title := extractNamedField(rawText, "title")
	body := extractNamedField(rawText, "body")
	explicitTitle := strings.TrimSpace(title) != ""
	explicitBody := strings.TrimSpace(body) != ""

	cleanComplaint := normalizeIssueComplaint(rawText)
	if title == "" {
		title = inferCreateIssueTitle(cleanComplaint, threadContext)
	}
	if body == "" {
		body = inferCreateIssueBody(cleanComplaint, requestUserID, threadContext)
	}
	if strings.TrimSpace(repo) == "" {
		repo = "BimRoss/create-issue"
	}

	return createIssueDraft{
		Title: strings.TrimSpace(title),
		Body:  strings.TrimSpace(body),
		Repo:  strings.TrimSpace(repo),
	}, explicitTitle, explicitBody
}

func normalizeIssueComplaint(raw string) string {
	plain := strings.TrimSpace(raw)
	plain = reSlackMentionToken.ReplaceAllString(plain, " ")
	plain = reCreateIssuePhrase.ReplaceAllString(plain, " ")
	plain = strings.ReplaceAll(plain, "-", " ")
	plain = reWhitespace.ReplaceAllString(plain, " ")
	plain = strings.TrimSpace(plain)
	if plain == "" {
		return "Issue requested from Slack conversation."
	}
	return plain
}

func inferCreateIssueTitle(complaint, threadContext string) string {
	complaint = cleanIssueTitleCandidate(complaint)
	complaint = strings.TrimSpace(complaint)
	if complaint != "" {
		for _, sep := range []string{"\n", ".", "!", "?"} {
			if idx := strings.Index(complaint, sep); idx > 0 {
				complaint = strings.TrimSpace(complaint[:idx])
				break
			}
		}
		if complaint != "" {
			return truncateRunes(complaint, 110)
		}
	}
	if threadContext != "" {
		flat := strings.Join(strings.Fields(threadContext), " ")
		if flat != "" {
			return truncateRunes(flat, 110)
		}
	}
	return "Issue from Slack thread"
}

func inferCreateIssueBody(complaint, requestUserID, threadContext string) string {
	var b strings.Builder
	b.WriteString("Reported from Slack")
	if strings.TrimSpace(requestUserID) != "" {
		b.WriteString(" by <@")
		b.WriteString(strings.TrimSpace(requestUserID))
		b.WriteString(">.")
	} else {
		b.WriteString(".")
	}
	b.WriteString("\n\n## Complaint\n")
	if strings.TrimSpace(complaint) == "" {
		b.WriteString("No explicit complaint text was provided.")
	} else {
		b.WriteString(strings.TrimSpace(complaint))
	}
	if strings.TrimSpace(threadContext) != "" {
		b.WriteString("\n\n## Thread Context\n")
		b.WriteString(truncateRunes(strings.TrimSpace(threadContext), 2600))
	}
	return b.String()
}

func cleanIssueTitleCandidate(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	raw = reSlackMentionToken.ReplaceAllString(raw, " ")
	raw = reCreateIssuePhrase.ReplaceAllString(raw, " ")
	raw = reWhitespace.ReplaceAllString(raw, " ")
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, ":- ")
	if raw == "" {
		return "Issue from Slack thread"
	}
	return raw
}

func synthesizeCreateIssueDraftGemini(ctx context.Context, provider ProviderConfig, draft createIssueDraft, rawText, requestUserID, threadCtx string, explicitTitle, explicitBody bool) createIssueDraft {
	if explicitTitle && explicitBody {
		draft.Title = cleanIssueTitleCandidate(draft.Title)
		return draft
	}
	synthCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	system := strings.TrimSpace(`
You draft GitHub issues from Slack. Reply with JSON only (no markdown fences): {"title":"...","body":"..."}
Rules:
- title: concise noun phrase, no Slack @mentions, no "create issue" boilerplate, max ~110 chars.
- body: markdown OK; include Problem, Context/Evidence, Proposed next step when useful.
`)
	user := fmt.Sprintf(
		"Requester Slack user id: %s\n\nRaw Slack message:\n%s\n\nCurrent title draft:\n%s\n\nCurrent body draft:\n%s\n\nThread context:\n%s",
		strings.TrimSpace(requestUserID),
		strings.TrimSpace(rawText),
		strings.TrimSpace(draft.Title),
		strings.TrimSpace(draft.Body),
		truncateRunes(strings.TrimSpace(threadCtx), 3200),
	)

	requestBody := map[string]any{
		"systemInstruction": map[string]any{
			"parts": []any{map[string]any{"text": system}},
		},
		"contents": []any{
			map[string]any{"parts": []any{map[string]any{"text": user}}},
		},
		"generationConfig": map[string]any{
			"temperature":      0.35,
			"maxOutputTokens":  2048,
			"topP":             0.9,
			"responseMimeType": "application/json",
		},
	}

	text, _, err := runGeminiGenerate(synthCtx, provider, requestBody)
	if err != nil {
		draft.Title = cleanIssueTitleCandidate(draft.Title)
		return draft
	}
	out, err := parseIssueSynthJSON(text)
	if err != nil {
		draft.Title = cleanIssueTitleCandidate(draft.Title)
		return draft
	}
	if !explicitTitle {
		if t := cleanIssueTitleCandidate(out.Title); t != "" {
			draft.Title = truncateRunes(t, 110)
		}
	}
	if !explicitBody {
		if b := strings.TrimSpace(out.Body); b != "" {
			draft.Body = truncateRunes(b, 6000)
		}
	}
	draft.Title = cleanIssueTitleCandidate(draft.Title)
	return draft
}

func geminiCreateIssueFollowUpQuestion(ctx context.Context, provider ProviderConfig, raw, threadCtx string, d createIssueDraft) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	system := `You are Ross on an AI team in Slack. The human wants a GitHub issue but there isn't enough to file yet.
Write ONE short Slack message (2–4 sentences): ask only for what you still need (title intent, scope, or acceptance), friendly and specific. No confirmation buttons language.`
	user := fmt.Sprintf("Their message:\n%s\n\nThread context:\n%s\n\nDraft title so far: %s\nDraft body so far (truncated): %s",
		strings.TrimSpace(raw),
		truncateRunes(strings.TrimSpace(threadCtx), 2000),
		strings.TrimSpace(d.Title),
		truncateRunes(strings.TrimSpace(d.Body), 400),
	)
	requestBody := map[string]any{
		"systemInstruction": map[string]any{"parts": []any{map[string]any{"text": system}}},
		"contents":          []any{map[string]any{"parts": []any{map[string]any{"text": user}}}},
		"generationConfig": map[string]any{
			"temperature":      0.45,
			"maxOutputTokens":  256,
			"responseMimeType": "text/plain",
		},
	}
	text, _, err := runGeminiGenerate(ctx, provider, requestBody)
	return strings.TrimSpace(text), err
}

func postGitHubIssue(ctx context.Context, token string, pending createIssueDraft, requestUserID string) (string, int, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", 0, fmt.Errorf("create-issue: missing GitHub token")
	}
	owner, repoName, err := splitOwnerRepo(strings.TrimSpace(pending.Repo))
	if err != nil {
		return "", 0, fmt.Errorf("create-issue: %w", err)
	}
	body := strings.TrimSpace(pending.Body)
	if requestUserID != "" {
		footer := fmt.Sprintf("**Slack requester:** `%s`", strings.TrimSpace(requestUserID))
		if body != "" {
			body += "\n\n---\n\n" + footer
		} else {
			body = footer
		}
	}
	payload := githubIssueCreateAPIRequest{
		Title: strings.TrimSpace(pending.Title),
		Body:  body,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", 0, fmt.Errorf("create-issue: encode: %w", err)
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues", owner, repoName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return "", 0, fmt.Errorf("create-issue: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("create-issue: http: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, fmt.Errorf("create-issue: github %d: %s", resp.StatusCode, truncateRunes(strings.TrimSpace(string(respBody)), 280))
	}
	var out githubIssueCreateAPIResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", 0, fmt.Errorf("create-issue: decode: %w", err)
	}
	if strings.TrimSpace(out.HTMLURL) == "" || out.Number <= 0 {
		return "", 0, fmt.Errorf("create-issue: github response missing issue url/number")
	}
	return strings.TrimSpace(out.HTMLURL), out.Number, nil
}

func splitOwnerRepo(raw string) (owner, repo string, err error) {
	raw = strings.TrimSpace(raw)
	parts := strings.Split(raw, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("repo must be owner/repo")
	}
	owner = strings.TrimSpace(parts[0])
	repo = strings.TrimSpace(parts[1])
	if owner == "" || repo == "" {
		return "", "", fmt.Errorf("repo must be owner/repo")
	}
	return owner, repo, nil
}

func normalizeOwnerRepoString(fromMsg, fallbackFull string) string {
	fromMsg = strings.TrimSpace(fromMsg)
	if fromMsg == "" {
		return fallbackFull
	}
	if strings.Contains(fromMsg, "/") {
		return fromMsg
	}
	// Allow shorthand "create-issue" with owner from fallback
	fOwner, _, ferr := splitOwnerRepo(fallbackFull)
	if ferr != nil {
		return fallbackFull
	}
	return fOwner + "/" + fromMsg
}

func parseIssueSynthJSON(raw string) (issueSynthJSON, error) {
	s := stripMarkdownJSONFence(strings.TrimSpace(raw))
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j > i {
			s = s[i : j+1]
		}
	}
	var out issueSynthJSON
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return issueSynthJSON{}, err
	}
	return out, nil
}

func stripMarkdownJSONFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSpace(s)
	if strings.HasPrefix(strings.ToLower(s), "json") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = strings.TrimSpace(s[i+1:])
		}
	}
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	return strings.TrimSpace(s)
}

func extractNamedField(raw, field string) string {
	field = strings.ToLower(strings.TrimSpace(field))
	if field == "" {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		lower := strings.ToLower(line)
		prefix := field + ":"
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		value := strings.TrimSpace(line[len(prefix):])
		if field != "body" {
			return value
		}
		for j := i + 1; j < len(lines); j++ {
			next := lines[j]
			nextTrim := strings.TrimSpace(next)
			lowerNext := strings.ToLower(nextTrim)
			if strings.HasPrefix(lowerNext, "title:") || strings.HasPrefix(lowerNext, "labels:") ||
				strings.HasPrefix(lowerNext, "assignees:") || strings.HasPrefix(lowerNext, "repo:") ||
				strings.HasPrefix(lowerNext, "number:") || strings.HasPrefix(lowerNext, "status:") ||
				strings.HasPrefix(lowerNext, "repository:") {
				break
			}
			if value == "" {
				value = nextTrim
			} else {
				value += "\n" + next
			}
		}
		return strings.TrimSpace(value)
	}
	return ""
}
