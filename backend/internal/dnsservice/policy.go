package dnsservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type RecordChange struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
	TTL   uint32 `json:"ttl,omitempty"`
}

type ClientGroupChange struct {
	Address string `json:"address"`
	Groups  []int  `json:"groups"`
}

type ClientGroupPolicy struct {
	Address                string  `json:"address"`
	Groups                 []int   `json:"groups"`
	Comment                *string `json:"comment"`
	CommentFingerprint     string  `json:"commentFingerprint"`
	OtherPolicyFingerprint string  `json:"otherPolicyFingerprint"`
}

type ZoneRecord struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Value       string `json:"value,omitempty"`
	TTL         uint32 `json:"ttl"`
	Disabled    bool   `json:"disabled"`
	Editable    bool   `json:"editable"`
	Comments    string `json:"comments,omitempty"`
	Fingerprint string `json:"fingerprint"`
}

type RecordInventory struct {
	Zone          string       `json:"zone"`
	Type          string       `json:"type"`
	Disabled      bool         `json:"disabled"`
	Internal      *bool        `json:"internal"`
	NativeVersion string       `json:"nativeVersion"`
	DNSSEC        string       `json:"dnssec"`
	Records       []ZoneRecord `json:"records"`
	Evidence      Reading      `json:"evidence"`
	Fingerprint   string       `json:"fingerprint"`
}

func policyAction(action string) bool {
	switch action {
	case "override_add", "override_remove", "record_add", "record_remove", "client_groups":
		return true
	}
	return false
}

func validatePolicyChange(req ChangeRequest, engine Engine) error {
	if !policyAction(req.Action) || req.Protection != nil || req.Upstreams != nil || req.AllowedClients != nil || req.DeniedClients != nil {
		return errors.New("native record and client changes cannot carry other policy fields")
	}
	if req.Action == "client_groups" {
		if engine != PiHole || req.Record != nil || req.Zone != "" || req.Client == nil || req.Client.Groups == nil || len(req.Client.Groups) > 64 || !literalClient(req.Client.Address) {
			return errors.New("Pi-hole group assignment requires an existing canonical address or prefix and an explicit bounded group list")
		}
		seen := map[int]bool{}
		for _, id := range req.Client.Groups {
			if id < 0 || id > 2147483647 || seen[id] {
				return errors.New("client group IDs must be unique nonnegative native IDs")
			}
			seen[id] = true
		}
		return nil
	}
	if req.Record == nil || req.Client != nil || !validZone(req.Record.Name) {
		return errors.New("record changes require a complete lower-case owner DNS name")
	}
	r := req.Record
	ip, err := netip.ParseAddr(r.Value)
	if err != nil || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() || ip.String() != r.Value || (r.Type != "A" && r.Type != "AAAA") || (r.Type == "A" && !ip.Is4()) || (r.Type == "AAAA" && (!ip.Is6() || ip.Is4In6())) {
		return errors.New("records accept only canonical unicast A or AAAA addresses of the declared family")
	}
	switch req.Action {
	case "override_add", "override_remove":
		if (engine != AdGuard && engine != PiHole) || req.Zone != "" || r.TTL != 0 {
			return errors.New("local overrides have no authoritative zone or caller-selected TTL")
		}
	case "record_add", "record_remove":
		if engine != Technitium || !validZone(req.Zone) || !withinZone(r.Name, req.Zone) || r.TTL < 1 || r.TTL > 86400 {
			return errors.New("Technitium records require an explicit containing zone and exact TTL from 1 to 86400 seconds")
		}
	}
	return nil
}

func literalClient(value string) bool {
	if ip, err := netip.ParseAddr(value); err == nil {
		return ip.Zone() == "" && !ip.IsUnspecified() && !ip.IsMulticast() && !ip.Is4In6() && ip.String() == value
	}
	p, err := netip.ParsePrefix(value)
	return err == nil && p.Masked() == p && !p.Addr().Is4In6() && p.String() == value && !p.Addr().IsMulticast()
}

func withinZone(name, zone string) bool { return name == zone || strings.HasSuffix(name, "."+zone) }

