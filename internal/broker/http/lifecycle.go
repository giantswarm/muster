package brokerhttp

import (
	"context"
	"errors"
	"log/slog"

	oauth "github.com/giantswarm/mcp-oauth"
	oauthserver "github.com/giantswarm/mcp-oauth/server"
	"golang.org/x/oauth2"

	"github.com/giantswarm/muster/internal/broker"
	"github.com/giantswarm/muster/pkg/logging"
)

// brokerLifecycleOptions returns the mcp-oauth options that wire
// token-family lifecycle events to the broker's persistence path and
// then forward to sink for aggregator-side reactions.
//
// Both manager and sink are tolerated as nil: a nil manager skips
// persistence (filesystem-mode); a nil sink skips forwarding. The
// returned options are always safe to append.
func brokerLifecycleOptions(manager *broker.Manager, sink broker.LifecycleSink) []oauth.ServerOption {
	return []oauth.ServerOption{
		oauth.WithSessionCreationHandler(func(ctx context.Context, userID, familyID string, token *oauth2.Token) {
			idToken := oauthserver.ExtractIDToken(token)
			logging.InfoWithAttrs("Broker", "OAuth session created",
				slog.String("userID", logging.TruncateIdentifier(userID)),
				slog.String("familyID", logging.TruncateIdentifier(familyID)),
				slog.Bool("hasIDToken", idToken != ""))
			persistErr := persistIDToken(manager, familyID, userID, idToken)
			if sink != nil {
				sink.OnSessionCreated(ctx, familyID, userID, idToken)
			}
			if persistErr != nil && errors.Is(persistErr, broker.ErrMalformedIDToken) && sink != nil {
				sink.OnTokenRefreshFailed(ctx, familyID, userID,
					"SessionCreationHandler: broker rejected ID token as malformed")
			}
		}),
		oauth.WithTokenRefreshHandler(func(ctx context.Context, userID, familyID string, newToken *oauth2.Token) {
			idToken := oauthserver.ExtractIDToken(newToken)
			if idToken == "" {
				if sink != nil {
					sink.OnTokenRefreshFailed(ctx, familyID, userID,
						"TokenRefreshHandler: refreshed token has no ID token")
				}
				return
			}
			if err := persistIDToken(manager, familyID, userID, idToken); err != nil {
				if errors.Is(err, broker.ErrMalformedIDToken) && sink != nil {
					sink.OnTokenRefreshFailed(ctx, familyID, userID,
						"TokenRefreshHandler: broker rejected refreshed ID token as malformed")
				} else {
					logging.WarnWithAttrs("Broker", "PersistMusterIDToken failed on refresh",
						slog.String("familyID", logging.TruncateIdentifier(familyID)),
						slog.String("error", err.Error()))
				}
			}
		}),
		oauth.WithSessionRevocationHandler(func(ctx context.Context, userID, familyID string) {
			if manager != nil {
				manager.ClearMusterSession(familyID)
			}
			if sink != nil {
				sink.OnSessionRevoked(ctx, familyID)
			}
			logging.InfoWithAttrs("Broker", "OAuth session revoked",
				slog.String("familyID", logging.TruncateIdentifier(familyID)),
				slog.String("userID", logging.TruncateIdentifier(userID)))
		}),
	}
}

func persistIDToken(manager *broker.Manager, sessionID, userID, idToken string) error {
	if manager == nil || idToken == "" {
		return nil
	}
	return manager.PersistMusterIDToken(sessionID, userID, idToken)
}
