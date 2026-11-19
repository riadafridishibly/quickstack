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

// SignIn creates an account with a verified email and returns its id and a
// session cookie.
func SignIn(t testing.TB, pool *pgxpool.Pool, email string) (int64, *http.Cookie) {
	t.Helper()
	ctx := t.Context()
	q := db.New(pool)
	h := hash()
	acc, err := q.CreateAccount(ctx, db.CreateAccountParams{Email: email, Name: email, PasswordHash: &h})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.MarkEmailVerified(ctx, acc.ID); err != nil {
		t.Fatal(err)
	}
	token, err := auth.NewSession(ctx, q, acc.ID, auth.AAL1, "test")
	if err != nil {
		t.Fatal(err)
	}
	return acc.ID, &http.Cookie{Name: auth.CookieName, Value: token}
}

// SetPlatformRole gives the account a platform role.
func SetPlatformRole(t testing.TB, pool *pgxpool.Pool, accountID int64, role auth.PlatformRole) {
	t.Helper()
	r := string(role)
	if _, err := db.New(pool).SetPlatformRole(t.Context(), db.SetPlatformRoleParams{ID: accountID, PlatformRole: &r}); err != nil {
		t.Fatal(err)
	}
}
