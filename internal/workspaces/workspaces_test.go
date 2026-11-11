package workspaces_test

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/riadafridishibly/quickstack/internal/apptest"
	"github.com/riadafridishibly/quickstack/internal/auth"
	"github.com/riadafridishibly/quickstack/internal/auth/authtest"
	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/testdb"
)

func TestCreateWorkspace(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	_, c := authtest.SignIn(t, s.Pool, "ana@example.com")

	w := s.Do(apptest.Req{Path: "/w", Cookie: c, Form: url.Values{"name": {""}}, Target: "-"})
	apptest.Status(t, w, http.StatusUnprocessableEntity)
	if strings.Contains(w.Body.String(), "<html") || !strings.Contains(w.Body.String(), "Enter a name.") {
		t.Errorf("htmx validation failure isn't the form alone:\n%s", w.Body)
	}

	apptest.Redirect(t, s.Post("/w", c, url.Values{"name": {"Acme Robotics"}}), "/w/acme-robotics")
	apptest.Status(t, s.Get("/w/acme-robotics", c), http.StatusOK)

	w = s.Post("/w", c, url.Values{"name": {"Acme"}, "slug": {"acme-robotics"}})
	apptest.Status(t, w, http.StatusUnprocessableEntity)
	if !strings.Contains(w.Body.String(), "Another workspace uses this URL.") {
		t.Error("no message for a taken URL")
	}
}

func TestOnlyMembersSeeAWorkspace(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	ws := testdb.CreateWorkspace(t, s.Pool, "acme")
	_, member := authtest.Member(t, s.Pool, ws, "ana@example.com", auth.Viewer)
	stranger, other := authtest.SignIn(t, s.Pool, "bo@example.com")
	authtest.SetPlatformRole(t, s.Pool, stranger, auth.Superadmin)

	apptest.Status(t, s.Get("/w/acme", member), http.StatusOK)
	// Not even platform staff get in without a membership.
	apptest.Status(t, s.Get("/w/acme", other), http.StatusNotFound)
	apptest.Status(t, s.Get("/w/acme/projects", other), http.StatusNotFound)
}

func TestViewerCannotManage(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	ws := testdb.CreateWorkspace(t, s.Pool, "acme")
	_, c := authtest.Member(t, s.Pool, ws, "ana@example.com", auth.Viewer)

	apptest.Status(t, s.Get("/w/acme/settings", c), http.StatusForbidden)
	apptest.Status(t, s.Post("/w/acme/members/invite", c, url.Values{"email": {"x@example.com"}, "role": {"member"}}), http.StatusForbidden)
	body := s.Get("/w/acme/members", c).Body.String()
	if strings.Contains(body, "Invite someone") || strings.Contains(body, "Settings") {
		t.Error("a viewer is shown what they can't use")
	}
}

func TestInviteAndAccept(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	ws := testdb.CreateWorkspace(t, s.Pool, "acme")
	_, owner := authtest.Member(t, s.Pool, ws, "owner@example.com", auth.Owner)

	w := s.Do(apptest.Req{Path: "/w/acme/members/invite", Cookie: owner, Target: "invitations",
		Form: url.Values{"email": {"Bo@Example.com"}, "role": {"owner"}}})
	apptest.Status(t, w, http.StatusUnprocessableEntity) // owner isn't an invitation role

	w = s.Do(apptest.Req{Path: "/w/acme/members/invite", Cookie: owner, Target: "invitations",
		Form: url.Values{"email": {"Bo@Example.com"}, "role": {"member"}}})
	apptest.Status(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), "Invitation sent to bo@example.com") {
		t.Errorf("no confirmation:\n%s", w.Body)
	}
	link := s.Link("bo@example.com")

	// Signed out: the page offers to sign up with the invited email.
	if body := s.Get(link, nil).Body.String(); !strings.Contains(body, "signup?email=bo%40example.com") {
		t.Errorf("signed-out invitation page:\n%s", body)
	}
	// Someone else's account can't use it.
	_, wrong := authtest.SignIn(t, s.Pool, "cy@example.com")
	apptest.Status(t, s.Post(link, wrong, url.Values{}), http.StatusForbidden)

	bo, c := authtest.SignIn(t, s.Pool, "bo@example.com")
	apptest.Redirect(t, s.Post(link, c, url.Values{}), "/w/acme")
	m, err := db.New(s.Pool).GetMember(t.Context(), db.GetMemberParams{WorkspaceID: ws.ID, AccountID: bo})
	if err != nil || m.Role != "member" {
		t.Fatalf("membership %+v, %v", m, err)
	}
	apptest.Status(t, s.Get(link, c), http.StatusNotFound) // used up
}

func TestOwnerRules(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	ws := testdb.CreateWorkspace(t, s.Pool, "acme")
	ownerID, owner := authtest.Member(t, s.Pool, ws, "owner@example.com", auth.Owner)
	adminID, admin := authtest.Member(t, s.Pool, ws, "admin@example.com", auth.Admin)
	memberID, _ := authtest.Member(t, s.Pool, ws, "member@example.com", auth.Member)
	role := func(id int64) string { return "/w/acme/members/" + strconv.FormatInt(id, 10) + "/role" }

	// An admin manages members but not owners.
	w := s.Do(apptest.Req{Path: role(memberID), Cookie: admin, Target: "-", Form: url.Values{"role": {"viewer"}}})
	apptest.Status(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), `<tr data-member="member@example.com"`) {
		t.Errorf("htmx role change doesn't return the row:\n%s", w.Body)
	}
	apptest.Status(t, s.Post(role(memberID), admin, url.Values{"role": {"owner"}}), http.StatusForbidden)
	apptest.Status(t, s.Post(role(ownerID), admin, url.Values{"role": {"member"}}), http.StatusForbidden)

	// The last owner can't step down or leave.
	w = s.Post(role(ownerID), owner, url.Values{"role": {"admin"}})
	apptest.Status(t, w, http.StatusUnprocessableEntity)
	apptest.Status(t, s.Post("/w/acme/members/"+strconv.FormatInt(ownerID, 10)+"/delete", owner, url.Values{}), http.StatusUnprocessableEntity)

	// With a second owner, they can.
	apptest.Redirect(t, s.Post(role(adminID), owner, url.Values{"role": {"owner"}}), "/w/acme/members")
	apptest.Redirect(t, s.Post("/w/acme/members/"+strconv.FormatInt(ownerID, 10)+"/delete", owner, url.Values{}), "/w")
	apptest.Status(t, s.Get("/w/acme", owner), http.StatusNotFound)
}

func TestSettings(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	ws := testdb.CreateWorkspace(t, s.Pool, "acme")
	_, c := authtest.Member(t, s.Pool, ws, "owner@example.com", auth.Owner)
	testdb.CreateWorkspace(t, s.Pool, "taken")

	apptest.Status(t, s.Post("/w/acme/settings", c, url.Values{"name": {"Acme"}, "slug": {"taken"}}), http.StatusUnprocessableEntity)
	apptest.Status(t, s.Post("/w/acme/settings", c, url.Values{"name": {"Acme"}, "slug": {"Not A Slug!"}}), http.StatusUnprocessableEntity)
	apptest.Redirect(t, s.Post("/w/acme/settings", c, url.Values{"name": {"Acme Inc"}, "slug": {"acme-inc"}, "public": {"on"}}),
		"/w/acme-inc/settings?saved=1")
	apptest.Status(t, s.Get("/p/acme-inc", nil), http.StatusOK)
}
