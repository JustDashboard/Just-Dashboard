package dnsservice

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxFilterEntries = 256

var errFilterVersion = errors.New("unsupported native filter version")

// Metadata stays separate from rule text and subscription destinations. A
// native count or update status is not proof of a client's filtering decision.
type FilterEntry struct {
	ID          *int64 `json:"id,omitempty"`
	Kind        string `json:"kind"`
	Origin      string `json:"origin,omitempty"`
	Enabled     *bool  `json:"enabled,omitempty"`
	Groups      *[]int `json:"groups,omitempty"`
	RuleCount   *int64 `json:"ruleCount,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
	Status      *int64 `json:"status,omitempty"`
	Fingerprint string `json:"fingerprint"`
}

type FilterSection struct {
	Evidence    Reading       `json:"evidence"`
	Entries     []FilterEntry `json:"entries"`
	Fingerprint string        `json:"fingerprint,omitempty"`
	Identity    Reading       `json:"identity"`
}

type FilterInventory struct {
	Engine        Engine        `json:"engine"`
	NativeVersion string        `json:"nativeVersion"`
	ObservedAt    time.Time     `json:"observedAt"`
	Transport     Reading       `json:"transport"`
	Runtime       Reading       `json:"runtime"`
	Protection    *bool         `json:"protection,omitempty"`
	Filtering     *bool         `json:"filtering,omitempty"`
	Sources       FilterSection `json:"sources"`
	Rules         FilterSection `json:"rules"`
	AppRules      Reading       `json:"appRules"`
	Fingerprint   string        `json:"fingerprint,omitempty"`
	Limitations   []string      `json:"limitations"`
}

type FilterView struct {
	Connection Connection       `json:"connection"`
	Inventory  *FilterInventory `json:"inventory,omitempty"`
	State      string           `json:"state"`
	Error      string           `json:"error,omitempty"`
}

func unknownFilters() FilterSection {
	return FilterSection{Evidence: Reading{"unknown", "unavailable", "Native filter metadata is unavailable or does not match its bounded contract."}, Entries: []FilterEntry{}, Identity: Reading{"redacted", "native_metadata", "Native IDs and fingerprints identify entries; source destinations, names, comments and rule contents are intentionally redacted."}}
}

// Only the scheme/host of an HTTP subscription is shown. Local paths and all
// URL userinfo, path, query and fragment data remain private, even on errors.
func (c *nativeClient) filterOrigin(value string) string {
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return ""
	}
	return c.text(u.Scheme + "://" + u.Host)
}

func filterGroups(groups []int) bool {
	if groups == nil || len(groups) > 64 {
		return false
	}
	seen := map[int]bool{}
	for _, id := range groups {
		if id < 0 || id > 2147483647 || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

// A null membership must not acquire group zero through integer decoding.
func filterRawGroups(raw json.RawMessage) ([]int, bool) {
	var entries []*int
	if json.Unmarshal(raw, &entries) != nil || entries == nil || len(entries) > 64 {
		return nil, false
	}
	groups := make([]int, 0, len(entries))
	for _, id := range entries {
		if id == nil {
			return nil, false
		}
		groups = append(groups, *id)
	}
	return groups, filterGroups(groups)
}

func filterNumber(value *int64) bool {
	return value != nil && *value >= 0 && *value <= 9007199254740991
}

func (s *Service) Filters(ctx context.Context, id string) (FilterView, error) {
	if err := s.ready(); err != nil {
		return FilterView{}, err
	}
	connection, req, err := s.connection(ctx, id)
	if err != nil {
		return FilterView{}, err
	}
	view := FilterView{Connection: connection, State: "unavailable"}
	view.Inventory, err = inspectNativeFilters(ctx, req)
	if ctx.Err() != nil {
		return FilterView{}, ctx.Err()
	}
	current, _, currentErr := s.connection(ctx, id)
	if currentErr != nil || current != connection {
		return FilterView{}, ErrConflict
	}
	if err != nil {
		view.Error = "Native filter inventory is unavailable; no filter operation was sent."
		if errors.Is(err, errFilterVersion) {
			view.State, view.Error = "unsupported", "The native engine version is outside the supported filter inventory contract."
		}
		return view, nil
	}
	view.State = "available"
	if view.Inventory.Sources.Evidence.State == "unknown" || view.Inventory.Rules.Evidence.State == "unknown" {
		view.State = "partial"
	}
	return view, nil
}

func inspectNativeFilters(ctx context.Context, req ConnectionRequest) (*FilterInventory, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c, err := newNativeClient(req)
	if err != nil {
		return nil, err
	}
	defer c.close()
	if err = c.login(ctx); err != nil {
		return nil, err
	}
	defer c.logout()
	i := &FilterInventory{Engine: req.Engine, ObservedAt: time.Now().UTC(), Sources: unknownFilters(), Rules: unknownFilters(),
		Runtime:     Reading{"unknown", "unmeasured", "Loaded filter contents, compilation and actual client filtering decisions are unmeasured."},
		AppRules:    Reading{"unsupported", "native_contract", "Installed app rule content is outside this built-in filter inventory; native client/view groups are inspected separately."},
		Limitations: []string{"Only bounded native metadata is read. No subscription URL is fetched or refreshed.", "Subscription origins omit credentials, paths, queries and fragments. Rule text, comments and local paths are fingerprint-only.", "Sequential management reads are not an atomic snapshot or a client DNS packet trace."}}
	switch req.Engine {
	case AdGuard:
		err = c.adGuardFilters(ctx, i)
	case PiHole:
		err = c.piHoleFilters(ctx, i)
	case Technitium:
		err = c.technitiumFilters(ctx, i)
	default:
		err = errors.New("unsupported native filter engine")
	}
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	i.Transport = c.transport
	if i.Sources.Fingerprint != "" && (i.Rules.Fingerprint != "" || i.Rules.Evidence.State == "unsupported") {
		i.Fingerprint = policyHash([]any{i.Engine, i.NativeVersion, i.Protection, i.Filtering, i.Sources.Fingerprint, i.Rules.Fingerprint})
	}
	encoded, _ := json.Marshal(i)
	if len(encoded) > 192<<10 {
		return nil, errors.New("native filter inventory exceeds its retained bound")
	}
	return i, nil
}

func (c *nativeClient) adGuardFilters(ctx context.Context, i *FilterInventory) error {
	var status struct {
		Version    string `json:"version"`
		Protection *bool  `json:"protection_enabled"`
		Running    *bool  `json:"running"`
	}
	if err := c.request(ctx, http.MethodGet, "/control/status", nil, &status); err != nil {
		return err
	}
	if status.Version != "" && !versionSupported(status.Version, "0.107.") {
		return errFilterVersion
	}
	if status.Version == "" || status.Protection == nil || status.Running == nil {
		return errors.New("unsupported AdGuard filter status")
	}
	i.NativeVersion, i.Protection = status.Version, status.Protection
	i.Runtime = Reading{"native_status", "native_status", "Authenticated status reports DNS running=" + strconv.FormatBool(*status.Running) + "; loaded rules and client decisions remain unmeasured."}
	var raw map[string]json.RawMessage
	if err := c.request(ctx, http.MethodGet, "/control/filtering/status", nil, &raw); err != nil {
		return nil
	}
	var enabled *bool
	if json.Unmarshal(raw["enabled"], &enabled) != nil || enabled == nil {
		return nil
	}
	i.Filtering = enabled
	i.Sources = c.adGuardFilterSources(raw, status.Version)
	values, ok := adGuardRuleStrings(raw["user_rules"], status.Version)
	if !ok {
		return nil
	}
	entries := []FilterEntry{}
	for _, rule := range values {
		entries = append(entries, FilterEntry{Kind: "native_rule", Fingerprint: policyHash(rule)})
	}
	i.Rules.Evidence, i.Rules.Entries, i.Rules.Fingerprint = Reading{"configured", "native_configuration", "Native custom-rule metadata; rule text and compilation are not exposed or evaluated."}, entries, policyHash(raw["user_rules"])
	return nil
}

// A null collection is a pinned empty-slice representation. A null element is
// malformed rule content, and must never become an invented empty string.
func adGuardRuleStrings(raw json.RawMessage, version string) ([]string, bool) {
	if strings.TrimPrefix(version, "v") == "0.107.71" && strings.TrimSpace(string(raw)) == "null" {
		return []string{}, true
	}
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil || entries == nil || len(entries) > maxFilterEntries {
		return nil, false
	}
	values := make([]string, 0, len(entries))
	for _, entry := range entries {
		var value *string
		if json.Unmarshal(entry, &value) != nil || value == nil || len(*value) > 4096 {
			return nil, false
		}
		values = append(values, *value)
	}
	return values, true
}

func (c *nativeClient) adGuardFilterSources(raw map[string]json.RawMessage, version string) FilterSection {
	unknown := unknownFilters()
	var filters, allow []json.RawMessage
	if json.Unmarshal(raw["filters"], &filters) != nil || json.Unmarshal(raw["whitelist_filters"], &allow) != nil {
		return unknown
	}
	// The exact pinned writer starts its subscription slices nil. Only its
	// explicitly present null fields mean zero items; missing stays unknown.
	if strings.TrimPrefix(version, "v") == "0.107.71" {
		if string(raw["filters"]) == "null" {
			filters = []json.RawMessage{}
		}
		if string(raw["whitelist_filters"]) == "null" {
			allow = []json.RawMessage{}
		}
	}
	if filters == nil || allow == nil || len(filters)+len(allow) > maxFilterEntries {
		return unknown
	}
	entries := []FilterEntry{}
	seen := map[int64]bool{}
	for index, list := range [][]json.RawMessage{filters, allow} {
		for _, entry := range list {
			var f struct {
				ID      *int64  `json:"id"`
				Enabled *bool   `json:"enabled"`
				Name    *string `json:"name"`
				URL     *string `json:"url"`
				Count   *int64  `json:"rules_count"`
				Updated string  `json:"last_updated"`
			}
			if json.Unmarshal(entry, &f) != nil || !filterNumber(f.ID) || f.Enabled == nil || f.Name == nil || f.URL == nil || len(*f.URL) == 0 || len(*f.URL) > 4096 || len(*f.Name) > 512 || !filterNumber(f.Count) || seen[*f.ID] || len(f.Updated) > 64 {
				return unknown
			}
			if f.Updated != "" {
				if _, err := time.Parse(time.RFC3339Nano, f.Updated); err != nil {
					return unknown
				}
			}
			seen[*f.ID] = true
			kind := "block_subscription"
			if index == 1 {
				kind = "allow_subscription"
			}
			entries = append(entries, FilterEntry{ID: f.ID, Kind: kind, Origin: c.filterOrigin(*f.URL), Enabled: f.Enabled, RuleCount: f.Count, UpdatedAt: c.text(f.Updated), Fingerprint: policyHash(entry)})
		}
	}
	section := unknownFilters()
	section.Evidence, section.Entries, section.Fingerprint = Reading{"configured", "native_configuration", "Native block/allow subscriptions and reported rule counts/update times; downloaded content is not inspected."}, entries, policyHash([]any{raw["enabled"], raw["interval"], raw["filters"], raw["whitelist_filters"]})
	return section
}

func (c *nativeClient) piHoleFilters(ctx context.Context, i *FilterInventory) error {
	var version struct {
		Version struct {
			FTL struct {
				Local struct {
					Version string `json:"version"`
				} `json:"local"`
			} `json:"ftl"`
		} `json:"version"`
	}
	if err := c.request(ctx, http.MethodGet, "/api/info/version", nil, &version); err != nil {
		return err
	}
	i.NativeVersion = version.Version.FTL.Local.Version
	if i.NativeVersion != "" && !versionSupported(i.NativeVersion, "6.") {
		return errFilterVersion
	}
	if i.NativeVersion == "" {
		return errors.New("unreadable native FTL filter version")
	}
	var blocking struct {
		Blocking string          `json:"blocking"`
		Timer    json.RawMessage `json:"timer"`
	}
	if err := c.request(ctx, http.MethodGet, "/api/dns/blocking", nil, &blocking); err == nil && (blocking.Blocking == "enabled" || blocking.Blocking == "disabled") {
		value := blocking.Blocking == "enabled"
		i.Protection = &value
		i.Runtime = Reading{"native_status", "native_api", "FTL reports blocking=" + blocking.Blocking + "; temporary state, loaded gravity contents and actual client decisions remain separate."}
	}
	for index, path := range []string{"/api/lists", "/api/domains"} {
		var response map[string]json.RawMessage
		if err := c.request(ctx, http.MethodGet, path, nil, &response); err != nil {
			continue
		}
		key := "lists"
		if index == 1 {
			key = "domains"
		}
		var list []json.RawMessage
		if json.Unmarshal(response[key], &list) != nil || list == nil || len(list) > maxFilterEntries {
			continue
		}
		entries := []FilterEntry{}
		seen := map[int64]bool{}
		valid := true
		for _, entry := range list {
			var f struct {
				ID      *int64          `json:"id"`
				Type    string          `json:"type"`
				Kind    string          `json:"kind"`
				Address *string         `json:"address"`
				Domain  *string         `json:"domain"`
				Enabled *bool           `json:"enabled"`
				Groups  json.RawMessage `json:"groups"`
				Count   *int64          `json:"number"`
				Updated *int64          `json:"date_updated"`
				Status  *int64          `json:"status"`
			}
			if json.Unmarshal(entry, &f) != nil {
				valid = false
				break
			}
			groups, groupsKnown := filterRawGroups(f.Groups)
			if !filterNumber(f.ID) || seen[*f.ID] || f.Enabled == nil || !groupsKnown {
				valid = false
				break
			}
			seen[*f.ID] = true
			e := FilterEntry{ID: f.ID, Enabled: f.Enabled, Groups: &groups, Fingerprint: policyHash(entry)}
			if index == 0 {
				if (f.Type != "block" && f.Type != "allow") || f.Address == nil || len(*f.Address) > 4096 || (f.Count != nil && !filterNumber(f.Count)) || (f.Updated != nil && !filterNumber(f.Updated)) || (f.Status != nil && !filterNumber(f.Status)) {
					valid = false
					break
				}
				e.Kind, e.Origin, e.RuleCount, e.Status = f.Type+"_subscription", c.filterOrigin(*f.Address), f.Count, f.Status
				if f.Updated != nil {
					e.UpdatedAt = strconv.FormatInt(*f.Updated, 10)
				}
			} else {
				if (f.Type != "deny" && f.Type != "allow") || (f.Kind != "exact" && f.Kind != "regex") || f.Domain == nil || len(*f.Domain) == 0 || len(*f.Domain) > 4096 {
					valid = false
					break
				}
				e.Kind = f.Type + "_" + f.Kind
			}
			entries = append(entries, e)
		}
		if !valid {
			continue
		}
		section := unknownFilters()
		section.Evidence, section.Entries, section.Fingerprint = Reading{"configured", "native_configuration", "Native FTL persistent filter metadata and exact group IDs; enabled memberships do not prove a filtering decision."}, entries, policyHash(response[key])
		if index == 0 {
			i.Sources = section
		} else {
			i.Rules = section
		}
	}
	return nil
}

func (c *nativeClient) technitiumFilters(ctx context.Context, i *FilterInventory) error {
	var raw map[string]json.RawMessage
	if err := c.technitium(ctx, "/api/settings/get", nil, &raw); err != nil {
		return err
	}
	var version string
	if json.Unmarshal(raw["version"], &version) != nil || version == "" {
		return errors.New("unreadable Technitium filter version")
	}
	if !versionSupported(version, "15.") {
		return errFilterVersion
	}
	i.NativeVersion = version
	i.Rules.Evidence = Reading{"unsupported", "native_contract", "Manual Allowed/Blocked zones are a separate native tree, not this subscription inventory. Their absence or contents are not inferred."}
	var enabled bool
	if json.Unmarshal(raw["enableBlocking"], &enabled) != nil || string(raw["enableBlocking"]) == "null" {
		return nil
	}
	i.Protection = &enabled
	// The pinned writer explicitly emits null for zero configured URLs. A missing
	// field is not equivalent; only the exact 15.6 writer has this null contract.
	urlsRaw, exists := raw["blockListUrls"]
	if !exists || (string(urlsRaw) == "null" && version != "15.6" && version != "15.6.0") {
		return nil
	}
	var urls []string
	if json.Unmarshal(urlsRaw, &urls) != nil || (urls == nil && string(urlsRaw) != "null") || len(urls) > 255 {
		return nil
	}
	var interval int64
	if json.Unmarshal(raw["blockListUpdateIntervalHours"], &interval) != nil || interval < 0 || interval > 168 || string(raw["blockListUpdateIntervalHours"]) == "null" {
		return nil
	}
	entries := []FilterEntry{}
	seen := map[string]bool{}
	for _, source := range urls {
		comment := strings.HasPrefix(strings.TrimSpace(source), "#")
		if source == "" || len(source) > 255 || (!comment && seen[source]) {
			return nil
		}
		seen[source] = true
		kind := "block_subscription"
		if strings.HasPrefix(source, "!") {
			kind = "allow_subscription"
		}
		if comment {
			kind = "subscription_comment"
		}
		entries = append(entries, FilterEntry{Kind: kind, Origin: c.filterOrigin(strings.TrimPrefix(source, "!")), Fingerprint: policyHash(source)})
	}
	i.Sources.Evidence, i.Sources.Entries, i.Sources.Fingerprint = Reading{"configured", "native_configuration", "Built-in native block/allow subscription metadata; schedule and blocking configuration do not establish loaded content or client decisions."}, entries, policyHash([]any{raw["enableBlocking"], raw["blockListUrls"], raw["blockListUpdateIntervalHours"], raw["blockingBypassList"], raw["blockingType"], raw["blockingAnswerTtl"], raw["customBlockingAddresses"]})
	return nil
}
