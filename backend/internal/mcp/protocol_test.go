package mcp

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func headers(pairs ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(pairs); i += 2 {
		h.Set(pairs[i], pairs[i+1])
	}
	return h
}

func modernBody(method, extra string) string {
	params := `"_meta": {"io.modelcontextprotocol/protocolVersion": "2026-07-28",
		"io.modelcontextprotocol/clientInfo": {"name": "claude-code", "version": "2.4.0"},
		"io.modelcontextprotocol/clientCapabilities": {}}`
	if extra != "" {
		params += ", " + extra
	}
	return `{"jsonrpc": "2.0", "id": 7, "method": "` + method + `", "params": {` + params + `}}`
}

func TestParseReadsOneMessageAndRefusesTheRest(t *testing.T) {
	for _, c := range []struct {
		body string
		code int
	}{
		{``, CodeInvalidRequest},
		{`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, CodeInvalidRequest},
		{`{"jsonrpc":"2.0","id":1,"method":`, CodeParseError},
		{`{"jsonrpc":"1.0","id":1,"method":"ping"}`, CodeInvalidRequest},
		{`{"jsonrpc":"2.0","id":null,"method":"ping"}`, CodeInvalidRequest},
		{`{"jsonrpc":"2.0","id":1.5,"method":"ping"}`, CodeInvalidRequest},
		{`{"jsonrpc":"2.0","id":1}`, CodeInvalidRequest},
		{`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":[1]}`, CodeInvalidParams},
	} {
		if _, err := Parse([]byte(c.body)); err == nil || err.Code != c.code || err.Status != http.StatusBadRequest {
			t.Errorf("%s: %+v, want 400 %d", c.body, err, c.code)
		}
	}

	req, err := Parse([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	if err != nil || !req.Notification {
		t.Errorf("a notification = %+v %v", req, err)
	}
	req, err = Parse([]byte(`{"jsonrpc":"2.0","id":"a","result":{}}`))
	if err != nil || !req.Reply {
		t.Errorf("a client's reply = %+v %v", req, err)
	}
	req, err = Parse([]byte(`{"jsonrpc":"2.0","id":"call-1","method":"tools/call","params":{"name":"run_query","arguments":{"sql":"SELECT 1"}}}`))
	if err != nil || string(req.ID) != `"call-1"` || req.Name != "run_query" || string(req.Arguments) != `{"sql":"SELECT 1"}` {
		t.Errorf("a tools/call = %+v %v", req, err)
	}
}

func TestTheLegacyHandshakeSettlesOnAVersionWeSpeak(t *testing.T) {
	for _, c := range []struct{ asked, want string }{
		{"2025-11-25", "2025-11-25"},
		{"2025-06-18", "2025-06-18"},
		{"2025-03-26", "2025-03-26"},
		// Older than any we speak, or not a version at all: the newest
		// legacy one, which the client may take or leave.
		{"2024-11-05", "2025-11-25"},
		{"tomorrow", "2025-11-25"},
	} {
		req, perr := Parse([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + c.asked +
			`","capabilities":{},"clientInfo":{"name":"cursor","version":"1.7"}}}`))
		if perr != nil {
			t.Fatal(perr)
		}
		call, err := Settle(req, http.Header{})
		if err != nil || call.Version != c.want || call.Modern {
			t.Errorf("initialize asking for %s settled on %+v %v", c.asked, call, err)
			continue
		}
		result := call.Initialize(Instructions(Offer{Databases: true}))
		if result["protocolVersion"] != c.want || req.Client.Name != "cursor" {
			t.Errorf("initialize answered %+v", result)
		}
		if _, ok := result["resultType"]; ok {
			t.Error("a legacy answer carries resultType")
		}
	}
}

func TestALegacyRequestIsReadByItsHeader(t *testing.T) {
	req, _ := Parse([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	for _, c := range []struct {
		header string
		want   string
		code   int
	}{
		{"", "2025-03-26", 0},
		{"2025-06-18", "2025-06-18", 0},
		{"2025-11-25", "2025-11-25", 0},
		{"2024-11-05", "", CodeUnsupportedVersion},
		// A modern version with nothing in _meta is a modern request missing
		// what every modern request carries.
		{"2026-07-28", "", CodeInvalidParams},
	} {
		call, err := Settle(req, headers("MCP-Protocol-Version", c.header))
		switch {
		case c.code != 0 && (err == nil || err.Code != c.code || err.Status != http.StatusBadRequest):
			t.Errorf("header %q: %+v, want 400 %d", c.header, err, c.code)
		case c.code == 0 && (err != nil || call.Version != c.want || call.Modern):
			t.Errorf("header %q: %+v %v, want %s", c.header, call, err, c.want)
		}
	}
}

func TestAModernRequestIsHeldToItsHeaders(t *testing.T) {
	call := modernBody("tools/call", `"name": "run_query", "arguments": {}`)
	good := []string{"MCP-Protocol-Version", Modern, "Mcp-Method", "tools/call", "Mcp-Name", "run_query"}
	req, perr := Parse([]byte(call))
	if perr != nil {
		t.Fatal(perr)
	}
	settled, err := Settle(req, headers(good...))
	if err != nil || !settled.Modern || settled.Version != Modern || req.Client.Name != "claude-code" {
		t.Fatalf("a well-formed modern call = %+v %v", settled, err)
	}
	// A name sent Base64-encoded is the same name.
	encoded := headers("MCP-Protocol-Version", Modern, "Mcp-Method", "tools/call", "Mcp-Name", "=?base64?cnVuX3F1ZXJ5?=")
	if _, err := Settle(req, encoded); err != nil {
		t.Errorf("an encoded Mcp-Name was refused: %v", err)
	}
	for name, h := range map[string]http.Header{
		"no version header":    headers("Mcp-Method", "tools/call", "Mcp-Name", "run_query"),
		"another version":      headers("MCP-Protocol-Version", "2025-11-25", "Mcp-Method", "tools/call", "Mcp-Name", "run_query"),
		"no method header":     headers("MCP-Protocol-Version", Modern, "Mcp-Name", "run_query"),
		"another method":       headers("MCP-Protocol-Version", Modern, "Mcp-Method", "tools/list", "Mcp-Name", "run_query"),
		"no name header":       headers("MCP-Protocol-Version", Modern, "Mcp-Method", "tools/call"),
		"another tool":         headers("MCP-Protocol-Version", Modern, "Mcp-Method", "tools/call", "Mcp-Name", "run_write"),
		"a name that is noise": headers("MCP-Protocol-Version", Modern, "Mcp-Method", "tools/call", "Mcp-Name", "=?base64?%%%?="),
	} {
		if _, err := Settle(req, h); err == nil || err.Code != CodeHeaderMismatch || err.Status != http.StatusBadRequest {
			t.Errorf("%s: %+v, want 400 HeaderMismatch", name, err)
		}
	}

	future, _ := Parse([]byte(strings.Replace(call, "2026-07-28", "2027-01-01", 1)))
	_, err = Settle(future, headers("MCP-Protocol-Version", "2027-01-01", "Mcp-Method", "tools/call", "Mcp-Name", "run_query"))
	if err == nil || err.Code != CodeUnsupportedVersion || err.Status != http.StatusBadRequest {
		t.Fatalf("an unknown modern version = %+v", err)
	}
	data, _ := err.Data.(map[string]any)
	if !reflect.DeepEqual(data["supported"], Supported()) || data["requested"] != "2027-01-01" {
		t.Errorf("the refusal lists %+v", data)
	}

	bare, _ := Parse([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`))
	if _, err := Settle(bare, headers("MCP-Protocol-Version", Modern, "Mcp-Method", "tools/list")); err == nil || err.Code != CodeInvalidParams {
		t.Errorf("a modern request without client capabilities = %+v", err)
	}
}

func TestAModernAnswerSaysItIsCompleteAndMayNotBeShared(t *testing.T) {
	req, _ := Parse([]byte(modernBody("server/discover", "")))
	call, err := Settle(req, headers("MCP-Protocol-Version", Modern, "Mcp-Method", "server/discover"))
	if err != nil {
		t.Fatal(err)
	}
	discover := call.Discover(Instructions(Offer{Databases: true}))
	if discover["resultType"] != "complete" || discover["cacheScope"] != "private" || discover["ttlMs"] == nil ||
		!reflect.DeepEqual(discover["supportedVersions"], Supported()) {
		t.Fatalf("server/discover = %+v", discover)
	}
	meta, _ := discover["_meta"].(map[string]any)
	if server, _ := meta["io.modelcontextprotocol/serverInfo"].(Implementation); server.Name != "just-dashboard" {
		t.Errorf("the server is not named: %+v", meta)
	}

	legacy := &Call{Request: &Request{Method: "tools/list"}, Version: "2025-11-25"}
	listed := legacy.Cached(map[string]any{"tools": Tools(Offer{Databases: true, Query: true, Write: true})}, 60_000)
	for _, key := range []string{"resultType", "ttlMs", "cacheScope", "_meta"} {
		if _, ok := listed[key]; ok {
			t.Errorf("a legacy list carries %s", key)
		}
	}
}

func TestAnUnknownMethodIsA404OnlyToAModernClient(t *testing.T) {
	modern := &Call{Request: &Request{Method: "resources/list"}, Modern: true}
	legacy := &Call{Request: &Request{Method: "resources/list"}}
	if err := modern.MethodNotFound(); err.Status != http.StatusNotFound || err.Code != CodeMethodNotFound {
		t.Errorf("modern = %+v", err)
	}
	if err := legacy.MethodNotFound(); err.Status != http.StatusOK || err.Code != CodeMethodNotFound {
		t.Errorf("legacy = %+v", err)
	}
}

func TestResponsesAreJSONRPC(t *testing.T) {
	var ok map[string]any
	if err := json.Unmarshal(Response(json.RawMessage(`3`), map[string]any{}), &ok); err != nil || ok["jsonrpc"] != "2.0" || ok["id"] != float64(3) {
		t.Errorf("response = %+v %v", ok, err)
	}
	var failed map[string]any
	_ = json.Unmarshal(ErrorResponse(nil, &Error{Code: CodeParseError, Message: "no"}), &failed)
	if _, has := failed["id"]; has || failed["error"].(map[string]any)["code"] != float64(CodeParseError) {
		t.Errorf("an error with no id = %+v", failed)
	}
}
