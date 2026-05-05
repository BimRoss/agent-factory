package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type GeminiResearchResult struct {
	Summary   string
	Citations []string
}

// FormatResearchResultForSlack builds plaintext for Slack from a Gemini research call (read-web).
// Grounding citation URLs are kept on GeminiResearchResult for logs/telemetry but omitted from user-visible text.
func FormatResearchResultForSlack(research GeminiResearchResult) string {
	return strings.TrimSpace(research.Summary)
}

// GeminiConversationResult is the outcome of a single conversational generateContent call.
// Citations are populated when Google Search grounding was used (same shape as read-web).
type GeminiConversationResult struct {
	Text      string
	Citations []string
}

var multiSpeakerLabelRE = regexp.MustCompile(`(?im)^(?:\*{1,2})?\s*(?:teammate|colleague|colleage|speaker|side)\s*([a-c1-3]|[ivx]+)\s*:\s*`)

// runGeminiConversation calls the model with memory + thread context. There is no
// hardcoded user-visible fallback—every posted reply should come from the model.
// Attempt chain: primary → without web search → recovery persona → minimal prompt.
func runGeminiConversation(ctx context.Context, provider ProviderConfig, employeeID, userText, mode, memoryContext string) (GeminiConversationResult, error) {
	p := provider
	var lastErr error
	setErr := func(err error) {
		if err != nil {
			lastErr = err
		}
	}

	if res, err := runGeminiConversationOnce(ctx, p, employeeID, userText, mode, memoryContext); err == nil && strings.TrimSpace(res.Text) != "" {
		return res, nil
	} else {
		setErr(err)
	}

	if p.EnableWebResearch {
		p2 := p
		p2.EnableWebResearch = false
		if res, err := runGeminiConversationOnce(ctx, p2, employeeID, userText, mode, memoryContext); err == nil && strings.TrimSpace(res.Text) != "" {
			return res, nil
		} else {
			setErr(err)
		}
	}

	pRecover := p
	pRecover.EnableWebResearch = false
	if res, err := runGeminiConversationRecovery(ctx, pRecover, employeeID, userText, mode, memoryContext); err == nil && strings.TrimSpace(res.Text) != "" {
		return res, nil
	} else {
		setErr(err)
	}

	pMin := p
	pMin.EnableWebResearch = false
	if res, err := runGeminiConversationMinimal(ctx, pMin, employeeID, userText); err == nil && strings.TrimSpace(res.Text) != "" {
		return res, nil
	} else {
		setErr(err)
	}

	if lastErr != nil {
		return GeminiConversationResult{}, fmt.Errorf("gemini conversation exhausted retries: %w", lastErr)
	}
	return GeminiConversationResult{}, fmt.Errorf("gemini conversation exhausted retries (empty or blocked)")
}

func runGeminiConversationRecovery(ctx context.Context, provider ProviderConfig, employeeID, userText, mode, memoryContext string) (GeminiConversationResult, error) {
	prompt := strings.TrimSpace(userText)
	if prompt == "" {
		prompt = "Say hello as a teammate and offer one concrete way you can help this channel today."
	}
	systemInstruction := conversationRecoverySystemInstruction(employeeID, mode)
	if strings.TrimSpace(memoryContext) != "" {
		systemInstruction += "\n\nContext (team channel/thread memory and transcript when present—use it):\n" + strings.TrimSpace(memoryContext)
	}
	systemInstruction += "\n\nReply substantively to their latest message. You are an AI team member: if they asked for something explicit (debate, role-play, creative prompt, general knowledge, humor), do that first in persona. Otherwise move work forward—recap, next step, owner, risk, or a crisp question tied to what they said—not generic meta-pushback unless there is truly nothing to respond to."

	requestBody := map[string]any{
		"systemInstruction": map[string]any{
			"parts": []any{map[string]any{"text": systemInstruction}},
		},
		"contents": []any{
			map[string]any{
				"parts": []any{map[string]any{"text": prompt}},
			},
		},
		"generationConfig": map[string]any{
			"temperature":      0.55,
			"maxOutputTokens":  768,
			"topP":             0.92,
			"responseMimeType": "text/plain",
		},
	}

	text, parsed, err := runGeminiGenerate(ctx, provider, requestBody)
	if err != nil {
		return GeminiConversationResult{}, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return GeminiConversationResult{}, fmt.Errorf("gemini recovery returned empty text")
	}
	if looksLikeMultiSpeakerReply(text) {
		return GeminiConversationResult{}, fmt.Errorf("gemini recovery returned multi-speaker roleplay")
	}
	return GeminiConversationResult{Text: text, Citations: groundingCitationsFromParsed(parsed)}, nil
}

