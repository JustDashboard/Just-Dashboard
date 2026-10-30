package api

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/linuxusers"
)

// The host alert that watches who can get in. Every other kind reads a condition
// and tells when it starts and stops; this one reads what is on the host now,
// compares it with what was there last pass, and tells what appeared. Reading the
// host, rather than noting what the dashboard's own routes did, is the point: the
// change an operator most wants to hear about is the one they did not make here.

// accountBaselineSetting is where the last pass's reading is kept between passes
// and across restarts.
const accountBaselineSetting = "system_account_baseline"

// accountBaselineMaxAge is how old a reading may be and still mean something. Past
// it the rule was off rather than the dashboard restarting, and telling everything
// that happened in the meantime, one message each, tells nobody anything.
const accountBaselineMaxAge = 24 * time.Hour

type accountBaseline struct {
	At       time.Time           `json:"at"`
	Snapshot linuxusers.Snapshot `json:"snapshot"`
}

// accountChangeFindings is the judgement alone, which a test can hand made-up
// readings to. A first pass, or one after a long gap, sets the line and says
// nothing: everything already on the host would otherwise read as new.
func accountChangeFindings(before *accountBaseline, after *linuxusers.Snapshot, now time.Time) hostAlertReading {
	reading := newHostAlertReading()
	if before == nil || now.Sub(before.At) > accountBaselineMaxAge {
		return reading
	}
	for _, change := range linuxusers.DiffSnapshots(&before.Snapshot, after) {
		reading.Firing = append(reading.Firing, hostAlertFinding{
			Subject: change.Subject, Label: change.Label, Detail: change.Detail,
		})
	}
	return reading
}

// accountChangeReading is one pass: read the host, judge it against the stored
// reading and store the new one. A pass that cannot store is not judged, or the
// next would tell the same changes again.
func (s *Server) accountChangeReading(
	ctx context.Context, now time.Time,
	read func(context.Context) (*linuxusers.Snapshot, error),
) hostAlertReading {
	current, err := read(ctx)
	if err != nil {
		return unjudgedHostAlert("The host's accounts could not be read.")
	}
	raw, ok, err := s.Store.Setting(ctx, accountBaselineSetting)
	if err != nil {
		return unjudgedHostAlert("The last reading of the host's accounts could not be read.")
	}
	var before *accountBaseline
	if ok && raw != "" {
		var stored accountBaseline
		// One that does not parse is no reading at all, and sets the line again.
		if json.Unmarshal([]byte(raw), &stored) == nil {
			before = &stored
		}
	}
	reading := accountChangeFindings(before, current, now)
	encoded, err := json.Marshal(accountBaseline{At: now, Snapshot: *current})
	if err != nil {
		return unjudgedHostAlert("The reading of the host's accounts could not be kept.")
	}
	if err := s.Store.SetSetting(ctx, accountBaselineSetting, string(encoded)); err != nil {
		return unjudgedHostAlert("The reading of the host's accounts could not be kept.")
	}
	return reading
}
