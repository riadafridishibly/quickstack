package auth

import (
	"context"
	"net/http"
	"slices"

	"github.com/riadafridishibly/quickstack/internal/web"
)

// Role is what a member may do in one workspace.
type Role string

const (
	Owner  Role = "owner"
	Admin  Role = "admin"
	Member Role = "member"
	Viewer Role = "viewer"
)

// Roles lists every role, most privileged first.
var Roles = []Role{Owner, Admin, Member, Viewer}

// InviteRoles are the roles an invitation can give. Owners are made by
// changing the role of a member.
var InviteRoles = []Role{Admin, Member, Viewer}

func (r Role) Valid() bool { return slices.Contains(Roles, r) }

func (r Role) Label() string {
	switch r {
	case Owner:
		return "Owner"
	case Admin:
		return "Admin"
	case Member:
		return "Member"
	case Viewer:
		return "Viewer"
	}
	return string(r)
}

// Action is something a workspace role may be allowed to do. Every member
// may see the workspace's projects and tasks.
type Action int

const (
	// EditTasks covers creating, editing, moving and deleting tasks.
	EditTasks Action = iota
	ManageProjects
	ManageMembers
	// ManageWorkspace covers the workspace's name, URL and public roadmap.
	ManageWorkspace
)

var allowed = map[Action][]Role{
	EditTasks:       {Owner, Admin, Member},
	ManageProjects:  {Owner, Admin, Member},
	ManageMembers:   {Owner, Admin},
	ManageWorkspace: {Owner, Admin},
}

// Can reports whether role may perform action.
func Can(role Role, action Action) bool {
	return slices.Contains(allowed[action], role)
}

type roleKey struct{}

// WithRole records the signed-in user's role in the workspace in ctx.
func WithRole(ctx context.Context, r Role) context.Context {
	return context.WithValue(ctx, roleKey{}, r)
}

// RoleFrom returns the role WithRole recorded, or "".
func RoleFrom(ctx context.Context) Role {
	r, _ := ctx.Value(roleKey{}).(Role)
	return r
}

// CanIn reports whether the signed-in user's role in the workspace in ctx
// allows action. Templates use it to decide what to show.
func CanIn(ctx context.Context, action Action) bool {
	return Can(RoleFrom(ctx), action)
}

// Require responds 403 unless the user's role in the workspace allows
// action.
func Require(action Action) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return web.Handle(func(w http.ResponseWriter, r *http.Request) error {
			if !CanIn(r.Context(), action) {
				return web.ErrForbidden
			}
			next.ServeHTTP(w, r)
			return nil
		})
	}
}

// PlatformRole is a role in running the service, separate from any
// workspace: it opens /admin.
type PlatformRole string

const (
	Superadmin PlatformRole = "superadmin"
	Support    PlatformRole = "support"
)

var PlatformRoles = []PlatformRole{Superadmin, Support}

func (r PlatformRole) Valid() bool { return slices.Contains(PlatformRoles, r) }

func (r PlatformRole) Label() string {
	switch r {
	case Superadmin:
		return "Superadmin"
	case Support:
		return "Support"
	}
	return string(r)
}

type PlatformAction int

const (
	ViewPlatform PlatformAction = iota
	// SignOutAccounts ends all of an account's sessions, for support.
	SignOutAccounts
	ManagePlatformRoles
)

var platformAllowed = map[PlatformAction][]PlatformRole{
	ViewPlatform:        {Superadmin, Support},
	SignOutAccounts:     {Superadmin, Support},
	ManagePlatformRoles: {Superadmin},
}

func CanPlatform(role PlatformRole, action PlatformAction) bool {
	return slices.Contains(platformAllowed[action], role)
}

// RequirePlatform responds 403 unless the signed-in user's platform role
// allows action.
func RequirePlatform(action PlatformAction) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return web.Handle(func(w http.ResponseWriter, r *http.Request) error {
			if u := From(r.Context()); u == nil || !CanPlatform(u.PlatformRole, action) {
				return web.ErrForbidden
			}
			next.ServeHTTP(w, r)
			return nil
		})
	}
}
