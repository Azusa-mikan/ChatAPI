package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zyf2007/ChatAPI/internal/platform/urlsafety"
)

var ErrRedirectBlocked = errors.New("webhook redirect blocked")
var ErrUnexpectedStatus = errors.New("webhook unexpected status")
var ErrInvalidBodyTemplate = errors.New("webhook body template is not valid JSON")

const (
	titlePlaceholder = "{{title}}"
	textPlaceholder  = "{{text}}"
)

type Client struct {
	httpClient *http.Client
}

// Message is one generic webhook delivery. The request body is JSON: either the
// default {"title","text"} object or the caller's BodyTemplate with placeholders
// substituted, so any endpoint (custom gateways, chat bridges, automation tools)
// can consume it without depending on a provider-specific convention.
type Message struct {
	URL          string
	Title        string
	Text         string
	BodyTemplate string
}

type payload struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

// RenderBody builds the JSON request body. An empty template uses the default
// {"title","text"} object. Otherwise {{title}} and {{text}} are replaced with
// JSON-string-escaped values (place them inside quotes) and the result must be
// valid JSON.
func RenderBody(template, title, text string) ([]byte, error) {
	title = strings.TrimSpace(title)
	text = strings.TrimSpace(text)
	if strings.TrimSpace(template) == "" {
		return json.Marshal(payload{Title: title, Text: text})
	}
	rendered := strings.NewReplacer(
		titlePlaceholder, escapeJSONString(title),
		textPlaceholder, escapeJSONString(text),
	).Replace(template)
	if !json.Valid([]byte(rendered)) {
		return nil, ErrInvalidBodyTemplate
	}
	return []byte(rendered), nil
}

// ValidateBodyTemplate reports whether a template can produce valid JSON.
// An empty template is valid (it means "use the default body").
func ValidateBodyTemplate(template string) error {
	if strings.TrimSpace(template) == "" {
		return nil
	}
	_, err := RenderBody(template, "title", "text")
	return err
}

// escapeJSONString returns value as the inside of a JSON string literal
// (no surrounding quotes), so callers can embed it between quotes safely.
func escapeJSONString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) < 2 {
		return ""
	}
	return string(encoded[1 : len(encoded)-1])
}

// NewClient builds a webhook client. When httpClient is nil, a safe dialer
// client is used so DNS rebinding cannot bypass the restricted-address policy.
func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = urlsafety.NewSafeHTTPClient(5*time.Second, nil)
	}
	return &Client{httpClient: httpClient}
}

// NewClientWithDialer builds a client whose transport re-validates addresses at dial time.
func NewClientWithDialer(timeout time.Duration, dialer *urlsafety.SafeDialer) *Client {
	return NewClient(urlsafety.NewSafeHTTPClient(timeout, dialer))
}

func (c *Client) Send(ctx context.Context, message Message) error {
	if c == nil || c.httpClient == nil {
		return nil
	}
	url := strings.TrimSpace(message.URL)
	text := strings.TrimSpace(message.Text)
	if url == "" || text == "" {
		return nil
	}
	body, err := RenderBody(message.BodyTemplate, message.Title, text)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.CopyN(io.Discard, resp.Body, 64<<10)
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return ErrRedirectBlocked
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ErrUnexpectedStatus
	}
	return nil
}