func policyHash(value any) string {
	b, _ := json.Marshal(value)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func knownPolicyFields(raw map[string]json.RawMessage, names string) bool {
	known := map[string]bool{}
	for _, name := range strings.Fields(names) {
		known[name] = true
	}
	for name := range raw {
		if !known[name] {
			return false
		}
	}
	return true
}

type overrideInventory struct {
	Hosts    []string
	CNAME    []string
	Rewrites []map[string]json.RawMessage
}

func (c *nativeClient) readOverrides(ctx context.Context) (overrideInventory, error) {
	var result overrideInventory
	if c.engine == AdGuard {
		if err := c.request(ctx, http.MethodGet, "/control/rewrite/list", nil, &result.Rewrites); err != nil {
			return result, err
		}
		if result.Rewrites == nil || len(result.Rewrites) > 256 {
			return result, errors.New("complete bounded native rewrite inventory is required")
		}
		for _, raw := range result.Rewrites {
			if !knownPolicyFields(raw, "domain answer enabled") {
				return result, errors.New("native rewrite has unsupported policy fields; inspect it in the native console")
			}
			if value, ok := raw["enabled"]; ok {
				var enabled bool
				if string(value) == "null" || json.Unmarshal(value, &enabled) != nil {
					return result, errors.New("native rewrite enable state is unreadable")
				}
			}
		}
	} else {
		var response struct {
			Config struct {
				DNS struct {
					Hosts []string `json:"hosts"`
					CNAME []string `json:"cnameRecords"`
				} `json:"dns"`
			} `json:"config"`
		}
		if err := c.request(ctx, http.MethodGet, "/api/config", nil, &response); err != nil {
			return result, err
		}
		result.Hosts, result.CNAME = response.Config.DNS.Hosts, response.Config.DNS.CNAME
		if result.Hosts == nil || result.CNAME == nil || len(result.Hosts)+len(result.CNAME) > 256 {
			return result, errors.New("complete bounded native hosts and CNAME inventories are required")
		}
	}
	return result, nil
}

func overrideCounts(engine Engine, r RecordChange, inv overrideInventory) (exact, owners int) {
	if engine == AdGuard {
		for _, v := range inv.Rewrites {
			var name, value string
			if json.Unmarshal(v["domain"], &name) != nil || json.Unmarshal(v["answer"], &value) != nil {
				return -1, -1
			}
			if strings.EqualFold(strings.TrimSuffix(name, "."), r.Name) {
				owners++
				enabled := true
				if v["enabled"] != nil && json.Unmarshal(v["enabled"], &enabled) != nil {
					return -1, -1
				}
				if name == r.Name && value == r.Value && enabled {
					exact++
				}
			}
		}
	} else {
		for _, v := range inv.Hosts {
			fields := strings.Fields(v)
			if len(fields) < 2 {
				continue
			}
			for _, name := range fields[1:] {
				if strings.EqualFold(strings.TrimSuffix(name, "."), r.Name) {
					owners++
					if v == r.Value+" "+r.Name {
						exact++
					}
				}
			}
		}
		for _, v := range inv.CNAME {
			if fields := strings.Split(v, ","); len(fields) > 0 && strings.EqualFold(strings.TrimSpace(fields[0]), r.Name) {
				owners++
			}
		}
	}
	return
}

type piClient struct {
	Client  string  `json:"client"`
	Comment *string `json:"comment"`
	Groups  []int   `json:"groups"`
}

type clientInventory struct {
	Clients []map[string]json.RawMessage `json:"clients"`
	Groups  []map[string]json.RawMessage `json:"groups"`
}

func (c *nativeClient) readClientGroups(ctx context.Context) (clientInventory, error) {
	var result clientInventory
	if err := c.request(ctx, http.MethodGet, "/api/clients", nil, &result); err != nil {
		return result, err
	}
	var groups struct {
		Groups []map[string]json.RawMessage `json:"groups"`
	}
	if err := c.request(ctx, http.MethodGet, "/api/groups", nil, &groups); err != nil {
		return result, err
	}
	result.Groups = groups.Groups
	if result.Clients == nil || result.Groups == nil || len(result.Clients) > 128 || len(result.Groups) > 128 {
		return result, errors.New("complete bounded native clients and groups are required")
	}
	return result, nil
}

func selectedClient(req ClientGroupChange, inv clientInventory) (piClient, error) {
	var selected piClient
	count := 0
	for _, raw := range inv.Clients {
		b, _ := json.Marshal(raw)
		var client piClient
		if json.Unmarshal(b, &client) != nil || client.Client == "" || client.Groups == nil || raw["comment"] == nil {
			return selected, errors.New("native client policy is incomplete")
		}
		if client.Client == req.Address {
			if !knownPolicyFields(raw, "client comment groups id date_added date_modified name") {
				return selected, errors.New("native client has unsupported replacement fields; no incomplete replacement is sent")
			}
			selected = client
			count++
		}
	}
	if count != 1 {
		return selected, errors.New("exactly one existing native client is required; no client is created or adopted")
	}
	known := map[int]bool{}
	for _, raw := range inv.Groups {
		var id int
		var enabled bool
		if json.Unmarshal(raw["id"], &id) != nil || json.Unmarshal(raw["enabled"], &enabled) != nil || known[id] {
			return selected, errors.New("native group identity or enable policy is unreadable")
		}
		known[id] = true
	}
	for _, id := range req.Groups {
		if !known[id] {
			return selected, errors.New("all selected filter groups must already exist in the native engine")
		}
	}
	return selected, nil
}

func (c *nativeClient) readZoneRecords(ctx context.Context, zone string) (*RecordInventory, error) {
	if c.version == "" {
		var settings struct {
			Version string `json:"version"`
		}
		if err := c.technitium(ctx, "/api/settings/get", nil, &settings); err != nil {
			return nil, err
		}
		if !versionSupported(settings.Version, "15.") {
			return nil, errors.New("native zone record version is unreadable or unsupported")
		}
		c.version = settings.Version
	}
	var response struct {
		Zone    map[string]json.RawMessage   `json:"zone"`
		Records []map[string]json.RawMessage `json:"records"`
	}
	if err := c.technitium(ctx, "/api/zones/records/get", url.Values{"zone": {zone}, "domain": {zone}, "listZone": {"true"}}, &response); err != nil {
		return nil, err
	}
	out := &RecordInventory{NativeVersion: c.version, Records: []ZoneRecord{}, Evidence: Reading{"native_authority_configuration", "native_api", "Native zone records and metadata; delegation, publication and client answers require separate evidence."}}
	if response.Zone == nil || response.Records == nil || len(response.Records) > 256 || json.Unmarshal(response.Zone["name"], &out.Zone) != nil || out.Zone != zone || json.Unmarshal(response.Zone["type"], &out.Type) != nil || json.Unmarshal(response.Zone["disabled"], &out.Disabled) != nil {
		return nil, errors.New("complete exact bounded native zone ownership and records are required")
	}
	if value, ok := response.Zone["internal"]; ok {
		var internal bool
		if json.Unmarshal(value, &internal) != nil || string(value) == "null" {
			return nil, errors.New("native internal-zone declaration is unreadable")
		}
		out.Internal = &internal
	}
	if value, ok := response.Zone["dnssecStatus"]; ok {
		if json.Unmarshal(value, &out.DNSSEC) != nil {
			return nil, errors.New("native zone DNSSEC declaration is unreadable")
		}
	}
	if out.Internal == nil {
		out.Evidence.Summary += " Native internal/external classification is not reported; the pinned 15.6 Primary type has no separate internal-zone category."
	}
	for _, raw := range response.Records {
		var record ZoneRecord
		if json.Unmarshal(raw["name"], &record.Name) != nil || !withinZone(record.Name, zone) || json.Unmarshal(raw["type"], &record.Type) != nil || json.Unmarshal(raw["ttl"], &record.TTL) != nil || json.Unmarshal(raw["disabled"], &record.Disabled) != nil {
			return nil, errors.New("native record identity or TTL is unreadable")
		}
		var rdata map[string]json.RawMessage
		if json.Unmarshal(raw["rData"], &rdata) != nil {
			return nil, errors.New("native record data is unreadable")
		}
		record.Editable = editableZone(out) && !record.Disabled && (record.Type == "A" || record.Type == "AAAA") && len(rdata) == 1 && json.Unmarshal(rdata["ipAddress"], &record.Value) == nil
		if !knownPolicyFields(raw, "name nameIdn type ttl ttlString disabled comments rData dnssecStatus lastUsedOn lastModified expiryTtl expiryTtlString") {
			record.Editable = false
		}
		if record.Editable {
			record.Editable = validatePolicyChange(ChangeRequest{Action: "record_remove", Zone: zone, Record: &RecordChange{Name: record.Name, Type: record.Type, Value: record.Value, TTL: record.TTL}}, Technitium) == nil
		}
		if raw["comments"] != nil {
			if json.Unmarshal(raw["comments"], &record.Comments) != nil {
				record.Editable = false
			}
		}
		var expiry uint32
		if raw["expiryTtl"] != nil && (json.Unmarshal(raw["expiryTtl"], &expiry) != nil || expiry != 0) {
			record.Editable = false
		}
		// Query activity must not invalidate a reviewed configuration generation.
		delete(raw, "lastUsedOn")
		preserved := map[string]json.RawMessage{}
		for k, v := range raw {
			preserved[k] = v
		}
		if record.Type == "SOA" {
			delete(rdata, "serial")
			preserved["rData"], _ = json.Marshal(rdata)
			delete(preserved, "lastModified")
		}
		record.Fingerprint = policyHash(preserved)
		record.Name, record.Type, record.Value, record.Comments = c.text(record.Name), c.text(record.Type), c.text(record.Value), c.text(record.Comments)
		out.Records = append(out.Records, record)
	}
	out.Fingerprint = policyHash(struct {
		Version string
		Policy  any
	}{c.version, response})
	return out, nil
}

func editableZone(inv *RecordInventory) bool {
	if inv == nil || inv.Type != "Primary" || inv.Disabled || inv.DNSSEC != "Unsigned" {
		return false
	}
	if inv.Internal != nil {
		return !*inv.Internal
	}
	// The pinned 15.6 writer omits the obsolete internal flag, and its Primary
	// class has no separate internal-zone category. Other absent flags stay closed.
	return inv.NativeVersion == "15.6" || inv.NativeVersion == "15.6.0"
}

func (c *nativeClient) inspectPolicySelection(ctx context.Context, s *Snapshot, req ChangeRequest) error {
	switch req.Action {
	case "override_add", "override_remove":
		inv, err := c.readOverrides(ctx)
		if err != nil {
			return err
		}
		s.SelectionFingerprint = policyHash(inv)
	case "client_groups":
		inv, err := c.readClientGroups(ctx)
		if err != nil {
			return err
		}
		s.SelectionFingerprint = policyHash(inv)
		client, err := selectedClient(*req.Client, inv)
		if err != nil {
			return err
		}
		s.SelectedClient = &ClientGroupPolicy{Address: client.Client, Groups: client.Groups, CommentFingerprint: policyHash(client.Comment)}
		if client.Comment != nil {
			comment := c.text(*client.Comment)
			s.SelectedClient.Comment = &comment
		}
		for _, raw := range inv.Clients {
			if string(raw["client"]) == strconv.Quote(client.Client) {
				delete(raw, "groups")
				delete(raw, "date_modified")
			}
		}
		s.SelectedClient.OtherPolicyFingerprint = policyHash(inv)
	case "record_add", "record_remove":
		inv, err := c.readZoneRecords(ctx, req.Zone)
		if err != nil {
			return err
		}
		s.Records = inv
		s.SelectionFingerprint = inv.Fingerprint
	}
	return nil
}

func validatePolicyBaseline(req ChangeRequest, s *Snapshot) error {
	if !policyAction(req.Action) {
		return nil
	}
	if s == nil || s.SelectionFingerprint == "" {
		return errors.New("complete native selection provenance is required")
	}
	switch req.Action {
	case "override_add", "override_remove":
		if s.OverrideEvidence.State != "configured" {
			return errors.New("complete native override policy is required")
		}
		inv := overrideInventory{}
		for _, v := range s.LocalOverrides {
			if s.Engine == AdGuard {
				raw := map[string]json.RawMessage{}
				raw["domain"], _ = json.Marshal(v.Name)
				raw["answer"], _ = json.Marshal(v.Value)
				if v.Enabled != nil {
					raw["enabled"], _ = json.Marshal(*v.Enabled)
				}
				inv.Rewrites = append(inv.Rewrites, raw)
			} else if v.Type == "hosts_entry" {
				inv.Hosts = append(inv.Hosts, v.Value)
			} else if v.Type == "cname_entry" {
				inv.CNAME = append(inv.CNAME, v.Value)
			}
		}
		exact, owners := overrideCounts(s.Engine, *req.Record, inv)
		if req.Action == "override_add" && owners != 0 {
			return errors.New("an override already names this owner; no replacement was staged")
		}
		if req.Action == "override_remove" && (exact != 1 || owners != 1) {
			return errors.New("removal requires one exact simple native override without another alias or answer")
		}
	case "client_groups":
		if s.ClientEvidence.State != "configured" || s.AppClientEvidence.State != "configured" {
			return errors.New("complete native client and filter-group policy is required")
		}
		count := 0
		for _, client := range s.Clients {
			if client.ID == req.Client.Address {
				count++
			}
		}
		if count != 1 {
			return errors.New("an exact existing native client is required")
		}
		for _, id := range req.Client.Groups {
			found := false
			for _, group := range s.FilterGroups {
				if group.ID != nil && *group.ID == id {
					found = true
				}
			}
			if !found {
				return errors.New("selected native filter group does not exist")
			}
		}
	case "record_add", "record_remove":
		inv := s.Records
		if s.ZoneEvidence.State != "native_authority_configuration" || inv == nil || inv.Zone != req.Zone || !editableZone(inv) {
			return errors.New("an enabled supported unsigned native primary zone with complete inventory is required")
		}
		for _, zone := range s.Zones {
			if zone.Name != req.Zone && withinZone(req.Record.Name, zone.Name) && withinZone(zone.Name, req.Zone) {
				return errors.New("a more specific native zone owns this record name")
			}
		}
		exact := 0
		for _, r := range inv.Records {
			if req.Action == "record_add" && r.Name != req.Record.Name && withinZone(req.Record.Name, r.Name) && (r.Type == "DNAME" || r.Type == "NS" && r.Name != req.Zone) {
				return errors.New("a native alias or delegation owns an ancestor of this record")
			}
			if r.Name != req.Record.Name {
				continue
			}
			if req.Action == "record_add" && r.Type == req.Record.Type && (r.TTL != req.Record.TTL || !r.Editable) {
				return errors.New("the existing record set has a different TTL or unsupported metadata")
			}
			if req.Action == "record_add" && (r.Type == "CNAME" || r.Type == "DNAME" || r.Type == "APP" || r.Type == "ANAME" || r.Type == "NS" && r.Name != req.Zone) {
				return errors.New("the record owner has a native alias, app or delegation; no conflicting answer was staged")
			}
			if r.Type == req.Record.Type && r.Value == req.Record.Value {
				if !r.Editable || r.TTL != req.Record.TTL {
					return errors.New("the existing record has different TTL or unsupported metadata")
				}
				exact++
			}
		}
		if req.Action == "record_add" && exact != 0 {
			return errors.New("the exact native record already exists; no duplicate was staged")
		}
		if req.Action == "record_remove" && exact != 1 {
			return errors.New("removal requires exactly one matching editable native record and TTL")
		}
	}
	return nil
}

func applyPolicyNative(ctx context.Context, c *nativeClient, req ChangeRequest, before *Snapshot) error {
	selection := &Snapshot{}
	if err := c.inspectPolicySelection(ctx, selection, req); err != nil {
		return err
	}
	if before == nil || selection.SelectionFingerprint != before.SelectionFingerprint {
		return ErrConflict
	}
	switch req.Action {
	case "override_add", "override_remove":
		if c.engine == AdGuard {
			action := "add"
			if req.Action == "override_remove" {
				action = "delete"
			}
			return c.request(ctx, http.MethodPost, "/control/rewrite/"+action, map[string]any{"domain": req.Record.Name, "answer": req.Record.Value, "enabled": true}, nil)
		}
		inv, err := c.readOverrides(ctx)
		if err != nil {
			return err
		}
		if policyHash(inv) != before.SelectionFingerprint {
			return ErrConflict
		}
		wanted := req.Record.Value + " " + req.Record.Name
		hosts := append([]string{}, inv.Hosts...)
		if req.Action == "override_add" {
			hosts = append(hosts, wanted)
		} else {
			next := []string{}
			for _, v := range hosts {
				if v != wanted {
					next = append(next, v)
				}
			}
			hosts = next
		}
		return c.request(ctx, http.MethodPatch, "/api/config", map[string]any{"config": map[string]any{"dns": map[string]any{"hosts": hosts}}}, nil)
	case "client_groups":
		inv, err := c.readClientGroups(ctx)
		if err != nil {
			return err
		}
		if policyHash(inv) != before.SelectionFingerprint {
			return ErrConflict
		}
		client, err := selectedClient(*req.Client, inv)
		if err != nil {
			return err
		}
		return c.request(ctx, http.MethodPut, "/api/clients/"+url.PathEscape(req.Client.Address), map[string]any{"comment": client.Comment, "groups": req.Client.Groups}, nil)
	case "record_add", "record_remove":
		path := "/api/zones/records/add"
		form := url.Values{"zone": {req.Zone}, "domain": {req.Record.Name}, "type": {req.Record.Type}, "ipAddress": {req.Record.Value}, "updateSvcbHints": {"false"}}
		if req.Action == "record_add" {
			form.Set("ttl", strconv.FormatUint(uint64(req.Record.TTL), 10))
			form.Set("overwrite", "false")
			form.Set("ptr", "false")
			form.Set("createPtrZone", "false")
		} else {
			path = "/api/zones/records/delete"
		}
		return c.technitium(ctx, path, form, nil)
	}
	return errors.New("unsupported bounded native policy change")
}

func policyMatches(req ChangeRequest, after *Snapshot) bool {
	switch req.Action {
	case "override_add", "override_remove":
		inv := overrideInventory{}
		for _, v := range after.LocalOverrides {
			if after.Engine == AdGuard {
				raw := map[string]json.RawMessage{}
				raw["domain"], _ = json.Marshal(v.Name)
				raw["answer"], _ = json.Marshal(v.Value)
				if v.Enabled != nil {
					raw["enabled"], _ = json.Marshal(*v.Enabled)
				}
				inv.Rewrites = append(inv.Rewrites, raw)
			} else if v.Type == "hosts_entry" {
				inv.Hosts = append(inv.Hosts, v.Value)
			} else if v.Type == "cname_entry" {
				inv.CNAME = append(inv.CNAME, v.Value)
			}
		}
		exact, owners := overrideCounts(after.Engine, *req.Record, inv)
		if after.OverrideEvidence.State != "configured" {
			return false
		}
		if req.Action == "override_add" {
			return exact == 1 && owners == 1
		}
		return exact == 0 && owners == 0
	case "client_groups":
		for _, client := range after.Clients {
			if client.ID == req.Client.Address {
				wanted, actual := append([]int{}, req.Client.Groups...), append([]int{}, client.Groups...)
				sort.Ints(wanted)
				sort.Ints(actual)
				return reflect.DeepEqual(wanted, actual)
			}
		}
	case "record_add", "record_remove":
		if after.ZoneEvidence.State != "native_authority_configuration" || !editableZone(after.Records) {
			return false
		}
		count := 0
		for _, record := range after.Records.Records {
			if record.Name == req.Record.Name && record.Type == req.Record.Type && record.Value == req.Record.Value {
				if !record.Editable || record.TTL != req.Record.TTL {
					return false
				}
				count++
			}
		}
		if req.Action == "record_add" {
			return count == 1
		}
		return count == 0
	}
	return false
}

func policyPreserved(req ChangeRequest, before, after *Snapshot) bool {
	if !policyAction(req.Action) {
		return true
	}
	if before == nil || after == nil {
		return false
	}
	switch req.Action {
	case "client_groups":
		return before.SelectedClient != nil && after.SelectedClient != nil && before.SelectedClient.CommentFingerprint == after.SelectedClient.CommentFingerprint && before.SelectedClient.OtherPolicyFingerprint == after.SelectedClient.OtherPolicyFingerprint
	case "override_add", "override_remove":
		keep := func(s *Snapshot) []LocalOverride {
			out := []LocalOverride{}
			for _, v := range s.LocalOverrides {
				selected := s.Engine == AdGuard && v.Name == req.Record.Name && v.Value == req.Record.Value || s.Engine == PiHole && v.Type == "hosts_entry" && v.Value == req.Record.Value+" "+req.Record.Name
				if !selected {
					out = append(out, v)
				}
			}
			return out
		}
		return reflect.DeepEqual(keep(before), keep(after))
	case "record_add", "record_remove":
		if before.Records == nil || after.Records == nil {
			return false
		}
		keep := func(inv *RecordInventory) []string {
			out := []string{}
			for _, r := range inv.Records {
				if r.Name == req.Record.Name && r.Type == req.Record.Type && r.Value == req.Record.Value {
					continue
				}
				out = append(out, r.Fingerprint)
			}
			sort.Strings(out)
			return out
		}
		return reflect.DeepEqual(keep(before.Records), keep(after.Records))
	}
	return false
}

func (s *Service) Records(ctx context.Context, id, zone string) (*RecordInventory, error) {
	if !validZone(zone) {
		return nil, errors.New("zone records require a complete lower-case DNS zone name")
	}
	_, req, err := s.connection(ctx, id)
	if err != nil {
		return nil, err
	}
	if req.Engine != Technitium {
		return nil, errors.New("authoritative record inventory requires Technitium; local overrides are a separate capability")
	}
	if err = s.acquire(ctx); err != nil {
		return nil, err
	}
	defer func() { <-s.active }()
	snapshot, err := inspectNativeSelection(ctx, req, &ChangeRequest{Action: "record_add", Zone: zone})
	if err != nil {
		return nil, err
	}
	return snapshot.Records, nil
}
