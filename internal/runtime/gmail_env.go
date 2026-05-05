package runtime

import (
	"fmt"
	"os"
	"strings"

	"github.com/bimross/agent-factory/internal/gmailsender"
)

// GmailOAuthEnvConfig holds refresh-token Gmail credentials for create-email (parity with employee-factory Joanne Gmail).
type GmailOAuthEnvConfig struct {
	ClientID     string
	ClientSecret string
	RefreshToken string
	SenderEmail  string
	SenderName   string
}

func LoadGmailOAuthConfigForEmployee(employeeID string) GmailOAuthEnvConfig {
	emp := strings.ToUpper(strings.TrimSpace(employeeID))
	prefix := emp + "_"
	return GmailOAuthEnvConfig{
		ClientID: strings.TrimSpace(firstNonEmpty(
			os.Getenv(prefix+"GOOGLE_CLIENT_ID"),
			os.Getenv("GOOGLE_CLIENT_ID"),
		)),
		ClientSecret: strings.TrimSpace(firstNonEmpty(
			os.Getenv(prefix+"GOOGLE_CLIENT_SECRET"),
			os.Getenv("GOOGLE_CLIENT_SECRET"),
		)),
		RefreshToken: strings.TrimSpace(firstNonEmpty(
			os.Getenv(prefix+"GOOGLE_REFRESH_TOKEN"),
			os.Getenv("GOOGLE_REFRESH_TOKEN"),
		)),
		SenderEmail: strings.TrimSpace(firstNonEmpty(
			os.Getenv(prefix+"GOOGLE_SENDER_EMAIL"),
			os.Getenv("GOOGLE_SENDER_EMAIL"),
		)),
		SenderName: strings.TrimSpace(firstNonEmpty(
			os.Getenv(prefix+"GOOGLE_SENDER_NAME"),
			os.Getenv("GOOGLE_SENDER_NAME"),
		)),
	}
}

func (c GmailOAuthEnvConfig) Validate() error {
	if strings.TrimSpace(c.ClientID) == "" {
		return fmt.Errorf("create-email: missing Google OAuth client id (GOOGLE_CLIENT_ID)")
	}
	if strings.TrimSpace(c.ClientSecret) == "" {
		return fmt.Errorf("create-email: missing Google OAuth client secret (GOOGLE_CLIENT_SECRET)")
	}
	if strings.TrimSpace(c.RefreshToken) == "" {
		return fmt.Errorf("create-email: missing Google OAuth refresh token (GOOGLE_REFRESH_TOKEN)")
	}
	if strings.TrimSpace(c.SenderEmail) == "" {
		return fmt.Errorf("create-email: missing sender mailbox (set JOANNE_GOOGLE_SENDER_EMAIL or GOOGLE_SENDER_EMAIL for the Gmail OAuth identity)")
	}
	return nil
}

func (c GmailOAuthEnvConfig) GmailsenderOAuth() gmailsender.OAuthConfig {
	return gmailsender.OAuthConfig{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		RefreshToken: c.RefreshToken,
		SenderEmail:  c.SenderEmail,
		SenderName:   c.SenderName,
	}
}
