package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// A generated tool is built from an operation of the OpenAPI document, its
// arguments become the request the route takes, and the route's answer
// becomes a result the model can read, bounded.

var fixtureDocument = map[string]any{
	"components": map[string]any{"schemas": map[string]any{
		"deploy.VariableWrite": map[string]any{"type": "object", "title": "deploy.VariableWrite", "properties": map[string]any{
			"value":     map[string]any{"type": "string", "writeOnly": true},
			"reference": map[string]any{"allOf": []any{map[string]any{"$ref": "#/components/schemas/deploy.Reference"}}},
		}},
		"deploy.Reference": map[string]any{"type": "object", "title": "deploy.Reference", "properties": map[string]any{
			"kind": map[string]any{"type": "string"},
			"next": map[string]any{"oneOf": []any{map[string]any{"$ref": "#/components/schemas/deploy.Reference"}, map[string]any{"type": "null"}}},
		}},
	}},
}

func fixtureOperation(t *testing.T) Operation {
	t.Helper()
	item := map[string]any{"parameters": []any{
		map[string]any{"name": "id", "in": "path", "required": true, "description": "The deployment's id.", "schema": map[string]any{"type": "integer", "format": "int64"}},
		map[string]any{"name": "name", "in": "path", "required": true, "schema": map[string]any{"type": "string"}},
		map[string]any{"name": "env", "in": "path", "required": true, "schema": map[string]any{"type": "integer"}},
	}}
	op := map[string]any{
		"operationId":  "deploymentVariablePut",
		"summary":      "Set a variable's value: or its reference.",
		"x-deploy-key": "allowed",
		"parameters":   []any{map[string]any{"name": "dryRun", "in": "query", "schema": map[string]any{"type": "boolean"}}},
		"requestBody": map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{
			"schema": map[string]any{"$ref": "#/components/schemas/deploy.VariableWrite"},
		}}},
	}
	hints := func(pattern, name string) string {
		if name == "id" {
			return "list_deployments gives it."
		}
		return ""
	}
	out, err := NewOperation(fixtureDocument, "deployment_variable_put", "PUT", "/deploy/{id}/environments/{env}/variables/{name}", item, op, GrantWrite, false, hints)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAnOperationIsAToolWithAStandaloneSchema(t *testing.T) {
	op := fixtureOperation(t)
	if op.Tool.Name != "deployment_variable_put" || op.Tool.Title != "Set a variable's value" || op.ID != "deploymentVariablePut" {
		t.Errorf("tool %+v", op.Tool)
	}
	if !strings.Contains(op.Tool.Description, "PUT /api/v1/deploy/{id}/environments/{env}/variables/{name}") || !strings.Contains(op.Tool.Description, "under body") {
		t.Errorf("description %q", op.Tool.Description)
	}
	if a := op.Tool.Annotations; a.ReadOnlyHint || a.DestructiveHint || !a.IdempotentHint || a.OpenWorldHint {
		t.Errorf("annotations %+v", a)
	}
	schema := op.Tool.InputSchema
	if got := schema["required"].([]string); strings.Join(got, ",") != "id,env,name" {
		t.Errorf("required %v, want the path's order", got)
	}
	props := schema["properties"].(map[string]any)
	if props["id"].(map[string]any)["description"] != "The deployment's id. list_deployments gives it." {
		t.Errorf("id %v", props["id"])
	}
	raw, _ := json.Marshal(schema)
	if strings.Contains(string(raw), "#/components/") || !strings.Contains(string(raw), `"$ref":"#/$defs/deploy.VariableWrite"`) ||
		!strings.Contains(string(raw), `"$ref":"#/$defs/deploy.Reference"`) {
		t.Errorf("the schema refers outside itself: %s", raw)
	}
	defs := schema["$defs"].(map[string]any)
	if len(defs) != 2 || defs["deploy.Reference"] == nil {
		t.Errorf("$defs %v", defs)
	}
	if schema["additionalProperties"] != false {
		t.Error("the input schema admits other arguments")
	}
}

func TestOffersReachTheirGrants(t *testing.T) {
	read, write, destructive := Operation{Grant: GrantRead}, Operation{Grant: GrantWrite}, Operation{Grant: GrantDestructive}
	for _, c := range []struct {
		offer                    Offer
		read, write, destructive bool
	}{
		{Offer{Operations: true}, false, false, false},
		{Offer{Deployments: true, Operations: true}, true, false, false},
		{Offer{Deployments: true, Deploy: true, Operations: true}, true, true, false},
		{Offer{Deployments: true, Deploy: true, Rollback: true, Operations: true}, true, true, true},
		// A key whose generated tools are off reaches none of them.
		{Offer{Deployments: true, Deploy: true, Rollback: true}, false, false, false},
	} {
		if c.offer.Reaches(read) != c.read || c.offer.Reaches(write) != c.write || c.offer.Reaches(destructive) != c.destructive {
			t.Errorf("%+v reaches read %v write %v destructive %v", c.offer, c.offer.Reaches(read), c.offer.Reaches(write), c.offer.Reaches(destructive))
		}
	}
}

func TestArgumentsBecomeTheRoutesRequest(t *testing.T) {
	op := fixtureOperation(t)
	req, err := DecodeRequest(op, json.RawMessage(`{"id":"12","env":3,"name":"A B/C","dryRun":true,"body":{"value":"x"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if req.Path != "/deploy/12/environments/3/variables/A%20B%2FC" || req.Query.Encode() != "dryRun=true" || string(req.Body) != `{"value":"x"}` {
		t.Errorf("request %+v %s", req, req.Body)
	}
	// A required body left out is sent as an empty object.
	if req, err := DecodeRequest(op, json.RawMessage(`{"id":1,"env":2,"name":"A"}`)); err != nil || string(req.Body) != "{}" || len(req.Query) != 0 {
		t.Errorf("no body: %+v %v", req, err)
	}
	for args, want := range map[string]string{
		`{"id":1,"env":2}`:                       `needs "name"`,
		`{"id":1,"env":2,"name":"A","x":1}`:      `no argument "x"`,
		`{"id":"one","env":2,"name":"A"}`:        "id must be an integer",
		`{"id":1.5,"env":2,"name":"A"}`:          "id must be an integer",
		`{"id":1,"env":2,"name":"A","dryRun":3}`: "dryRun must be a boolean",
		`[1]`:                                    "must be an object",
	} {
		if _, err := DecodeRequest(op, json.RawMessage(args)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", args, err, want)
		}
	}
}

func TestAnAnswerIsAResultBoundedForTheModel(t *testing.T) {
	object := OperationResult(200, []byte(`{"a":1}`), 7)
	if object["isError"] != false || object["structuredContent"].(map[string]any)["a"] != float64(1) {
		t.Errorf("an object: %v", object)
	}
	list := OperationResult(200, []byte(`[1,2]`), 5)
	if items, _ := list["structuredContent"].(map[string]any)["items"].([]any); len(items) != 2 {
		t.Errorf("a list is structured under items: %v", list)
	}
	empty := OperationResult(204, nil, 0)
	if empty["structuredContent"].(map[string]any)["done"] != true {
		t.Errorf("no content: %v", empty)
	}
	big := []byte(`["` + strings.Repeat("é", ResultBytes) + `"]`)
	cut := OperationResult(200, big[:ResultBytes+1], len(big))
	content := cut["content"].([]map[string]any)
	if cut["structuredContent"] != nil || len(content) != 2 || len(content[0]["text"].(string)) > ResultBytes+len("…") ||
		!strings.Contains(content[1]["text"].(string), "these are its first") {
		t.Errorf("a long answer: %d blocks", len(content))
	}
}
