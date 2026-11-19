package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/jackc/pgx/v5"
	"rsc.io/qr"

	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/web"
)

// TOTP is the authenticator-app factor (RFC 6238): SHA-1, six digits,
// 30-second steps, which every app supports.
const (
	totpPath      = SecurityPath + "/totp"
	totpIssuer    = "Quickstack"
	totpStep      = 30
	totpDigits    = 6
	totpSecretLen = 20
	// totpSkew is how many steps either side of now a code may be from,
	// for clocks that are slightly off.
	totpSkew = 1
)

type TOTP struct {
	a      *Auth
	sealer sealer
}

// NewTOTP returns the authenticator-app factor. key is KeySize bytes; it
// seals the secrets that the apps share with the server.
func NewTOTP(a *Auth, key []byte) *TOTP { return &TOTP{a: a, sealer: newSealer(key)} }

func (*TOTP) Name() string { return "totp" }

func (*TOTP) Enrolled(ctx context.Context, q *db.Queries, accountID int64) (bool, error) {
	_, err := q.GetTOTP(ctx, accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (*TOTP) Form(errMsg string) templ.Component { return totpForm(errMsg) }

func (t *TOTP) Verify(ctx context.Context, q *db.Queries, accountID int64, r *http.Request) (bool, error) {
	cred, err := q.GetTOTP(ctx, accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	secret, err := t.sealer.open(cred.Secret, totpAAD("stored", accountID))
	if err != nil {
		return false, err
	}
	step, ok := totpMatch(secret, r.PostFormValue("totp_code"), time.Now())
	if !ok {
		return false, nil
	}
	n, err := q.UseTOTPStep(ctx, db.UseTOTPStepParams{AccountID: accountID, Step: step})
	return n == 1, err
}

func (*TOTP) Settings(ctx context.Context, q *db.Queries, accountID int64) (templ.Component, error) {
	cred, err := q.GetTOTP(ctx, accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return totpSection(nil), nil
	}
	if err != nil {
		return nil, err
	}
	return totpSection(&cred.CreatedAt), nil
}

func (t *TOTP) Routes(mux *web.Mux) {
	mux.Handle("GET "+totpPath, RequireRecent(web.Handle(t.setupForm)))
	mux.Handle("POST "+totpPath, RequireRecent(web.Handle(t.setup)))
	mux.Handle("POST "+totpPath+"/delete", RequireRecent(web.Handle(t.remove)))
}

type totpSetupView struct {
	// Sealed is the new secret, sealed, which the form posts back so that
	// nothing is stored until the app shows it has the secret.
	Sealed string
	// Key is the secret in base32, in groups of four, to type into an app.
	Key string
	// QR is the path of a QR code of the otpauth URI, in modules.
	QR     string
	QRSize int
	Error  string
}

func (t *TOTP) setupView(u *User, secret []byte) (totpSetupView, error) {
	key := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
	uri := "otpauth://totp/" + url.PathEscape(totpIssuer+":"+u.Email) + "?" + url.Values{
		"secret": {key}, "issuer": {totpIssuer}, "algorithm": {"SHA1"},
		"digits": {strconv.Itoa(totpDigits)}, "period": {strconv.Itoa(totpStep)},
	}.Encode()
	path, size, err := qrPath(uri)
	if err != nil {
		return totpSetupView{}, err
	}
	var groups []string
	for i := 0; i < len(key); i += 4 {
		groups = append(groups, key[i:min(i+4, len(key))])
	}
	return totpSetupView{
		Sealed: base64.RawURLEncoding.EncodeToString(t.sealer.seal(secret, totpAAD("setup", u.ID))),
		Key:    strings.Join(groups, " "),
		QR:     path,
		QRSize: size,
	}, nil
}

func (t *TOTP) setupForm(w http.ResponseWriter, r *http.Request) error {
	u := From(r.Context())
	if ok, err := t.Enrolled(r.Context(), t.a.q, u.ID); err != nil || ok {
		if ok {
			http.Redirect(w, r, SecurityPath, http.StatusSeeOther)
		}
		return err
	}
	secret := make([]byte, totpSecretLen)
	rand.Read(secret)
	v, err := t.setupView(u, secret)
	if err != nil {
		return err
	}
	return web.Render(w, r, http.StatusOK, totpSetupPage(t.a.Layout, v))
}

func (t *TOTP) setup(w http.ResponseWriter, r *http.Request) error {
	u := From(r.Context())
	sealed, err := base64.RawURLEncoding.DecodeString(r.PostFormValue("secret"))
	if err != nil {
		return web.ErrNotFound
	}
	secret, err := t.sealer.open(sealed, totpAAD("setup", u.ID))
	if errors.Is(err, errUnsealed) {
		return web.ErrNotFound
	}
	if err != nil {
		return err
	}
	v, err := t.setupView(u, secret)
	if err != nil {
		return err
	}
	if !t.a.factorTries.Allow(strconv.FormatInt(u.ID, 10)) {
		v.Error = "Too many attempts. Wait a few minutes and try again."
		return web.Render(w, r, http.StatusTooManyRequests, totpSetupPage(t.a.Layout, v))
	}
	step, ok := totpMatch(secret, r.PostFormValue("totp_code"), time.Now())
	if !ok {
		v.Error = "That code didn’t match. Check the time on your phone and enter the code the app shows now."
		return web.Render(w, r, http.StatusUnprocessableEntity, totpSetupPage(t.a.Layout, v))
	}
	var codes []string
	added := false
	err = pgx.BeginTxFunc(r.Context(), t.a.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		n, err := db.New(tx).CreateTOTP(r.Context(), db.CreateTOTPParams{
			AccountID: u.ID, Secret: t.sealer.seal(secret, totpAAD("stored", u.ID)), LastStep: step,
		})
		if err != nil || n == 0 {
			return err
		}
		added = true
		codes, err = t.a.factorAdded(r.Context(), tx, u, "account.totp_added", "An authenticator app was set up for your Quickstack account.")
		return err
	})
	if err != nil {
		return err
	}
	if !added || codes == nil {
		web.Redirect(w, r, SecurityPath)
		return nil
	}
	return web.Render(w, r, http.StatusOK, recoveryCodesPage(t.a.Layout, "Your authenticator app is set up.", codes))
}

func (t *TOTP) remove(w http.ResponseWriter, r *http.Request) error {
	u := From(r.Context())
	err := pgx.BeginTxFunc(r.Context(), t.a.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		n, err := db.New(tx).DeleteTOTP(r.Context(), u.ID)
		if err != nil || n == 0 {
			return err
		}
		return t.a.factorRemoved(r.Context(), tx, u, "account.totp_removed", "The authenticator app was removed from your Quickstack account.")
	})
	if err != nil {
		return err
	}
	web.Redirect(w, r, SecurityPath)
	return nil
}

// totpAAD binds a sealed secret to its account and to where it is kept, so
// the copy in the setup form can't be stored as is, or used by another
// account.
func totpAAD(purpose string, accountID int64) []byte {
	return []byte("totp " + purpose + " " + strconv.FormatInt(accountID, 10))
}

// totpMatch returns the step whose code is code, within totpSkew of now.
func totpMatch(secret []byte, code string, now time.Time) (int64, bool) {
	code = strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' {
			return -1
		}
		return r
	}, code)
	if len(code) != totpDigits {
		return 0, false
	}
	step := now.Unix() / totpStep
	for d := int64(-totpSkew); d <= totpSkew; d++ {
		if subtle.ConstantTimeCompare([]byte(hotp(secret, step+d)), []byte(code)) == 1 {
			return step + d, true
		}
	}
	return 0, false
}

// hotp is the RFC 4226 code for counter.
func hotp(secret []byte, counter int64) string {
	mac := hmac.New(sha1.New, secret)
	_ = binary.Write(mac, binary.BigEndian, counter)
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, n%1_000_000)
}

// qrPath draws text as a QR code: an SVG path of one unit per module, inside
// a four-module quiet zone, and the size of the whole.
func qrPath(text string) (string, int, error) {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", 0, err
	}
	const quiet = 4
	var b strings.Builder
	for y := range c.Size {
		for x := range c.Size {
			if c.Black(x, y) {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+quiet, y+quiet)
			}
		}
	}
	return b.String(), c.Size + 2*quiet, nil
}
