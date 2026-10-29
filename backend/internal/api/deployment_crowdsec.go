package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// crowdSecRefreshEvery is how stale the list a route refuses from may be: a
// new ban reaches the routes within a minute, and a quiet engine costs one
// cscli read a minute.
const crowdSecRefreshEvery = time.Minute

// crowdSecDecisions is netsec's read of the engine's bans, which is the one
// place this dashboard asks CrowdSec anything; a test hands in its own.
type crowdSecDecisions func(context.Context, int) (bool, []netsec.CrowdSecDecision, error)

// startCrowdSecRefresh keeps the proxies' CrowdSec list current on a host
// where CrowdSec is installed. On one where it is not, nothing is read and
// nothing is written: a route with the switch on refuses nobody, and the
// Domains page says why.
func (s *Server) startCrowdSecRefresh(ctx context.Context) {
	if s.modules.netsec == nil || s.modules.proxy == nil {
		return
	}
	read := s.modules.netsec.CrowdSecBans
	go func() {
		ticker := time.NewTicker(crowdSecRefreshEvery)
		defer ticker.Stop()
		for {
			refreshCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			if _, err := refreshCrowdSecList(refreshCtx, read, s.modules.proxy); err != nil && s.Log != nil {
				s.Log.Warn("crowdsec list refresh", slog.String("error", err.Error()))
			}
			cancel()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// crowdSecListWriter is the proxy's half of a refresh.
type crowdSecListWriter interface {
	RefreshCrowdSecList(context.Context, []string) (proxysvc.CrowdSecRefresh, error)
}

// refreshCrowdSecList reads the engine's decisions and hands the bans to the
// proxy. An engine that is not installed, or whose decisions could not be
// read, leaves the list as it was: an unreadable list is not an empty one,
// and emptying it would let every banned address back in.
func refreshCrowdSecList(ctx context.Context, read crowdSecDecisions, proxy crowdSecListWriter) (proxysvc.CrowdSecRefresh, error) {
	installed, found, err := read(ctx, proxysvc.MaxCrowdSecEntries)
	if err != nil || !installed {
		return proxysvc.CrowdSecRefresh{}, err
	}
	decisions := make([]proxysvc.CrowdSecBan, 0, len(found))
	for _, decision := range found {
		decisions = append(decisions, proxysvc.CrowdSecBan{Scope: decision.Scope, Value: decision.Value, Type: decision.Type})
	}
	banned, omitted := proxysvc.CrowdSecBanned(decisions)
	result, err := proxy.RefreshCrowdSecList(ctx, banned)
	result.Omitted = omitted
	return result, err
}
