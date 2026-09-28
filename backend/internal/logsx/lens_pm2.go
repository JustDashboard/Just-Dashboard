package logsx

import (
	"strings"
	"time"
)

// The pm2 lens is the app lens behind PM2's own timestamp. A process started
// with --time, or with log_date_format set, has every chunk it writes
// prefixed by PM2 — "2024-01-01T00:00:00: " by default, "2024-01-01 00:00:00
// +00:00: " with the common "YYYY-MM-DD HH:mm:ss Z" — and without the prefix
// stripped the app lens would see a date where it looks for "Error:" or
// "GET /". Without either setting a PM2 line is the application's own output,
// and this is the app lens exactly.
//
// PM2's restarts are not read here: they are written to pm2.log, which the
// PM2 source does not read, and the process record already counts them.

func init() {
	register(&Lens{
		ID:     "pm2",
		Events: appEvents,
		Attrs:  appAttrs,
		New:    func() Reader { return &pm2Reader{} },
	})
}

type pm2Reader struct {
	app appReader
}

func (r *pm2Reader) Read(l *Line) {
	text := l.Text
	if t, rest, ok := pm2Prefix(text); ok {
		appSetStamp(l, t)
		text = rest
	}
	r.app.read(l, text)
}

// pm2Prefix splits PM2's timestamp from the line it prefixed. The stamp is
// the host's local time unless the format wrote a zone: dayjs formats in the
// daemon's zone, which is the host's.
func pm2Prefix(text string) (t time.Time, rest string, ok bool) {
	if text == "" || text[0] < '0' || text[0] > '9' {
		return t, text, false
	}
	t, n, ok := appParseStamp(text)
	if !ok || !strings.HasPrefix(text[n:], ": ") {
		return t, text, false
	}
	return t, text[n+2:], true
}
