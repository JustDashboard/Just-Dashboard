package netx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Limits on a fetched list. The cap is on the body as sent, so a feed that
// answers with a gigabyte costs sixteen megabytes of this process's time and
// no more.
const (
	maxFeedBytes             = 16 << 20
	maxListEntries           = 500_000
	maxManualList            = 10_000
	maxCountries             = 30
	blocklistStaleAfter      = 24 * time.Hour
	blocklistRefreshInterval = time.Hour
)

// httpClient fetches feeds and country zones. A variable so tests point it at
// a server of their own; the real one follows at most three redirects, which
// is what a feed behind a CDN needs and what a loop of them cannot exceed.
var httpClient = &http.Client{
	Timeout: 30 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 {
			return errors.New("the feed redirected more than three times")
		}
		return nil
	},
}

// The country zone files ipdeny publishes, aggregated so a country is
// thousands of lines rather than tens of thousands. Variables so a test can
// stand a server of its own behind them.
var (
	ipdenyV4 = "https://www.ipdeny.com/ipblocks/data/aggregated/%s-aggregated.zone"
	ipdenyV6 = "https://www.ipdeny.com/ipv6/ipaddresses/aggregated/%s-aggregated.zone"
)

// FeedPreset is a feed the page offers by name.
type FeedPreset struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

// feedPresets are the feeds with a name. Both are the networks no legitimate
// traffic should come from; the lists are small enough to be dropped without
// thought and are kept up by their maintainers.
var feedPresets = []FeedPreset{
	{
		ID: "spamhaus-drop", Name: "Spamhaus DROP",
		URL:         "https://www.spamhaus.org/drop/drop.txt",
		Description: "Networks Spamhaus has found hijacked or run by spammers and cybercriminals. A few hundred entries, very few false positives.",
	},
	{
		ID: "firehol-level1", Name: "FireHOL level 1",
		URL:         "https://iplists.firehol.org/files/firehol_level1.netset",
		Description: "FireHOL's no-false-positives list, folding Spamhaus DROP, DShield and several botnet trackers into one. Private and reserved ranges in it are skipped.",
	},
}

// neverBlock are the ranges a fetched list may not take, however its feed
// lists them. FireHOL's level 1 carries the bogons — 10/8, 192.168/16, the
// carrier-grade 100.64/10 Tailscale lives in — and a prerouting drop on them
// would cut off every Docker container, LAN client and tailnet peer. The
// trusted set protects the operator, not the machines behind the host.
var neverBlock = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("::1/128"),
}

func overlaps(a, b netip.Prefix) bool {
	return a.Contains(b.Addr()) || b.Contains(a.Addr())
}

// parseFeed reads a feed's text: one network per line, optionally followed by
// a comment (Spamhaus writes "1.10.16.0/20 ; SBL256894"), or a JSON object per
// line with a "cidr" (Spamhaus's newer form). Lines that are neither are
// skipped, not fatal: a feed gaining a header line must not stop the list
// being loaded. skipped counts them, and the networks it dropped for being
// private or reserved.
func parseFeed(data []byte) (nets []netip.Prefix, skipped int) {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "{") {
			var o struct {
				CIDR string `json:"cidr"`
			}
			if json.Unmarshal([]byte(line), &o) != nil {
				skipped++
				continue
			}
			line = o.CIDR
		}
		if i := strings.IndexAny(line, "#;"); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		p, err := ParsePrefix(fields[0])
		if err != nil || p.Bits() == 0 {
			skipped++
			continue
		}
		p = p.Masked()
		reserved := false
		for _, never := range neverBlock {
			if overlaps(p, never) {
				reserved = true
				break
			}
		}
		if reserved {
			skipped++
			continue
		}
		nets = append(nets, p)
	}
	return nets, skipped
}

