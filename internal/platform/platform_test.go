package platform_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/riadafridishibly/quickstack/internal/apptest"
	"github.com/riadafridishibly/quickstack/internal/auth"
	"github.com/riadafridishibly/quickstack/internal/auth/authtest"
	"github.com/riadafridishibly/quickstack/internal/db"
)

func TestPlatformRoles(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	_, nobody := authtest.SignIn(t, s.Pool, "ana@example.com")
	supportID, support := authtest.SignIn(t, s.Pool, "sam@example.com")
	authtest.SetPlatformRole(t, s.Pool, supportID, auth.Support)
	adminID, admin := authtest.SignIn(t, s.Pool, "ada@example.com")
	authtest.SetPlatformRole(t, s.Pool, adminID, auth.Superadmin)

	apptest.Status(t, s.Get("/admin", nobody), http.StatusForbidden)
	for _, p := range []string{"/admin", "/admin/accounts", "/admin/workspaces", "/admin/audit"} {
		apptest.Status(t, s.Get(p, support), http.StatusOK)
	}
	w := s.Do(apptest.Req{Path: "/admin/accounts?q=ana", Cookie: support, Target: "admin-list"})
	if b := w.Body.String(); strings.Contains(b, "<html") || !strings.Contains(b, "ana@example.com") || strings.Contains(b, "sam@example.com") {
		t.Errorf("account search fragment:\n%s", b)
	}

	role := fmt.Sprintf("/admin/accounts/%d/role", supportID)
	apptest.Status(t, s.Post(role, support, url.Values{"role": {"superadmin"}}), http.StatusForbidden)
	apptest.Redirect(t, s.Post(role, admin, url.Values{"role": {""}}), fmt.Sprintf("/admin/accounts/%d?done=role", supportID))
	apptest.Status(t, s.Get("/admin", support), http.StatusForbidden)
	apptest.Status(t, s.Post(fmt.Sprintf("/admin/accounts/%d/role", adminID), admin, url.Values{"role": {""}}), http.StatusUnprocessableEntity)

	rows, err := db.New(s.Pool).ListAudit(t.Context(), db.ListAuditParams{Action: "platform.role_changed", Lim: 10})
	if err != nil || len(rows) != 1 || rows[0].Target != "sam@example.com → none" {
		t.Errorf("audit %+v, %v", rows, err)
	}
}

func TestSignOutEverywhere(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	anaID, ana := authtest.SignIn(t, s.Pool, "ana@example.com")
	supportID, support := authtest.SignIn(t, s.Pool, "sam@example.com")
	authtest.SetPlatformRole(t, s.Pool, supportID, auth.Support)

	apptest.Status(t, s.Get("/w", ana), http.StatusOK)
	apptest.Redirect(t, s.Post(fmt.Sprintf("/admin/accounts/%d/sign-out", anaID), support, url.Values{}),
		fmt.Sprintf("/admin/accounts/%d?done=signed-out", anaID))
	apptest.Redirect(t, s.Get("/w", ana), "/login?next=%2Fw")
}
