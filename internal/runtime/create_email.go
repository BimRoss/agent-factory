package runtime

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/bimross/agent-factory/internal/emailaction"
	"github.com/bimross/agent-factory/internal/gmailsender"
	"github.com/bimross/agent-factory/internal/htmlemail"
)

const createEmailDefaultSubject = "Note from Make A Company"

var (
	reHTMLTagPreview = regexp.MustCompile(`(?s)<[^>]+>`)
)

func (e *Engine) runCreateEmail(ctx context.Context, task Task) (RenderPayload, error) {
	gmailCfg := LoadGmailOAuthConfigForEmployee(task.OwnerEmployeeID)
	if err := gmailCfg.Validate(); err != nil {
		return RenderPayload{}, err
	}

	threadAnchor := TermsToolThreadAnchor(task.ThreadTS, task.MessageTS)
	draft, draftActive, draftErr := loadCreateEmailDraftState(ctx, task.ChannelID, task.HumanUserID, threadAnchor)
	if draftErr != nil {
		log.Printf("create-email: draft load err=%v", draftErr)
	}
	action, matched, parseErr := emailaction.ParseSendEmailAction(task.RequestText)
	if !matched {
		if !draftActive {
			return RenderPayload{
				OutputID:     fmt.Sprintf("%s-create-email-help", task.ID),
				FallbackText: "Tell me what to send and I will gather the fields for you. Example: `send an email with 3 short paragraphs about the launch`, optional `subject is ...`, optional `to: ...`, optional `button is ...` + `link is https://...`.",
				FinalSummary: "create-email help",
				Transport:    "slack",
			}, nil
		}
		patch, hasPatch, patchErr := emailaction.ParseSendEmailPatch(task.RequestText)
		if patchErr != nil {
			return RenderPayload{
				OutputID:     fmt.Sprintf("%s-create-email-followup", task.ID),
				FallbackText: fmt.Sprintf("I’m still gathering this email draft, but I couldn’t apply that update yet: %v. Please share the corrected field value.", patchErr),
				FinalSummary: "create-email followup parse error",
				Transport:    "slack",
			}, nil
		}
		if hasPatch {
			draft = mergeCreateEmailDraft(draft, patch)
		}
		action = draft.toAction()
	} else {
		patch := action
		if parseErr != nil {
			patch, _, _ = emailaction.ParseSendEmailPatch(task.RequestText)
		}
		draft = mergeCreateEmailDraft(draft, patch)
		action = draft.toAction()
		if parseErr != nil {
			_ = saveCreateEmailDraftState(ctx, task.ChannelID, task.HumanUserID, threadAnchor, draft)
			return RenderPayload{
				OutputID:     fmt.Sprintf("%s-create-email-followup", task.ID),
				FallbackText: fmt.Sprintf("I captured the draft and need one fix before I can continue: %v", parseErr),
				FinalSummary: "create-email followup needed",
				Transport:    "slack",
			}, nil
		}
	}

	recipients, err := resolveRecipientsForCreateEmail(ctx, task, action)
	if err != nil {
		_ = saveCreateEmailDraftState(ctx, task.ChannelID, task.HumanUserID, threadAnchor, draft)
		return RenderPayload{
			OutputID:     fmt.Sprintf("%s-create-email-to", task.ID),
			FallbackText: fmt.Sprintf("I still need recipient details before sending: %v. If this should go to you, I can use your profile email; otherwise share `to: person@domain`.", err),
			FinalSummary: "create-email recipient followup",
			Transport:    "slack",
		}, nil
	}
	if len(recipients) == 0 {
		_ = saveCreateEmailDraftState(ctx, task.ChannelID, task.HumanUserID, threadAnchor, draft)
		return RenderPayload{
			OutputID:     fmt.Sprintf("%s-create-email-to", task.ID),
			FallbackText: "I need a reachable email for you (the sender). Add `to: you@domain` for someone else, or ensure your Slack profile email / Make A Company email is available when sending to yourself.",
			FinalSummary: "create-email recipient followup",
			Transport:    "slack",
		}, nil
	}

	ctaText := strings.TrimSpace(action.CTAText)
	ctaURL := strings.TrimSpace(action.CTAURL)
	if (ctaText != "" || ctaURL != "") && (ctaText == "" || ctaURL == "") {
		_ = saveCreateEmailDraftState(ctx, task.ChannelID, task.HumanUserID, threadAnchor, draft)
		return RenderPayload{
			OutputID:     fmt.Sprintf("%s-create-email-cta", task.ID),
			FallbackText: "I captured the draft. For a primary button, provide both `button:` label text and `link:` / `url:` with an absolute http(s) URL.",
			FinalSummary: "create-email cta followup",
			Transport:    "slack",
		}, nil
	}
	if ctaText != "" && ctaURL != "" {
		if err := htmlemail.ValidateCTAURL(ctaURL); err != nil {
			_ = saveCreateEmailDraftState(ctx, task.ChannelID, task.HumanUserID, threadAnchor, draft)
			return RenderPayload{
				OutputID:     fmt.Sprintf("%s-create-email-cta", task.ID),
				FallbackText: fmt.Sprintf("That button link still needs a valid URL (%v). Please send `link: https://...`.", err),
				FinalSummary: "create-email cta followup",
				Transport:    "slack",
			}, nil
		}
	}

	threadCtx := ""
	if e.threadContext != nil {
		threadCtx = strings.TrimSpace(e.threadContext(ctx, task))
	}

	if strings.TrimSpace(action.BodyText) == "" && strings.TrimSpace(action.BodyInstruction) == "" {
		_ = saveCreateEmailDraftState(ctx, task.ChannelID, task.HumanUserID, threadAnchor, draft)
		return RenderPayload{
			OutputID:     fmt.Sprintf("%s-create-email-followup", task.ID),
			FallbackText: "What should the email say? Share `instruction:` (what to write) or `body:` (exact prose/HTML), and I’ll draft the preview.",
			FinalSummary: "create-email body followup",
			Transport:    "slack",
		}, nil
	}

	bodyFragment, genErr := buildCreateEmailBodyHTML(ctx, e.provider, task, action, threadCtx)
	if genErr != nil {
		_ = saveCreateEmailDraftState(ctx, task.ChannelID, task.HumanUserID, threadAnchor, draft)
		return RenderPayload{}, genErr
	}
	if strings.TrimSpace(bodyFragment) == "" {
		return RenderPayload{}, fmt.Errorf("create-email: empty body after normalization")
	}

	subject := strings.TrimSpace(action.Subject)
	if subject == "" {
		if inferred, err := generateCreateEmailSubject(ctx, e.provider, task, action, threadCtx, bodyFragment); err != nil {
			log.Printf("create-email: subject generation failed: %v", err)
		} else if strings.TrimSpace(inferred) != "" {
			subject = strings.TrimSpace(inferred)
		}
	}
	if subject == "" {
		subject = createEmailDefaultSubject
	}

	bodyHTML := bodyFragment
	if ctaText != "" && ctaURL != "" {
		bodyHTML = htmlemail.StripAnchorsMatchingCTAURL(bodyHTML, ctaURL)
	}
	bodyHTML = htmlemail.BuildBrandedEmailInner(bodyHTML, ctaText, ctaURL)
	if strings.TrimSpace(bodyHTML) == "" {
		return RenderPayload{}, fmt.Errorf("create-email: empty layout after branding")
	}

	anchor := threadAnchor
	preview := formatCreateEmailPreviewMrkdwn(recipients, subject, bodyHTML)
	blocks := BuildEmailConfirmationBlocks(preview, task.ChannelID, task.HumanUserID, anchor)
	_ = clearCreateEmailDraftState(ctx, task.ChannelID, task.HumanUserID, anchor)

	return RenderPayload{
		OutputID:     fmt.Sprintf("%s-create-email-preview", task.ID),
		FallbackText: "Email ready — confirm or cancel",
		FinalSummary: "create-email awaiting confirmation",
		Transport:    "slack",
		BlockKit:     blocks,
		EmailSkillPending: &EmailSkillPendingAnchor{
			ChannelID:     strings.TrimSpace(task.ChannelID),
			RequestUserID: strings.TrimSpace(task.HumanUserID),
			ThreadAnchor:  anchor,
			Recipients:    recipients,
			Subject:       subject,
			BodyHTML:      bodyHTML,
		},
	}, nil
}

