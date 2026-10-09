package dnsservice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
)

type DomainFilterChange struct {
	Domain      string `json:"domain"`
	Disposition string `json:"disposition"`
	Match       string `json:"match"`
	Groups      *[]int `json:"groups,omitempty"`
}

func (f *DomainFilterChange) UnmarshalJSON(body []byte) error {
	var raw struct {
		Domain      string          `json:"domain"`
		Disposition string          `json:"disposition"`
		Match       string          `json:"match"`
		Groups      json.RawMessage `json:"groups"`
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(&raw); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("domain filter intent must be one JSON object")
	}
	*f = DomainFilterChange{Domain: raw.Domain, Disposition: raw.Disposition, Match: raw.Match}
	if raw.Groups != nil {
		groups, ok := domainFilterGroups(raw.Groups)
		if !ok {
			return errors.New("domain filter groups must be explicit bounded integer IDs")
		}
		f.Groups = &groups
	}
	return nil
}

func domainFilterGroups(raw json.RawMessage) ([]int, bool) {
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil || values == nil || len(values) > 64 {
		return nil, false
	}
	groups := make([]int, 0, len(values))
	for _, value := range values {
		var id *int
		if json.Unmarshal(value, &id) != nil || id == nil {
			return nil, false
		}
		groups = append(groups, *id)
	}
	return groups, filterGroups(groups)
}

type DomainFilterPolicy struct {
	Domain                 string  `json:"domain"`
	Disposition            string  `json:"disposition"`
	Match                  string  `json:"match"`
	Present                bool    `json:"present"`
	Enabled                *bool   `json:"enabled,omitempty"`
	Groups                 *[]int  `json:"groups,omitempty"`
	Comment                *string `json:"comment"`
	CommentReported        bool    `json:"commentReported"`
	RuleFingerprint        string  `json:"ruleFingerprint,omitempty"`
	OtherPolicyFingerprint string  `json:"otherPolicyFingerprint"`
	Evidence               Reading `json:"evidence"`
	Owners                 int     `json:"owners"`
	Exact                  int     `json:"exact"`
	InventoryCount         int     `json:"inventoryCount"`
}

func domainFilterAction(action string) bool {
	return action == "filter_add" || action == "filter_remove"
}

func validateDomainFilterChange(req ChangeRequest, engine Engine) error {
	if !domainFilterAction(req.Action) || req.Filter == nil || req.Protection != nil || req.Upstreams != nil || req.AllowedClients != nil || req.DeniedClients != nil || req.Zone != "" || req.Record != nil || req.Client != nil {
		return errors.New("domain filter changes take only one closed filter intent")
	}
	f := req.Filter
	if !validZone(f.Domain) || (f.Disposition != "allow" && f.Disposition != "deny") {
		return errors.New("domain filters require a complete lower-case DNS name and allow or deny disposition")
	}
	if engine == AdGuard && f.Match == "suffix" && f.Groups == nil {
		return nil
	}
	if engine != PiHole || f.Match != "exact" || (req.Action == "filter_remove" && f.Groups != nil) || (req.Action == "filter_add" && (f.Groups == nil || !filterGroups(*f.Groups))) {
		return errors.New("AdGuard domain-suffix filters have no groups; Pi-hole exact additions require explicit bounded existing group IDs and removals have no replacement fields")
	}
	return nil
}

func adGuardDomainRule(f DomainFilterChange) string {
	prefix := "||"
	if f.Disposition == "allow" {
		prefix = "@@||"
	}
	return prefix + f.Domain + "^"
}

// Raw native rule text stays private. The retained review keeps selected and
// unselected fingerprints; replacement always uses the freshly compared text.
type domainFilterInventory struct {
	Native  map[string]json.RawMessage
	Rules   []string
	Rows    []map[string]json.RawMessage
	Sources []map[string]json.RawMessage
	Groups  []map[string]json.RawMessage
}