// fetch reads one URL, bounded in time, redirects and size. A body over the
// cap is refused, not cut off: half a feed loaded as if it were the whole is
// a list that silently stops protecting.
func fetch(ctx context.Context, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "just-dashboard-blocklist")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096)) // drained so the connection can be reused
		return nil, resp.StatusCode, fmt.Errorf("%s answered %s", url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedBytes+1))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("reading %s: %w", url, err)
	}
	if len(body) > maxFeedBytes {
		return nil, resp.StatusCode, fmt.Errorf("%s is larger than %d MB; a list that size is not a blocklist this host should load", url, maxFeedBytes>>20)
	}
	return body, resp.StatusCode, nil
}

// fetchList gets the networks of a country or feed list, merged. A country
// has an IPv4 zone and usually an IPv6 one; a country with no IPv6 zone is a
// 404 there, which is information and not a failure.
func fetchList(ctx context.Context, kind string, countries []string, url string) ([]netip.Prefix, error) {
	var all []netip.Prefix
	switch kind {
	case "feed":
		body, _, err := fetch(ctx, url)
		if err != nil {
			return nil, err
		}
		all, _ = parseFeed(body)
	case "country":
		for _, cc := range countries {
			body, code, err := fetch(ctx, fmt.Sprintf(ipdenyV4, cc))
			if err != nil {
				if code == http.StatusNotFound {
					return nil, fmt.Errorf("there is no address list for the country %q", strings.ToUpper(cc))
				}
				return nil, err
			}
			nets, _ := parseFeed(body)
			all = append(all, nets...)
			body, code, err = fetch(ctx, fmt.Sprintf(ipdenyV6, cc))
			switch {
			case err == nil:
				nets, _ := parseFeed(body)
				all = append(all, nets...)
			case code == http.StatusNotFound:
				// No IPv6 zone for this country.
			default:
				return nil, err
			}
		}
	}
	merged := mergePrefixes(all)
	if len(merged) == 0 {
		return nil, errors.New("the list held no networks this host could use")
	}
	if len(merged) > maxListEntries {
		return nil, fmt.Errorf("the list holds %d networks; more than %d is too many to load as one set", len(merged), maxListEntries)
	}
	return merged, nil
}

// writeBlocklistCache stores a fetched list, one network per line.
func writeBlocklistCache(dir string, id int, nets []netip.Prefix) error {
	var b bytes.Buffer
	for _, p := range nets {
		b.WriteString(p.String())
		b.WriteByte('\n')
	}
	return writeFileAtomic(blocklistFile(dir, id), b.Bytes(), 0o644)
}

// BlocklistRequest is the body of a list's create and update.
type BlocklistRequest struct {
	Name string `json:"name"`
	// Kind is manual, country or feed; fixed once the list exists.
	Kind string `json:"kind"`
	// Entries are a manual list's networks or addresses.
	Entries []string `json:"entries"`
	// Countries are ISO 3166-1 alpha-2 codes.
	Countries []string `json:"countries"`
	// URL is a feed's address; Preset names one of the offered feeds
	// instead.
	URL     string `json:"url"`
	Preset  string `json:"preset"`
	Enabled *bool  `json:"enabled"`
}

