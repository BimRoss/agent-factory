package runtime

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/bimross/agent-factory/internal/emailaction"
)

const (
	createEmailDraftRedisPrefix = "agent-factory:create_email_draft"
	createEmailDraftTTL         = 20 * time.Minute
)

type createEmailDraftState struct {
	To              string `json:"to,omitempty"`
	Subject         string `json:"subject,omitempty"`
	BodyInstruction string `json:"body_instruction,omitempty"`
	BodyText        string `json:"body_text,omitempty"`
	CTAText         string `json:"cta_text,omitempty"`
	CTAURL          string `json:"cta_url,omitempty"`
}

func createEmailDraftRedisKey(channelID, requestUserID, threadTS string) string {
	return createEmailDraftRedisPrefix + ":" + threadFlowPendingKey(channelID, requestUserID, threadTS)
}

func loadCreateEmailDraftState(ctx context.Context, channelID, requestUserID, threadTS string) (createEmailDraftState, bool, error) {
	var out createEmailDraftState
	url := strings.TrimSpace(os.Getenv("REDIS_URL"))
	if url == "" {
		return out, false, nil
	}
	client, err := redisOpen(url)
	if err != nil {
		return out, false, err
	}
	defer client.Close()
	opCtx, cancel := context.WithTimeout(ctx, skillConfirmationRedisOpTimeout)
	defer cancel()
	raw, err := client.Get(opCtx, createEmailDraftRedisKey(channelID, requestUserID, threadTS)).Result()
	if err != nil {
		return out, false, nil
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return out, false, err
	}
	return out, out.hasAnyFields(), nil
}

func saveCreateEmailDraftState(ctx context.Context, channelID, requestUserID, threadTS string, draft createEmailDraftState) error {
	if !draft.hasAnyFields() {
		return clearCreateEmailDraftState(ctx, channelID, requestUserID, threadTS)
	}
	url := strings.TrimSpace(os.Getenv("REDIS_URL"))
	if url == "" {
		return nil
	}
	raw, err := json.Marshal(draft)
	if err != nil {
		return err
	}
	client, err := redisOpen(url)
	if err != nil {
		return err
	}
	defer client.Close()
	opCtx, cancel := context.WithTimeout(ctx, skillConfirmationRedisOpTimeout)
	defer cancel()
	return client.Set(opCtx, createEmailDraftRedisKey(channelID, requestUserID, threadTS), raw, createEmailDraftTTL).Err()
}

func clearCreateEmailDraftState(ctx context.Context, channelID, requestUserID, threadTS string) error {
	url := strings.TrimSpace(os.Getenv("REDIS_URL"))
	if url == "" {
		return nil
	}
	client, err := redisOpen(url)
	if err != nil {
		return err
	}
	defer client.Close()
	opCtx, cancel := context.WithTimeout(ctx, skillConfirmationRedisOpTimeout)
	defer cancel()
	return client.Del(opCtx, createEmailDraftRedisKey(channelID, requestUserID, threadTS)).Err()
}

func mergeCreateEmailDraft(base createEmailDraftState, patch emailaction.SendEmailAction) createEmailDraftState {
	if v := strings.TrimSpace(patch.To); v != "" {
		base.To = v
	}
	if v := strings.TrimSpace(patch.Subject); v != "" {
		base.Subject = v
	}
	if v := strings.TrimSpace(patch.BodyInstruction); v != "" {
		base.BodyInstruction = v
	}
	if v := strings.TrimSpace(patch.BodyText); v != "" {
		base.BodyText = v
	}
	if v := strings.TrimSpace(patch.CTAText); v != "" {
		base.CTAText = v
	}
	if v := strings.TrimSpace(patch.CTAURL); v != "" {
		base.CTAURL = v
	}
	return base
}

func (d createEmailDraftState) toAction() emailaction.SendEmailAction {
	return emailaction.SendEmailAction{
		Intent:          emailaction.IntentSendEmail,
		To:              strings.TrimSpace(d.To),
		Subject:         strings.TrimSpace(d.Subject),
		BodyInstruction: strings.TrimSpace(d.BodyInstruction),
		BodyText:        strings.TrimSpace(d.BodyText),
		CTAText:         strings.TrimSpace(d.CTAText),
		CTAURL:          strings.TrimSpace(d.CTAURL),
	}
}

func (d createEmailDraftState) hasAnyFields() bool {
	return strings.TrimSpace(d.To) != "" ||
		strings.TrimSpace(d.Subject) != "" ||
		strings.TrimSpace(d.BodyInstruction) != "" ||
		strings.TrimSpace(d.BodyText) != "" ||
		strings.TrimSpace(d.CTAText) != "" ||
		strings.TrimSpace(d.CTAURL) != ""
}
