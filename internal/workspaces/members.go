package workspaces

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/riadafridishibly/quickstack/internal/auth"
	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/mail"
	"github.com/riadafridishibly/quickstack/internal/tenant"
	"github.com/riadafridishibly/quickstack/internal/web"
)

const (
	invitationTTL     = 7 * 24 * time.Hour
	invitationsTarget = "invitations"
)

var errLastOwner = errors.New("workspaces: last owner")

type membersView struct {
	Members     []db.ListMembersRow
	Invitations []db.ListInvitationsRow
	Invite      inviteForm
	Invited     string
	Error       string
}

type inviteForm struct {
	Email  string
	Role   auth.Role
	Errors map[string]string
}

func (h handler) loadMembers(ctx context.Context, v *membersView) (err error) {
	ws := tenant.From(ctx)
	if v.Members, err = h.q.ListMembers(ctx, ws.ID); err != nil {
		return err
	}
	if auth.CanIn(ctx, auth.ManageMembers) {
		v.Invitations, err = h.q.ListInvitations(ctx, ws.ID)
	}
	return err
}

func (h handler) members(w http.ResponseWriter, r *http.Request) error {
	v := membersView{Invite: inviteForm{Role: auth.Member, Errors: map[string]string{}}, Invited: r.URL.Query().Get("invited")}
	if err := h.loadMembers(r.Context(), &v); err != nil {
		return err
	}
	return web.Render(w, r, http.StatusOK, membersPage(v))
}

