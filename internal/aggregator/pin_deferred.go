package aggregator

import (
	"log/slog"
	"strings"
	"sync"

	"github.com/giantswarm/muster/v5/pkg/logging"
)

// pinDeferrals holds, per server and UID, the reason its authorization server
// pin was last deferred. The pin is retried at every registration, definition
// sync and login, so a Secret that is missing or refused would otherwise log
// the same warning many times a minute.
var pinDeferrals sync.Map

// pinDeferralKey identifies a server's MCPServer object: a server deleted and
// created again with the same name is logged afresh.
func pinDeferralKey(info *ServerInfo) string {
	return info.Name + "|" + info.UID
}

// logPinDeferred warns that a server's pin could not be applied, once per
// server, UID and reason; the same reason again is logged at debug level.
func logPinDeferred(info *ServerInfo, err error) {
	reason := err.Error()
	if previous, ok := pinDeferrals.Swap(pinDeferralKey(info), reason); ok && previous == reason {
		logging.Debug("Aggregator", "Authorization server pin of %s still deferred: %s", info.Name, reason)
		return
	}
	logging.WarnWithAttrs("Aggregator", "Authorization server pin deferred",
		slog.String("server", info.Name),
		slog.String("error", reason))
}

// forgetPinDeferral records that a server's pin was applied, so a later
// deferral is logged again.
func forgetPinDeferral(info *ServerInfo) {
	pinDeferrals.Delete(pinDeferralKey(info))
}

// forgetServerPinDeferrals drops what is recorded for a server, whatever its
// UID, when its definition goes, so the record does not outlive the servers.
func forgetServerPinDeferrals(name string) {
	prefix := name + "|"
	pinDeferrals.Range(func(key, _ any) bool {
		if k, ok := key.(string); ok && strings.HasPrefix(k, prefix) {
			pinDeferrals.Delete(key)
		}
		return true
	})
}