func (c *nativeClient) readDomainFilters(ctx context.Context, version string) (domainFilterInventory, error) {
	inv := domainFilterInventory{}
	// A native restart or upgrade between the ordinary snapshot and selected
	// policy read must not borrow that snapshot's response-writer assumptions.
	var currentVersion string
	if c.engine == AdGuard {
		var status struct {
			Version string `json:"version"`
		}
		if err := c.request(ctx, http.MethodGet, "/control/status", nil, &status); err != nil {
			return inv, err
		}
		currentVersion = status.Version
	} else if c.engine == PiHole {
		var status struct {
			Version struct {
				FTL struct {
					Local struct {
						Version string `json:"version"`
					} `json:"local"`
				} `json:"ftl"`
			} `json:"version"`
		}
		if err := c.request(ctx, http.MethodGet, "/api/info/version", nil, &status); err != nil {
			return inv, err
		}
		currentVersion = status.Version.FTL.Local.Version
	}
	if currentVersion == "" || currentVersion != version {
		return inv, errors.New("native domain-filter version changed or is unreadable")
	}
	if c.engine == AdGuard {
		if !versionSupported(version, "0.107.") {
			return inv, errors.New("unsupported native domain-filter version")
		}
		if err := c.request(ctx, http.MethodGet, "/control/filtering/status", nil, &inv.Native); err != nil {
			return inv, err
		}
		var enabled *bool
		var interval *uint32
		if !knownPolicyFields(inv.Native, "enabled interval filters whitelist_filters user_rules") || json.Unmarshal(inv.Native["enabled"], &enabled) != nil || enabled == nil || json.Unmarshal(inv.Native["interval"], &interval) != nil || interval == nil || c.adGuardFilterSources(inv.Native, version).Evidence.State != "configured" {
			return inv, errors.New("complete supported native filter settings are required")
		}
		var valid bool
		inv.Rules, valid = adGuardRuleStrings(inv.Native["user_rules"], version)
		if !valid {
			return inv, errors.New("complete bounded native custom rules are required")
		}
		for _, rule := range inv.Rules {
			if len(rule) > 4096 || strings.ContainsAny(rule, "\x00\r\n") {
				return inv, errors.New("native custom rule text is outside the replacement contract")
			}
		}
		return inv, nil
	}
	if c.engine != PiHole || !versionSupported(version, "6.") {
		return inv, errors.New("unsupported native domain-filter version")
	}
	var response struct {
		Domains []map[string]json.RawMessage `json:"domains"`
	}
	if err := c.request(ctx, http.MethodGet, "/api/domains", nil, &response); err != nil {
		return inv, err
	}
	inv.Rows = response.Domains
	var sources struct {
		Lists []map[string]json.RawMessage `json:"lists"`
	}
	if err := c.request(ctx, http.MethodGet, "/api/lists", nil, &sources); err != nil {
		return inv, err
	}
	inv.Sources = sources.Lists
	var groups struct {
		Groups []map[string]json.RawMessage `json:"groups"`
	}
	if err := c.request(ctx, http.MethodGet, "/api/groups", nil, &groups); err != nil {
		return inv, err
	}
	inv.Groups = groups.Groups
	if inv.Rows == nil || len(inv.Rows) > maxFilterEntries || inv.Groups == nil || len(inv.Groups) > 128 || inv.Sources == nil || len(inv.Sources) > maxFilterEntries {
		return inv, errors.New("complete bounded native domain rules and groups are required")
	}
	sourceIDs := map[int64]bool{}
	for _, row := range inv.Sources {
		var source struct {
			ID      *int64  `json:"id"`
			Type    string  `json:"type"`
			Address *string `json:"address"`
			Enabled *bool   `json:"enabled"`
			Count   *int64  `json:"number"`
			Updated *int64  `json:"date_updated"`
			Status  *int64  `json:"status"`
		}
		body, _ := json.Marshal(row)
		_, validGroups := domainFilterGroups(row["groups"])
		if json.Unmarshal(body, &source) != nil || !filterNumber(source.ID) || sourceIDs[*source.ID] || source.Address == nil || *source.Address == "" || len(*source.Address) > 4096 || source.Enabled == nil || !validGroups || (source.Type != "allow" && source.Type != "block") || (source.Count != nil && !filterNumber(source.Count)) || (source.Updated != nil && !filterNumber(source.Updated)) || (source.Status != nil && !filterNumber(source.Status)) {
			return inv, errors.New("native subscription metadata is incomplete or outside its bounds")
		}
		sourceIDs[*source.ID] = true
	}
	ids := map[int64]bool{}
	for _, row := range inv.Rows {
		var r struct {
			Domain   string  `json:"domain"`
			Type     string  `json:"type"`
			Kind     string  `json:"kind"`
			ID       *int64  `json:"id"`
			Enabled  *bool   `json:"enabled"`
			Comment  *string `json:"comment"`
			Added    *int64  `json:"date_added"`
			Modified *int64  `json:"date_modified"`
		}
		body, _ := json.Marshal(row)
		_, validGroups := domainFilterGroups(row["groups"])
		if json.Unmarshal(body, &r) != nil || !filterNumber(r.ID) || !filterNumber(r.Added) || !filterNumber(r.Modified) || ids[*r.ID] || r.Domain == "" || len(r.Domain) > 4096 || (r.Type != "allow" && r.Type != "deny") || (r.Kind != "exact" && r.Kind != "regex") || r.Enabled == nil || !validGroups || row["comment"] == nil || r.Comment != nil && len(*r.Comment) > 4096 {
			return inv, errors.New("native domain rule identity or metadata is unreadable")
		}
		ids[*r.ID] = true
	}
	groupIDs := map[int]bool{}
	for _, row := range inv.Groups {
		var id *int
		var enabled *bool
		var name *string
		if json.Unmarshal(row["id"], &id) != nil || id == nil || *id < 0 || *id > 2147483647 || groupIDs[*id] || json.Unmarshal(row["enabled"], &enabled) != nil || enabled == nil || json.Unmarshal(row["name"], &name) != nil || name == nil || *name == "" || len(*name) > 512 {
			return inv, errors.New("native filter group identity or policy is unreadable")
		}
		groupIDs[*id] = true
	}
	return inv, nil
}

