package protocol

import (
	"testing"

	"github.com/zyf2007/ChatAPI/internal/repository/common"
)

func TestHTTPStatusMapsRepositoryErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{name: "not_found", err: common.ErrNotFound, want: 404},
		{name: "turn_conflict", err: common.ErrTurnConflict, want: 409},
		{name: "invalid_request", err: InvalidRequest("bad", "field"), want: 400},
		{name: "unknown", err: errString("boom"), want: 500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HTTPStatus(tc.err); got != tc.want {
				t.Fatalf("HTTPStatus(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

func TestBuildErrorBodyMapsRepositoryErrors(t *testing.T) {
	body := BuildErrorBody(string(ProtocolResponses), common.ErrNotFound)
	payload, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected error body: %#v", body)
	}
	if payload["code"] != "not_found" {
		t.Fatalf("unexpected error code: %#v", payload)
	}

	anthropicBody := BuildErrorBody(string(ProtocolAnthropicMessages), common.ErrTurnConflict)
	anthropicErr, ok := anthropicBody["error"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected anthropic error body: %#v", anthropicBody)
	}
	if anthropicErr["type"] != "invalid_request_error" {
		t.Fatalf("unexpected anthropic error type: %#v", anthropicErr)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
