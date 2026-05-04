package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

const (
	docsCreateEndpointBase = "https://docs.googleapis.com/v1/documents"
	docsScopeWrite         = "https://www.googleapis.com/auth/documents"
	drivePermissionsBase   = "https://www.googleapis.com/drive/v3/files"
	driveScopeFile         = "https://www.googleapis.com/auth/drive.file"
)

type GoogleDocsClient struct {
	http *http.Client
}

type GoogleDocsCreateInput struct {
	Title string
	Body  string
}

type GoogleDocsCreateResult struct {
	DocumentID string
	Title      string
	URL        string
}

func NewGoogleDocsClient(cfg GoogleDocsEnvConfig) (*GoogleDocsClient, error) {
	oauthCfg := &oauth2.Config{
		ClientID:     strings.TrimSpace(cfg.ClientID),
		ClientSecret: strings.TrimSpace(cfg.ClientSecret),
		Scopes:       []string{docsScopeWrite, driveScopeFile},
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://accounts.google.com/o/oauth2/auth",
			TokenURL: "https://oauth2.googleapis.com/token",
		},
	}
	tok := &oauth2.Token{RefreshToken: strings.TrimSpace(cfg.RefreshToken)}
	httpClient := oauthCfg.Client(context.Background(), tok)
	httpClient.Timeout = 30 * time.Second
	return &GoogleDocsClient{http: httpClient}, nil
}

func (c *GoogleDocsClient) Create(ctx context.Context, in GoogleDocsCreateInput) (GoogleDocsCreateResult, error) {
	if c == nil || c.http == nil {
		return GoogleDocsCreateResult{}, fmt.Errorf("google docs client is not initialized")
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = "Joanne Draft"
	}
	body := strings.TrimSpace(in.Body)
	if body == "" {
		return GoogleDocsCreateResult{}, fmt.Errorf("create-doc: missing document body")
	}

	documentID, createdTitle, err := c.createDocument(ctx, title)
	if err != nil {
		return GoogleDocsCreateResult{}, err
	}
	if err := c.insertDocumentBody(ctx, documentID, body); err != nil {
		return GoogleDocsCreateResult{}, err
	}
	return GoogleDocsCreateResult{
		DocumentID: documentID,
		Title:      createdTitle,
		URL:        fmt.Sprintf("https://docs.google.com/document/d/%s/edit", url.PathEscape(documentID)),
	}, nil
}

func (c *GoogleDocsClient) GrantEditor(ctx context.Context, documentID, email string) error {
	return c.grantPermission(ctx, documentID, email, "writer")
}

func (c *GoogleDocsClient) GrantCommenter(ctx context.Context, documentID, email string) error {
	return c.grantPermission(ctx, documentID, email, "commenter")
}

func (c *GoogleDocsClient) GrantViewer(ctx context.Context, documentID, email string) error {
	return c.grantPermission(ctx, documentID, email, "reader")
}

func (c *GoogleDocsClient) createDocument(ctx context.Context, title string) (string, string, error) {
	payload := map[string]string{"title": title}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, docsCreateEndpointBase, bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return "", "", fmt.Errorf("create-doc: docs create failed status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	var out struct {
		DocumentID string `json:"documentId"`
		Title      string `json:"title"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", "", err
	}
	out.DocumentID = strings.TrimSpace(out.DocumentID)
	out.Title = strings.TrimSpace(out.Title)
	if out.DocumentID == "" {
		return "", "", fmt.Errorf("create-doc: docs create returned empty documentId")
	}
	if out.Title == "" {
		out.Title = title
	}
	return out.DocumentID, out.Title, nil
}

func (c *GoogleDocsClient) insertDocumentBody(ctx context.Context, documentID, body string) error {
	payload := map[string]any{
		"requests": []map[string]any{
			{
				"insertText": map[string]any{
					"location": map[string]any{
						"index": 1,
					},
					"text": body,
				},
			},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("%s/%s:batchUpdate", docsCreateEndpointBase, url.PathEscape(strings.TrimSpace(documentID)))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return fmt.Errorf("create-doc: docs batchUpdate failed status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	return nil
}

func (c *GoogleDocsClient) grantPermission(ctx context.Context, documentID, email, role string) error {
	if c == nil || c.http == nil {
		return fmt.Errorf("google docs client is not initialized")
	}
	documentID = strings.TrimSpace(documentID)
	if documentID == "" {
		return fmt.Errorf("create-doc: missing document id")
	}
	emailAddress, err := normalizeEmail(email)
	if err != nil {
		return err
	}
	payload := map[string]string{
		"type":         "user",
		"role":         strings.TrimSpace(strings.ToLower(role)),
		"emailAddress": emailAddress,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	q := url.Values{}
	q.Set("sendNotificationEmail", "false")
	q.Set("supportsAllDrives", "true")
	endpoint := fmt.Sprintf("%s/%s/permissions?%s", drivePermissionsBase, url.PathEscape(documentID), q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return fmt.Errorf("create-doc: drive permissions.create failed status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	return nil
}

func normalizeEmail(raw string) (string, error) {
	addr := strings.TrimSpace(raw)
	if addr == "" {
		return "", fmt.Errorf("create-doc: missing email")
	}
	parsed, err := mail.ParseAddress(addr)
	if err != nil || parsed == nil {
		return "", fmt.Errorf("create-doc: invalid email %q", addr)
	}
	email := strings.TrimSpace(parsed.Address)
	if email == "" {
		return "", fmt.Errorf("create-doc: invalid email %q", addr)
	}
	return email, nil
}