func (inv domainFilterInventory) selection(engine Engine, f DomainFilterChange) (*DomainFilterPolicy, error) {
	out := &DomainFilterPolicy{Domain: f.Domain, Disposition: f.Disposition, Match: f.Match, Evidence: Reading{"configured", "native_configuration", "Native custom-domain rule configuration; filtering priority, loaded rules and client decisions remain separate."}}
	if engine == AdGuard {
		out.InventoryCount = len(inv.Rules)
		wanted := adGuardDomainRule(f)
		for _, rule := range inv.Rules {
			normal := strings.ToLower(strings.TrimSpace(rule))
			anchor := strings.TrimPrefix(normal, "@@")
			prefix := "||" + f.Domain
			if normal == f.Domain || (strings.HasPrefix(anchor, prefix) && (len(anchor) == len(prefix) || strings.ContainsRune("^$|/:*", rune(anchor[len(prefix)])))) {
				out.Owners++
			}
			if rule == wanted {
				out.Exact++
				out.RuleFingerprint = policyHash(rule)
			}
		}
	} else {
		out.InventoryCount = len(inv.Rows)
		for _, row := range inv.Rows {
			var domain, disposition, kind string
			json.Unmarshal(row["domain"], &domain)
			json.Unmarshal(row["type"], &disposition)
			json.Unmarshal(row["kind"], &kind)
			if kind != "exact" || !strings.EqualFold(strings.TrimSuffix(domain, "."), f.Domain) {
				continue
			}
			out.Owners++
			if domain != f.Domain || disposition != f.Disposition {
				continue
			}
			if !knownPolicyFields(row, "domain unicode type kind comment groups enabled id date_added date_modified") {
				return nil, errors.New("selected native domain rule has unsupported policy fields")
			}
			var enabled bool
			var groups []int
			json.Unmarshal(row["enabled"], &enabled)
			json.Unmarshal(row["groups"], &groups)
			json.Unmarshal(row["comment"], &out.Comment)
			out.Enabled, out.Groups, out.CommentReported = &enabled, &groups, true
			out.Exact++
			out.RuleFingerprint = policyHash(row)
		}
		if f.Groups != nil {
			for _, id := range *f.Groups {
				found := false
				for _, row := range inv.Groups {
					var nativeID int
					json.Unmarshal(row["id"], &nativeID)
					found = found || nativeID == id
				}
				if !found {
					return nil, errors.New("all new rule group IDs must already exist")
				}
			}
		}
	}
	out.Present = out.Exact == 1
	out.OtherPolicyFingerprint = inv.otherFingerprint(engine, f)
	return out, nil
}

func (inv domainFilterInventory) otherFingerprint(engine Engine, f DomainFilterChange) string {
	if engine == AdGuard {
		rules := []string{}
		for _, rule := range inv.Rules {
			if rule != adGuardDomainRule(f) {
				rules = append(rules, rule)
			}
		}
		native := map[string]json.RawMessage{}
		for key, value := range inv.Native {
			if key != "user_rules" {
				native[key] = value
			}
		}
		return policyHash([]any{native, rules})
	}
	rows := []map[string]json.RawMessage{}
	for _, row := range inv.Rows {
		var domain, disposition, kind string
		json.Unmarshal(row["domain"], &domain)
		json.Unmarshal(row["type"], &disposition)
		json.Unmarshal(row["kind"], &kind)
		if domain != f.Domain || disposition != f.Disposition || kind != "exact" {
			rows = append(rows, row)
		}
	}
	return policyHash([]any{rows, inv.Groups, inv.Sources})
}

