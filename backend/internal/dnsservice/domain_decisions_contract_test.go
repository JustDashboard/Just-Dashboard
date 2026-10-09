package dnsservice

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

type decisionInventory struct {
	Filters domainFilterInventory
	Clients json.RawMessage
}

func decisionVersion(ctx context.Context, c *nativeClient) (string, error) {
	if c.engine == AdGuard {
		var status struct {
			Version string `json:"version"`
		}
		if err := c.request(ctx, http.MethodGet, "/control/status", nil, &status); err != nil || strings.TrimPrefix(status.Version, "v") != "0.107.71" {
			return "", errors.New("decision fixture requires exact AdGuard 0.107.71")
		}
		return status.Version, nil
	}
	var status struct {
		Version struct {
			FTL struct {
				Local struct {
					Version string `json:"version"`
				} `json:"local"`
			} `json:"ftl"`
		} `json:"version"`
	}
	if err := c.request(ctx, http.MethodGet, "/api/info/version", nil, &status); err != nil || strings.TrimPrefix(status.Version.FTL.Local.Version, "v") != "6.7.1" {
		return "", errors.New("decision fixture requires exact Pi-hole FTL 6.7.1")
	}
	return status.Version.FTL.Local.Version, nil
}

func readDecisionInventory(t *testing.T, ctx context.Context, c *nativeClient, engine Engine, parent string) decisionInventory {
	t.Helper()
	version, err := decisionVersion(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := c.readDomainFilters(ctx, version)
	if err != nil {
		t.Fatal("complete actual decision inventory", err)
	}
	var clients map[string]json.RawMessage
	path := "/control/clients"
	if engine == PiHole {
		path = "/api/clients"
	}
	if err = c.request(ctx, http.MethodGet, path, nil, &clients); err != nil {
		t.Fatal(err)
	}
	if _, err := decisionClientRows(engine, clients); err != nil {
		t.Fatal(err)
	}
	if engine == AdGuard {
		if len(inv.Rules) < 3 || inv.Rules[0] != "# owned unselected decision fixture comment" || inv.Rules[1] != "" || inv.Rules[2] != "||"+parent+"^" {
			t.Fatal("literal ordered foreign comment/blank/suffix seed was not preserved")
		}
		if len(c.adGuardFilterSources(inv.Native, version).Entries) != 0 {
			t.Fatal("decision fixture must have explicit empty subscription inventories")
		}
	} else {
		seed := "(^|[.])" + strings.ReplaceAll(parent, ".", "[.]") + "$"
		found := 0
		for _, row := range inv.Rows {
			var domain, disposition, kind, comment string
			json.Unmarshal(row["domain"], &domain)
			json.Unmarshal(row["type"], &disposition)
			json.Unmarshal(row["kind"], &kind)
			json.Unmarshal(row["comment"], &comment)
			if domain == seed {
				groups, ok := filterRawGroups(row["groups"])
				var enabled *bool
				if disposition != "deny" || kind != "regex" || comment != "owned unselected decision fixture comment" || !ok || !reflect.DeepEqual(groups, []int{0}) || json.Unmarshal(row["enabled"], &enabled) != nil || enabled == nil || !*enabled {
					t.Fatal("literal foreign regex/groups/comment seed differs")
				}
				found++
			}
		}
		if found != 1 || len(inv.Sources) != 0 {
			t.Fatal("unique foreign regex and explicit empty subscriptions required")
		}
	}
	return decisionInventory{Filters: inv, Clients: append(json.RawMessage{}, clients["clients"]...)}
}

func stripDecisionSelections(engine Engine, parent string, inv decisionInventory) decisionInventory {
	deny := strings.Replace(strings.TrimSuffix(parent, ".invalid"), "seed-", "deny-", 1) + ".invalid"
	empty := strings.Replace(deny, "deny-", "empty-", 1)
	selected := map[string]bool{"allow." + parent: true, deny: true, empty: true}
	if engine == AdGuard {
		rules := []string{}
		for _, rule := range inv.Filters.Rules {
			managed := false
			for name := range selected {
				managed = managed || rule == "||"+name+"^" || rule == "@@||"+name+"^"
			}
			if !managed {
				rules = append(rules, rule)
			}
		}
		inv.Filters.Rules = rules
		native := map[string]json.RawMessage{}
		for key, raw := range inv.Filters.Native {
			native[key] = raw
		}
		native["user_rules"], _ = json.Marshal(rules)
		inv.Filters.Native = native
	} else {
		rows := []map[string]json.RawMessage{}
		for _, row := range inv.Filters.Rows {
			var name, kind string
			json.Unmarshal(row["domain"], &name)
			json.Unmarshal(row["kind"], &kind)
			if !selected[name] || kind != "exact" {
				rows = append(rows, row)
			}
		}
		inv.Filters.Rows = rows
	}
	return inv
}

func assertDecisionSeed(t *testing.T, ctx context.Context, c *nativeClient, engine Engine, parent string, seed decisionInventory) {
	t.Helper()
	current := stripDecisionSelections(engine, parent, readDecisionInventory(t, ctx, c, engine, parent))
	seed = stripDecisionSelections(engine, parent, seed)
	if !reflect.DeepEqual(seed, current) {
		t.Fatal("unselected native rules/clients/groups/subscriptions/filter configuration changed")
	}
}

func decisionClientRows(engine Engine, clients map[string]json.RawMessage) ([]map[string]json.RawMessage, error) {
	raw, present := clients["clients"]
	if !present {
		return nil, errors.New("native persistent-client field is missing")
	}
	// AdGuard 0.107.71 appends to a nil slice, so an empty persistent
	// inventory is explicitly null. Missing data and Pi-hole null stay unknown.
	if engine == AdGuard && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return []map[string]json.RawMessage{}, nil
	}
	var rows []map[string]json.RawMessage
	if json.Unmarshal(raw, &rows) != nil || rows == nil || len(rows) > 128 {
		return nil, errors.New("complete bounded native client inventory required")
	}
	return rows, nil
}

