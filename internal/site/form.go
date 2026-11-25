package site

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/media"
)

const defaultBackground = "#ffffff"

var colorRE = regexp.MustCompile(`^#[0-9a-f]{6}$`)

type iconForm struct {
	Icon       string
	Background string
	Errors     map[string]string

	// Set by validate.
	iconKey    string
	background string
}

func iconFormFrom(r *http.Request) iconForm {
	return iconForm{
		Icon:       strings.TrimSpace(r.PostFormValue("icon")),
		Background: strings.TrimSpace(r.PostFormValue("background")),
		Errors:     map[string]string{},
	}
}

func iconFormOf(row db.GetSiteSettingsRow) iconForm {
	f := iconForm{Background: row.SiteSetting.IconBackground, Errors: map[string]string{}}
	if row.IconKey != nil {
		f.Icon = media.URL(*row.IconKey)
	}
	return f
}

// validate checks the form on its own. The image is checked against the
// database when the form is saved.
func (f *iconForm) validate() bool {
	if f.Icon != "" {
		key := strings.TrimPrefix(f.Icon, "/media/")
		if !media.ValidKey(key) {
			f.Errors["icon"] = "Enter an image’s address from Media, such as /media/ab12cd34."
		}
		f.iconKey = key
	}
	f.background = strings.ToLower(f.Background)
	switch {
	case f.background == "":
		f.background = defaultBackground
	case !colorRE.MatchString(f.background):
		f.Errors["background"] = "Enter a colour as # and six hex digits, such as #ffffff."
	}
	return len(f.Errors) == 0
}
