package config

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/zyf2007/ChatAPI/internal/ops/observability/logging"
	"github.com/zyf2007/ChatAPI/internal/platform/urlsafety"
	platformwebhook "github.com/zyf2007/ChatAPI/internal/platform/webhook"
	"github.com/zyf2007/ChatAPI/internal/repository/chat"
	"github.com/zyf2007/ChatAPI/internal/repository/common"
	configrepo "github.com/zyf2007/ChatAPI/internal/repository/config"
	chatevents "github.com/zyf2007/ChatAPI/internal/service/chat/events"
	"go.uber.org/zap"
)

// ErrInvalidWebhookConfig is returned when enabled webhook settings fail shared URL safety checks.
var ErrInvalidWebhookConfig = errors.New("invalid webhook config")

type Deps struct {
	Configs configrepo.Store
	Chat    chat.Store
	Events  chatevents.Publisher
	Logger  *zap.Logger
	// Lookup is optional; production uses the system resolver via urlsafety defaults.
	Lookup urlsafety.HostLookup
}

type Service struct {
	configs configrepo.Store
	chat    chat.Store
	events  chatevents.Publisher
	logger  *zap.Logger
	lookup  urlsafety.HostLookup
}

func New(deps Deps) *Service {
	return &Service{
		configs: deps.Configs,
		chat:    deps.Chat,
		events:  deps.Events,
		logger:  deps.Logger,
		lookup:  deps.Lookup,
	}
}

func (s *Service) GetUserConfig(ctx context.Context, userID string) (common.UserConfig, error) {
	item, err := s.configs.GetUserConfig(ctx, strings.TrimSpace(userID), "settings")
	if err == nil {
		logging.BindContext(s.logger, ctx, zap.String("owner.id", strings.TrimSpace(userID)), zap.String("config.key", "settings")).Debug("usercontrol config fetched user config")
	}
	return item, err
}

func (s *Service) UpdateUserConfig(ctx context.Context, userID string, value map[string]any) (common.UserConfig, error) {
	cloned := cloneMap(value)
	if err := s.validateWebhookSettings(ctx, cloned); err != nil {
		return common.UserConfig{}, err
	}
	item, err := s.configs.SetUserConfig(ctx, common.SetUserConfigInput{
		UserID: strings.TrimSpace(userID),
		Key:    "settings",
		Value:  cloned,
	})
	if err == nil {
		logging.BindContext(s.logger, ctx, zap.String("owner.id", strings.TrimSpace(userID)), zap.String("config.key", "settings")).Info("usercontrol config updated user config")
	}
	return item, err
}

// validateWebhookSettings enforces:
//   - enabled=true requires a non-empty URL that passes the shared safety policy (syntax + DNS + restricted IP).
//   - enabled=false allows any syntactically valid URL (including empty) so users can keep drafts while disabled;
//     private/restricted destinations are rejected even when disabled to avoid storing known-unsafe endpoints.
//
// This intentionally does not treat "disabled private URL" as a general allow-private capability.
func (s *Service) validateWebhookSettings(ctx context.Context, value map[string]any) error {
	if value == nil {
		return nil
	}
	if err := validateWebhookBodyTemplate(value["webhook_body_template"]); err != nil {
		return err
	}
	enabled := asBool(value["webhook_url_enabled"])
	rawURL, hasURL := value["webhook_url"]
	if !hasURL && !enabled {
		return nil
	}
	urlText := ""
	if hasURL && rawURL != nil {
		urlText = strings.TrimSpace(fmt.Sprint(rawURL))
		if urlText == "<nil>" {
			urlText = ""
		}
	}
	if !enabled {
		if urlText == "" {
			return nil
		}
		// Disabled drafts still must be syntactically valid webhook URLs.
		// Restricted destinations are rejected so we never persist known-unsafe endpoints.
		parsed, syntax := urlsafety.ParseWebhookURL(urlText)
		if !syntax.OK {
			return fmt.Errorf("%w: %s", ErrInvalidWebhookConfig, syntax.Reason)
		}
		if parsed == nil {
			return nil
		}
		safety := urlsafety.AssessWebhookHost(ctx, parsed.Hostname, false, s.lookup)
		if !safety.OK {
			// For disabled saves, DNS resolution failures are soft: the URL is not active.
			// Only hard-reject when we positively identify a restricted destination or bad syntax above.
			if safety.IsPrivate {
				return fmt.Errorf("%w: %s", ErrInvalidWebhookConfig, safety.Reason)
			}
			// Literal private IPs are IsPrivate; hostname DNS failures are not — allow draft keep.
			return nil
		}
		return nil
	}
	if urlText == "" {
		return fmt.Errorf("%w: 启用 webhook 时必须填写地址", ErrInvalidWebhookConfig)
	}
	safety := urlsafety.ValidateWebhookURLContext(ctx, urlText, false, s.lookup)
	if !safety.OK {
		return fmt.Errorf("%w: %s", ErrInvalidWebhookConfig, safety.Reason)
	}
	return nil
}

// validateWebhookBodyTemplate rejects a custom body template that cannot render
// to valid JSON. Empty is allowed and means "use the default {"title","text"}" body.
func validateWebhookBodyTemplate(raw any) error {
	if raw == nil {
		return nil
	}
	template := strings.TrimSpace(fmt.Sprint(raw))
	if template == "" || template == "<nil>" {
		return nil
	}
	if err := platformwebhook.ValidateBodyTemplate(template); err != nil {
		return fmt.Errorf("%w: webhook 请求体模板不是合法 JSON", ErrInvalidWebhookConfig)
	}
	return nil
}

func (s *Service) DeleteConversation(ctx context.Context, conversationID string) (common.DeleteConversationsResult, error) {
	result, err := s.chat.DeleteConversations(ctx, []string{strings.TrimSpace(conversationID)})
	if err == nil {
		chatevents.PublishDeletedConversations(ctx, s.events, result)
	}
	return result, err
}

func (s *Service) DeleteConversations(ctx context.Context, conversationIDs []string) (common.DeleteConversationsResult, error) {
	result, err := s.chat.DeleteConversations(ctx, conversationIDs)
	if err == nil {
		chatevents.PublishDeletedConversations(ctx, s.events, result)
	}
	return result, err
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func asBool(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true
		default:
			return false
		}
	case float64:
		return v != 0
	case int:
		return v != 0
	case int64:
		return v != 0
	default:
		return false
	}
}