func decisionClientScope(engine Engine, clients map[string]json.RawMessage, groups []map[string]json.RawMessage, address, mac string) error {
	rows, err := decisionClientRows(engine, clients)
	if err != nil {
		return err
	}
	if engine == AdGuard {
		// This fixture creates no persistent AdGuard client, so every request
		// inherits the global policy. Auto-discovered runtime rows are separate.
		if len(rows) != 0 {
			return errors.New("fixture AdGuard client overrides must be explicitly empty")
		}
		return nil
	}
	if engine != PiHole || len(rows) != 1 {
		return errors.New("fixture Pi-hole client policy must contain exactly its foreign control")
	}
	var selected, comment *string
	if json.Unmarshal(rows[0]["client"], &selected) != nil || selected == nil || *selected != "198.51.100.77" || *selected == address || *selected == mac || json.Unmarshal(rows[0]["comment"], &comment) != nil || comment == nil || *comment != "owned unselected client comment" {
		return errors.New("foreign control identity/comment differs; default-client scope unproved")
	}
	membership, ok := filterRawGroups(rows[0]["groups"])
	if !ok || !reflect.DeepEqual(membership, []int{0}) || len(groups) != 1 {
		return errors.New("foreign memberships or complete default-group inventory differs")
	}
	var id *int
	var enabled *bool
	if json.Unmarshal(groups[0]["id"], &id) != nil || id == nil || *id != 0 || json.Unmarshal(groups[0]["enabled"], &enabled) != nil || enabled == nil || !*enabled {
		return errors.New("exact enabled native default group zero is required")
	}
	return nil
}

