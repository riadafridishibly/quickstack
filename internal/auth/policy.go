package auth

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"

	"github.com/riadafridishibly/quickstack/internal/web"
)

// Policy says which roles may perform each action in one scope, such as a
// workspace or the platform admin. roles.go defines one per scope.
type Policy[R ~string, A ~string] struct {
	roles   []R
	allowed map[A][]R
	role    func(context.Context) R
}

// NewPolicy returns a Policy. roles lists every role, most privileged first;
// role returns the signed-in user's role in the scope, or "".
func NewPolicy[R ~string, A ~string](roles []R, allowed map[A][]R, role func(context.Context) R) *Policy[R, A] {
	own := make(map[A][]R, len(allowed))
	for a, rs := range allowed {
		for _, r := range rs {
			if !slices.Contains(roles, r) {
				panic(fmt.Sprintf("auth: action %q names unknown role %q", a, r))
			}
		}
		own[a] = slices.Clone(rs)
	}
	return &Policy[R, A]{roles: slices.Clone(roles), allowed: own, role: role}
}

// Can reports whether role may perform action.
func (p *Policy[R, A]) Can(role R, action A) bool {
	return role != "" && slices.Contains(p.allowed[action], role)
}

// CanIn reports whether the signed-in user's role in ctx allows action.
// Templates use it to decide what to show.
func (p *Policy[R, A]) CanIn(ctx context.Context, action A) bool {
	return p.Can(p.role(ctx), action)
}

// Require responds 403 unless the signed-in user's role allows action.
func (p *Policy[R, A]) Require(action A) func(http.Handler) http.Handler {
	p.mustKnow(action)
	return func(next http.Handler) http.Handler {
		return web.Handle(func(w http.ResponseWriter, r *http.Request) error {
			if !p.CanIn(r.Context(), action) {
				return web.ErrForbidden
			}
			next.ServeHTTP(w, r)
			return nil
		})
	}
}

// Roles lists the policy's roles, most privileged first.
func (p *Policy[R, A]) Roles() []R { return slices.Clone(p.roles) }

// Actions lists the policy's actions sorted by name.
func (p *Policy[R, A]) Actions() []A {
	as := make([]A, 0, len(p.allowed))
	for a := range p.allowed {
		as = append(as, a)
	}
	slices.Sort(as)
	return as
}

func (p *Policy[R, A]) mustKnow(action A) {
	if _, ok := p.allowed[action]; !ok {
		panic(fmt.Sprintf("auth: unknown action %q", action))
	}
}

// Router registers handlers on mux, each behind the action it requires.
func (p *Policy[R, A]) Router(mux *web.Mux) *Router[R, A] {
	return &Router[R, A]{mux: mux, policy: p}
}

// Router registers routes that each name the action they require, and
// remembers the actions for the route listing.
type Router[R ~string, A ~string] struct {
	mux     *web.Mux
	policy  *Policy[R, A]
	actions map[string]string
}

func (r *Router[R, A]) Get(path string, action A, h web.HandlerFunc) {
	r.handle("GET "+path, action, h)
}

func (r *Router[R, A]) Post(path string, action A, h web.HandlerFunc) {
	r.handle("POST "+path, action, h)
}

func (r *Router[R, A]) handle(pattern string, action A, h web.HandlerFunc) {
	r.mux.Handle(pattern, r.policy.Require(action)(web.Handle(h)))
	if r.actions == nil {
		r.actions = map[string]string{}
	}
	r.actions[pattern] = string(action)
}

// Actions maps each pattern registered through r to its action.
func (r *Router[R, A]) Actions() map[string]string { return maps.Clone(r.actions) }
