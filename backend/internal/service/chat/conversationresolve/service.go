package conversationresolve

import (
	"context"
	"errors"
	"strings"

	"github.com/zyf2007/ChatAPI/internal/protocol"
	"github.com/zyf2007/ChatAPI/internal/repository/chat"
	"github.com/zyf2007/ChatAPI/internal/repository/common"
	conversationstate "github.com/zyf2007/ChatAPI/internal/service/chat/conversationstate"
)

type PendingLookup interface {
	FindConversationIDByToolCallID(ownerID string, toolCallID string) (string, bool)
}

type Service struct {
	Store   chat.Reader
	Pending PendingLookup
}

type ResolveInput struct {
	OwnerID string
	Request protocol.TurnRequest
}

type Target struct {
	ConversationID string
	Reuse          bool
	Source         string
}

func New(store chat.Reader, pending PendingLookup) *Service {
	return &Service{Store: store, Pending: pending}
}

func (s *Service) Resolve(ctx context.Context, input ResolveInput) (Target, error) {
	ownerID := strings.TrimSpace(input.OwnerID)
	if conversationID := strings.TrimSpace(input.Request.ConversationID); conversationID != "" {
		conversation, err := s.Store.GetConversation(ctx, conversationID)
		if err != nil {
			return Target{}, err
		}
		if ownerID != "" && ownerID != ownerIDOfConversation(conversation) {
			return Target{}, common.ErrNotFound
		}
		if !protocolCompatible(conversation, input.Request.Protocol.String()) {
			return Target{}, common.ErrTurnConflict
		}
		return Target{ConversationID: conversationID, Reuse: true, Source: "explicit_id"}, nil
	}
	// Responses API continuity: previous_response_id identifies the prior turn.
	// Reuse its conversation so stateless "continue" calls don't spawn a new one.
	// Only the Responses protocol defines this field.
	if input.Request.Protocol == protocol.ProtocolResponses {
		if responseID := strings.TrimSpace(input.Request.PreviousResponseID); responseID != "" {
			conversation, err := s.Store.FindConversationByResponseID(ctx, ownerID, responseID)
			if err != nil {
				if errors.Is(err, common.ErrNotFound) {
					return Target{}, common.ErrNotFound
				}
				return Target{}, err
			}
			if !protocolCompatible(conversation, input.Request.Protocol.String()) {
				return Target{}, common.ErrTurnConflict
			}
			return Target{ConversationID: conversation.ID, Reuse: true, Source: "previous_response_id"}, nil
		}
	}
	for _, toolCallID := range extractToolCallIDs(input.Request.InputParts) {
		if s.Pending != nil {
			if conversationID, ok := s.Pending.FindConversationIDByToolCallID(ownerID, toolCallID); ok {
				return Target{ConversationID: conversationID, Reuse: true, Source: "pending_tool_call_id"}, nil
			}
		}
		if conversation, err := s.Store.FindConversationByToolCallID(ctx, ownerID, toolCallID); err == nil {
			return Target{ConversationID: conversation.ID, Reuse: true, Source: "stored_tool_call_id"}, nil
		} else if err != nil && err != common.ErrNotFound {
			return Target{}, err
		}
	}
	return Target{}, nil
}

func extractToolCallIDs(parts []protocol.InputPart) []string {
	seen := map[string]struct{}{}
	var ids []string
	for _, part := range parts {
		if part.Type != "tool_result" {
			continue
		}
		if part.ToolResult == nil {
			continue
		}
		id := strings.TrimSpace(part.ToolResult.CallID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func protocolCompatible(conversation common.Conversation, requestFormat string) bool {
	locked := strings.TrimSpace(conversationstate.RequestFormatRaw(conversation))
	if locked == "" {
		return true
	}
	return locked == strings.TrimSpace(requestFormat)
}

func ownerIDOfConversation(conversation common.Conversation) string {
	return conversationstate.OwnerID(conversation)
}
