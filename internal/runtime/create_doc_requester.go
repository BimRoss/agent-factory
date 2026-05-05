package runtime

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/slack-go/slack"
)

const (
	defaultMakeACompanyBySlackPrefix = "makeacompany:user_by_slack:"
)

type createDocRequesterEmailResolver func(ctx context.Context, task Task) (email, source string, err error)

func injectCreateDocRequesterEditor(ctx context.Context, req *createDocRequest, task Task, resolver createDocRequesterEmailResolver) (email, source string, err error) {
	if req == nil {
		return "", "", fmt.Errorf("create-doc: request is nil")
	}
	if resolver == nil {
		resolver = resolveCreateDocRequesterEmail
	}
	email, source, err = resolver(ctx, task)
	if err != nil {
		return "", "", err
	}
	req.Editors = append(req.Editors, strings.TrimSpace(email))
	return strings.TrimSpace(email), strings.TrimSpace(source), nil
}

func resolveCreateDocRequesterEmail(ctx context.Context, task Task) (email, source string, err error) {
	userID := strings.TrimSpace(task.HumanUserID)
	if userID == "" {
		return "", "", fmt.Errorf("create-doc: missing requesting slack user id")
	}

	redisEmail, redisErr := lookupRequesterEmailFromRedis(ctx, userID)
	if redisErr == nil {
		return redisEmail, "inferred_from_makeacompany_redis", nil
	}

	slackEmail, slackErr := lookupRequesterEmailFromSlack(ctx, task.OwnerEmployeeID, userID)
	if slackErr == nil {
		return slackEmail, "inferred_from_slack_user", nil
	}

	return "", "", fmt.Errorf("create-doc: requester email lookup failed (redis=%v, slack=%v)", redisErr, slackErr)
}

func lookupRequesterEmailFromRedis(ctx context.Context, userID string) (string, error) {
	redisURL := strings.TrimSpace(os.Getenv("REDIS_URL"))
	if redisURL == "" {
		return "", fmt.Errorf("REDIS_URL is not configured")
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return "", fmt.Errorf("parse REDIS_URL: %w", err)
	}

	client := redis.NewClient(opts)
	defer client.Close()

	prefix := strings.TrimSpace(firstNonEmpty(
		os.Getenv("AGENT_FACTORY_MAKEACOMPANY_BY_SLACK_KEY_PREFIX"),
		defaultMakeACompanyBySlackPrefix,
	))
	key := strings.TrimSuffix(prefix, ":") + ":" + strings.TrimSpace(userID)

	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	emailRaw, err := client.Get(lookupCtx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return "", fmt.Errorf("missing redis index key %s", key)
		}
		return "", fmt.Errorf("redis get %s: %w", key, err)
	}
	email, err := normalizeEmail(strings.TrimSpace(emailRaw))
	if err != nil {
		return "", fmt.Errorf("invalid redis email for %s: %w", userID, err)
	}
	return email, nil
}

func lookupRequesterEmailFromSlack(ctx context.Context, ownerEmployeeID, userID string) (string, error) {
	token := slackBotTokenForEmployee(ownerEmployeeID)
	if token == "" {
		return "", fmt.Errorf("slack bot token is not configured")
	}
	api := slack.New(token)
	lookupCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	user, err := api.GetUserInfoContext(lookupCtx, strings.TrimSpace(userID))
	if err != nil {
		return "", err
	}
	if user == nil {
		return "", fmt.Errorf("slack user lookup returned nil user")
	}
	email, err := normalizeEmail(strings.TrimSpace(user.Profile.Email))
	if err != nil {
		return "", fmt.Errorf("invalid slack profile email: %w", err)
	}
	return email, nil
}

func slackBotTokenForEmployee(employeeID string) string {
	emp := strings.ToUpper(strings.TrimSpace(employeeID))
	if emp != "" {
		if tok := strings.TrimSpace(os.Getenv(emp + "_SLACK_BOT_TOKEN")); tok != "" {
			return tok
		}
	}
	return strings.TrimSpace(os.Getenv("SLACK_BOT_TOKEN"))
}
