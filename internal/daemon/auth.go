package daemon

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/syndg/tack/internal/daemonauth"
)

type authContextKey struct{}

type requestAuth struct {
	daemon bool
	agent  *daemonauth.AgentClaims
}

func (d *Daemon) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		token, ok := bearerToken(r.Header.Get("Authorization"))
		if ok && subtle.ConstantTimeCompare([]byte(token), []byte(d.authToken)) == 1 {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authContextKey{}, requestAuth{daemon: true})))
			return
		}
		if ok {
			claims, err := daemonauth.ParseAgentToken(token, d.authToken)
			if err == nil {
				if !agentRequestAllowed(r, claims) {
					writeError(w, http.StatusForbidden, "forbidden")
					return
				}
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authContextKey{}, requestAuth{agent: claims})))
				return
			}
		}
		if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(d.authToken)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="tack"`)
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
	})
}

func requestAuthFromRequest(r *http.Request) requestAuth {
	info, _ := r.Context().Value(authContextKey{}).(requestAuth)
	return info
}

func agentClaimsFromRequest(r *http.Request) *daemonauth.AgentClaims {
	return requestAuthFromRequest(r).agent
}

func agentRequestAllowed(r *http.Request, claims *daemonauth.AgentClaims) bool {
	if claims == nil {
		return false
	}
	return r.Method == http.MethodPost && r.URL.Path == "/mail"
}

func bearerToken(header string) (string, bool) {
	header = strings.TrimSpace(header)
	if !strings.HasPrefix(header, "Bearer ") {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	if token == "" {
		return "", false
	}
	return token, true
}
