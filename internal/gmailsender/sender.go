package gmailsender

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/bimross/agent-factory/internal/htmlemail"
	"golang.org/x/oauth2"
)

const gmailSendEndpoint = "https://gmail.googleapis.com/gmail/v1/users/me/messages/send"

// OAuthConfig holds Gmail API OAuth client credentials (refresh token flow).
type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	RefreshToken string
	SenderEmail  string
	SenderName   string
}

// Sender sends Gmail messages using OAuth refresh-token auth.
type Sender struct {
	client      *http.Client
	senderEmail string
	senderName  string
}

type SendResult struct {
	MessageID string
	ThreadID  string
}

type SendInput struct {
	To      string
	Subject string
	Body    string
}

// SendError describes a send failure with status/body context and retry signal.
type SendError struct {
	Op         string
	StatusCode int
	Body       string
	Err        error
}

func (e *SendError) Error() string {
	if e == nil {
		return "gmail send failed"
	}
	switch {
	case e.StatusCode > 0:
		return fmt.Sprintf("gmail send failed: status=%d body=%s", e.StatusCode, strings.TrimSpace(e.Body))
	case strings.TrimSpace(e.Op) != "" && e.Err != nil:
		return fmt.Sprintf("gmail send %s failed: %v", strings.TrimSpace(e.Op), e.Err)
	case e.Err != nil:
		return fmt.Sprintf("gmail send failed: %v", e.Err)
	default:
		return "gmail send failed"
	}
}

func (e *SendError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *SendError) Retryable() bool {
	if e == nil {
		return false
	}
	if e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= http.StatusInternalServerError {
		return true
	}
	if e.Err != nil {
		var netErr net.Error
		if errors.As(e.Err, &netErr) {
			return true
		}
		if errors.Is(e.Err, context.DeadlineExceeded) {
			return true
		}
	}
	return false
}

// IsRetryableSendError reports whether err is a transient Gmail send failure.
func IsRetryableSendError(err error) bool {
	if err == nil {
		return false
	}
	var sendErr *SendError
	if errors.As(err, &sendErr) {
		return sendErr.Retryable()
	}
	return false
}

// New constructs a Sender from OAuth refresh-token configuration.
func New(cfg OAuthConfig) (*Sender, error) {
	if strings.TrimSpace(cfg.ClientID) == "" || strings.TrimSpace(cfg.ClientSecret) == "" || strings.TrimSpace(cfg.RefreshToken) == "" {
		return nil, fmt.Errorf("gmail: missing oauth client id, secret, or refresh token")
	}
	oauthCfg := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Scopes:       []string{"https://www.googleapis.com/auth/gmail.send"},
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://accounts.google.com/o/oauth2/auth",
			TokenURL: "https://oauth2.googleapis.com/token",
		},
	}
	tok := &oauth2.Token{RefreshToken: strings.TrimSpace(cfg.RefreshToken)}
	client := oauthCfg.Client(context.Background(), tok)
	client.Timeout = 20 * time.Second
	name := strings.TrimSpace(cfg.SenderName)
	if name == "" {
		name = "Joanne"
	}
	return &Sender{
		client:      client,
		senderEmail: strings.TrimSpace(cfg.SenderEmail),
		senderName:  name,
	}, nil
}

func (s *Sender) Send(ctx context.Context, in SendInput) (SendResult, error) {
	if s == nil || s.client == nil {
		return SendResult{}, fmt.Errorf("gmail sender is not initialized")
	}
	to := strings.TrimSpace(in.To)
	subject := strings.TrimSpace(in.Subject)
	body := strings.TrimSpace(in.Body)
	if to == "" {
		return SendResult{}, fmt.Errorf("missing recipient email")
	}
	if subject == "" {
		return SendResult{}, fmt.Errorf("missing email subject")
	}
	if body == "" {
		return SendResult{}, fmt.Errorf("missing email body")
	}

	raw := buildRawMessage(s.senderName, s.senderEmail, to, subject, body)
	payload := map[string]string{"raw": raw}
	b, err := json.Marshal(payload)
	if err != nil {
		return SendResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gmailSendEndpoint, bytes.NewReader(b))
	if err != nil {
		return SendResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return SendResult{}, &SendError{Op: "request", Err: err}
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return SendResult{}, &SendError{
			Op:         "send",
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(rb)),
		}
	}
	result := parseSendResultJSON(rb)
	return result, nil
}

func buildRawMessage(fromName, fromEmail, to, subject, body string) string {
	from := strings.TrimSpace(fromEmail)
	if n := strings.TrimSpace(fromName); n != "" {
		quoted := strings.ReplaceAll(n, "\"", "\\\"")
		from = fmt.Sprintf("\"%s\" <%s>", quoted, from)
	}
	html := htmlemail.WrapMessageIfFragment(body)
	msg := "From: " + from + "\r\n" +
		"To: " + strings.TrimSpace(to) + "\r\n" +
		"Subject: " + strings.TrimSpace(subject) + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=\"UTF-8\"\r\n" +
		"\r\n" +
		strings.TrimSpace(html)
	return base64.RawURLEncoding.EncodeToString([]byte(msg))
}

func parseSendResultJSON(body []byte) SendResult {
	if len(body) == 0 {
		return SendResult{}
	}
	var payload struct {
		ID       string `json:"id"`
		ThreadID string `json:"threadId"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return SendResult{}
	}
	return SendResult{
		MessageID: strings.TrimSpace(payload.ID),
		ThreadID:  strings.TrimSpace(payload.ThreadID),
	}
}
