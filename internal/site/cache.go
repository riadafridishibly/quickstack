package site

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/media"
	"github.com/riadafridishibly/quickstack/internal/web"
)

// channel is where a trigger on site_settings notifies every change.
const channel = "site_settings"

// Settings are what pages need from site_settings.
type Settings struct {
	// Icon is the live icon, or nil if there is none.
	Icon *Icon
	// Background is the Apple icon's background, also the manifest's
	// theme colour.
	Background string
}

// Icon is the site icon whose files pages link.
type Icon struct {
	Rev int32
	// SVG says whether there is an icon.svg.
	SVG bool
}

// Cache keeps the settings in memory while Listen hears of every change.
// Until then, and whenever it has lost its connection, each Get reads the
// database.
type Cache struct {
	pool *pgxpool.Pool

	mu        sync.Mutex
	listening bool
	// gen goes up with each change, so a read that raced one isn't kept.
	gen    uint64
	cached *Settings
}

func NewCache(pool *pgxpool.Pool) *Cache { return &Cache{pool: pool} }

func (c *Cache) Get(ctx context.Context) (Settings, error) {
	c.mu.Lock()
	if c.cached != nil {
		s := *c.cached
		c.mu.Unlock()
		return s, nil
	}
	gen := c.gen
	c.mu.Unlock()

	row, err := db.New(c.pool).GetSiteSettings(ctx)
	if err != nil {
		return Settings{}, err
	}
	set := row.SiteSetting
	s := Settings{Background: set.IconBackground}
	if set.LiveIconID != nil && set.LiveIconRev > 0 {
		s.Icon = &Icon{Rev: set.LiveIconRev, SVG: row.LiveIconContentType != nil && *row.LiveIconContentType == "image/svg+xml"}
	}

	c.mu.Lock()
	if c.listening && c.gen == gen {
		c.cached = &s
	}
	c.mu.Unlock()
	return s, nil
}

func (c *Cache) changed(listening bool) {
	c.mu.Lock()
	c.listening = listening
	c.gen++
	c.cached = nil
	c.mu.Unlock()
}

// Listen keeps the cache up to date until ctx is done, connecting again
// when its connection fails.
func (c *Cache) Listen(ctx context.Context) {
	for {
		err := c.listen(ctx)
		c.changed(false)
		if ctx.Err() != nil {
			return
		}
		slog.WarnContext(ctx, "site: lost the connection that listens for settings changes", "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (c *Cache) listen(ctx context.Context) error {
	// Its own connection: one that listens can't go back to the pool.
	conn, err := pgx.ConnectConfig(ctx, c.pool.Config().ConnConfig.Copy())
	if err != nil {
		return err
	}
	defer conn.Close(context.WithoutCancel(ctx))
	if _, err := conn.Exec(ctx, "LISTEN "+channel); err != nil {
		return err
	}
	c.changed(true)
	for {
		if _, err := conn.WaitForNotification(ctx); err != nil {
			return err
		}
		c.changed(true)
	}
}

// Middleware lets web.Document link the site icon.
func (c *Cache) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(web.WithIcons(r.Context(), c.icons)))
	})
}

func (c *Cache) icons(ctx context.Context) web.Icons {
	s, err := c.Get(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "site: settings", "err", err)
		return web.Icons{}
	}
	if s.Icon == nil {
		return web.Icons{}
	}
	i := web.Icons{
		Favicon:    media.IconURL(s.Icon.Rev, media.FaviconFile),
		AppleTouch: media.IconURL(s.Icon.Rev, media.AppleIconFile),
		Manifest:   manifestPath,
	}
	if s.Icon.SVG {
		i.SVG = media.IconURL(s.Icon.Rev, media.IconSVGFile)
	}
	return i
}