func resolveRecipientsForCreateEmail(ctx context.Context, task Task, action emailaction.SendEmailAction) ([]string, error) {
	rawTo := strings.TrimSpace(action.To)
	if rawTo == "" {
		emailAddr, _, err := resolveCreateDocRequesterEmail(ctx, task)
		if err != nil {
			return nil, fmt.Errorf("resolve default recipient: %w", err)
		}
		return []string{strings.TrimSpace(emailAddr)}, nil
	}
	list, err := emailaction.ParseRecipientEmails(rawTo)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(list))
	for _, r := range list {
		r = strings.TrimSpace(strings.ToLower(r))
		if r == "me" || r == "myself" || r == "self" {
			emailAddr, _, err := resolveCreateDocRequesterEmail(ctx, task)
			if err != nil {
				return nil, fmt.Errorf("resolve self recipient: %w", err)
			}
			out = append(out, strings.TrimSpace(emailAddr))
			continue
		}
		out = append(out, r)
	}
	return dedupeEmails(out), nil
}

func buildCreateEmailBodyHTML(ctx context.Context, provider ProviderConfig, task Task, action emailaction.SendEmailAction, threadCtx string) (string, error) {
	if bt := strings.TrimSpace(action.BodyText); bt != "" {
		out := htmlemail.NormalizeFragmentForSend(bt)
		if out == "" {
			return "", fmt.Errorf("create-email: body normalized to empty")
		}
		return out, nil
	}
	instruction := strings.TrimSpace(action.BodyInstruction)
	if instruction == "" {
		return "", fmt.Errorf("create-email: missing body content")
	}
	raw, err := generateCreateEmailHTML(ctx, provider, task, instruction, threadCtx, action)
	if err != nil {
		return "", err
	}
	out := htmlemail.NormalizeFragmentForSend(raw)
	if out == "" {
		return "", fmt.Errorf("create-email: draft normalized to empty")
	}
	return out, nil
}

