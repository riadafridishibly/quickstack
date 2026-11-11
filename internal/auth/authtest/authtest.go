// Package authtest signs test users in without going through the sign-in
// form.
package authtest

import (
	"net/http"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/riadafridishibly/quickstack/internal/auth"
	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/tenant"
)

// Password is the password of every account SignIn creates.
const Password = "correct horse battery"

var hash = sync.OnceValue(func() string {
	h, err := auth.HashPassword(Password)
	if err != nil {
		panic(err)
	}
	return h
})

// SignIn creates an account and returns its id and a session cookie.
func SignIn(t testing.TB, pool *pgxpool.Pool, email string) (int64, *http.Cookie) {
	t.Helper()
	ctx := t.Context()
	q := db.New(pool)
	acc, err := q.CreateAccount(ctx, db.CreateAccountParams{Email: email, Name: email, PasswordHash: hash()})
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.NewSession(ctx, q, acc.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	return acc.ID, &http.Cookie{Name: auth.CookieName, Value: token}
}

// Join makes the account a member of ws with role.
func Join(t testing.TB, pool *pgxpool.Pool, ws *tenant.Workspace, accountID int64, role auth.Role) {
	t.Helper()
	err := db.New(pool).AddMember(t.Context(), db.AddMemberParams{WorkspaceID: ws.ID, AccountID: accountID, Role: string(role)})
	if err != nil {
		t.Fatal(err)
	}
}

// Member signs in a new account that has role in ws.
func Member(t testing.TB, pool *pgxpool.Pool, ws *tenant.Workspace, email string, role auth.Role) (int64, *http.Cookie) {
	t.Helper()
	id, c := SignIn(t, pool, email)
	Join(t, pool, ws, id, role)
	return id, c
}

// SetPlatformRole gives the account a platform role.
func SetPlatformRole(t testing.TB, pool *pgxpool.Pool, accountID int64, role auth.PlatformRole) {
	t.Helper()
	r := string(role)
	if _, err := db.New(pool).SetPlatformRole(t.Context(), db.SetPlatformRoleParams{ID: accountID, PlatformRole: &r}); err != nil {
		t.Fatal(err)
	}
}
