package adminapi

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bimross/agent-factory/internal/channelknowledge"
	"github.com/bimross/agent-factory/internal/channelknowledgerefresh"
	"github.com/slack-go/slack"
)

func (s *Server) runChannelKnowledgeRefresh(ctx context.Context, channelID string) (runes int, err error) {
	ch := strings.TrimSpace(channelID)
	if ch == "" {
		return 0, fmt.Errorf("empty channel id")
	}
	tok := strings.TrimSpace(s.cfg.OrchestratorSlackBotToken)
	if tok == "" {
		return 0, fmt.Errorf("ORCHESTRATOR_SLACK_BOT_TOKEN is not set on agent-factory-admin")
	}
	api := slack.New(tok)
	auth, authErr := api.AuthTestContext(ctx)
	botUserID, botID := "", ""
	if authErr != nil {
		s.log.Printf("channel_knowledge_refresh: auth.test: %v", authErr)
	} else if auth != nil {
		botUserID = strings.TrimSpace(auth.UserID)
		botID = strings.TrimSpace(auth.BotID)
	}
	store := channelknowledge.NewRedisStoreFromClient(s.rdb)
	p := channelknowledgerefresh.ParamsFromEnv()
	md, err := channelknowledgerefresh.RefreshOneChannel(ctx, api, store, ch, p, botUserID, botID)
	if err != nil {
		return 0, err
	}
	return utf8.RuneCountInString(md), nil
}

func (s *Server) backgroundRefreshChannelDigests(channelIDs []string) {
	if len(channelIDs) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
		defer cancel()
		for _, id := range channelIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			n, err := s.runChannelKnowledgeRefresh(ctx, id)
			if err != nil {
				s.log.Printf("channel_knowledge_refresh: channel=%s err=%v", id, err)
				continue
			}
			s.log.Printf("channel_knowledge_refresh: ok channel=%s digest_runes=%d", id, n)
		}
	}()
}
