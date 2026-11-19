package auth

import (
	"context"
	"slices"
)

// This file is the app's permissions: who may do what in a workspace and in
// /admin. A project that copies auth rewrites it. Code outside it asks about
// actions, never about role names. internal/app/testdata/permissions.golden
// and routes.golden show the result; go test ./internal/app -update rewrites them.

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

// Action is something a workspace role may be allowed to do.
type Action string

const (
	// AnyMember is for what every member may do, such as see the
	// workspace's projects and tasks.
	AnyMember Action = "any_member"
	// EditTasks covers creating, editing, moving and deleting tasks.
	EditTasks      Action = "edit_tasks"
	ManageProjects Action = "manage_projects"
	ManageMembers  Action = "manage_members"
	// ManageOwners covers making someone owner, and changing the role of or
	// removing an owner.
	ManageOwners Action = "manage_owners"
	// ManageWorkspace covers the workspace's name, URL and public roadmap.
	ManageWorkspace Action = "manage_workspace"
)

// Workspace is what each role may do in a workspace. The role comes from
// workspaces.Middleware through WithRole.
var Workspace = NewPolicy(Roles, map[Action][]Role{
	AnyMember:       Roles,
	EditTasks:       {Owner, Admin, Member},
	ManageProjects:  {Owner, Admin, Member},
	ManageMembers:   {Owner, Admin},
	ManageOwners:    {Owner},
	ManageWorkspace: {Owner, Admin},
}, roleFrom)

// WorkspaceRouter registers routes inside /w/{ws}.
type WorkspaceRouter = Router[Role, Action]

type roleKey struct{}

// WithRole records the signed-in user's role in the workspace in ctx.
func WithRole(ctx context.Context, r Role) context.Context {
	return context.WithValue(ctx, roleKey{}, r)
}

func roleFrom(ctx context.Context) Role {
	r, _ := ctx.Value(roleKey{}).(Role)
	return r
}

// PlatformRole is a role in running the service, separate from any
// workspace: it opens /admin.
type PlatformRole string

const (
	Superadmin PlatformRole = "superadmin"
	Support    PlatformRole = "support"
	// Editor writes the site's blog and sees no accounts or workspaces.
	Editor PlatformRole = "editor"
)

var PlatformRoles = []PlatformRole{Superadmin, Support, Editor}

func (r PlatformRole) Valid() bool { return slices.Contains(PlatformRoles, r) }

func (r PlatformRole) Label() string {
	switch r {
	case Superadmin:
		return "Superadmin"
	case Support:
		return "Support"
	case Editor:
		return "Editor"
	}
	return string(r)
}

type PlatformAction string

const (
	// OpenAdmin lets a role into /admin. Each section there checks its own
	// action as well.
	OpenAdmin PlatformAction = "open_admin"
	// ViewAccounts covers the overview, accounts, workspaces and the audit
	// log.
	ViewAccounts PlatformAction = "view_accounts"
	// SignOutAccounts ends all of an account's sessions, for support.
	SignOutAccounts     PlatformAction = "sign_out_accounts"
	ManagePlatformRoles PlatformAction = "manage_platform_roles"
	ManageBlog          PlatformAction = "manage_blog"
)

// Platform is what each platform role may do in /admin.
var Platform = NewPolicy(PlatformRoles, map[PlatformAction][]PlatformRole{
	OpenAdmin:           {Superadmin, Support, Editor},
	ViewAccounts:        {Superadmin, Support},
	SignOutAccounts:     {Superadmin, Support},
	ManagePlatformRoles: {Superadmin},
	ManageBlog:          {Superadmin, Editor},
}, func(ctx context.Context) PlatformRole {
	if u := From(ctx); u != nil {
		return u.PlatformRole
	}
	return ""
})

// PlatformRouter registers routes inside /admin.
type PlatformRouter = Router[PlatformRole, PlatformAction]
