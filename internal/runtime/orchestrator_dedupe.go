package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bimross/agent-factory/internal/orchestratorevent"
	"github.com/redis/go-redis/v9"
)

const orchestratorDedupeKeyPrefix = "agent-factory:orch:seen:"

// ShouldSkipDuplicateOrchestratorPayload returns true when this payload was already
// processed successfully (Redis SETNX miss). JetStream redeliveries, duplicate
// consumers, or accidental republish should not double-post to Slack.
// When REDIS_URL is empty or Redis is unreachable, returns (false, nil) so work still runs.
func ShouldSkipDuplicateOrchestratorPayload(ctx context.Context, ev orchestratorevent.EventV1) (bool, error) {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("ORCHESTRATOR_DEDUPE_ENABLED")), "false") {
		return false, nil
	}
	redisURL := strings.TrimSpace(os.Getenv("REDIS_URL"))
	if redisURL == "" {
		return false, nil
	}
	canon := orchestratorDedupeCanonical(ev)
	if canon == "" {
		return false, nil
	}
	sum := sha256.Sum256([]byte(canon))
	key := orchestratorDedupeKeyPrefix + hex.EncodeToString(sum[:])

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Printf("orchestrator dedupe: redis parse: %v", err)
		return false, nil
	}
	rdb := redis.NewClient(opts)
	defer func() { _ = rdb.Close() }()

	ttl := orchestratorDedupeTTL()
	dedupeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ok, err := rdb.SetNX(dedupeCtx, key, "1", ttl).Result()
	if err != nil {
		log.Printf("orchestrator dedupe: setnx: %v", err)
		return false, nil
	}
	if !ok {
		log.Printf("orchestrator dedupe: skip duplicate canonical=%s", canon)
		return true, nil
	}
	return false, nil
}

func orchestratorDedupeTTL() time.Duration {
	raw := strings.TrimSpace(os.Getenv("ORCHESTRATOR_DEDUPE_TTL_SEC"))
	if raw == "" {
		return 7 * 24 * time.Hour
	}
	sec, err := strconv.Atoi(raw)
	if err != nil || sec < 60 {
		return 7 * 24 * time.Hour
	}
	return time.Duration(sec) * time.Second
}

func orchestratorDedupeCanonical(ev orchestratorevent.EventV1) string {
	if ev.Continuation != nil {
		if hid := strings.TrimSpace(ev.Continuation.HandoffID); hid != "" {
			return "handoff:" + hid
		}
	}
	run := firstNonEmptyString(ev.RunID, ev.TraceID, ev.SlackEventID)
	ch := strings.TrimSpace(ev.Message.ChannelID)
	ms := strings.TrimSpace(ev.Message.MessageTS)
	target := strings.ToLower(strings.TrimSpace(ev.TargetEmployee))
	step := ev.Decision.PipelineStepIndex
	inner := strings.TrimSpace(ev.InnerType)
	kind := strings.TrimSpace(ev.Decision.Kind)
	tool := strings.TrimSpace(ev.Decision.ToolID)
	if run == "" {
		run = "norun"
	}
	if ch == "" {
		ch = "noch"
	}
	if ms == "" {
		ms = "nots"
	}
	return strings.Join([]string{
		run, ch, ms, strconv.Itoa(step), target, inner, kind, tool,
	}, "|")
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}