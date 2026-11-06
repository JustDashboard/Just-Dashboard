package accountlink

import (
	"encoding/json"
	"strings"
	"testing"
)

// Making the engine run as more than one instance must not move a single key an
// installed account link has written: an upgrade would otherwise forget the
// link, its identity or its sequence.
func TestTheAccountLinkKeepsItsKeysWhereEarlierReleasesWroteThem(t *testing.T) {
	e := newEnv(t)
	e.link(ScopeOperate)
	rows, err := e.st.DB.Query(`SELECT key FROM settings WHERE key LIKE '%.link.%' ORDER BY key`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k)
	}
	if got := strings.Join(keys, ","); got != "account.link.identity,account.link.sequence,account.link.state" {
		t.Fatalf("keys after a link = %s", got)
	}
}

func TestExtraHeartbeatKeysAreAddedWholeAndNeverReplaceTheSnapshots(t *testing.T) {
	snap := Snapshot{Protocol: 1, SentAt: 5, Server: ServerInfo{Name: "box", Version: "0.8.5", Scope: ScopeObserve}}
	body, err := snap.encodeWith(map[string]json.RawMessage{"signIn": json.RawMessage(`{"grants":[]}`)})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("not one JSON object: %s", body)
	}
	if string(m["signIn"]) != `{"grants":[]}` || string(m["protocol"]) != "1" {
		t.Fatalf("body = %s", body)
	}
	plain, _ := snap.encode()
	if !strings.HasPrefix(string(body), strings.TrimSuffix(string(plain), "}")) {
		t.Fatalf("the snapshot changed when a key was added:\n%s\n%s", plain, body)
	}
	if _, err := snap.encodeWith(map[string]json.RawMessage{"server": json.RawMessage(`{}`)}); err == nil {
		t.Fatal("a key that would shadow the snapshot's own was accepted")
	}

	// The lists make room for the extra key; the key itself is never cut.
	big := Snapshot{Protocol: 1, Containers: &ContainersInfo{}}
	for i := 0; i < 200; i++ {
		big.Containers.List = append(big.Containers.List, ContainerInfo{ID: strings.Repeat("a", 12), Name: strings.Repeat("n", 200), Image: strings.Repeat("i", 200), State: "running"})
	}
	extra := json.RawMessage(`"` + strings.Repeat("x", 20<<10) + `"`)
	body, err = big.encodeWith(map[string]json.RawMessage{"signIn": extra})
	if err != nil || len(body) > maxRequestBody-1024 {
		t.Fatalf("body is %d bytes, %v", len(body), err)
	}
	if err := json.Unmarshal(body, &m); err != nil || string(m["signIn"]) != string(extra) {
		t.Fatal("the extra key was cut or the body is not JSON")
	}
}