// buildBlocklist validates a request into the fields of a spec entry. It
// reads nothing from the host; fetching is the caller's.
func buildBlocklist(req BlocklistRequest, kind, client string) (BlocklistSpec, error) {
	bl := BlocklistSpec{Kind: kind}
	if req.Kind != "" && req.Kind != kind {
		return bl, fmt.Errorf("a %s list cannot become a %s list; make a new one", kind, req.Kind)
	}
	switch kind {
	case "manual":
		if len(req.Entries) == 0 {
			return bl, errors.New("a manual list needs at least one network or address")
		}
		if len(req.Entries) > maxManualList {
			return bl, fmt.Errorf("a manual list holds at most %d entries; use a feed for more", maxManualList)
		}
		var nets []netip.Prefix
		for _, raw := range req.Entries {
			p, err := ParsePrefix(raw)
			if err != nil {
				return bl, err
			}
			if p.Bits() == 0 {
				return bl, fmt.Errorf("%s is every address on the internet; a list that drops it drops everything", p)
			}
			nets = append(nets, p.Masked())
		}
		nets = mergePrefixes(nets)
		if addr, err := ParseAddr(client); err == nil {
			for _, p := range nets {
				if p.Contains(addr) {
					return bl, guarded("%s contains your own address (%s), so this list would cut you off from the dashboard. Take it out of the list.", p, addr)
				}
			}
		}
		for _, p := range nets {
			bl.Entries = append(bl.Entries, p.String())
		}
		bl.Count = len(nets)
	case "country":
		if len(req.Countries) == 0 {
			return bl, errors.New("choose at least one country")
		}
		seen := map[string]bool{}
		for _, raw := range req.Countries {
			cc, err := ValidCountry(raw)
			if err != nil {
				return bl, err
			}
			if !seen[cc] {
				seen[cc] = true
				bl.Countries = append(bl.Countries, cc)
			}
		}
		if len(bl.Countries) > maxCountries {
			return bl, fmt.Errorf("a list covers at most %d countries", maxCountries)
		}
		sort.Strings(bl.Countries)
	case "feed":
		url := req.URL
		if req.Preset != "" {
			found := false
			for _, p := range feedPresets {
				if p.ID == req.Preset {
					url, found = p.URL, true
					if strings.TrimSpace(req.Name) == "" {
						req.Name = p.Name
					}
				}
			}
			if !found {
				return bl, fmt.Errorf("%q is not a feed this dashboard offers", req.Preset)
			}
		}
		u, err := ParseFeedURL(url)
		if err != nil {
			return bl, err
		}
		bl.URL = u
	default:
		return bl, errors.New("a list is manual, a country or a feed")
	}
	name, err := CleanLabel(req.Name, 64)
	if err != nil {
		return bl, err
	}
	bl.Name = name
	bl.Enabled = true
	return bl, nil
}

// AddBlocklist creates a list. A country or feed list is fetched first and
// refused if the fetch fails: a list saved empty would look like protection
// and be none.
func (s *Service) AddBlocklist(ctx context.Context, req BlocklistRequest, client, actor string) (BlocklistView, error) {
	bl, err := buildBlocklist(req, strings.ToLower(strings.TrimSpace(req.Kind)), client)
	if err != nil {
		return BlocklistView{}, err
	}
	if req.Enabled != nil {
		bl.Enabled = *req.Enabled
	}
	var fetched []netip.Prefix
	if bl.Kind != "manual" {
		// Outside the lock: a country is up to sixty requests, and holding
		// every other change to the network for that long is not a price
		// anyone should pay for a list that has not been saved yet.
		if fetched, err = fetchList(ctx, bl.Kind, bl.Countries, bl.URL); err != nil {
			return BlocklistView{}, err
		}
		bl.Count, bl.Refreshed = len(fetched), time.Now().UTC()
	}
	var wrote int
	err = s.mutateGateway(ctx, func(old, next *Spec) (bool, error) {
		s.trustClient(next, client)
		bl.ID, bl.Made = next.takeID(), gwStamp(actor)
		if fetched != nil {
			if err := writeBlocklistCache(filepath.Join(s.paths.Dir, "lists"), bl.ID, fetched); err != nil {
				return false, fmt.Errorf("saving the list: %w", err)
			}
			wrote = bl.ID
		}
		next.Blocklists = append(next.Blocklists, bl)
		return false, nil
	})
	if err != nil {
		if wrote != 0 {
			_ = os.Remove(blocklistFile(filepath.Join(s.paths.Dir, "lists"), wrote)) // the list was never saved
		}
		return BlocklistView{}, err
	}
	return s.blocklistViewByID(ctx, bl.ID, client)
}