func verifyDecisionClient(t *testing.T, ctx context.Context, c *nativeClient, engine Engine, address, mac string, protection bool) {
	t.Helper()
	version, err := decisionVersion(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := c.readDomainFilters(ctx, version)
	if err != nil {
		t.Fatal(err)
	}
	var overrides []json.RawMessage
	if engine == AdGuard {
		if c.request(ctx, http.MethodGet, "/control/rewrite/list", nil, &overrides) != nil || overrides == nil || len(overrides) != 0 {
			t.Fatal("positive decisions require explicit empty native local rewrites")
		}
	}
	var clients map[string]json.RawMessage
	path := "/control/clients"
	if engine == PiHole {
		path = "/api/clients"
	}
	if err = c.request(ctx, http.MethodGet, path, nil, &clients); err == nil {
		err = decisionClientScope(engine, clients, inv.Groups, address, mac)
	}
	if err != nil {
		t.Fatal("actual source/default native client scope not established", err)
	}
	if engine == AdGuard {
		var enabled *bool
		if json.Unmarshal(inv.Native["enabled"], &enabled) != nil || enabled == nil || !*enabled {
			t.Fatal("native AdGuard filtering must be explicitly enabled")
		}
		var config struct {
			Mode string `json:"blocking_mode"`
		}
		var status struct {
			Protection *bool `json:"protection_enabled"`
		}
		if c.request(ctx, http.MethodGet, "/control/dns_info", nil, &config) != nil || config.Mode != "default" && config.Mode != "null_ip" || c.request(ctx, http.MethodGet, "/control/status", nil, &status) != nil || status.Protection == nil || *status.Protection != protection {
			t.Fatal("native AdGuard null-IP blocking mode/protection prerequisite differs")
		}
	} else {
		var config struct {
			Config struct {
				DNS struct {
					Hosts    []string `json:"hosts"`
					CNAME    []string `json:"cnameRecords"`
					Blocking struct {
						Mode string `json:"mode"`
					} `json:"blocking"`
				} `json:"dns"`
			} `json:"config"`
		}
		var status struct {
			Blocking string `json:"blocking"`
		}
		want := "disabled"
		if protection {
			want = "enabled"
		}
		if c.request(ctx, http.MethodGet, "/api/config", nil, &config) != nil || config.Config.DNS.Blocking.Mode != "NULL" || config.Config.DNS.Hosts == nil || config.Config.DNS.CNAME == nil || len(config.Config.DNS.Hosts)+len(config.Config.DNS.CNAME) != 0 || c.request(ctx, http.MethodGet, "/api/dns/blocking", nil, &status) != nil || status.Blocking != want {
			t.Fatal("native Pi-hole NULL mode/protection prerequisite differs")
		}
	}
	t.Logf("default-client engine=%s address=%s mac=%s selectedPersistentOverride=absent protection=%t group=%s", engine, address, mac, protection, map[Engine]string{AdGuard: "global", PiHole: "0"}[engine])
}

func decisionHistoryPhase(engine Engine, raw json.RawMessage, notBefore, now time.Time) (bool, error) {
	if notBefore.IsZero() || now.IsZero() || notBefore.After(now) {
		return false, errors.New("actual matrix/read clock boundary required")
	}
	if engine == AdGuard {
		var reported *string
		if json.Unmarshal(raw, &reported) != nil || reported == nil {
			return false, errors.New("native AdGuard history time is missing/null/malformed")
		}
		at, err := time.Parse(time.RFC3339Nano, *reported)
		if err != nil || at.After(now) {
			return false, errors.New("native AdGuard history time is malformed or in the future")
		}
		return !at.Before(notBefore), nil
	}
	if engine != PiHole {
		return false, errors.New("unsupported native decision history engine")
	}
	var reported *float64
	if json.Unmarshal(raw, &reported) != nil || reported == nil || *reported < 0 || math.IsNaN(*reported) || math.IsInf(*reported, 0) || *reported > float64(now.UnixNano())/1e9 {
		return false, errors.New("native Pi-hole history epoch is missing/null/malformed or in the future")
	}
	return *reported >= float64(notBefore.UnixNano())/1e9, nil
}

func decisionHistory(ctx context.Context, c *nativeClient, engine Engine, address, allow, deny string, notBefore time.Time) (bool, error) {
	if notBefore.IsZero() || notBefore.After(time.Now()) {
		return false, errors.New("actual matrix/read clock boundary required")
	}
	listIDs := map[string]int64{}
	if engine == PiHole {
		version, err := decisionVersion(ctx, c)
		if err != nil {
			return false, err
		}
		inv, err := c.readDomainFilters(ctx, version)
		if err != nil {
			return false, err
		}
		for _, row := range inv.Rows {
			var name, kind, disposition string
			var id *int64
			json.Unmarshal(row["domain"], &name)
			json.Unmarshal(row["kind"], &kind)
			json.Unmarshal(row["type"], &disposition)
			json.Unmarshal(row["id"], &id)
			if kind == "exact" && id != nil && (name == allow && disposition == "allow" || name == deny && disposition == "deny") {
				if _, duplicate := listIDs[name]; duplicate {
					return false, errors.New("native selected history rule ID is ambiguous")
				}
				listIDs[name] = *id
			}
		}
		if len(listIDs) != 2 {
			return false, errors.New("selected allow/deny IDs required for query corroboration")
		}
	}
	var envelope map[string]json.RawMessage
	path, key := "/control/querylog?limit=100", "data"
	if engine == PiHole {
		path, key = "/api/queries?length=100&client_ip="+address, "queries"
	}
	if err := c.request(ctx, http.MethodGet, path, nil, &envelope); err != nil {
		return false, err
	}
	now := time.Now()
	var rows []map[string]json.RawMessage
	if json.Unmarshal(envelope[key], &rows) != nil || rows == nil || len(rows) > 100 {
		return false, errors.New("bounded explicit native history rows required")
	}
	allowSeen, denySeen := map[string]bool{}, map[string]bool{}
	for _, row := range rows {
		if row == nil {
			return false, errors.New("native history row is null")
		}
		fromPhase, err := decisionHistoryPhase(engine, row["time"], notBefore, now)
		if err != nil {
			return false, err
		}
		var client, name, kind, status string
		if engine == AdGuard {
			var question map[string]json.RawMessage
			if json.Unmarshal(row["question"], &question) != nil || question == nil || json.Unmarshal(question["name"], &name) != nil || name == "" || json.Unmarshal(question["type"], &kind) != nil || kind == "" || json.Unmarshal(row["client"], &client) != nil || client == "" || json.Unmarshal(row["reason"], &status) != nil || status == "" {
				return false, errors.New("native AdGuard history question.name/client/reason is malformed")
			}
		} else {
			var nativeClient map[string]json.RawMessage
			if json.Unmarshal(row["client"], &nativeClient) != nil || nativeClient == nil || json.Unmarshal(nativeClient["ip"], &client) != nil || client == "" || json.Unmarshal(row["domain"], &name) != nil || name == "" || json.Unmarshal(row["type"], &kind) != nil || kind == "" || row["status"] == nil {
				return false, errors.New("native Pi-hole history question/client is malformed")
			}
			var reported *string
			if json.Unmarshal(row["status"], &reported) != nil {
				return false, errors.New("native Pi-hole status is malformed")
			}
			if reported != nil {
				status = *reported
			}
		}
		if !fromPhase || client != address || kind != "A" && kind != "AAAA" || name != allow && name != deny {
			continue
		}
		if engine == AdGuard {
			if name == allow && status != "NotFilteredWhiteList" || name == deny && status != "FilteredBlackList" {
				continue
			}
			var protocol *string
			var rules []struct {
				Text *string `json:"text"`
				ID   *int    `json:"filter_list_id"`
			}
			// The pinned enum describes encryption; its explicit empty value is plain DNS.
			// UDP/TCP are independently measured by the complete wire matrices.
			if json.Unmarshal(row["client_proto"], &protocol) != nil || protocol == nil || *protocol != "" || json.Unmarshal(row["rules"], &rules) != nil || len(rules) != 1 || rules[0].Text == nil || rules[0].ID == nil || *rules[0].ID != 0 {
				return false, errors.New("native controlled AdGuard query rule/plain-DNS encryption marker is missing or unsupported")
			}
			if name == allow && status == "NotFilteredWhiteList" && *rules[0].Text == "@@||"+allow+"^" {
				allowSeen[kind] = true
			} else if name == deny && status == "FilteredBlackList" && *rules[0].Text == "||"+deny+"^" {
				denySeen[kind] = true
			}
		} else {
			if name == allow && status != "FORWARDED" && status != "CACHE" || name == deny && status != "DENYLIST" {
				continue
			}
			var list *int64
			if row["list_id"] == nil || json.Unmarshal(row["list_id"], &list) != nil {
				return false, errors.New("native controlled Pi-hole list identity is malformed")
			}
			if list == nil || *list != listIDs[name] {
				continue
			}
			if name == allow && (status == "FORWARDED" || status == "CACHE") {
				allowSeen[kind] = true
			} else if name == deny && status == "DENYLIST" {
				denySeen[kind] = true
			}
		}
	}
	return len(allowSeen) == 2 && len(denySeen) == 2, nil
}

func verifyDecisionHistory(t *testing.T, ctx context.Context, c *nativeClient, engine Engine, address, allow, deny string, notBefore time.Time) {
	t.Helper()
	for attempt := 0; attempt < 8; attempt++ {
		ok, err := decisionHistory(ctx, c, engine, address, allow, deny, notBefore)
		now := time.Now()
		if err != nil {
			t.Fatal("native actual-query corroboration failed", err)
		}
		if ok {
			t.Logf("native query history corroborated engine=%s client=%s allow/deny=A+AAAA phaseNotBefore=%s readNotAfter=%s transportBasis=%s", engine, address, notBefore.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano), map[Engine]string{AdGuard: "wire_only_native_plain_encryption_marker", PiHole: "wire_only_native_transport_unreported"}[engine])
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
	t.Fatal("bounded native query history did not corroborate the controlled source and decisions")
}

func TestDNSDomainDecisionMatrixAndBudget(t *testing.T) {
	if decisionMaximumQueries() != 256 {
		t.Fatal("finite worst-case question budget changed")
	}
	for _, engine := range []Engine{AdGuard, PiHole} {
		for _, phase := range []string{"disabled", "baseline", "added", "removed"} {
			matrix, err := decisionMatrix(engine, "012345abcdef", phase)
			if err != nil || len(matrix) != decisionNameCount {
				t.Fatal(err)
			}
			if phase == "disabled" {
				for _, c := range matrix {
					if c.blocked {
						t.Fatal("disabled protection invented a block")
					}
				}
			} else if phase == "added" {
				if !matrix[0].blocked || matrix[1].blocked || !matrix[3].blocked || matrix[5].blocked || matrix[6].blocked || matrix[7].blocked || matrix[2].blocked != (engine == PiHole) || matrix[4].blocked != (engine == AdGuard) {
					t.Fatal("suffix/exact/empty-group/neutral decisions conflated")
				}
			} else if !matrix[0].blocked || !matrix[1].blocked || !matrix[2].blocked || matrix[3].blocked || matrix[4].blocked {
				t.Fatal("baseline/removal causality differs")
			}
		}
	}
	for _, input := range []struct {
		engine       Engine
		nonce, phase string
	}{{Technitium, "012345abcdef", "added"}, {AdGuard, "../bad", "added"}, {PiHole, "012345abcdef", "unknown"}} {
		if _, err := decisionMatrix(input.engine, input.nonce, input.phase); err == nil {
			t.Fatal("unsupported fixture input accepted")
		}
	}
}

func TestDNSDomainDecisionDefaultClientRefusesUnknown(t *testing.T) {
	groups := []map[string]json.RawMessage{{"id": json.RawMessage("0"), "enabled": json.RawMessage("true")}}
	clients := map[string]json.RawMessage{"clients": json.RawMessage(`[{"client":"198.51.100.77","groups":[0],"comment":"owned unselected client comment"}]`)}
	if err := decisionClientScope(PiHole, clients, groups, "172.20.0.3", "02:00:00:00:00:03"); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`null`, `[null]`, `[]`, `[{"client":"172.20.0.3","groups":[0],"comment":"owned unselected client comment"}]`, `[{"client":"198.51.100.77","groups":[null],"comment":"owned unselected client comment"}]`, `[{"client":"198.51.100.77","groups":[],"comment":null}]`} {
		if decisionClientScope(PiHole, map[string]json.RawMessage{"clients": json.RawMessage(body)}, groups, "172.20.0.3", "02:00:00:00:00:03") == nil {
			t.Fatal("unproved default client accepted", body)
		}
	}
	for _, id := range []string{"null", `"0"`, "1"} {
		bad := []map[string]json.RawMessage{{"id": json.RawMessage(id), "enabled": json.RawMessage("true")}}
		if decisionClientScope(PiHole, clients, bad, "172.20.0.3", "02:00:00:00:00:03") == nil {
			t.Fatal("unproved group zero accepted", id)
		}
	}
}

func TestDNSDomainDecisionAdGuardEmptyClientWriter(t *testing.T) {
	for _, body := range []string{`null`, `[]`, ` null `} {
		t.Run(body, func(t *testing.T) {
			clients := map[string]json.RawMessage{"clients": json.RawMessage(body), "auto_clients": json.RawMessage(`[{"ip":"172.20.0.3","name":"runtime-only"}]`)}
			if err := decisionClientScope(AdGuard, clients, nil, "172.20.0.3", "02:00:00:00:00:03"); err != nil {
				t.Fatal("pinned writer's empty persistent inventory was refused", err)
			}
			if string(clients["clients"]) != body {
				t.Fatal("raw persistent-client evidence was replaced")
			}
		})
	}
	for _, body := range []string{"", `{}`, `true`, `"null"`, `[null]`, `[{"ids":["172.20.0.3"]}]`} {
		if decisionClientScope(AdGuard, map[string]json.RawMessage{"clients": json.RawMessage(body)}, nil, "172.20.0.3", "02:00:00:00:00:03") == nil {
			t.Fatal("unknown or nonempty persistent-client evidence accepted", body)
		}
	}
	if decisionClientScope(AdGuard, map[string]json.RawMessage{"auto_clients": json.RawMessage(`[]`)}, nil, "172.20.0.3", "02:00:00:00:00:03") == nil {
		t.Fatal("missing persistent-client field became empty")
	}
}

func TestDNSDomainDecisionSeedPreservesOpaquePolicy(t *testing.T) {
	parent := "seed-012345abcdef.invalid"
	rules := []string{"# untouched comment", "", "||" + parent + "^", "@@||allow." + parent + "^", "||deny-012345abcdef.invalid^"}
	raw, _ := json.Marshal(rules)
	inv := decisionInventory{Filters: domainFilterInventory{Rules: rules, Native: map[string]json.RawMessage{"user_rules": raw, "opaque": json.RawMessage(`{"nested":true}`)}}, Clients: json.RawMessage("[]")}
	filtered := stripDecisionSelections(AdGuard, parent, inv)
	if !reflect.DeepEqual(filtered.Filters.Rules, rules[:3]) || string(filtered.Filters.Native["opaque"]) != `{"nested":true}` || len(inv.Filters.Rules) != 5 || string(inv.Filters.Native["user_rules"]) != string(raw) {
		t.Fatal("selection subtraction changed literal opaque input or original inventory")
	}
	for _, result := range []decisionResult{{Type: "A", Address: "198.51.100.23"}, {Type: "AAAA", Address: "2001:db8::23"}} {
		if !decisionMatches(result, false) || decisionMatches(result, true) {
			t.Fatal("positive upstream and null-IP denial conflated", fmt.Sprint(result))
		}
	}
}

func TestDNSDomainDecisionHistoryDistinguishesPriorAndSelectedMatches(t *testing.T) {
	for _, engine := range []Engine{AdGuard, PiHole} {
		t.Run(string(engine), func(t *testing.T) {
			allow, deny, address := "allow.seed-012345abcdef.invalid", "deny-012345abcdef.invalid", "172.20.0.3"
			phaseStart := time.Now().Add(-time.Second)
			oldTime, currentTime := phaseStart.Add(-time.Second), phaseStart.Add(500*time.Millisecond)
			nativeTime := func(at time.Time) any {
				if engine == AdGuard {
					return at.UTC().Format(time.RFC3339Nano)
				}
				return float64(at.UnixNano()) / 1e9
			}
			var mu sync.Mutex
			rows := []any{}
			if engine == AdGuard {
				rows = append(rows, map[string]any{"client": address, "reason": "NotFilteredNotFound", "question": map[string]any{"name": allow, "type": "A"}, "client_proto": "", "rules": []any{}})
				for _, kind := range []string{"A", "AAAA"} {
					rows = append(rows, map[string]any{"client": address, "reason": "NotFilteredWhiteList", "question": map[string]any{"name": allow, "type": kind}, "client_proto": "", "rules": []any{map[string]any{"text": "@@||" + allow + "^", "filter_list_id": 0}}}, map[string]any{"client": address, "reason": "FilteredBlackList", "question": map[string]any{"name": deny, "type": kind}, "client_proto": "", "rules": []any{map[string]any{"text": "||" + deny + "^", "filter_list_id": 0}}})
				}
			} else {
				rows = append(rows, map[string]any{"client": map[string]any{"ip": address}, "status": "FORWARDED", "domain": allow, "type": "A", "list_id": nil}, map[string]any{"client": map[string]any{"ip": address}, "status": nil, "domain": allow, "type": "A", "list_id": nil})
				for _, kind := range []string{"A", "AAAA"} {
					rows = append(rows, map[string]any{"client": map[string]any{"ip": address}, "status": "FORWARDED", "domain": allow, "type": kind, "list_id": 7}, map[string]any{"client": map[string]any{"ip": address}, "status": "DENYLIST", "domain": deny, "type": kind, "list_id": 8})
				}
			}
			for _, row := range rows {
				row.(map[string]any)["time"] = nativeTime(currentTime)
			}
			rows[0].(map[string]any)["time"] = nativeTime(oldTime)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				if r.Method != http.MethodGet {
					t.Error("query corroboration acquired mutation authority")
				}
				var result any
				switch r.URL.Path {
				case "/control/querylog":
					if r.URL.RawQuery != "limit=100" {
						t.Error("history row bound changed")
					}
					result = map[string]any{"data": rows}
				case "/api/queries":
					if r.URL.Query().Get("length") != "100" || r.URL.Query().Get("client_ip") != address {
						t.Error("history bound/source changed")
					}
					result = map[string]any{"queries": rows}
				case "/api/info/version":
					result = map[string]any{"version": map[string]any{"ftl": map[string]any{"local": map[string]any{"version": "v6.7.1"}}}}
				case "/api/domains":
					result = map[string]any{"domains": []any{domainRuleRow(allow, "allow", 7), domainRuleRow(deny, "deny", 8)}}
				case "/api/lists":
					result = map[string]any{"lists": []any{}}
				case "/api/groups":
					result = map[string]any{"groups": []any{map[string]any{"id": 0, "enabled": true, "name": "Default"}}}
				default:
					t.Error("unexpected native history path", r.URL.Path)
				}
				json.NewEncoder(w).Encode(result)
			}))
			defer server.Close()
			credential := Credential{Password: "private-fixture-password"}
			if engine == AdGuard {
				credential.Username = "admin"
			}
			client, err := newNativeClient(ConnectionRequest{Name: "Native history corroboration", Engine: engine, Endpoint: server.URL, Credential: credential})
			if err != nil {
				t.Fatal(err)
			}
			defer client.close()
			client.sid = "owned-test-session"
			if ok, err := decisionHistory(t.Context(), client, engine, address, allow, deny, phaseStart); err != nil || !ok {
				t.Fatal("controlled native decisions did not survive valid prior-phase rows", ok, err)
			}

			validPhaseRows := rows
			mu.Lock()
			for _, row := range rows {
				row.(map[string]any)["time"] = nativeTime(oldTime)
			}
			mu.Unlock()
			if ok, err := decisionHistory(t.Context(), client, engine, address, allow, deny, phaseStart); err != nil || ok {
				t.Fatal("old same-rule-ID/status rows were reused as current-phase proof", ok, err)
			}
			mu.Lock()
			for _, row := range validPhaseRows {
				row.(map[string]any)["time"] = nativeTime(currentTime)
			}
			rows = validPhaseRows
			mu.Unlock()
			if ok, err := decisionHistory(t.Context(), client, engine, address, allow, deny, phaseStart); err != nil || !ok {
				t.Fatal("actual current-phase same-ID decisions were withheld", ok, err)
			}
			if engine == AdGuard {
				for _, test := range []struct {
					name    string
					present bool
					value   any
				}{
					{"missing", false, nil}, {"null", true, nil}, {"number", true, 0},
					{"boolean", true, false}, {"object", true, map[string]any{}}, {"array", true, []any{}},
					{"udp_is_not_encryption_enum", true, "udp"}, {"tcp_is_not_encryption_enum", true, "tcp"},
					{"doh", true, "doh"}, {"dot", true, "dot"}, {"doq", true, "doq"},
					{"dnscrypt", true, "dnscrypt"}, {"unknown", true, "plain"},
				} {
					t.Run(test.name, func(t *testing.T) {
						mu.Lock()
						selected := rows[1].(map[string]any)
						if test.present {
							selected["client_proto"] = test.value
						} else {
							delete(selected, "client_proto")
						}
						mu.Unlock()
						if ok, err := decisionHistory(t.Context(), client, engine, address, allow, deny, phaseStart); err == nil || ok {
							t.Fatal("missing/malformed/encrypted/transport enum became plain-DNS evidence", ok, err)
						}
						mu.Lock()
						selected["client_proto"] = ""
						mu.Unlock()
					})
				}
				if ok, err := decisionHistory(t.Context(), client, engine, address, allow, deny, phaseStart); err != nil || !ok {
					t.Fatal("explicit pinned empty plain-DNS marker was withheld", ok, err)
				}
			}
			for _, name := range []string{allow, deny} {
				mu.Lock()
				valid := rows
				rows = nil
				for _, row := range valid {
					selected := row.(map[string]any)
					rowName, kind := selected["domain"], selected["type"]
					if engine == AdGuard {
						question := selected["question"].(map[string]any)
						rowName, kind = question["name"], question["type"]
					}
					if rowName != name || kind != "AAAA" {
						rows = append(rows, row)
					}
				}
				mu.Unlock()
				if ok, err := decisionHistory(t.Context(), client, engine, address, allow, deny, phaseStart); err != nil || ok {
					t.Fatal("missing selected AAAA decision became complete A+AAAA evidence", ok, err)
				}
				mu.Lock()
				rows = valid
				mu.Unlock()
			}
			for _, badTime := range []any{nil, true, "malformed", nativeTime(time.Now().Add(time.Hour))} {
				mu.Lock()
				rows[0].(map[string]any)["time"] = badTime
				mu.Unlock()
				if ok, err := decisionHistory(t.Context(), client, engine, address, allow, deny, phaseStart); err == nil || ok {
					t.Fatal("null/malformed/future native time became phase evidence", ok, err)
				}
			}
			mu.Lock()
			rows[0].(map[string]any)["time"] = nativeTime(currentTime)
			mu.Unlock()
			mu.Lock()
			valid := rows
			rows = append(append([]any{}, rows...), nil)
			mu.Unlock()
			if ok, err := decisionHistory(t.Context(), client, engine, address, allow, deny, phaseStart); err == nil || ok {
				t.Fatal("malformed native rows escaped complete history")
			}
			mu.Lock()
			rows = valid[:1]
			mu.Unlock()
			if ok, err := decisionHistory(t.Context(), client, engine, address, allow, deny, phaseStart); err != nil || ok {
				t.Fatal("unmatched/unreported prior decision became selected proof", ok, err)
			}
		})
	}
}

