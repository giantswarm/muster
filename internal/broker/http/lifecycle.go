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

// reasonMalformedAtCreation and friends are the reason strings forwarded
// to the LifecycleSink when the broker refuses an ID token. They are
// stable enough for log correlation; callers MUST NOT branch on the text
// (LifecycleSink.OnTokenRefreshFailed documents this).
const (
	reasonMalformedAtCreation = "SessionCreationHandler: broker rejected ID token as malformed"
	reasonMalformedAtRefresh  = "TokenRefreshHandler: broker rejected refreshed ID token as malformed"
	reasonRefreshMissingID    = "TokenRefreshHandler: refreshed token has no ID token"
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
			handlePersistError(ctx, persistErr, sink, familyID, userID, reasonMalformedAtCreation)
		}),
		oauth.WithTokenRefreshHandler(func(ctx context.Context, userID, familyID string, newToken *oauth2.Token) {
			idToken := oauthserver.ExtractIDToken(newToken)
			if idToken == "" {
				if sink != nil {
					sink.OnTokenRefreshFailed(ctx, familyID, userID, reasonRefreshMissingID)
				}
				return
			}
			persistErr := persistIDToken(manager, familyID, userID, idToken)
			handlePersistError(ctx, persistErr, sink, familyID, userID, reasonMalformedAtRefresh)
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

// handlePersistError routes a PersistMusterIDToken error to the right
// observer. Malformed-ID-token errors surface to the sink as a
// "refresh failed" signal so the consumer can react (re-auth UX);
// every other error is operational and logged at WARN.
func handlePersistError(ctx context.Context, err error, sink broker.LifecycleSink, familyID, userID, malformedReason string) {
	if err == nil {
		return
	}
	if errors.Is(err, broker.ErrMalformedIDToken) {
		if sink != nil {
			sink.OnTokenRefreshFailed(ctx, familyID, userID, malformedReason)
		}
		return
	}
	logging.WarnWithAttrs("Broker", "PersistMusterIDToken failed",
		slog.String("familyID", logging.TruncateIdentifier(familyID)),
		slog.String("error", err.Error()))
}

func persistIDToken(manager *broker.Manager, sessionID, userID, idToken string) error {
	if manager == nil || idToken == "" {
		return nil
	}
	return manager.PersistMusterIDToken(sessionID, userID, idToken)
}
