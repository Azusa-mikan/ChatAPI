package conversationresolve

import (
	"context"
	"errors"
	"testing"

	"github.com/zyf2007/ChatAPI/internal/protocol"
	"github.com/zyf2007/ChatAPI/internal/repository/chat"
	"github.com/zyf2007/ChatAPI/internal/repository/common"
)

// stubReader embeds chat.Reader so only the methods under test need stubbing;
// the embedded nil interface panics on any other (unused) method call.
type stubReader struct {
	chat.Reader
	byResponseID map[string]common.Conversation // key: ownerID + "\x00" + responseID
	byToolCallID map[string]common.Conversation // key: ownerID + "\x00" + toolCallID
	byID         map[string]common.Conversation
}

func (s stubReader) GetConversation(_ context.Context, id string) (common.Conversation, error) {
	if c, ok := s.byID[id]; ok {
		return c, nil
	}
	return common.Conversation{}, common.ErrNotFound
}

func (s stubReader) FindConversationByToolCallID(_ context.Context, ownerID, toolCallID string) (common.Conversation, error) {
	if c, ok := s.byToolCallID[ownerID+"\x00"+toolCallID]; ok {
		return c, nil
	}
	return common.Conversation{}, common.ErrNotFound
}

func (s stubReader) FindConversationByResponseID(_ context.Context, ownerID, responseID string) (common.Conversation, error) {
	if c, ok := s.byResponseID[ownerID+"\x00"+responseID]; ok {
		return c, nil
	}
	return common.Conversation{}, common.ErrNotFound
}

var _ chat.Reader = stubReader{}

func responsesConversation(id string) common.Conversation {
	return common.Conversation{
		ID:       id,
		Metadata: map[string]any{"owner_id": "user_a", "request_format": "responses"},
	}
}

func TestResolve_PreviousResponseIDReusesConversation(t *testing.T) {
	store := stubReader{
		byResponseID: map[string]common.Conversation{
			"user_a\x00resp_1": responsesConversation("conv_1"),
		},
	}
	svc := New(store, nil)

	target, err := svc.Resolve(context.Background(), ResolveInput{
		OwnerID: "user_a",
		Request: protocol.TurnRequest{Protocol: protocol.ProtocolResponses, PreviousResponseID: "resp_1"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !target.Reuse || target.ConversationID != "conv_1" || target.Source != "previous_response_id" {
		t.Fatalf("unexpected target: %#v", target)
	}
}

func TestResolve_PreviousResponseIDOwnerIsolated(t *testing.T) {
	store := stubReader{
		byResponseID: map[string]common.Conversation{
			"user_a\x00resp_1": responsesConversation("conv_1"),
		},
	}
	svc := New(store, nil)

	_, err := svc.Resolve(context.Background(), ResolveInput{
		OwnerID: "user_b",
		Request: protocol.TurnRequest{Protocol: protocol.ProtocolResponses, PreviousResponseID: "resp_1"},
	})
	if !errors.Is(err, common.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for other owner, got %v", err)
	}
}

func TestResolve_PreviousResponseIDUnknown(t *testing.T) {
	svc := New(stubReader{}, nil)
	_, err := svc.Resolve(context.Background(), ResolveInput{
		OwnerID: "user_a",
		Request: protocol.TurnRequest{Protocol: protocol.ProtocolResponses, PreviousResponseID: "resp_missing"},
	})
	if !errors.Is(err, common.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unknown response id, got %v", err)
	}
}

func TestResolve_PreviousResponseIDProtocolMismatch(t *testing.T) {
	conflicting := responsesConversation("conv_1")
	conflicting.Metadata["request_format"] = "chat_completions"
	store := stubReader{
		byResponseID: map[string]common.Conversation{
			"user_a\x00resp_1": conflicting,
		},
	}
	svc := New(store, nil)

	_, err := svc.Resolve(context.Background(), ResolveInput{
		OwnerID: "user_a",
		Request: protocol.TurnRequest{Protocol: protocol.ProtocolResponses, PreviousResponseID: "resp_1"},
	})
	if !errors.Is(err, common.ErrTurnConflict) {
		t.Fatalf("expected ErrTurnConflict for protocol mismatch, got %v", err)
	}
}

func TestResolve_PreviousResponseIDIgnoredForNonResponsesProtocol(t *testing.T) {
	store := stubReader{
		byResponseID: map[string]common.Conversation{
			"user_a\x00resp_1": responsesConversation("conv_1"),
		},
	}
	svc := New(store, nil)

	target, err := svc.Resolve(context.Background(), ResolveInput{
		OwnerID: "user_a",
		Request: protocol.TurnRequest{Protocol: protocol.ProtocolAnthropicMessages, PreviousResponseID: "resp_1"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if target.Reuse || target.ConversationID != "" {
		t.Fatalf("previous_response_id must be ignored outside Responses, got %#v", target)
	}
}

func TestResolve_ExplicitConversationIDWinsOverPreviousResponseID(t *testing.T) {
	store := stubReader{
		byID: map[string]common.Conversation{"conv_explicit": responsesConversation("conv_explicit")},
		byResponseID: map[string]common.Conversation{
			"user_a\x00resp_1": responsesConversation("conv_from_response"),
		},
	}
	svc := New(store, nil)

	target, err := svc.Resolve(context.Background(), ResolveInput{
		OwnerID: "user_a",
		Request: protocol.TurnRequest{
			Protocol:           protocol.ProtocolResponses,
			ConversationID:     "conv_explicit",
			PreviousResponseID: "resp_1",
		},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if target.Source != "explicit_id" || target.ConversationID != "conv_explicit" {
		t.Fatalf("explicit conversation_id should win, got %#v", target)
	}
}

func TestResolve_NoSignalsCreatesNew(t *testing.T) {
	svc := New(stubReader{}, nil)
	target, err := svc.Resolve(context.Background(), ResolveInput{
		OwnerID: "user_a",
		Request: protocol.TurnRequest{Protocol: protocol.ProtocolResponses},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if target.Reuse || target.ConversationID != "" {
		t.Fatalf("expected new conversation target, got %#v", target)
	}
}