func (h handler) invite(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	ws, u := tenant.From(ctx), auth.From(ctx)
	f := inviteForm{Email: auth.NormalizeEmail(r.PostFormValue("email")), Role: auth.Role(r.PostFormValue("role")), Errors: map[string]string{}}
	if !auth.ValidEmail(f.Email) {
		f.Errors["email"] = "Enter an email address such as ana@example.com."
	}
	if !slices.Contains(auth.InviteRoles, f.Role) {
		f.Errors["role"] = "Choose a role."
	}
	if len(f.Errors) == 0 {
		if _, err := h.q.GetMember(ctx, db.GetMemberParams{WorkspaceID: ws.ID, AccountID: h.accountID(ctx, f.Email)}); err == nil {
			f.Errors["email"] = "This person is already a member."
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	if len(f.Errors) > 0 {
		v := membersView{Invite: f}
		if err := h.loadMembers(ctx, &v); err != nil {
			return err
		}
		if web.IsHTMX(r) {
			return web.Render(w, r, http.StatusUnprocessableEntity, invitations(v))
		}
		return web.Render(w, r, http.StatusUnprocessableEntity, membersPage(v))
	}

	token := rand.Text()
	err := pgx.BeginTxFunc(ctx, h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := db.New(tx)
		_, err := q.UpsertInvitation(ctx, db.UpsertInvitationParams{
			WorkspaceID: ws.ID, Email: f.Email, Role: string(f.Role), TokenHash: auth.HashToken(token),
			InvitedBy: &u.ID, ExpiresAt: time.Now().Add(invitationTTL),
		})
		if err != nil {
			return err
		}
		if err := q.RecordAudit(ctx, db.RecordAuditParams{ActorID: &u.ID, WorkspaceID: &ws.ID, Action: "member.invited", Target: f.Email}); err != nil {
			return err
		}
		return mail.Enqueue(ctx, h.jobs, tx, mail.Message{
			To:      f.Email,
			Subject: u.Name + " invited you to " + ws.Name + " on Quickstack",
			Text: u.Name + " (" + u.Email + ") invited you to join the workspace " + ws.Name + " as " + f.Role.Label() + ".\n\n" +
				"To accept, open this link within a week:\n" + h.publicURL + "/invite/" + url.PathEscape(token) + "\n",
		})
	})
	if err != nil {
		return err
	}
	if web.IsHTMX(r) {
		v := membersView{Invite: inviteForm{Role: f.Role, Errors: map[string]string{}}, Invited: f.Email}
		if err := h.loadMembers(ctx, &v); err != nil {
			return err
		}
		return web.Render(w, r, http.StatusOK, invitations(v))
	}
	web.Redirect(w, r, ws.URL("/members?invited="+url.QueryEscape(f.Email)))
	return nil
}

// accountID returns the id of the account with email, or 0.
func (h handler) accountID(ctx context.Context, email string) int64 {
	acc, err := h.q.GetAccountByEmail(ctx, email)
	if err != nil {
		return 0
	}
	return acc.ID
}

func (h handler) revokeInvitation(w http.ResponseWriter, r *http.Request) error {
	ws := tenant.From(r.Context())
	id, err := web.PathID(r, "id")
	if err != nil {
		return err
	}
	n, err := h.q.DeleteInvitation(r.Context(), db.DeleteInvitationParams{WorkspaceID: ws.ID, ID: id})
	if err != nil {
		return err
	}
	if n == 0 {
		return web.ErrNotFound
	}
	if web.IsHTMX(r) {
		w.WriteHeader(http.StatusOK)
		return nil
	}
	web.Redirect(w, r, ws.URL("/members"))
	return nil
}

// changeRole sets a member's role. Only owners can make or unmake owners,
// and a workspace always keeps one.
func (h handler) changeRole(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	ws, u := tenant.From(ctx), auth.From(ctx)
	id, err := web.PathID(r, "id")
	if err != nil {
		return err
	}
	role := auth.Role(r.PostFormValue("role"))
	if !role.Valid() {
		return web.ErrNotFound
	}
	var row db.ListMembersRow
	err = pgx.BeginTxFunc(ctx, h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := db.New(tx)
		owners, err := q.LockOwners(ctx, ws.ID)
		if err != nil {
			return err
		}
		m, err := q.GetMember(ctx, db.GetMemberParams{WorkspaceID: ws.ID, AccountID: id})
		if err != nil {
			return err
		}
		touchesOwner := role == auth.Owner || auth.Role(m.Role) == auth.Owner
		if touchesOwner && auth.RoleFrom(ctx) != auth.Owner {
			return web.ErrForbidden
		}
		if auth.Role(m.Role) == auth.Owner && role != auth.Owner && len(owners) == 1 {
			return errLastOwner
		}
		if _, err := q.UpdateMemberRole(ctx, db.UpdateMemberRoleParams{WorkspaceID: ws.ID, AccountID: id, Role: string(role)}); err != nil {
			return err
		}
		if err := q.RecordAudit(ctx, db.RecordAuditParams{ActorID: &u.ID, WorkspaceID: &ws.ID, Action: "member.role_changed", Target: m.Email + " → " + string(role)}); err != nil {
			return err
		}
		row = db.ListMembersRow(m)
		row.Role = string(role)
		return nil
	})
	msg := ""
	if errors.Is(err, errLastOwner) {
		msg = "A workspace needs an owner. Make someone else owner first."
	} else if err != nil {
		return web.NotFoundIfNoRows(err)
	}
	if msg != "" {
		v := membersView{Error: msg}
		if err := h.loadMembers(ctx, &v); err != nil {
			return err
		}
		return web.Render(w, r, http.StatusUnprocessableEntity, membersPage(v))
	}
	if web.IsHTMX(r) && id != u.ID {
		return web.Render(w, r, http.StatusOK, memberRow(row))
	}
	// Changing your own role can change what the page shows you.
	web.Redirect(w, r, ws.URL("/members"))
	return nil
}

