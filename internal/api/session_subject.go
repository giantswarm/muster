package api

import (
	"context"
	"sync"
)

// SessionSubjectResolver names the subject an MCP request presenting a
// bearer muster did not issue is served as: the subject the person's grants
// are filed under when that request signs in to a server. The token broker
// releases a grant by it, so a grant is released by the same identity it was
// filed under.
type SessionSubjectResolver interface {
	// SessionSubject validates bearer as muster's MCP endpoint does for an
	// externally issued token and returns the session subject it yields.
	SessionSubject(ctx context.Context, bearer string) (string, error)
}

var (
	sessionSubjectResolver      SessionSubjectResolver
	sessionSubjectResolverMutex sync.RWMutex
)

// RegisterSessionSubjectResolver registers the session subject resolver.
func RegisterSessionSubjectResolver(r SessionSubjectResolver) {
	sessionSubjectResolverMutex.Lock()
	defer sessionSubjectResolverMutex.Unlock()
	sessionSubjectResolver = r
}

// GetSessionSubjectResolver returns the registered resolver, or nil when
// muster runs no OAuth server.
func GetSessionSubjectResolver() SessionSubjectResolver {
	sessionSubjectResolverMutex.RLock()
	defer sessionSubjectResolverMutex.RUnlock()
	return sessionSubjectResolver
}