func TestDNSDomainDecisionSidecarRefusesForeignCleanup(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*container.InspectResponse)
	}{
		{"foreign_labels", func(i *container.InspectResponse) { i.Config.Labels[decisionLabel] = "foreign" }},
		{"changed_image", func(i *container.InspectResponse) { i.Image = "sha256:foreign" }},
		{"changed_user", func(i *container.InspectResponse) { i.Config.User = "0" }},
		{"writable_root", func(i *container.InspectResponse) { i.HostConfig.ReadonlyRootfs = false }},
		{"host_mount", func(i *container.InspectResponse) { i.HostConfig.Binds = []string{"/host:/host"} }},
		{"capability", func(i *container.InspectResponse) { i.HostConfig.CapAdd = []string{"NET_ADMIN"} }},
		{"changed_network", func(i *container.InspectResponse) { i.NetworkSettings.Networks["owned"].NetworkID = "foreign" }},
		{"changed_source", func(i *container.InspectResponse) { i.NetworkSettings.Networks["owned"].IPAddress = "172.20.0.9" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			side := &decisionSidecar{nonce: "012345abcdef", digest: strings.Repeat("b", 64), imageID: "sha256:" + strings.Repeat("c", 64), containerID: strings.Repeat("d", 64), networkID: strings.Repeat("e", 64), address: "172.20.0.3", mac: "02:00:00:00:00:03"}
			pids := int64(16)
			info := container.InspectResponse{ContainerJSONBase: &container.ContainerJSONBase{ID: side.containerID, Name: "/jd-dns-decisions-" + side.nonce, Image: side.imageID, State: &container.State{Running: true}, HostConfig: &container.HostConfig{NetworkMode: container.NetworkMode(side.networkID), ReadonlyRootfs: true, CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges:true"}, Resources: container.Resources{Memory: 64 << 20, MemorySwap: 64 << 20, NanoCPUs: 250000000, PidsLimit: &pids}, RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled}}}, Config: &container.Config{Image: side.imageID, User: "65534:65534", Entrypoint: []string{"/dns-fixture"}, Cmd: []string{"serve", side.nonce}, Labels: side.labels()}, NetworkSettings: &container.NetworkSettings{Networks: map[string]*network.EndpointSettings{"owned": {NetworkID: side.networkID, IPAddress: side.address, MacAddress: side.mac}}}}
			var mu sync.Mutex
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Method != http.MethodGet || r.URL.Path != "/v1.51/containers/"+side.containerID+"/json" {
					t.Error("foreign receipt acquired stop/remove/image authority", r.Method, r.URL.Path)
					http.Error(w, "refused", 409)
					return
				}
				json.NewEncoder(w).Encode(info)
			}))
			defer server.Close()
			cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.51"))
			if err != nil {
				t.Fatal(err)
			}
			side.d = &dockerRuntime{cli: cli}
			defer side.d.Close()
			if _, err := side.inspect(t.Context()); err != nil {
				t.Fatal("complete owned sidecar receipt refused", err)
			}
			mu.Lock()
			test.mutate(&info)
			mu.Unlock()
			if err := side.cleanup(t.Context()); err == nil {
				t.Fatal("foreign sidecar was adopted for cleanup")
			}
		})
	}
}

