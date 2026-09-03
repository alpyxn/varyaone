package httpapi

// The demo endpoints and the demo guard exist only on the public showcase
// deployment: nothing in this file is reachable unless the router was built
// with WithDemo, which app.RunServer passes only when VARYAONE_DEMO_MODE is on.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alpyxn/varyaone/internal/demo"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/go-chi/chi/v5"
)

type demoHandler struct {
	runtime *DemoRuntime
	cache   *demoStateCache
}

// mountDemoRoutes owns every /api/v1/demo path, the passwordless session
// included: chi routes a sub-tree to one sub-router, so these cannot be split
// across two mount functions.
func mountDemoRoutes(router chi.Router, identityService *identity.Service, secureCookies bool, runtime *DemoRuntime, cache *demoStateCache) {
	handler := demoHandler{runtime: runtime, cache: cache}
	auth := identityHandler{service: identityService, secureCookies: secureCookies, demo: runtime}
	router.Route("/api/v1/demo", func(r chi.Router) {
		r.Get("/state", handler.state)
		r.Post("/reset", handler.reset)
		r.Post("/session", auth.startDemoSession)
	})
}

// demoStateResponse is what the browser needs to present the demo: whether it
// is usable, when its data is next wiped, and the shared account to sign in
// with. The credentials travel here because the login screen shows them filled
// in - the visitor was never given them to type.
type demoStateResponse struct {
	demo.State
	Email    string `json:"email"`
	Password string `json:"password"`
}

// state tells the visitor's browser whether the demo is usable and when its
// data is next wiped, so the settings card can show a countdown instead of
// surprising people with an empty screen.
func (h demoHandler) state(w http.ResponseWriter, r *http.Request) {
	state, err := h.runtime.Runner.State(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Demo durumu okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, demoStateResponse{State: state, Email: h.runtime.Email, Password: h.runtime.Password})
}

// reset is the visitor-facing rebuild. Everyone shares one company, so whoever
// finds the data in a mess can clean it up without waiting for the timer - but
// not often enough to keep the demo permanently empty.
func (h demoHandler) reset(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, r, http.StatusForbidden, "CSRF_REJECTED", "İstek kaynağı doğrulanamadı.")
		return
	}
	err := h.runtime.Runner.RequestReset(r.Context())
	h.cache.invalidate()
	switch {
	case errors.Is(err, demo.ErrResetTooSoon):
		writeError(w, r, http.StatusTooManyRequests, "DEMO_RESET_TOO_SOON", "Demo az önce sıfırlandı, birkaç dakika sonra tekrar deneyin.")
	case errors.Is(err, demo.ErrResetInProgress):
		writeError(w, r, http.StatusConflict, "DEMO_RESET_IN_PROGRESS", "Demo şu anda yenileniyor.")
	case err != nil:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Demo sıfırlanamadı.")
	default:
		state, stateErr := h.runtime.Runner.State(r.Context())
		if stateErr != nil {
			state = demo.State{Status: "READY"}
		}
		writeJSON(w, http.StatusOK, demoStateResponse{State: state, Email: h.runtime.Email, Password: h.runtime.Password})
	}
}

// demoStateCache keeps the reset flag out of every request's critical path. A
// rebuild takes a couple of seconds, so a second of staleness costs nothing and
// saves a database round trip per request.
type demoStateCache struct {
	runner *demo.Runner
	mu     sync.Mutex
	state  demo.State
	readAt time.Time
}

const demoStateTTL = time.Second

func newDemoStateCache(runner *demo.Runner) *demoStateCache {
	return &demoStateCache{runner: runner, state: demo.State{Status: "READY"}}
}

func (c *demoStateCache) invalidate() {
	c.mu.Lock()
	c.readAt = time.Time{}
	c.mu.Unlock()
}

func (c *demoStateCache) resetting(ctx context.Context) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.readAt) < demoStateTTL {
		return !c.state.Ready()
	}
	state, err := c.runner.State(ctx)
	if err != nil {
		// A read failure must not lock visitors out of a working demo.
		return false
	}
	c.state, c.readAt = state, time.Now()
	return !state.Ready()
}

// demoRules are the operations the public demo refuses. They fall into two
// groups: things that reach outside the demo (sending mail, contacting a
// provider) and things that would break the demo for everyone else (deleting
// the company, restoring a backup, editing users and roles, minting API
// tokens). This is deliberately one auditable list rather than a check
// scattered across the domain services - it is an environment restriction, not
// a permission.
var demoRules = []struct {
	method string
	prefix string
}{
	{"POST", "/api/v1/system/backups"},
	{"POST", "/api/v1/system/restore"},
	{"POST", "/api/v1/companies"},
	{"DELETE", "/api/v1/companies"},
	{"POST", "/api/v1/users"},
	{"PUT", "/api/v1/users"},
	{"POST", "/api/v1/roles"},
	{"PUT", "/api/v1/roles"},
	{"POST", "/api/v1/api-tokens"},
	{"DELETE", "/api/v1/api-tokens"},
	{"POST", "/api/v1/security/totp"},
	{"POST", "/api/v1/settings/email"},
	{"PUT", "/api/v1/settings/email"},
	{"POST", "/api/v1/email/send"},
	{"POST", "/api/v1/exchange-rates/refresh"},
	{"POST", "/api/v1/system/update"},
}

// demoGuard refuses the operations above and answers "being rebuilt" while a
// reset is running. It is mounted on the whole router, so a route added later
// is covered by the same list without anyone remembering to opt in.
func demoGuard(cache *demoStateCache) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, rule := range demoRules {
				if r.Method == rule.method && strings.HasPrefix(r.URL.Path, rule.prefix) {
					writeError(w, r, http.StatusForbidden, "DEMO_RESTRICTED", "Bu işlem demo sürümünde kapalıdır.")
					return
				}
			}
			// The demo's own endpoints must stay answerable during a rebuild:
			// that is exactly when the browser needs to ask what is going on.
			if strings.HasPrefix(r.URL.Path, "/api/v1/") &&
				!strings.HasPrefix(r.URL.Path, "/api/v1/demo/") &&
				cache.resetting(r.Context()) {
				w.Header().Set("Retry-After", "5")
				writeError(w, r, http.StatusServiceUnavailable, "DEMO_RESETTING", "Demo yenileniyor, birazdan hazır olacak.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
