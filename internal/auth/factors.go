package auth

import (
	"context"
	"net/http"

	"github.com/a-h/templ"
	"github.com/jackc/pgx/v5"

	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/mail"
	"github.com/riadafridishibly/quickstack/internal/web"
)

// SecurityPath is the page where people manage how they sign in. Factors add
// their pages under it.
const SecurityPath = "/account/security"

// A Factor is a second factor, such as an authenticator app. Each lives in
// its own file and is added with Auth.AddFactor. Signing in, and confirming
// it's you, ask for one when the account has any.
type Factor interface {
	// Name tells factors apart in the challenge form, such as "totp".
	Name() string
	// Enrolled reports whether the account has set the factor up.
	Enrolled(ctx context.Context, q *db.Queries, accountID int64) (bool, error)
	// Form renders the factor's fields and submit button inside the
	// challenge form. errMsg is set after Verify fails.
	Form(errMsg string) templ.Component
	// Verify checks what Form posted.
	Verify(ctx context.Context, q *db.Queries, accountID int64, r *http.Request) (bool, error)
	// Settings renders the factor's section of the security page.
	Settings(ctx context.Context, q *db.Queries, accountID int64) (templ.Component, error)
	// Routes adds the pages that set the factor up and remove it. mux is
	// behind RequireLogin; wrap them in RequireRecent.
	Routes(mux *web.Mux)
}

// AddFactor adds a second factor. Call it before SecurityRoutes.
func (a *Auth) AddFactor(f Factor) { a.factors = append(a.factors, f) }

// enrolled returns the factors the account has set up, with recovery codes
// last. Recovery codes count only alongside another factor.
func (a *Auth) enrolled(ctx context.Context, q *db.Queries, accountID int64) ([]Factor, error) {
	var fs []Factor
	for _, f := range a.factors {
		ok, err := f.Enrolled(ctx, q, accountID)
		if err != nil {
			return nil, err
		}
		if ok {
			fs = append(fs, f)
		}
	}
	if len(fs) == 0 {
		return nil, nil
	}
	ok, err := a.recovery.Enrolled(ctx, q, accountID)
	if err != nil {
		return nil, err
	}
	if ok {
		fs = append(fs, a.recovery)
	}
	return fs, nil
}

// SecurityRoutes adds each factor's pages and the recovery code pages to a
// mux behind RequireLogin.
func (a *Auth) SecurityRoutes(mux *web.Mux) {
	for _, f := range a.factors {
		f.Routes(mux)
	}
	a.recovery.Routes(mux)
}

// SecuritySections renders a section of the security page for each factor,
// and one for recovery codes once the account has a factor.
func (a *Auth) SecuritySections(ctx context.Context, accountID int64) ([]templ.Component, error) {
	var out []templ.Component
	on := false
	for _, f := range a.factors {
		c, err := f.Settings(ctx, a.q, accountID)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
		ok, err := f.Enrolled(ctx, a.q, accountID)
		if err != nil {
			return nil, err
		}
		on = on || ok
	}
	if on {
		c, err := a.recovery.Settings(ctx, a.q, accountID)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// factorAdded records that u set up a factor and tells them by email. It
// returns new recovery codes, to show once, when u had none left.
func (a *Auth) factorAdded(ctx context.Context, tx pgx.Tx, u *User, action, notice string) ([]string, error) {
	q := db.New(tx)
	if err := q.RecordAudit(ctx, db.RecordAuditParams{ActorID: &u.ID, Action: action, Target: u.Email}); err != nil {
		return nil, err
	}
	if err := a.securityNotice(ctx, tx, u.Email, notice); err != nil {
		return nil, err
	}
	n, err := q.CountRecoveryCodes(ctx, u.ID)
	if err != nil || n > 0 {
		return nil, err
	}
	return newRecoveryCodes(ctx, q, u.ID)
}

// factorRemoved records that u removed a factor and tells them by email.
// Without another factor, the recovery codes go too.
func (a *Auth) factorRemoved(ctx context.Context, tx pgx.Tx, u *User, action, notice string) error {
	q := db.New(tx)
	if err := q.RecordAudit(ctx, db.RecordAuditParams{ActorID: &u.ID, Action: action, Target: u.Email}); err != nil {
		return err
	}
	if err := a.securityNotice(ctx, tx, u.Email, notice); err != nil {
		return err
	}
	fs, err := a.enrolled(ctx, q, u.ID)
	if err != nil || len(fs) > 0 {
		return err
	}
	return q.DeleteRecoveryCodes(ctx, u.ID)
}

// securityNotice mails the account holder about a change to how they sign
// in, so someone who took over their session can't make it unnoticed.
func (a *Auth) securityNotice(ctx context.Context, tx pgx.Tx, email, notice string) error {
	return mail.Enqueue(ctx, a.jobs, tx, mail.Message{
		To:      email,
		Subject: "Security notice for your Quickstack account",
		Text: notice + "\n\n" +
			"If this wasn't you, reset your password at " + a.publicURL + forgotPath + " and review your account's security settings.\n",
	})
}