func TestDNSDomainDecisionQueryBudgetAndCancellationPreventExec(t *testing.T) {
	queries := &decisionQueries{count: decisionQueryLimit}
	if _, err := queries.read("never-executed.invalid", "A", "udp"); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatal("exhausted query acquired exec authority", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	queries = &decisionQueries{ctx: ctx, next: time.Now().Add(time.Hour)}
	if _, err := queries.read("never-executed.invalid", "A", "udp"); !errors.Is(err, context.Canceled) || queries.count != 0 {
		t.Fatal("cancelled pacing acquired exec authority or a counted query", err)
	}
}

func TestDNSDomainDecisionBridgeRefusesChangedClientOrEngine(t *testing.T) {
	side := &decisionSidecar{networkID: strings.Repeat("a", 64), containerID: strings.Repeat("b", 64), engineID: strings.Repeat("c", 64), address: "172.20.0.3", mac: "02:00:00:00:00:03", engineAddress: "172.20.0.2", engineMAC: "02:00:00:00:00:02", spec: provisionSpec{ID: "owned-provision", Owner: "owned-account"}}
	baseline := network.Inspect{ID: side.networkID, Driver: "bridge", Scope: "local", Labels: ownedLabels(side.spec), Containers: map[string]network.EndpointResource{side.engineID: {IPv4Address: side.engineAddress + "/16", MacAddress: side.engineMAC}, side.containerID: {IPv4Address: side.address + "/16", MacAddress: side.mac}}}
	if err := side.bridgeIdentity(baseline); err != nil {
		t.Fatal("complete ordinary owned two-endpoint bridge refused", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*network.Inspect)
	}{
		{"internal_bridge", func(n *network.Inspect) { n.Internal = true }},
		{"foreign_owner", func(n *network.Inspect) { n.Labels[dnsOwnerLabel] = "foreign" }},
		{"extra_endpoint", func(n *network.Inspect) { n.Containers["foreign"] = network.EndpointResource{} }},
		{"missing_engine", func(n *network.Inspect) { delete(n.Containers, side.engineID) }},
		{"changed_engine_ip", func(n *network.Inspect) {
			e := n.Containers[side.engineID]
			e.IPv4Address = "172.20.0.8/16"
			n.Containers[side.engineID] = e
		}},
		{"changed_engine_mac", func(n *network.Inspect) {
			e := n.Containers[side.engineID]
			e.MacAddress = side.mac
			n.Containers[side.engineID] = e
		}},
		{"changed_client_ip", func(n *network.Inspect) {
			e := n.Containers[side.containerID]
			e.IPv4Address = "172.20.0.9/16"
			n.Containers[side.containerID] = e
		}},
		{"changed_client_mac", func(n *network.Inspect) {
			e := n.Containers[side.containerID]
			e.MacAddress = side.engineMAC
			n.Containers[side.containerID] = e
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			body, _ := json.Marshal(baseline)
			var changed network.Inspect
			if err := json.Unmarshal(body, &changed); err != nil {
				t.Fatal(err)
			}
			test.mutate(&changed)
			if err := side.bridgeIdentity(changed); err == nil {
				t.Fatal("changed endpoint or bridge gained query authority")
			}
		})
	}
	if _, err := side.query(t.Context(), "172.20.0.99", "neutral-012345abcdef.invalid", "A", "udp", 1); err == nil {
		t.Fatal("foreign target reached Docker inspection/exec")
	}
}

func decisionMockImage(side *decisionSidecar, id string) map[string]any {
	return map[string]any{"Id": id, "Parent": "", "RepoTags": []string{side.tag}, "Config": map[string]any{"Labels": side.labels(), "User": "65534:65534", "WorkingDir": "/", "Entrypoint": []string{"/dns-fixture"}, "Cmd": []string{"serve", side.nonce}}, "Os": "linux", "Architecture": "amd64", "Size": 123, "RootFS": map[string]any{"Type": "layers", "Layers": []string{side.layerID}}}
}

func TestDNSDomainDecisionRootFSHasOnlyFrozenRegularExecutable(t *testing.T) {
	helper := []byte("controlled frozen helper bytes")
	digest := sha256.Sum256(helper)
	body, layer, err := decisionRootFS(helper, hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(body)
	if layer != "sha256:"+hex.EncodeToString(hash[:]) {
		t.Fatal("rootfs layer is not bound to exact tar bytes")
	}
	reader := tar.NewReader(bytes.NewReader(body))
	header, err := reader.Next()
	if err != nil || header.Name != "dns-fixture" || header.Typeflag != tar.TypeReg || header.Mode != 0555 || header.Uid != 0 || header.Gid != 0 || header.Size != int64(len(helper)) || header.Linkname != "" || len(header.PAXRecords) != 0 {
		t.Fatal("rootfs is not one fixed regular executable", header, err)
	}
	actual, err := io.ReadAll(reader)
	if err != nil || !bytes.Equal(actual, helper) {
		t.Fatal("frozen helper bytes changed", err)
	}
	if _, err = reader.Next(); err != io.EOF {
		t.Fatal("rootfs acquired an extra file/link/directory", err)
	}
	for _, candidate := range [][]byte{nil, []byte("foreign bytes")} {
		if _, _, err := decisionRootFS(candidate, hex.EncodeToString(digest[:])); err == nil {
			t.Fatal("changed/empty helper acquired import authority")
		}
	}
}

func TestDNSDomainDecisionImportRefusesMalformedAndSpoofedReply(t *testing.T) {
	resultID := "sha256:" + strings.Repeat("a", 64)
	complete := fmt.Sprintf("{\"status\":%q}\n", resultID)
	if got, err := decisionImportID([]byte(complete)); err != nil || got != resultID {
		t.Fatal("closed full imported ID refused", got, err)
	}
	for _, body := range []string{"null", "[]", "{}", `{"status":null}`, `{"status":42}`, `{"status":"sha256:bad"}`, `{"error":"refused"}`, complete + complete, fmt.Sprintf(`{"status":%q,"error":"spoof"}`, resultID), fmt.Sprintf(`{"status":"sha256:bad","status":%q}`, resultID), fmt.Sprintf(`{"Status":%q}`, resultID)} {
		if got, err := decisionImportID([]byte(body)); err == nil || got != "" {
			t.Fatal("malformed/duplicate/error reply acquired imported image identity", got)
		}
	}
}

func TestDNSDomainDecisionImportBindsLocalTarAndRefusesReboundTag(t *testing.T) {
	for _, rebound := range []bool{false, true} {
		t.Run(fmt.Sprintf("rebound=%t", rebound), func(t *testing.T) {
			resultID := "sha256:" + strings.Repeat("a", 64)
			helper := []byte("controlled mock payload")
			hash := sha256.Sum256(helper)
			side := &decisionSidecar{nonce: "012345abcdef", digest: hex.EncodeToString(hash[:]), tag: "jd-dns-decisions:012345abcdef"}
			archive, layer, err := decisionRootFS(helper, side.digest)
			if err != nil {
				t.Fatal(err)
			}
			side.layerID = layer
			inspectCalls, importCalls := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && r.URL.Path == "/v1.51/images/create" {
					importCalls++
					params := r.URL.Query()
					expected := []string{"USER 65534:65534", "WORKDIR /", `ENTRYPOINT ["/dns-fixture"]`, `CMD ["serve","012345abcdef"]`, fmt.Sprintf("LABEL %s=%s io.justdashboard.dns.decision-helper=%s", decisionLabel, side.nonce, side.digest)}
					if params.Get("fromSrc") != "-" || params.Get("fromImage") != "" || params.Get("repo") != side.tag || params.Get("platform") != "linux/amd64" || params.Get("message") != "Owned finite DNS decision fixture" || !reflect.DeepEqual(params["changes"], expected) || r.Header.Get("X-Registry-Auth") != "" {
						t.Error("import acquired remote/pull/arbitrary configuration authority", params)
					}
					body, err := io.ReadAll(io.LimitReader(r.Body, int64(len(archive)+1)))
					if err != nil || !bytes.Equal(body, archive) {
						t.Error("Docker import did not receive the exact one-entry rootfs", err)
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, "{\"status\":%q}\n", resultID)
					return
				}
				if r.Method != http.MethodGet || r.URL.Path != "/v1.51/images/"+side.tag+"/json" {
					t.Error("changed import tag acquired unexpected native authority", r.Method, r.URL.Path)
					http.Error(w, "refused", 409)
					return
				}
				inspectCalls++
				if inspectCalls == 1 {
					w.WriteHeader(http.StatusNotFound)
					fmt.Fprint(w, `{"message":"not found"}`)
					return
				}
				// Exact layers/configuration cannot authorize adoption of a different returned image ID.
				id := resultID
				if rebound {
					id = "sha256:" + strings.Repeat("b", 64)
				}
				json.NewEncoder(w).Encode(decisionMockImage(side, id))
			}))
			defer server.Close()
			cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.51"))
			if err != nil {
				t.Fatal(err)
			}
			side.d = &dockerRuntime{cli: cli}
			defer side.d.Close()
			err = side.importHelper(t.Context(), helper)
			if inspectCalls != 2 || importCalls != 1 || rebound && (err == nil || side.imageID != "") || !rebound && (err != nil || side.imageID != resultID || side.layerID != layer) {
				t.Fatal("completed image identity was not exactly bound before adoption", err, side.imageID, inspectCalls, importCalls)
			}
		})
	}
}

