package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/mail"
	"github.com/riadafridishibly/quickstack/internal/web"
)

const (
	verifyEmailPath = "/verify-email"
	verifyEmailTTL  = 3 * 24 * time.Hour
)

var ErrEmailVerified = errors.New("auth: email already verified")

// sendVerification mails a link that verifies email for the account. Call it
// in the transaction that creates or changes the address.
func (a *Auth) sendVerification(ctx context.Context, tx pgx.Tx, accountID int64, email string) error {
	token := rand.Text()
	err := db.New(tx).CreateEmailVerification(ctx, db.CreateEmailVerificationParams{
		TokenHash: hashToken(token), AccountID: accountID, Email: email, ExpiresAt: time.Now().Add(verifyEmailTTL),
	})
	if err != nil {
		return err
	}
	link := a.publicURL + verifyEmailPath + "?token=" + url.QueryEscape(token)
	return mail.Enqueue(ctx, a.jobs, tx, mail.Message{
		To:      email,
		Subject: "Verify your email for Quickstack",
		Text: "To verify that this address belongs to your Quickstack account, open this link within three days:\n" + link + "\n\n" +
			"If you didn't create an account, ignore this email.\n",
	})
}

// SendVerification mails u a new verification link.
func (a *Auth) SendVerification(ctx context.Context, u *User) error {
	if u.EmailVerified {
		return ErrEmailVerified
	}
	if !a.verifies.Allow(strconv.FormatInt(u.ID, 10)) {
		return ErrTooManyAttempts
	}
	return pgx.BeginTxFunc(ctx, a.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return a.sendVerification(ctx, tx, u.ID, u.Email)
	})
}

// verifyEmailForm shows a button rather than verifying on GET, so a mail
// scanner that opens the link doesn't verify the address.
func (a *Auth) verifyEmailForm(w http.ResponseWriter, r *http.Request) error {
	if r.URL.Query().Has("done") {
		return web.Render(w, r, http.StatusOK, emailVerifiedPage())
	}
	token := r.URL.Query().Get("token")
	ev, err := a.q.GetEmailVerification(r.Context(), hashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return web.Render(w, r, http.StatusNotFound, verifyExpiredPage())
	}
	if err != nil {
		return err
	}
	return web.Render(w, r, http.StatusOK, verifyEmailPage(token, ev.Email))
}

func (a *Auth) verifyEmail(w http.ResponseWriter, r *http.Request) error {
	err := pgx.BeginTxFunc(r.Context(), a.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := db.New(tx)
		ev, err := q.TakeEmailVerification(r.Context(), hashToken(r.PostFormValue("token")))
		if err != nil {
			return err
		}
		n, err := q.VerifyEmail(r.Context(), db.VerifyEmailParams{ID: ev.AccountID, Email: ev.Email})
		if err != nil {
			return err
		}
		if n == 0 {
			return pgx.ErrNoRows
		}
		if err := q.DeleteEmailVerifications(r.Context(), ev.AccountID); err != nil {
			return err
		}
		return q.RecordAudit(r.Context(), db.RecordAuditParams{ActorID: &ev.AccountID, Action: "account.email_verified", Target: ev.Email})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return web.Render(w, r, http.StatusNotFound, verifyExpiredPage())
	}
	if err != nil {
		return err
	}
	web.Redirect(w, r, verifyEmailPath+"?done=1")
	return nil
}
