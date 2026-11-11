package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/mail"
	"github.com/riadafridishibly/quickstack/internal/web"
)

const (
	forgotPath = "/forgot-password"
	resetPath  = "/reset-password"
	resetTTL   = time.Hour
)

func (a *Auth) forgotForm(w http.ResponseWriter, r *http.Request) error {
	return web.Render(w, r, http.StatusOK, forgotPage(forgotView{Email: r.URL.Query().Get("email")}))
}

// forgot mails a reset link if the email has an account, and says the same
// thing either way, so the page doesn't reveal which emails have one.
func (a *Auth) forgot(w http.ResponseWriter, r *http.Request) error {
	v := forgotView{Email: NormalizeEmail(r.PostFormValue("email"))}
	ip := web.ClientKey(r)
	if !a.byIP.Allow(ip) || !a.byIPEmail.Allow(ip+" "+v.Email) {
		v.Error = "Too many requests. Wait a few minutes and try again."
		return web.Render(w, r, http.StatusTooManyRequests, forgotPage(v))
	}
	if !ValidEmail(v.Email) {
		v.Error = "Enter an email address such as ana@example.com."
		return web.Render(w, r, http.StatusUnprocessableEntity, forgotPage(v))
	}
	err := pgx.BeginTxFunc(r.Context(), a.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := db.New(tx)
		acc, err := q.GetAccountByEmail(r.Context(), v.Email)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		token := rand.Text()
		err = q.CreatePasswordReset(r.Context(), db.CreatePasswordResetParams{
			TokenHash: hashToken(token), AccountID: acc.ID, ExpiresAt: time.Now().Add(resetTTL),
		})
		if err != nil {
			return err
		}
		link := a.publicURL + resetPath + "?token=" + url.QueryEscape(token)
		return mail.Enqueue(r.Context(), a.jobs, tx, mail.Message{
			To:      acc.Email,
			Subject: "Reset your Quickstack password",
			Text: "Someone asked to reset the password of your Quickstack account.\n\n" +
				"To choose a new password, open this link within an hour:\n" + link + "\n\n" +
				"If it wasn't you, ignore this email; your password stays the same.\n",
		})
	})
	if err != nil {
		return err
	}
	return web.Render(w, r, http.StatusOK, forgotSentPage(v.Email))
}

func (a *Auth) resetForm(w http.ResponseWriter, r *http.Request) error {
	token := r.URL.Query().Get("token")
	_, err := a.q.GetPasswordReset(r.Context(), hashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return web.Render(w, r, http.StatusNotFound, resetExpiredPage())
	}
	if err != nil {
		return err
	}
	return web.Render(w, r, http.StatusOK, resetPage(resetView{Token: token}))
}

func (a *Auth) reset(w http.ResponseWriter, r *http.Request) error {
	v := resetView{Token: r.PostFormValue("token"), Errors: map[string]string{}}
	password := r.PostFormValue("password")
	if msg := CheckNewPassword(password); msg != "" {
		v.Errors["password"] = msg
	} else if password != r.PostFormValue("confirm_password") {
		v.Errors["confirm_password"] = "The passwords don’t match."
	}
	if len(v.Errors) > 0 {
		return web.Render(w, r, http.StatusUnprocessableEntity, resetPage(v))
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	var session string
	err = pgx.BeginTxFunc(r.Context(), a.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := db.New(tx)
		id, err := q.TakePasswordReset(r.Context(), hashToken(v.Token))
		if err != nil {
			return err
		}
		if _, err := q.UpdateAccountPassword(r.Context(), db.UpdateAccountPasswordParams{ID: id, PasswordHash: hash}); err != nil {
			return err
		}
		if err := q.DeletePasswordResets(r.Context(), id); err != nil {
			return err
		}
		if _, err := q.DeleteAccountSessions(r.Context(), id); err != nil {
			return err
		}
		if err := q.RecordAudit(r.Context(), db.RecordAuditParams{ActorID: &id, Action: "account.password_reset"}); err != nil {
			return err
		}
		session, err = NewSession(r.Context(), q, id, r.UserAgent())
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return web.Render(w, r, http.StatusNotFound, resetExpiredPage())
	}
	if err != nil {
		return err
	}
	SetCookie(w, session)
	web.Redirect(w, r, HomePath)
	return nil
}

// CleanupArgs is the daily job that deletes expired sessions, password
// resets and invitations.
type CleanupArgs struct{}

func (CleanupArgs) Kind() string { return "cleanup_expired" }

type cleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	q *db.Queries
}

func (w *cleanupWorker) Work(ctx context.Context, _ *river.Job[CleanupArgs]) error {
	if _, err := w.q.PurgeExpiredSessions(ctx); err != nil {
		return err
	}
	if _, err := w.q.PurgeExpiredPasswordResets(ctx); err != nil {
		return err
	}
	_, err := w.q.PurgeExpiredInvitations(ctx)
	return err
}

// AddWorkers registers the cleanup worker and returns its daily schedule.
func AddWorkers(w *river.Workers, pool *pgxpool.Pool) []*river.PeriodicJob {
	river.AddWorker(w, &cleanupWorker{q: db.New(pool)})
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(24*time.Hour), func() (river.JobArgs, *river.InsertOpts) {
			return CleanupArgs{}, nil
		}, &river.PeriodicJobOpts{RunOnStart: true}),
	}
}