func TestDNSDomainDecisionForeignImageNeverAcquiresCleanupAuthority(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"ID", func(i map[string]any) { i["Id"] = "sha256:foreign" }},
		{"tag", func(i map[string]any) { i["RepoTags"] = []string{"foreign:latest"} }},
		{"parent", func(i map[string]any) { i["Parent"] = "sha256:foreign" }},
		{"layer", func(i map[string]any) { i["RootFS"].(map[string]any)["Layers"] = []string{"sha256:foreign"} }},
		{"labels", func(i map[string]any) {
			i["Config"].(map[string]any)["Labels"] = map[string]string{decisionLabel: "foreign"}
		}},
		{"user", func(i map[string]any) { i["Config"].(map[string]any)["User"] = "0" }},
		{"entrypoint", func(i map[string]any) { i["Config"].(map[string]any)["Entrypoint"] = []string{"/foreign"} }},
		{"command", func(i map[string]any) { i["Config"].(map[string]any)["Cmd"] = []string{"serve", "foreign"} }},
		{"volume", func(i map[string]any) {
			i["Config"].(map[string]any)["Volumes"] = map[string]any{"/foreign": map[string]any{}}
		}},
		{"architecture", func(i map[string]any) { i["Architecture"] = "arm64" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			side := &decisionSidecar{nonce: "012345abcdef", digest: strings.Repeat("a", 64), tag: "jd-dns-decisions:012345abcdef", imageID: "sha256:" + strings.Repeat("b", 64), layerID: "sha256:" + strings.Repeat("c", 64)}
			info := decisionMockImage(side, side.imageID)
			var mu sync.Mutex
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Method != http.MethodGet || r.URL.Path != "/v1.51/images/"+side.imageID+"/json" {
					t.Error("foreign image acquired remove/prune authority", r.Method, r.URL.Path)
					http.Error(w, "refused", 409)
					return
				}
				json.NewEncoder(w).Encode(info)
			}))
			defer server.Close()
			cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.51"))
			if err != nil {
				t.Fatal(err)
			}
			side.d = &dockerRuntime{cli: cli}
			defer side.d.Close()
			valid, err := cli.ImageInspect(t.Context(), side.imageID)
			if err != nil || side.imageIdentity(valid, side.imageID) != nil {
				t.Fatal("complete captured image receipt refused", err)
			}
			mu.Lock()
			test.mutate(info)
			mu.Unlock()
			if err := side.cleanup(t.Context()); err == nil {
				t.Fatal("foreign image was adopted for cleanup")
			}
		})
	}
}