func generateCreateEmailSubject(ctx context.Context, provider ProviderConfig, task Task, action emailaction.SendEmailAction, threadCtx, bodyHTML string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	instruction := strings.TrimSpace(action.BodyInstruction)
	if instruction == "" {
		instruction = strings.TrimSpace(action.BodyText)
	}
	if instruction == "" {
		instruction = strings.TrimSpace(task.RequestText)
	}
	system := strings.TrimSpace(`
You write concise email subject lines for Joanne.
Return plain text only: subject text with no quotes, no markdown, and no trailing period.
Keep it under 12 words.
`)
	user := fmt.Sprintf(
		"Employee: %s\nSlack request:\n%s\n\nCompose instruction:\n%s\n\nThread context:\n%s\n\nRendered body preview:\n%s\n\nProduce one specific subject line.",
		strings.TrimSpace(task.OwnerEmployeeID),
		truncateRunes(strings.TrimSpace(task.RequestText), 2500),
		truncateRunes(instruction, 5000),
		truncateRunes(threadCtx, 5000),
		truncateRunes(stripHTMLForPreview(bodyHTML), 1400),
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
		return "", fmt.Errorf("create-email: subject generation failed: %w", err)
	}
	return sanitizeGeneratedCreateDocTitle(raw), nil
}

func generateCreateEmailHTML(ctx context.Context, provider ProviderConfig, task Task, instruction, threadCtx string, action emailaction.SendEmailAction) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	system := strings.TrimSpace(`
You compose concise transactional HTML email bodies for Joanne.
Return HTML fragment only (no outer html/head/body document shell): semantic tags like <p>, <ul>, <li>, <strong>, <em>, <a href="https://...">.
When the user asks for HTML, bullet lists, links in the prose, or light formatting in the body (distinct from any separate primary CTA button), use safe semantic markup only.
No markdown fences. No script, style, iframe, or inline event handlers.
`)
	user := fmt.Sprintf(
		"Employee: %s\nSlack request:\n%s\n\nInstruction for email:\n%s\n\nOptional labeled context:\nto=%s subject=%s\n\nThread transcript:\n%s\n\nWrite recipient-facing prose.",
		strings.TrimSpace(task.OwnerEmployeeID),
		truncateRunes(strings.TrimSpace(task.RequestText), 4000),
		truncateRunes(instruction, 8000),
		strings.TrimSpace(action.To),
		strings.TrimSpace(action.Subject),
		truncateRunes(threadCtx, 12000),
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
			"maxOutputTokens":  4096,
			"topP":             0.9,
			"responseMimeType": "text/plain",
		},
	}
	raw, _, err := runGeminiGenerate(ctx, provider, requestBody)
	if err != nil {
		return "", fmt.Errorf("create-email: draft generation failed: %w", err)
	}
	raw = strings.TrimSpace(raw)
	return raw, nil
}

func formatCreateEmailPreviewMrkdwn(recipients []string, subject, bodyHTML string) string {
	toLine := strings.Join(recipients, ", ")
	preview := stripHTMLForPreview(bodyHTML)
	if len([]rune(preview)) > 420 {
		preview = string([]rune(preview)[:420]) + "…"
	}
	var b strings.Builder
	b.WriteString("*Queued email*\n")
	b.WriteString(fmt.Sprintf("• *To:* %s\n", toLine))
	b.WriteString(fmt.Sprintf("• *Subject:* %s\n", strings.TrimSpace(subject)))
	b.WriteString("• *Body preview:* ")
	b.WriteString(preview)
	b.WriteString("\n\nTap *Confirm* to send via Gmail or *Cancel* to discard.")
	return b.String()
}

func stripHTMLForPreview(html string) string {
	s := reHTMLTagPreview.ReplaceAllString(html, " ")
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}

// SendQueuedCreateEmail delivers one Gmail message per recipient (employee-factory batch semantics).
func SendQueuedCreateEmail(ctx context.Context, cfg GmailOAuthEnvConfig, payload emailPendingPayloadJSON) error {
	sender, err := gmailsender.New(cfg.GmailsenderOAuth())
	if err != nil {
		return err
	}
	subject := strings.TrimSpace(payload.Subject)
	body := strings.TrimSpace(payload.BodyHTML)
	for _, to := range payload.Recipients {
		to = strings.TrimSpace(to)
		if to == "" {
			continue
		}
		var sendErr error
		for attempt := 1; attempt <= 3; attempt++ {
			sendCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
			_, sendErr = sender.Send(sendCtx, gmailsender.SendInput{To: to, Subject: subject, Body: body})
			cancel()
			if sendErr == nil {
				break
			}
			if !gmailsender.IsRetryableSendError(sendErr) || attempt == 3 {
				break
			}
			time.Sleep(time.Duration(attempt*250) * time.Millisecond)
		}
		if sendErr != nil {
			return fmt.Errorf("send to %s: %w", to, sendErr)
		}
		log.Printf("create-email: sent ok recipient=%s subject=%q", to, subject)
	}
	return nil
}
