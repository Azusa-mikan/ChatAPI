package turn_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zyf2007/ChatAPI/internal/protocol"
	"github.com/zyf2007/ChatAPI/internal/repository/common"
	"github.com/zyf2007/ChatAPI/internal/repository/migrations"
	"github.com/zyf2007/ChatAPI/internal/repository/sqlite"
	"github.com/zyf2007/ChatAPI/internal/service/chat/conversationresolve"
	"github.com/zyf2007/ChatAPI/internal/service/chat/pending"
	"github.com/zyf2007/ChatAPI/internal/service/chat/turn"
)

func newPreviousResponseService(t *testing.T) (*turn.Service, common.Conversation) {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "chat.sqlite3"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = store.DB().Close() })
	if err := migrations.Bootstrap(ctx, store.DB()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	conversation, _, err := store.CreatePendingTurn(ctx, common.CreatePendingInput{
		ConversationID: "conv_1",
		RequestID:      "req_1",
		ResponseID:     "resp_1",
		OwnerID:        "user_a",
		RequestFormat:  "responses",
		Model:          "gpt-test",
		UserContent:    "first",
	})
	if err != nil {
		t.Fatalf("seed conversation: %v", err)
	}
	registry := pending.NewPendingRegistry()
	svc := &turn.Service{
		Submitter:          &turn.Submitter{Store: store, Pending: registry},
		Pending:            registry,
		Store:              store,
		Resolver:           conversationresolve.New(store, registry),
		OwnerIDFromContext: func(context.Context) string { return "user_a" },
	}
	return svc, conversation
}

func TestCreatePendingStreamReusesConversationByPreviousResponseID(t *testing.T) {
	ctx := context.Background()
	svc, seeded := newPreviousResponseService(t)

	turnResult, conversation, err := svc.CreatePendingStream(ctx, turn.SubmitInput{
		Request: protocol.TurnRequest{
			Protocol:           protocol.ProtocolResponses,
			PreviousResponseID: "resp_1",
			Model:              "gpt-test",
			LastUserContent:    "second",
		},
	})
	if err != nil {
		t.Fatalf("create pending stream: %v", err)
	}
	if conversation.ID != seeded.ID {
		t.Fatalf("previous_response_id should reuse conversation %q, got %q", seeded.ID, conversation.ID)
	}
	if turnResult.ConversationID != seeded.ID {
		t.Fatalf("pending turn bound to %q, want %q", turnResult.ConversationID, seeded.ID)
	}
}

func TestCreatePendingStreamNewConversationWithoutSignals(t *testing.T) {
	ctx := context.Background()
	svc, seeded := newPreviousResponseService(t)

	_, conversation, err := svc.CreatePendingStream(ctx, turn.SubmitInput{
		Request: protocol.TurnRequest{
			Protocol:        protocol.ProtocolResponses,
			Model:           "gpt-test",
			LastUserContent: "standalone",
		},
	})
	if err != nil {
		t.Fatalf("create pending stream: %v", err)
	}
	if conversation.ID == seeded.ID {
		t.Fatalf("request without signals must create a new conversation, got seeded %q", conversation.ID)
	}
}

func TestCreatePendingStreamUnknownPreviousResponseIDFails(t *testing.T) {
	ctx := context.Background()
	svc, _ := newPreviousResponseService(t)

	_, _, err := svc.CreatePendingStream(ctx, turn.SubmitInput{
		Request: protocol.TurnRequest{
			Protocol:           protocol.ProtocolResponses,
			PreviousResponseID: "resp_missing",
			Model:              "gpt-test",
			LastUserContent:    "x",
		},
	})
	if err == nil {
		t.Fatal("expected error for unknown previous_response_id")
	}
}