// UpdateBlocklist changes a list: its name, whether it is enabled, and its
// contents — the entries of a manual list, the countries or address of a
// fetched one, which are fetched again.
func (s *Service) UpdateBlocklist(ctx context.Context, id int, req BlocklistRequest, client, actor string) (BlocklistView, error) {
	cur, err := s.loadSpec()
	if err != nil {
		return BlocklistView{}, err
	}
	var existing *BlocklistSpec
	for i := range cur.Blocklists {
		if cur.Blocklists[i].ID == id {
			existing = &cur.Blocklists[i]
		}
	}
	if existing == nil {
		return BlocklistView{}, fmt.Errorf("blocklist %d: %w", id, ErrNotFound)
	}
	upd, err := buildBlocklist(req, existing.Kind, client)
	if err != nil {
		return BlocklistView{}, err
	}
	var fetched []netip.Prefix
	refetch := upd.Kind != "manual" && (!sameStrings(upd.Countries, existing.Countries) || upd.URL != existing.URL)
	if refetch {
		if fetched, err = fetchList(ctx, upd.Kind, upd.Countries, upd.URL); err != nil {
			return BlocklistView{}, err
		}
	}
	dir := filepath.Join(s.paths.Dir, "lists")
	var prev []byte
	var cached bool
	err = s.mutateGateway(ctx, func(old, next *Spec) (bool, error) {
		s.trustClient(next, client)
		for i := range next.Blocklists {
			bl := &next.Blocklists[i]
			if bl.ID != id {
				continue
			}
			bl.Name = upd.Name
			if req.Enabled != nil {
				bl.Enabled = *req.Enabled
			}
			switch bl.Kind {
			case "manual":
				bl.Entries, bl.Count = upd.Entries, upd.Count
			default:
				bl.Countries, bl.URL = upd.Countries, upd.URL
				if fetched != nil {
					prev, _ = os.ReadFile(blocklistFile(dir, id))
					cached = true
					if err := writeBlocklistCache(dir, id, fetched); err != nil {
						return false, fmt.Errorf("saving the list: %w", err)
					}
					bl.Count, bl.Refreshed, bl.Error = len(fetched), time.Now().UTC(), ""
				}
			}
			return false, nil
		}
		return false, fmt.Errorf("blocklist %d: %w", id, ErrNotFound)
	})
	if err != nil {
		if cached {
			restoreCache(dir, id, prev)
		}
		return BlocklistView{}, err
	}
	return s.blocklistViewByID(ctx, id, client)
}

