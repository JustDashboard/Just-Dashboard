package api

import (
	"context"
	"errors"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

type crowdSecWriterFake struct {
	calls  int
	banned []string
}

func (f *crowdSecWriterFake) RefreshCrowdSecList(_ context.Context, banned []string) (proxysvc.CrowdSecRefresh, error) {
	f.calls++
	f.banned = append([]string(nil), banned...)
	return proxysvc.CrowdSecRefresh{Entries: len(banned), Changed: true}, nil
}

// The refresh hands the proxies the engine's bans and nothing else, and an
// engine that is absent or unreadable leaves the list as it was: emptying it
// would let every banned address back in.
func TestCrowdSecRefreshKeepsTheListWhenTheEngineCannotBeRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	writer := &crowdSecWriterFake{}
	absent := func(context.Context, int) (bool, []netsec.CrowdSecDecision, error) { return false, nil, nil }
	if _, err := refreshCrowdSecList(ctx, absent, writer); err != nil || writer.calls != 0 {
		t.Fatalf("no CrowdSec wrote a list: %d, %v", writer.calls, err)
	}
	failing := func(context.Context, int) (bool, []netsec.CrowdSecDecision, error) {
		return true, nil, errors.New("cscli: connection refused")
	}
	if _, err := refreshCrowdSecList(ctx, failing, writer); err == nil || writer.calls != 0 {
		t.Fatalf("an unreadable engine emptied the list: %d, %v", writer.calls, err)
	}
	var asked int
	reading := func(_ context.Context, limit int) (bool, []netsec.CrowdSecDecision, error) {
		asked = limit
		return true, []netsec.CrowdSecDecision{
			{Scope: "Ip", Value: "192.0.2.7", Type: "ban"},
			{Scope: "Ip", Value: "192.0.2.8", Type: "captcha"},
			{Scope: "Range", Value: "198.51.100.0/24", Type: "ban"},
			{Scope: "Country", Value: "XX", Type: "ban"},
		}, nil
	}
	result, err := refreshCrowdSecList(ctx, reading, writer)
	if err != nil || writer.calls != 1 || asked != proxysvc.MaxCrowdSecEntries || !result.Changed {
		t.Fatalf("refresh = %+v, %v (asked %d)", result, err, asked)
	}
	if len(writer.banned) != 2 || writer.banned[0] != "192.0.2.7" || writer.banned[1] != "198.51.100.0/24" {
		t.Fatalf("banned = %v", writer.banned)
	}
}
