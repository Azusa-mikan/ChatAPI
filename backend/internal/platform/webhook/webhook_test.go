package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientSend_Success(t *testing.T) {
	var gotContentType string
	var gotPayload payload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotPayload)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	client := NewClient(&http.Client{Timeout: time.Second})
	err := client.Send(context.Background(), Message{
		URL:   server.URL + "/hook",
		Title: "ChatAPI · hello",
		Text:  "新请求：\nping",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if !strings.Contains(gotContentType, "application/json") {
		t.Fatalf("unexpected content type: %q", gotContentType)
	}
	if gotPayload.Title != "ChatAPI · hello" {
		t.Fatalf("unexpected title: %q", gotPayload.Title)
	}
	if gotPayload.Text != "新请求：\nping" {
		t.Fatalf("unexpected text: %q", gotPayload.Text)
	}
}

func TestClientSend_Non2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	client := NewClient(&http.Client{Timeout: time.Second})
	err := client.Send(context.Background(), Message{URL: server.URL, Title: "t", Text: "body"})
	if !errors.Is(err, ErrUnexpectedStatus) {
		t.Fatalf("expected unexpected status, got %v", err)
	}
}

func TestClientSend_RedirectBlocked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/other", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)

	client := NewClient(&http.Client{
		Timeout: time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	})
	err := client.Send(context.Background(), Message{URL: server.URL, Title: "t", Text: "body"})
	if !errors.Is(err, ErrRedirectBlocked) {
		t.Fatalf("expected redirect blocked, got %v", err)
	}
}

func TestClientSend_NetworkError(t *testing.T) {
	client := NewClient(&http.Client{Timeout: 50 * time.Millisecond})
	err := client.Send(context.Background(), Message{
		URL:   "http://127.0.0.1:1/hook",
		Title: "t",
		Text:  "body",
	})
	if err == nil {
		t.Fatal("expected network error")
	}
}

func TestClientSend_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	client := NewClient(&http.Client{Timeout: 30 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := client.Send(ctx, Message{URL: server.URL, Title: "t", Text: "body"})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestClientSend_UTF8TitlePreservedInJSON(t *testing.T) {
	var gotPayload payload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotPayload)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	client := NewClient(&http.Client{Timeout: time.Second})
	if err := client.Send(context.Background(), Message{
		URL:   server.URL,
		Title: "ChatAPI · 你好",
		Text:  "body",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if gotPayload.Title != "ChatAPI · 你好" {
		t.Fatalf("expected UTF-8 title preserved, got %q", gotPayload.Title)
	}
}

func TestRenderBody_Default(t *testing.T) {
	body, err := RenderBody("", "T", "X")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if string(body) != `{"title":"T","text":"X"}` {
		t.Fatalf("unexpected default body: %s", body)
	}
}

func TestRenderBody_CustomTemplate(t *testing.T) {
	template := `{"msg_type":"text","content":{"text":"{{title}}\n{{text}}"}}`
	body, err := RenderBody(template, "ChatAPI · 会话", "新请求：\n你好")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var decoded struct {
		MsgType string `json:"msg_type"`
		Content struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("rendered body is not valid JSON: %v (%s)", err, body)
	}
	if decoded.MsgType != "text" {
		t.Fatalf("unexpected msg_type: %q", decoded.MsgType)
	}
	want := "ChatAPI · 会话\n新请求：\n你好"
	if decoded.Content.Text != want {
		t.Fatalf("unexpected content.text: %q", decoded.Content.Text)
	}
}

func TestRenderBody_EscapesSpecialCharacters(t *testing.T) {
	template := `{"text":"{{text}}"}`
	raw := `line1
line2	"quoted" \backslash\`
	body, err := RenderBody(template, "", raw)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var decoded struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("rendered body is not valid JSON: %v (%s)", err, body)
	}
	if decoded.Text != raw {
		t.Fatalf("escaping did not round-trip: got %q want %q", decoded.Text, raw)
	}
}

func TestValidateBodyTemplate(t *testing.T) {
	if err := ValidateBodyTemplate(""); err != nil {
		t.Fatalf("empty template must be valid: %v", err)
	}
	if err := ValidateBodyTemplate(`{"text":"{{text}}"}`); err != nil {
		t.Fatalf("valid template rejected: %v", err)
	}
	if err := ValidateBodyTemplate(`{"text":"{{title}}`); !errors.Is(err, ErrInvalidBodyTemplate) {
		t.Fatalf("expected invalid template error, got %v", err)
	}
}

func TestClientSend_CustomBodyTemplate(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	client := NewClient(&http.Client{Timeout: time.Second})
	err := client.Send(context.Background(), Message{
		URL:          server.URL,
		Title:        "T",
		Text:         "X",
		BodyTemplate: `{"msg_type":"text","text":"{{title}}:{{text}}"}`,
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if gotBody["msg_type"] != "text" || gotBody["text"] != "T:X" {
		t.Fatalf("unexpected custom body: %#v", gotBody)
	}
}

func TestClientSend_InvalidBodyTemplate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("must not reach the server with an invalid template")
	}))
	t.Cleanup(server.Close)

	client := NewClient(&http.Client{Timeout: time.Second})
	err := client.Send(context.Background(), Message{
		URL:          server.URL,
		Title:        "T",
		Text:         "X",
		BodyTemplate: `{"text":"{{text}}"`,
	})
	if !errors.Is(err, ErrInvalidBodyTemplate) {
		t.Fatalf("expected invalid body template, got %v", err)
	}
}