// restoreCache puts a list's previous cache back after a failed change.
func restoreCache(dir string, id int, prev []byte) {
	if prev == nil {
		_ = os.Remove(blocklistFile(dir, id)) // there was no cache before
		return
	}
	_ = writeFileAtomic(blocklistFile(dir, id), prev, 0o644) // best effort on the way out of a failure
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// DeleteBlocklist removes a list and its cache.
func (s *Service) DeleteBlocklist(ctx context.Context, id int, client string) error {
	err := s.mutateGateway(ctx, func(old, next *Spec) (bool, error) {
		for i, bl := range next.Blocklists {
			if bl.ID == id {
				next.Blocklists = append(next.Blocklists[:i], next.Blocklists[i+1:]...)
				s.trustClient(next, client)
				return false, nil
			}
		}
		return false, fmt.Errorf("blocklist %d: %w", id, ErrNotFound)
	})
	if err == nil {
		_ = os.Remove(blocklistFile(filepath.Join(s.paths.Dir, "lists"), id)) // a cache of a list that no longer exists
	}
	return err
}

// RefreshBlocklist fetches a fetched list again and reloads the table with
// it. A failure keeps the list as it was, and says why on the list.
func (s *Service) RefreshBlocklist(ctx context.Context, id int) error {
	cur, err := s.loadSpec()
	if err != nil {
		return err
	}
	var kind, url string
	var countries []string
	found := false
	for _, bl := range cur.Blocklists {
		if bl.ID == id {
			kind, url, countries, found = bl.Kind, bl.URL, bl.Countries, true
		}
	}
	switch {
	case !found:
		return fmt.Errorf("blocklist %d: %w", id, ErrNotFound)
	case kind == "manual":
		return errors.New("a manual list is what you typed; there is nothing to fetch")
	}
	fetched, err := fetchList(ctx, kind, countries, url)
	if err != nil {
		s.recordBlocklistError(ctx, id, err)
		return err
	}
	dir := filepath.Join(s.paths.Dir, "lists")
	var prev []byte
	err = s.mutateGateway(ctx, func(old, next *Spec) (bool, error) {
		for i := range next.Blocklists {
			bl := &next.Blocklists[i]
			if bl.ID != id {
				continue
			}
			prev, _ = os.ReadFile(blocklistFile(dir, id))
			if err := writeBlocklistCache(dir, id, fetched); err != nil {
				return false, fmt.Errorf("saving the list: %w", err)
			}
			bl.Count, bl.Refreshed, bl.Error = len(fetched), time.Now().UTC(), ""
			return false, nil
		}
		return false, fmt.Errorf("blocklist %d: %w", id, ErrNotFound)
	})
	if err != nil {
		restoreCache(dir, id, prev)
		s.recordBlocklistError(ctx, id, err)
	}
	return err
}

// recordBlocklistError writes why a refresh failed onto the list, so the page
// shows it where the list is. The list itself — its cache and what is loaded
// — is untouched.
func (s *Service) recordBlocklistError(ctx context.Context, id int, cause error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, err := s.loadSpec()
	if err != nil {
		return
	}
	next := sp.clone()
	for i := range next.Blocklists {
		if next.Blocklists[i].ID == id {
			msg := cause.Error()
			if len(msg) > 300 {
				msg = msg[:300] + "…"
			}
			next.Blocklists[i].Error = msg
			if err := s.commit(ctx, next, step{}); err != nil {
				s.log.Error("recording a blocklist refresh failure", "list", id, "err", err)
			}
			return
		}
	}
}

// StartBlocklistRefresh refreshes every enabled fetched list that is more than
// a day old, checking hourly, for as long as ctx lives. The first check is
// a minute after start, so a host that was down for days catches up without
// the dashboard's own start-up waiting on the network.
func (s *Service) StartBlocklistRefresh(ctx context.Context) {
	go s.refreshLoop(ctx, time.Minute, blocklistRefreshInterval)
}

// refreshLoop checks after first and then every interval, and returns when ctx
// does.
func (s *Service) refreshLoop(ctx context.Context, first, every time.Duration) {
	timer := time.NewTimer(first)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			s.refreshStale(ctx, time.Now())
			timer.Reset(every)
		}
	}
}

// refreshStale refreshes each enabled fetched list not refreshed within the
// last day, one at a time. A list that fails stays stale, so the next hour
// tries it again.
func (s *Service) refreshStale(ctx context.Context, now time.Time) {
	sp, err := s.loadSpec()
	if err != nil {
		s.log.Error("reading the spec to refresh blocklists", "err", err)
		return
	}
	for _, bl := range sp.Blocklists {
		if ctx.Err() != nil {
			return
		}
		if !bl.Enabled || bl.Kind == "manual" {
			continue
		}
		if !bl.Refreshed.IsZero() && now.Sub(bl.Refreshed) < blocklistStaleAfter {
			continue
		}
		fetchCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		err := s.RefreshBlocklist(fetchCtx, bl.ID)
		cancel()
		if err != nil {
			s.log.Warn("blocklist refresh failed", "list", bl.ID, "name", bl.Name, "err", err)
		}
	}
}

// Blocklist reads one list as the page shows it.
func (s *Service) Blocklist(ctx context.Context, id int, client string) (BlocklistView, error) {
	return s.blocklistViewByID(ctx, id, client)
}
