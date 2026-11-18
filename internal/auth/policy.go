package auth

import (
	"context"
	"fmt"
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
func (p *Policy[R, A]) Router(mux *http.ServeMux) *Router[R, A] {
	return &Router[R, A]{mux: mux, policy: p}
}

// Router registers routes that each name the action they require, and
// remembers them for the permissions golden file.
type Router[R ~string, A ~string] struct {
	mux    *http.ServeMux
	policy *Policy[R, A]
	routes []Route
}

type Route struct {
	Pattern string
	Action  string
}

func (r *Router[R, A]) Handle(pattern string, action A, h func(http.ResponseWriter, *http.Request) error) {
	r.mux.Handle(pattern, r.policy.Require(action)(web.Handle(h)))
	r.routes = append(r.routes, Route{Pattern: pattern, Action: string(action)})
}

// Routes lists what Handle registered, in order.
func (r *Router[R, A]) Routes() []Route { return slices.Clone(r.routes) }