// removeMember takes someone out of the workspace: anyone may leave, and
// ManageMembers may remove others. Owners go only by an owner's hand, and
// the last owner can't go.
func (h handler) removeMember(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	ws, u := tenant.From(ctx), auth.From(ctx)
	id, err := web.PathID(r, "id")
	if err != nil {
		return err
	}
	self := id == u.ID
	if !self && !auth.CanIn(ctx, auth.ManageMembers) {
		return web.ErrForbidden
	}
	err = pgx.BeginTxFunc(ctx, h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := db.New(tx)
		owners, err := q.LockOwners(ctx, ws.ID)
		if err != nil {
			return err
		}
		m, err := q.GetMember(ctx, db.GetMemberParams{WorkspaceID: ws.ID, AccountID: id})
		if err != nil {
			return err
		}
		if auth.Role(m.Role) == auth.Owner {
			if !self && auth.RoleFrom(ctx) != auth.Owner {
				return web.ErrForbidden
			}
			if len(owners) == 1 {
				return errLastOwner
			}
		}
		if _, err := q.DeleteMember(ctx, db.DeleteMemberParams{WorkspaceID: ws.ID, AccountID: id}); err != nil {
			return err
		}
		action := "member.removed"
		if self {
			action = "member.left"
		}
		return q.RecordAudit(ctx, db.RecordAuditParams{ActorID: &u.ID, WorkspaceID: &ws.ID, Action: action, Target: m.Email})
	})
	if errors.Is(err, errLastOwner) {
		v := membersView{Error: "A workspace needs an owner. Make someone else owner first."}
		if err := h.loadMembers(ctx, &v); err != nil {
			return err
		}
		return web.Render(w, r, http.StatusUnprocessableEntity, membersPage(v))
	}
	if err != nil {
		return web.NotFoundIfNoRows(err)
	}
	if self {
		web.Redirect(w, r, auth.HomePath)
		return nil
	}
	if web.IsHTMX(r) {
		w.WriteHeader(http.StatusOK)
		return nil
	}
	web.Redirect(w, r, ws.URL("/members"))
	return nil
}

type invitationView struct {
	Token       string
	Invitation  db.GetInvitationRow
	SignedInAs  string
	WrongPerson bool
}

func (h handler) loadInvitation(r *http.Request) (invitationView, error) {
	v := invitationView{Token: r.PathValue("token")}
	inv, err := h.q.GetInvitation(r.Context(), auth.HashToken(v.Token))
	if err != nil {
		return v, err
	}
	v.Invitation = inv
	if u := auth.From(r.Context()); u != nil {
		v.SignedInAs = u.Email
		v.WrongPerson = u.Email != inv.Email
	}
	return v, nil
}

func (h handler) invitation(w http.ResponseWriter, r *http.Request) error {
	v, err := h.loadInvitation(r)
	if errors.Is(err, pgx.ErrNoRows) {
		return web.Render(w, r, http.StatusNotFound, invitationExpiredPage())
	}
	if err != nil {
		return err
	}
	return web.Render(w, r, http.StatusOK, invitationPage(v))
}

// accept adds the signed-in user to the workspace. The invitation is for an
// email, so only the account with that email can accept it.
func (h handler) accept(w http.ResponseWriter, r *http.Request) error {
	u := auth.From(r.Context())
	if u == nil {
		web.Redirect(w, r, auth.LoginPath+"?next="+url.QueryEscape(r.URL.Path))
		return nil
	}
	v, err := h.loadInvitation(r)
	if errors.Is(err, pgx.ErrNoRows) {
		return web.Render(w, r, http.StatusNotFound, invitationExpiredPage())
	}
	if err != nil {
		return err
	}
	if v.WrongPerson {
		return web.Render(w, r, http.StatusForbidden, invitationPage(v))
	}
	inv := v.Invitation
	err = pgx.BeginTxFunc(r.Context(), h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := db.New(tx)
		if err := q.AddMember(r.Context(), db.AddMemberParams{WorkspaceID: inv.WorkspaceID, AccountID: u.ID, Role: inv.Role}); err != nil {
			return err
		}
		if _, err := q.DeleteInvitation(r.Context(), db.DeleteInvitationParams{WorkspaceID: inv.WorkspaceID, ID: inv.ID}); err != nil {
			return err
		}
		return q.RecordAudit(r.Context(), db.RecordAuditParams{ActorID: &u.ID, WorkspaceID: &inv.WorkspaceID, Action: "member.joined", Target: u.Email})
	})
	if err != nil {
		return err
	}
	web.Redirect(w, r, "/w/"+inv.WorkspaceSlug)
	return nil
}