func TestDNSDomainDecisionHistoryPhaseRequiresActualBoundedTimes(t *testing.T) {
	start := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	now := start.Add(time.Second)
	for _, engine := range []Engine{AdGuard, PiHole} {
		native := func(at time.Time) json.RawMessage {
			var value any = at.UTC().Format(time.RFC3339Nano)
			if engine == PiHole {
				value = float64(at.UnixNano()) / 1e9
			}
			raw, _ := json.Marshal(value)
			return raw
		}
		for _, test := range []struct {
			at   time.Time
			want bool
		}{{start.Add(-time.Nanosecond * 1000), false}, {start, true}, {start.Add(500 * time.Millisecond), true}, {now, true}} {
			ok, err := decisionHistoryPhase(engine, native(test.at), start, now)
			if err != nil || ok != test.want {
				t.Fatal("actual phase/read interval misclassified", engine, test.at, ok, err)
			}
		}
		bad := []json.RawMessage{nil, json.RawMessage("null"), json.RawMessage("true"), json.RawMessage("{}"), json.RawMessage(`""`), json.RawMessage(`"malformed"`), native(now.Add(time.Second))}
		if engine == PiHole {
			bad = append(bad, json.RawMessage("-1"), json.RawMessage("1e1000"), json.RawMessage(`"1791532800"`))
		}
		for _, raw := range bad {
			if ok, err := decisionHistoryPhase(engine, raw, start, now); err == nil || ok {
				t.Fatal("missing/null/malformed/negative/overflow/future time admitted", engine, string(raw), ok, err)
			}
		}
		if engine == PiHole {
			if ok, err := decisionHistoryPhase(engine, json.RawMessage("0"), start, now); err != nil || ok {
				t.Fatal("explicit epoch zero became a current phase", ok, err)
			}
		}
		if ok, err := decisionHistoryPhase(engine, native(start), time.Time{}, now); err == nil || ok {
			t.Fatal("missing actual phase boundary accepted")
		}
		if ok, err := decisionHistoryPhase(engine, native(start), now.Add(time.Second), now); err == nil || ok {
			t.Fatal("reversed phase/read boundary accepted")
		}
	}
}
