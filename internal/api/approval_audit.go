package api

import (
	"context"
	"log/slog"

	"github.com/giantswarm/muster/v5/pkg/logging"
)

// AuditApproval records one audit event of approval: a request, a decision,
// a use, a refusal (Block, governance, an unavailable prerequisite) or a
// change of a connector's settings. Every event names who acted in full --
// the issuer and the raw subject, and the caller kind with its evidence --
// and the governance mode; callers add the approval id, the target, the
// arguments' hash and, for a change, the settings old and new as structured
// values, so no free-form name can forge another field. The one helper
// keeps every approval event in the same shape (M1-MU-S-10, M1-MU-C-16).
func AuditApproval(ctx context.Context, action string, attrs ...slog.Attr) {
	identity := SignInIdentityFromContext(ctx)
	subject := identity.Subject
	if subject == "" {
		subject = GetSubjectFromContext(ctx)
	}
	base := []slog.Attr{
		slog.String("action", action),
		slog.String("issuer", identity.Issuer),
		slog.String("subject", subject),
		slog.String("callerKind", CallerKind(ctx, GetSessionIDFromContext(ctx))),
		slog.String("governance", GetGovernance().Mode),
	}
	logging.InfoWithAttrsCtx(ctx, "Approval", "[AUDIT] "+action, append(base, attrs...)...)
}