func runGeminiConversationMinimal(ctx context.Context, provider ProviderConfig, employeeID, userText string) (GeminiConversationResult, error) {
	prompt := truncateRunes(strings.TrimSpace(userText), 6000)
	if prompt == "" {
		prompt = "Brief hello—as a teammate offer how you can help this channel."
	}
	systemInstruction := conversationMinimalSystemInstruction(employeeID)

	requestBody := map[string]any{
		"systemInstruction": map[string]any{
			"parts": []any{map[string]any{"text": systemInstruction}},
		},
		"contents": []any{
			map[string]any{
				"parts": []any{map[string]any{"text": prompt}},
			},
		},
		"generationConfig": map[string]any{
			"temperature":      0.65,
			"maxOutputTokens":  512,
			"topP":             0.92,
			"responseMimeType": "text/plain",
		},
	}

	text, _, err := runGeminiGenerate(ctx, provider, requestBody)
	if err != nil {
		return GeminiConversationResult{}, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return GeminiConversationResult{}, fmt.Errorf("gemini minimal returned empty text")
	}
	if looksLikeMultiSpeakerReply(text) {
		return GeminiConversationResult{}, fmt.Errorf("gemini minimal returned multi-speaker roleplay")
	}
	return GeminiConversationResult{Text: text}, nil
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

func conversationRecoverySystemInstruction(employeeID, mode string) string {
	if normalizeID(mode) == "task" {
		return "You are a hands-on AI teammate in task mode. Break the next one or two execution moves from the user’s message; name dependencies, owners, and risks. No hardcoded templates—write a real reply."
	}
	switch normalizeID(employeeID) {
	case "joanne":
		return "You are Joanne (executive operations) on this team. Write a real Slack message: address what they asked, use any context you were given, and move work forward (recap, routing, or next step)."
	case "ross":
		return "You are Ross (engineering / automation) on this team. Write a real Slack message: constraints, smallest verifiable step, and what would prove you’re wrong—grounded in their message."
	case "alex":
		return "You are Alex (GTM / revenue thinking) on this team. Write a real Slack message grounded in their message. If they asked for a non-work exercise (debate, hypothetical, creative bit), engage in your voice first; only steer to customers/offers when they clearly want GTM help. Never speak as Ross or another teammate’s name."
	case "tim":
		return "You are Tim (experiments, networking, practical decision quality) on this team. Write a real Slack message grounded in their message. If they asked for a non-work exercise (debate, thought experiment, creative bit), engage in your voice first; only steer to experiments/decisions when they clearly want that lane. Never speak as Ross or another teammate’s name."
	case "garth":
		return "You are Garth (research / synthesis / intern lane) on this team. Write a real Slack message: pull threads together, name what to verify next, and keep it helpful—not generic engineering triage. Never speak as Ross or another teammate’s name."
	case "anna":
		return "You are Anna (visual / image work) on this team. Write a real Slack message: what to show, constraints for visuals, and the next step—grounded in their message. Never speak as Ross or another teammate’s name."
	default:
		return "You are an AI teammate in this Slack workspace. Write a substantive reply that moves the thread forward. Use your configured employee identity—do not introduce yourself as Ross unless you are Ross."
	}
}

func conversationMinimalSystemInstruction(employeeID string) string {
	switch normalizeID(employeeID) {
	case "joanne":
		return "You are Joanne—exec ops on this AI team. Slack reply only: 2–7 short sentences. Respond directly to the message; propose a next step or recap. Never refuse with boilerplate—say something useful."
	case "ross":
		return "You are Ross—engineering/automation on this AI team. Slack reply only: 2–7 short sentences. Technical and concrete; smallest useful step from what they said."
	case "alex":
		return "You are Alex—GTM/revenue lens on this AI team. Slack reply only: 2–7 short sentences. If they asked for a debate or non-work prompt, do that in your voice first; otherwise customer and offer clarity. No engineering-default voice. Never call yourself Ross."
	case "tim":
		return "You are Tim—experiments and practical judgment on this AI team. Slack reply only: 2–7 short sentences. If they asked for a debate or non-work prompt, do that in your voice first; otherwise small tests, tradeoffs, next move. Never call yourself Ross."
	case "garth":
		return "You are Garth—research/synthesis on this AI team. Slack reply only: 2–7 short sentences. Summarize what matters and what to check next. Never call yourself Ross."
	case "anna":
		return "You are Anna—visuals on this AI team. Slack reply only: 2–7 short sentences. Image constraints and outcomes. Never call yourself Ross."
	default:
		return "You are an AI teammate in Slack. 2–7 short sentences—direct, helpful, moves the conversation forward. Match your configured employee identity; do not call yourself Ross unless you are Ross."
	}
}

func runGeminiConversationOnce(ctx context.Context, provider ProviderConfig, employeeID, userText, mode, memoryContext string) (GeminiConversationResult, error) {
	prompt := strings.TrimSpace(userText)
	if prompt == "" {
		prompt = "Say hello and ask one concise clarifying question."
	}
	systemInstruction := conversationSystemInstruction(employeeID)
	systemInstruction += "\n\n" + conversationToneConstraints()
	if strings.TrimSpace(memoryContext) != "" {
		systemInstruction += "\n\nUse this memory context when useful, but prioritize the latest human message as ground truth:\n" + strings.TrimSpace(memoryContext)
	}
	if normalizeID(mode) == "task" {
		systemInstruction += "\n\nTask mode is active: keep replies execution-focused and rely on channel/thread memory only when it helps complete the task."
	}
	if provider.EnableWebResearch {
		systemInstruction += "\n\n" + conversationWebSearchPolicy()
	}

	maxTokens := 768
	temp := 0.55
	if provider.EnableWebResearch {
		// Room for grounded answers + visible prose; Pro/thinking models can burn budget before emitting text.
		maxTokens = 2048
		// Lower variance when search tools are attached so personas behave more alike on factual turns.
		temp = 0.35
		if provider.MaxOutputTokensWithWeb > 0 {
			maxTokens = provider.MaxOutputTokensWithWeb
		}
	} else if provider.MaxOutputTokens > 0 {
		maxTokens = provider.MaxOutputTokens
	}

	requestBody := map[string]any{
		"systemInstruction": map[string]any{
			"parts": []any{
				map[string]any{
					"text": systemInstruction,
				},
			},
		},
		"contents": []any{
			map[string]any{
				"parts": []any{
					map[string]any{
						"text": prompt,
					},
				},
			},
		},
		"generationConfig": map[string]any{
			"temperature":      temp,
			"maxOutputTokens":  maxTokens,
			"topP":             0.92,
			"responseMimeType": "text/plain",
		},
	}
	if provider.EnableWebResearch {
		requestBody["tools"] = []any{
			map[string]any{
				"google_search": map[string]any{},
			},
		}
	}

	text, parsed, err := runGeminiGenerate(ctx, provider, requestBody)
	if err != nil {
		return GeminiConversationResult{}, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return GeminiConversationResult{}, fmt.Errorf("gemini returned empty conversation candidate")
	}
	if looksLikeMultiSpeakerReply(text) {
		return GeminiConversationResult{}, fmt.Errorf("gemini returned multi-speaker roleplay")
	}
	return GeminiConversationResult{
		Text:      text,
		Citations: groundingCitationsFromParsed(parsed),
	}, nil
}

func conversationWebSearchPolicy() string {
	return strings.TrimSpace(`
Google Search is available on this turn (grounding). This applies to every employee persona the same way.

For questions about current public events, news, geopolitics, or “give me an update on…”, you must lean on search-backed facts first—do not answer from training memory alone, and do not refuse with “there is no war / I can’t verify” unless you have actually used search and the results still support that conclusion. Summarize what sources indicate; note uncertainty or conflicting reports briefly if needed.

Skip search only when the message is purely internal coordination or social (“how is everyone”) with no external-fact request—though if a single message mixes both, handle the external-fact part with search.

Stay concise. Do not add a "Sources" section, bullet URLs, or citation links in your reply—summarize only. Web results can be wrong or noisy; avoid inventing specific dates or claims not reflected in retrieved material.
`)
}

func groundingCitationsFromParsed(parsed geminiGenerateResponse) []string {
	citationSet := map[string]struct{}{}
	out := make([]string, 0)
	for _, cand := range parsed.Candidates {
		for _, chunk := range cand.GroundingMetadata.GroundingChunks {
			link := strings.TrimSpace(chunk.Web.URI)
			if link == "" {
				continue
			}
			if _, exists := citationSet[link]; exists {
				continue
			}
			citationSet[link] = struct{}{}
			out = append(out, link)
		}
	}
	return out
}

func runGeminiResearch(ctx context.Context, provider ProviderConfig, query string) (GeminiResearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return GeminiResearchResult{}, fmt.Errorf("research query is required")
	}

	requestBody := map[string]any{
		"contents": []any{
			map[string]any{
				"parts": []any{
					map[string]any{
						"text": "Research the following query and respond with a concise summary. Do not include source links or a Sources section.\n\nQuery: " + query,
					},
				},
			},
		},
		"generationConfig": map[string]any{
			"temperature": 0.2,
		},
	}
	if provider.EnableWebResearch {
		requestBody["tools"] = []any{
			map[string]any{
				"google_search": map[string]any{},
			},
		}
	}

	summary, parsed, err := runGeminiGenerate(ctx, provider, requestBody)
	if err != nil {
		return GeminiResearchResult{}, err
	}
	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return GeminiResearchResult{}, fmt.Errorf("Gemini response missing candidate text")
	}

	summary = strings.TrimSpace(summary)
	citations := groundingCitationsFromParsed(parsed)
	return GeminiResearchResult{
		Summary:   summary,
		Citations: citations,
	}, nil
}

