package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// A function, procedure or trigger is code the engine runs later, so the
// routes that write one ask for what the console asks for the same CREATE: the
// destructive capability, on a preview as much as on a run. Reading one for
// editing is the schema read like any other.

const stampTrigger = `{"name":"orders_stamp","table":"orders","timing":"after","events":["insert"],"forEach":"row",` +
	`"body":"UPDATE orders SET total = 1 WHERE id = new.id"}`

func TestStoredProgramsAreWrittenOnlyByAccountsThatMayRunCode(t *testing.T) {
	s, router, db := schemaRouter(t, auth.RoleLimited)
	before := schemaText(t, db)
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, "/databases/1/ddl/trigger", stampTrigger},
		{http.MethodPost, "/databases/1/ddl/trigger?preview=1", stampTrigger},
		{http.MethodPost, "/databases/1/ddl/routine?preview=1", `{"kind":"function","name":"f","language":"sql","returns":"int","body":"SELECT 1"}`},
		{http.MethodDelete, "/databases/1/ddl/trigger", `{"name":"orders_touch","table":"orders"}`},
		{http.MethodDelete, "/databases/1/ddl/routine?preview=1", `{"kind":"function","name":"f"}`},
	} {
		rec, body := schemaSend(t, router, c.method, c.path, c.body)
		if rec.Code != http.StatusForbidden || schemaErrCode(body) != "forbidden" {
			t.Errorf("%s %s without the destructive capability = %d %s", c.method, c.path, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
	if !strings.Contains(before, "orders_touch") || schemaText(t, db) != before {
		t.Errorf("a refused request changed the schema:\n%s", schemaText(t, db))
	}
	// The refusal says why, so the operator knows it is the kind of change and
	// not the form.
	_, body := schemaSend(t, router, http.MethodPost, "/databases/1/ddl/trigger?preview=1", stampTrigger)
	if !strings.Contains(schemaErrMessage(body), "stores code that runs later") {
		t.Errorf("the refusal reads %q", schemaErrMessage(body))
	}
	// A preview refused for who asked is on the trail; one that showed nothing is not.
	var refused int
	if err := s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'database.ddl.save_trigger'`).Scan(&refused); err != nil || refused != 3 {
		t.Errorf("%d refused trigger requests on the trail, %v", refused, err)
	}
}

func TestATriggerIsMadeEditedReadAndDroppedThroughTheRoutes(t *testing.T) {
	s, router, db := schemaRouter(t, auth.RoleAdmin)
	before := schemaText(t, db)

	rec, shown := schemaSend(t, router, http.MethodPost, "/databases/1/ddl/trigger?preview=1", stampTrigger)
	want := "CREATE TRIGGER \"orders_stamp\"\nAFTER INSERT ON \"orders\"\nFOR EACH ROW\nBEGIN\nUPDATE orders SET total = 1 WHERE id = new.id;\nEND"
	if rec.Code != http.StatusOK || shown["statement"] != want || shown["preview"] != true {
		t.Fatalf("preview = %d %s", rec.Code, rec.Body.String())
	}
	if schemaText(t, db) != before {
		t.Fatal("a preview changed the schema")
	}
	var audited int
	if err := s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&audited); err != nil || audited != 0 {
		t.Fatalf("a preview left %d audit entries, %v", audited, err)
	}

	rec, ran := schemaSend(t, router, http.MethodPost, "/databases/1/ddl/trigger", stampTrigger)
	if rec.Code != http.StatusOK || ran["statement"] != want || ran["preview"] != nil || !schemaHas(t, db, "trigger", "orders_stamp") {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	var action, detail string
	if err := s.Store.DB.QueryRow(`SELECT action, detail FROM audit_log ORDER BY id DESC LIMIT 1`).Scan(&action, &detail); err != nil ||
		action != "database.ddl.save_trigger" || !strings.Contains(detail, `"replace":false`) || !strings.Contains(detail, "CREATE TRIGGER") {
		t.Errorf("audit = %q %q, %v", action, detail, err)
	}

	// Read back as the fields the form writes it with.
	var program dbx.Program
	if code := getJSON(t, router, "/databases/1/program?kind=trigger&name=orders_stamp&table=orders", &program); code != http.StatusOK ||
		!program.Editable || program.Trigger == nil || program.Trigger.Timing != "after" || program.Trigger.Table != "orders" ||
		program.Trigger.ForEach != "row" || !strings.Contains(program.Trigger.Body, "UPDATE orders SET total = 1") {
		t.Fatalf("program = %d %+v %+v", code, program, program.Trigger)
	}

	// Editing replaces it, and says how.
	edit := `{"name":"orders_stamp","table":"orders","timing":"before","events":["insert"],"forEach":"row","replace":true,` +
		`"condition":"new.total > 0","body":"SELECT 1"}`
	rec, replaced := schemaSend(t, router, http.MethodPost, "/databases/1/ddl/trigger", edit)
	if rec.Code != http.StatusOK || !strings.HasPrefix(replaced["statement"].(string), "DROP TRIGGER \"orders_stamp\";\nCREATE TRIGGER") ||
		replaced["note"] == nil || !strings.Contains(replaced["note"].(string), "one transaction") {
		t.Fatalf("edit = %d %s", rec.Code, rec.Body.String())
	}
	if code := getJSON(t, router, "/databases/1/program?kind=trigger&name=orders_stamp", &program); code != http.StatusOK ||
		program.Trigger.Timing != "before" || program.Trigger.Condition != "new.total > 0" {
		t.Errorf("program after the edit = %d %+v", code, program.Trigger)
	}

	// An edit the engine refuses leaves the trigger as it was.
	bad := strings.Replace(edit, `"SELECT 1"`, `"INSERT INTO"`, 1)
	if rec, _ := schemaSend(t, router, http.MethodPost, "/databases/1/ddl/trigger", bad); rec.Code != http.StatusBadRequest {
		t.Errorf("a broken body = %d %s", rec.Code, rec.Body.String())
	}
	if code := getJSON(t, router, "/databases/1/program?kind=trigger&name=orders_stamp", &program); code != http.StatusOK || program.Trigger.Body != "SELECT 1;" {
		t.Errorf("after a refused edit: %d %+v", code, program.Trigger)
	}

	rec, dropped := schemaSend(t, router, http.MethodDelete, "/databases/1/ddl/trigger", `{"name":"orders_stamp","table":"orders"}`)
	if rec.Code != http.StatusOK || dropped["statement"] != `DROP TRIGGER "orders_stamp"` || schemaHas(t, db, "trigger", "orders_stamp") {
		t.Errorf("drop = %d %s", rec.Code, rec.Body.String())
	}
	if code := getJSON(t, router, "/databases/1/program?kind=trigger&name=orders_stamp", nil); code != http.StatusNotFound {
		t.Errorf("a dropped trigger is read as %d", code)
	}
}

func TestStoredProgramRoutesRefuseWhatTheEngineOrTheFormCannotSay(t *testing.T) {
	_, router, db := schemaRouter(t, auth.RoleAdmin)
	before := schemaText(t, db)
	for _, c := range []struct{ method, path, body, says string }{
		{http.MethodPost, "/databases/1/ddl/routine", `{"kind":"function","name":"f","language":"sql","returns":"int","body":"SELECT 1"}`, "SQLite has no stored functions"},
		{http.MethodPost, "/databases/1/ddl/routine", `{"kind":"procedure","name":"p","body":"SELECT 1"}`, "SQLite has no stored procedures"},
		{http.MethodDelete, "/databases/1/ddl/routine", `{"kind":"function","name":"f"}`, "SQLite has no stored functions"},
		{http.MethodPost, "/databases/1/ddl/routine", `{"kind":"function","name":"f","extra":"x"}`, "unknown field"},
		{http.MethodPost, "/databases/1/ddl/routine", `{"kind":"function","body":"SELECT 1"}`, "name is required"},
		{http.MethodPost, "/databases/1/ddl/trigger", `{"table":"orders","timing":"after","events":["insert"],"forEach":"row","body":"SELECT 1"}`, "name and table are required"},
		{http.MethodPost, "/databases/1/ddl/trigger", `{"name":"t","table":"orders","timing":"after","events":["insert","update"],"forEach":"row","body":"SELECT 1"}`, "fires on one event"},
		{http.MethodPost, "/databases/1/ddl/trigger", `{"name":"t","table":"orders","timing":"after","events":["insert"],"forEach":"row","body":""}`, "body is required"},
		{http.MethodPost, "/databases/1/ddl/trigger", `{"name":"t","table":"orders","timing":"after","events":["insert"],"forEach":"row","condition":"1) BEGIN SELECT 1; END; --","body":"SELECT 1"}`, "closes a parenthesis"},
		{http.MethodPost, "/databases/1/ddl/trigger", `{"schema":"other","name":"t","table":"orders","timing":"after","events":["insert"],"forEach":"row","body":"SELECT 1"}`, "main database only"},
		{http.MethodPost, "/databases/1/ddl/trigger", `{"name":"t","table":"orders","timing":"after","events":["insert"],"forEach":"row","body":"SELECT 1","extra":1}`, "unknown field"},
		{http.MethodDelete, "/databases/1/ddl/trigger", `{"table":"orders"}`, "name is required"},
		{http.MethodPost, "/databases/1/ddl/trigger", `{"name":"missing","table":"orders","timing":"after","events":["insert"],"forEach":"row","body":"SELECT 1","replace":true}`, "no such object"},
	} {
		for _, suffix := range []string{"", "?preview=1"} {
			rec, body := schemaSend(t, router, c.method, c.path+suffix, c.body)
			if rec.Code < 400 || rec.Code >= 500 || !strings.Contains(schemaErrMessage(body), c.says) {
				t.Errorf("%s %s%s %s = %d %s, want a refusal saying %q", c.method, c.path, suffix, c.body,
					rec.Code, strings.TrimSpace(rec.Body.String()), c.says)
			}
		}
	}
	if schemaText(t, db) != before {
		t.Fatalf("a refused request changed the schema:\n%s", schemaText(t, db))
	}
	// The read answers for what cannot be edited, and for what is not there.
	for path, code := range map[string]int{
		"/databases/1/program?kind=function&name=f":     http.StatusBadRequest,
		"/databases/1/program?kind=trigger":             http.StatusBadRequest,
		"/databases/1/program?kind=table&name=orders":   http.StatusBadRequest,
		"/databases/1/program?kind=trigger&name=nobody": http.StatusNotFound,
	} {
		if got := getJSON(t, router, path, nil); got != code {
			t.Errorf("GET %s = %d, want %d", path, got, code)
		}
	}
}

func TestStoredProgramsAreReadByAnyoneWhoMayOpenATable(t *testing.T) {
	_, router, _ := schemaRouter(t, auth.RoleReadOnly)
	var program dbx.Program
	if code := getJSON(t, router, "/databases/1/program?kind=trigger&name=orders_touch&table=orders", &program); code != http.StatusOK || !program.Editable {
		t.Errorf("a read-only account reading a trigger got %d %+v", code, program)
	}
	rec, _ := schemaSend(t, router, http.MethodPost, "/databases/1/ddl/trigger?preview=1", stampTrigger)
	if rec.Code != http.StatusForbidden {
		t.Errorf("a read-only account previewing a trigger got %d", rec.Code)
	}
}
