package api

import (
	"slices"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

func TestParseStatsIDs(t *testing.T) {
	t.Parallel()
	full := strings.Repeat("a1", 32)
	cases := []struct {
		name    string
		raw     string
		want    []string
		wantErr bool
	}{
		{name: "absent means every container", raw: "", want: nil},
		{name: "prefix and full id", raw: "0123456789ab," + full, want: []string{"0123456789ab", full}},
		{name: "too short", raw: "0123456", wantErr: true},
		{name: "not hex", raw: "0123456789zz", wantErr: true},
		{name: "empty element", raw: "0123456789ab,", wantErr: true},
		{name: "too many", raw: strings.TrimSuffix(strings.Repeat("0123456789ab,", maxStatsIDs+1), ","), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseStatsIDs(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("ids = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSelectStatsIDsNarrowsToRunningMatches(t *testing.T) {
	t.Parallel()
	a := strings.Repeat("a", 64)
	b := strings.Repeat("b", 64)
	c := strings.Repeat("c", 64)
	list := []dockerx.Container{
		{ID: a, State: "running"},
		{ID: b, State: "running"},
		{ID: c, State: "exited"},
	}
	if got := selectStatsIDs(list, nil); !slices.Equal(got, []string{a, b}) {
		t.Fatalf("all = %v, want both running containers", got)
	}
	if got := selectStatsIDs(list, []string{a[:12]}); !slices.Equal(got, []string{a}) {
		t.Fatalf("prefix = %v, want only the full id of a", got)
	}
	if got := selectStatsIDs(list, []string{c[:12], strings.Repeat("d", 12)}); len(got) != 0 {
		t.Fatalf("stopped or unknown = %v, want none", got)
	}
}