func (c *nativeClient) inspectDomainFilterSelection(ctx context.Context, s *Snapshot, req ChangeRequest) error {
	if err := validateDomainFilterChange(req, c.engine); err != nil {
		return err
	}
	inv, err := c.readDomainFilters(ctx, s.Version)
	if err != nil {
		return err
	}
	s.SelectedFilter, err = inv.selection(c.engine, *req.Filter)
	if err != nil {
		return err
	}
	s.SelectedFilter.OtherPolicyFingerprint = policyHash([]string{c.fingerprint(), s.SelectedFilter.OtherPolicyFingerprint})
	if s.SelectedFilter.Comment != nil {
		redacted := c.text(*s.SelectedFilter.Comment)
		s.SelectedFilter.Comment = &redacted
	}
	s.SelectionFingerprint = policyHash(inv)
	return nil
}

func validateDomainFilterBaseline(req ChangeRequest, s *Snapshot) error {
	if s == nil || s.SelectedFilter == nil || s.SelectionFingerprint == "" || req.Filter == nil {
		return errors.New("complete retained native domain-filter selection is required")
	}
	f := s.SelectedFilter
	if f.Evidence.State != "configured" || f.Domain != req.Filter.Domain || f.Disposition != req.Filter.Disposition || f.Match != req.Filter.Match || f.OtherPolicyFingerprint == "" {
		return errors.New("native domain-filter selection does not match the reviewed scope")
	}
	if req.Action == "filter_add" && (f.Owners != 0 || f.Exact != 0 || f.Present) {
		return errors.New("a native rule already names this domain; no duplicate or opposite policy was staged")
	}
	if req.Action == "filter_add" && f.InventoryCount >= maxFilterEntries {
		return errors.New("native custom-domain rule inventory is full")
	}
	if req.Action == "filter_remove" && (f.Owners != 1 || f.Exact != 1 || !f.Present || f.RuleFingerprint == "") {
		return errors.New("removal requires one exact supported native domain rule")
	}
	if req.Action == "filter_remove" && s.Engine == PiHole && (f.Enabled == nil || !*f.Enabled) {
		return errors.New("disabled native domain rules must be reviewed in the native console")
	}
	return nil
}

func applyDomainFilterNative(ctx context.Context, c *nativeClient, req ChangeRequest, before *Snapshot) error {
	if err := validateDomainFilterBaseline(req, before); err != nil {
		return err
	}
	inv, err := c.readDomainFilters(ctx, before.Version)
	if err != nil {
		return ErrConflict
	}
	if policyHash(inv) != before.SelectionFingerprint {
		return ErrConflict
	}
	selected, err := inv.selection(c.engine, *req.Filter)
	if err != nil {
		return err
	}
	if selected.Exact != before.SelectedFilter.Exact || selected.Owners != before.SelectedFilter.Owners {
		return ErrConflict
	}
	if c.engine == AdGuard {
		rules := []string{}
		for _, rule := range inv.Rules {
			if req.Action != "filter_remove" || rule != adGuardDomainRule(*req.Filter) {
				rules = append(rules, rule)
			}
		}
		if req.Action == "filter_add" {
			if len(rules) >= maxFilterEntries {
				return errors.New("native custom-rule inventory is full")
			}
			rules = append(rules, adGuardDomainRule(*req.Filter))
		}
		return c.request(ctx, http.MethodPost, "/control/filtering/set_rules", map[string]any{"rules": rules}, nil)
	}
	path := "/api/domains/" + req.Filter.Disposition + "/exact"
	if req.Action == "filter_remove" {
		return c.request(ctx, http.MethodDelete, path+"/"+url.PathEscape(req.Filter.Domain), nil, nil)
	}
	if len(inv.Rows) >= maxFilterEntries {
		return errors.New("native domain-rule inventory is full")
	}
	return c.request(ctx, http.MethodPost, path, map[string]any{"domain": req.Filter.Domain, "enabled": true, "groups": *req.Filter.Groups, "comment": nil}, nil)
}

func domainFilterMatches(req ChangeRequest, s *Snapshot) bool {
	if s == nil || s.SelectedFilter == nil || req.Filter == nil {
		return false
	}
	f := s.SelectedFilter
	if req.Action == "filter_remove" {
		return !f.Present && f.Exact == 0 && f.Owners == 0
	}
	if !f.Present || f.Exact != 1 || f.Owners != 1 {
		return false
	}
	if s.Engine == AdGuard {
		return true
	}
	if f.Enabled == nil || !*f.Enabled || f.Groups == nil || req.Filter.Groups == nil || !f.CommentReported || f.Comment != nil {
		return false
	}
	wanted, actual := append([]int{}, (*req.Filter.Groups)...), append([]int{}, (*f.Groups)...)
	sort.Ints(wanted)
	sort.Ints(actual)
	return reflect.DeepEqual(wanted, actual)
}
