package auth

import (
	"context"
	"crypto/rand"
	"net/http"
	"strconv"
	"strings"

	"github.com/a-h/templ"
	"github.com/jackc/pgx/v5"

	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/web"
)

const (
	recoveryPath = SecurityPath + "/recovery-codes"
	// Each code is 12 base32 characters, 60 bits.
	recoveryCodeCount = 10
	recoveryCodeLen   = 12
)

// recoveryCodes is the factor of last resort. It is part of the core, not
// added with AddFactor, and counts only while the account has another
// factor.
type recoveryCodes struct{ a *Auth }

func (recoveryCodes) Name() string { return "recovery" }

func (recoveryCodes) Enrolled(ctx context.Context, q *db.Queries, accountID int64) (bool, error) {
	n, err := q.CountRecoveryCodes(ctx, accountID)
	return n > 0, err
}

func (recoveryCodes) Form(errMsg string) templ.Component { return recoveryForm(errMsg) }

// Verify spends the code and tells the account holder, since a recovery
// code in use can mean the other factor is lost or someone else has the
// codes.
func (rc recoveryCodes) Verify(ctx context.Context, _ *db.Queries, accountID int64, r *http.Request) (bool, error) {
	code := normalizeRecoveryCode(r.PostFormValue("recovery_code"))
	if code == "" {
		return false, nil
	}
	ok := false
	err := pgx.BeginTxFunc(ctx, rc.a.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := db.New(tx)
		n, err := q.UseRecoveryCode(ctx, db.UseRecoveryCodeParams{AccountID: accountID, CodeHash: hashToken(code)})
		if err != nil || n == 0 {
			return err
		}
		ok = true
		acc, err := q.GetAccount(ctx, accountID)
		if err != nil {
			return err
		}
		left, err := q.CountRecoveryCodes(ctx, accountID)
		if err != nil {
			return err
		}
		if err := q.RecordAudit(ctx, db.RecordAuditParams{ActorID: &accountID, Action: "account.recovery_code_used", Target: acc.Email}); err != nil {
			return err
		}
		return rc.a.securityNotice(ctx, tx, acc.Email,
			"A recovery code was used for your Quickstack account. "+strconv.FormatInt(left, 10)+" of your recovery codes are left.")
	})
	return ok, err
}

func (recoveryCodes) Settings(ctx context.Context, q *db.Queries, accountID int64) (templ.Component, error) {
	n, err := q.CountRecoveryCodes(ctx, accountID)
	return recoverySection(int(n)), err
}

func (rc recoveryCodes) Routes(mux *web.Mux) {
	mux.Handle("POST /account/security/recovery-codes", RequireRecent(web.Handle(rc.regenerate)))
}

// regenerate replaces the account's recovery codes and shows the new ones.
func (rc recoveryCodes) regenerate(w http.ResponseWriter, r *http.Request) error {
	noStore(w)
	u := From(r.Context())
	var codes []string
	err := pgx.BeginTxFunc(r.Context(), rc.a.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := db.New(tx)
		fs, err := rc.a.enrolled(r.Context(), q, u.ID)
		if err != nil || len(fs) == 0 {
			return err
		}
		if codes, err = newRecoveryCodes(r.Context(), q, u.ID); err != nil {
			return err
		}
		if err := q.RecordAudit(r.Context(), db.RecordAuditParams{ActorID: &u.ID, Action: "account.recovery_codes_created", Target: u.Email}); err != nil {
			return err
		}
		return rc.a.securityNotice(r.Context(), tx, u.Email, "New recovery codes were made for your Quickstack account. The old ones no longer work.")
	})
	if err != nil {
		return err
	}
	if codes == nil {
		web.Redirect(w, r, SecurityPath)
		return nil
	}
	return web.Render(w, r, http.StatusOK, recoveryCodesPage(rc.a.Layout, "", codes))
}

// newRecoveryCodes replaces the account's recovery codes and returns them,
// formatted to show.
func newRecoveryCodes(ctx context.Context, q *db.Queries, accountID int64) ([]string, error) {
	if err := q.DeleteRecoveryCodes(ctx, accountID); err != nil {
		return nil, err
	}
	codes := make([]string, recoveryCodeCount)
	hashes := make([][]byte, recoveryCodeCount)
	for i := range codes {
		c := rand.Text()[:recoveryCodeLen]
		hashes[i] = hashToken(c)
		codes[i] = strings.ToLower(c[:4] + "-" + c[4:8] + "-" + c[8:])
	}
	err := q.CreateRecoveryCodes(ctx, db.CreateRecoveryCodesParams{AccountID: accountID, CodeHashes: hashes})
	return codes, err
}

// normalizeRecoveryCode returns the code as it is hashed, ignoring case,
// spaces and dashes, or "" if it can't be a recovery code.
func normalizeRecoveryCode(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - 'a' + 'A'
		case r >= 'A' && r <= 'Z', r >= '2' && r <= '7':
			return r
		case r == '-' || r == ' ':
			return -1
		}
		return '!'
	}, s)
	if len(s) != recoveryCodeLen || strings.ContainsRune(s, '!') {
		return ""
	}
	return s
}