type geminiGenerateResponse struct {
	Candidates []struct {
		FinishReason string `json:"finishReason"`
		Content      struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
		GroundingMetadata struct {
			GroundingChunks []struct {
				Web struct {
					URI string `json:"uri"`
				} `json:"web"`
			} `json:"groundingChunks"`
		} `json:"groundingMetadata"`
	} `json:"candidates"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
}

func runGeminiGenerate(ctx context.Context, provider ProviderConfig, requestBody map[string]any) (string, geminiGenerateResponse, error) {
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return "", geminiGenerateResponse{}, fmt.Errorf("marshal Gemini request: %w", err)
	}

	endpoint := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s",
		url.PathEscape(strings.TrimSpace(provider.Model)),
		url.QueryEscape(strings.TrimSpace(provider.APIKey)),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", geminiGenerateResponse{}, fmt.Errorf("create Gemini request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", geminiGenerateResponse{}, fmt.Errorf("Gemini request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", geminiGenerateResponse{}, fmt.Errorf("read Gemini response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", geminiGenerateResponse{}, fmt.Errorf("Gemini returned status %d", resp.StatusCode)
	}

	var parsed geminiGenerateResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", geminiGenerateResponse{}, fmt.Errorf("parse Gemini response: %w", err)
	}
	if text := extractGeminiCandidateText(parsed); text != "" {
		return text, parsed, nil
	}
	if strings.TrimSpace(parsed.PromptFeedback.BlockReason) != "" {
		return "", parsed, fmt.Errorf("Gemini response blocked: %s", strings.TrimSpace(parsed.PromptFeedback.BlockReason))
	}
	fr := firstCandidateFinishReason(parsed)
	return "", parsed, fmt.Errorf("Gemini returned empty candidate text (finishReason=%q)", fr)
}

func conversationToneConstraints() string {
	return strings.TrimSpace(`
Voice (non-negotiable):
- Write like a sharp colleague in Slack: plain English, short sentences, no corporate polish.
- Formatting: Slack mrkdwn uses single asterisks for bold (*like this*) and underscores for italic—not Markdown **double** asterisks (those render literally in Slack).
- Threading: when prior Slack thread lines are provided, read them. Do not repeat the same generic “what’s the priority?” pushback if the human already added specifics; respond to what they actually said.
- Explicit human requests win: if they name a clear non-work ask (debate between personas, role-play, creative exercise, general knowledge, “play along”), fulfill it in persona first. Do not refuse, moralize, or pivot to pipeline, revenue, or “what we should work on” unless they asked for prioritization or the topic is unsafe.
- Default length: about 2–6 sentences unless the user explicitly asks for depth (longer is fine when they asked for depth or a structured debate).
- Answer what they actually asked. If the message is vague or a room-wide ping (@here / “how is everyone”), say what’s unclear or what decision is missing—don’t pretend enthusiasm fixes ambiguity.
- Lean skeptical: name tradeoffs, risks, hidden assumptions, or the cheapest way to falsify an idea. Prefer one precise question over a three-step “framework.”
- Do not open with praise or hype: avoid leading with words/phrases like “great,” “nice,” “love,” “excited,” “amazing,” “strong signal,” “great signal,” “nice direction,” or calling their note a “signal” or “direction” unless they asked for directional feedback.
- Avoid motivational coaching and cheerleading unless they explicitly asked for morale. No fake positivity to “balance” critique.
- Do not paste your role label (“From an operations lens…”) unless it genuinely disambiguates; prefer direct wording.
- You are exactly one speaker in this reply. Never simulate multiple participants or output role labels like "Teammate A:", "Colleague B:", or "Side 1:".
`)
}

func conversationSystemInstruction(employeeID string) string {
	switch normalizeID(employeeID) {
	case "joanne":
		return strings.TrimSpace(`
You are Joanne (executive operations). Be direct and practical: clarify ownership, sequencing, and what decision or artifact would unblock progress.
When thread transcript or memory context describes recent company or channel activity, answer from that material—summarize what’s going on instead of asking the human to sharpen a prompt you can already ground.
When the human’s ask is thin and there is no usable context, push gently on scope, stakeholders, and deadlines instead of offering generic process cheerleading.
`)
	case "ross":
		return strings.TrimSpace(`
You are Ross (engineering / automation). Be direct and practical: name constraints, failure modes, and the smallest verifiable step.
When the human’s ask is thin, ask what “done” means or what changed observably—avoid vague iterative platitudes.
`)
	case "alex":
		return strings.TrimSpace(`
You are Alex (GTM / revenue). Be direct about customers, offers, and distribution when that is what they want—concrete next moves, not generic engineering triage.
If they clearly asked for something else first (debate, creative prompt, general topic), do that in your voice; optional one-line commercial tie-in only after, and only if it fits without deflecting.
When the human wants business help but the ask is thin on specifics, ask who the customer is or what single move would change revenue this week. Never introduce yourself as Ross or another teammate.
`)
	case "tim":
		return strings.TrimSpace(`
You are Tim (experiments, networking, decision quality). Be practical: reversible tests, relationship-aware asks, and one crisp next step when that is what they want.
If they clearly asked for something else first (debate, thought experiment, creative prompt), do that in your voice; optional one-line experiment tie-in only after, and only if it fits without deflecting.
When they want judgment but the ask is thin, name the smallest experiment or conversation that would reduce uncertainty. Never introduce yourself as Ross or another teammate.
`)
	case "garth":
		return strings.TrimSpace(`
You are Garth (research / synthesis). Pull threads together: what we know, what’s still fuzzy, and what to verify next—without sounding like the engineering lead.
Never introduce yourself as Ross or another teammate.
`)
	case "anna":
		return strings.TrimSpace(`
You are Anna (visual / image work). Be concrete about what to show, references or constraints for visuals, and the next step to get there.
Never introduce yourself as Ross or another teammate.
`)
	default:
		return "Reply as a concise teammate: concrete and human. If your runtime identity is not Ross, do not use Ross’s name or engineering-automation voice—stay in your own lane."
	}
}

func extractGeminiCandidateText(parsed geminiGenerateResponse) string {
	for _, candidate := range parsed.Candidates {
		for _, part := range candidate.Content.Parts {
			if text := strings.TrimSpace(part.Text); text != "" {
				return text
			}
		}
	}
	return ""
}

func firstCandidateFinishReason(parsed geminiGenerateResponse) string {
	if len(parsed.Candidates) == 0 {
		return ""
	}
	return strings.TrimSpace(parsed.Candidates[0].FinishReason)
}

func looksLikeMultiSpeakerReply(text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	matches := multiSpeakerLabelRE.FindAllStringSubmatch(text, -1)
	if len(matches) >= 2 {
		return true
	}
	if len(matches) == 1 {
		return true
	}
	return false
}
