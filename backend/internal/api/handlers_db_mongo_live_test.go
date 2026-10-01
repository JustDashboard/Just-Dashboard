package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
	"go.mongodb.org/mongo-driver/bson"
)

// The Mongo routes driven over HTTP against a real server: the request and
// response shapes a page is built against, and what each request leaves on
// the audit trail. The engine's own behaviour is covered in dbx; what is
// proved here is that the route reaches it with the right half of the
// request.

type mongoLive struct {
	t      *testing.T
	s      *Server
	router http.Handler
	base   string
	db     string
}

// liveMongoAPI saves the test server as a connection and returns a router
// that acts as role. The mutation audit middleware is in front of it, as it
// is in production, so what a handler records can be read back.
//
// It runs only against a server the environment names. These tests write
// documents, drop collections, kill an operation and move the profiler's
// threshold, and the address a MongoDB listens on by default is whatever
// happens to be listening there.
func liveMongoAPI(t *testing.T, role auth.Role) *mongoLive {
	t.Helper()
	dsn := os.Getenv("JD_TEST_MONGO_DSN")
	if dsn == "" {
		t.Skip("set JD_TEST_MONGO_DSN to a MongoDB these tests may write to")
	}
	return liveMongoAPIAt(t, role, dsn)
}

// liveMongoPrivate is a server that is this test's alone, where it may make
// accounts and views in the admin database. It is the replica set the dbx
// tests use for the same reason.
func liveMongoPrivate(t *testing.T, role auth.Role) *mongoLive {
	t.Helper()
	dsn := os.Getenv("JD_TEST_MONGO_RS_DSN")
	if dsn == "" {
		t.Skip("set JD_TEST_MONGO_RS_DSN to a private replica set to run this")
	}
	return liveMongoAPIAt(t, role, dsn)
}

func liveMongoAPIAt(t *testing.T, role auth.Role, dsn string) *mongoLive {
	t.Helper()
	s := testServer(t)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{
				User: &auth.User{ID: 1, Username: "tester"},
				Role: role, Kind: "session", IP: "127.0.0.1",
			}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	r.Use(httpx.AuditMutations(s.Audit))
	s.mountDatabaseRoutes(r)
	sealed, err := s.Sealer.Seal(dsn)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Store.DB.Exec(
		`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,?)`,
		"live-mongo", string(dbx.DriverMongo), sealed, 0)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	m := &mongoLive{t: t, s: s, router: r, base: fmt.Sprintf("/databases/%d", id), db: "jdtest"}
	if info, err := dbx.ParseDSN(dbx.DriverMongo, dsn); err == nil && info.Database != "" {
		m.db = info.Database
	}
	var ping struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(mongoDo(r, http.MethodGet, m.base+"/ping", "").Body.Bytes(), &ping)
	if !ping.OK {
		t.Skipf("MongoDB unreachable: %s", ping.Error)
	}
	return m
}

// call sends a request whose body is the JSON of body, and decodes the
// answer into out when the status is the one wanted.
func (m *mongoLive) call(method, path string, body any, want int, out any) *httptest.ResponseRecorder {
	m.t.Helper()
	text := ""
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			m.t.Fatal(err)
		}
		text = string(b)
	}
	rec := mongoDo(m.router, method, m.base+path, text)
	if rec.Code != want {
		m.t.Fatalf("%s %s: %d %s (want %d)", method, path, rec.Code, strings.TrimSpace(rec.Body.String()), want)
	}
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			m.t.Fatalf("%s %s: answer does not decode: %v\n%s", method, path, err, rec.Body.String())
		}
	}
	return rec
}

func (m *mongoLive) drop(collection string) {
	mongoDo(m.router, http.MethodDelete, m.base+"/mongo/collections", `{"collection":"`+collection+`"}`)
}

// audited returns the detail of the newest audit entry for an action.
func (m *mongoLive) audited(action string) string {
	m.t.Helper()
	var detail string
	err := m.s.Store.DB.QueryRow(`SELECT detail FROM audit_log WHERE action = ? AND success = 1 ORDER BY id DESC LIMIT 1`, action).Scan(&detail)
	if err != nil {
		m.t.Errorf("no audit entry for %s: %v", action, err)
	}
	return detail
}

type obj = map[string]any

// secretMarker is in every document body, update and validator these tests
// send, and in none of the filters. It must never reach the audit log.
const secretMarker = "s3cr3t-b0dy-marker"

func TestLiveAPIMongoDocuments(t *testing.T) {
	m := liveMongoAPI(t, auth.RoleAdmin)
	const coll = "jd_api_documents"
	m.drop(coll)
	t.Cleanup(func() { m.drop(coll) })

	var inserted dbx.MongoInsertResult
	m.call(http.MethodPost, "/mongo/documents", obj{"collection": coll, "documents": `[
	  { _id: ObjectId("65f1c0ffee0123456789abcd"), name: "Ann", n: NumberLong("9007199254740993"), at: ISODate("2024-05-01T12:00:00Z"), note: "` + secretMarker + `" },
	  { _id: "text-id", name: "Bo", n: 2, note: "` + secretMarker + `" },
	  { _id: 3, name: "Cy", n: 3.5, note: "` + secretMarker + `" }
	]`}, http.StatusOK, &inserted)
	if inserted.Inserted != 3 || len(inserted.InsertedIDs) != 3 || inserted.InsertedIDs[1] != `"text-id"` {
		t.Fatalf("insert = %+v", inserted)
	}
	if d := m.audited("database.document.insert"); !strings.Contains(d, `"inserted":3`) || strings.Contains(d, secretMarker) {
		t.Errorf("insert audit = %s", d)
	}

	var found dbx.MongoFindResult
	m.call(http.MethodPost, "/mongo/find", obj{
		"collection": coll, "filter": `{ name: { $in: ['Ann', 'Bo'] } }`, "sort": `{ name: 1 }`, "limit": 1,
	}, http.StatusOK, &found)
	if found.Returned != 1 || !found.HasMore || found.Count == nil || found.Count.Value != 2 || !found.Count.Exact {
		t.Fatalf("find = %+v (count %+v)", found, found.Count)
	}
	ann := found.Documents[0]
	if ann.ID != `{"$oid":"65f1c0ffee0123456789abcd"}` ||
		!strings.Contains(ann.Canonical, `"n":{"$numberLong":"9007199254740993"}`) ||
		!strings.Contains(ann.Canonical, `"at":{"$date":{"$numberLong":"1714564800000"}}`) {
		t.Errorf("document = %+v", ann)
	}
	// The display form keeps the long exact too, and shows the date as one.
	if r := string(ann.Relaxed); !strings.Contains(r, `"n":{"$numberLong":"9007199254740993"}`) || !strings.Contains(r, `"at":{"$date":"2024-05-01T12:00:00Z"}`) {
		t.Errorf("relaxed = %s", r)
	}

	var count struct {
		Count dbx.MongoCounted `json:"count"`
	}
	m.call(http.MethodPost, "/mongo/count", obj{"collection": coll, "filter": `{ n: { $gt: 2 } }`}, http.StatusOK, &count)
	if count.Count.Value != 2 || !count.Count.Exact || count.Count.Scope != "filter" {
		t.Errorf("count = %+v", count.Count)
	}

	var got struct {
		Document dbx.MongoDoc `json:"document"`
	}
	m.call(http.MethodPost, "/mongo/document", obj{"collection": coll, "id": `"text-id"`}, http.StatusOK, &got)
	if got.Document.ID != `"text-id"` {
		t.Errorf("document by a string id = %+v", got.Document)
	}
	rec := m.call(http.MethodPost, "/mongo/document", obj{"collection": coll, "id": `"missing"`}, http.StatusNotFound, nil)
	if mongoErrorCode(rec) != "document_not_found" {
		t.Errorf("a missing document: %s", rec.Body.String())
	}

	// Replace: unchanged, then guarded against a change made in between,
	// whichever way the editor says what it read.
	var written dbx.MongoWriteResult
	if len(ann.Digest) != 64 {
		t.Errorf("digest = %q", ann.Digest)
	}
	for _, guard := range []obj{{"expectedDigest": ann.Digest}, {"expected": ann.Canonical}} {
		body := obj{"collection": coll, "id": ann.ID, "document": ann.Canonical}
		for k, v := range guard {
			body[k] = v
		}
		m.call(http.MethodPut, "/mongo/documents", body, http.StatusOK, &written)
		if written.Matched != 1 || written.Modified != 0 {
			t.Errorf("replacing a document with itself, guarded by %v = %+v", guard, written)
		}
	}
	if d := m.audited("database.document.replace"); !strings.Contains(d, `"guarded":true`) || strings.Contains(d, secretMarker) {
		t.Errorf("replace audit = %s", d)
	}
	m.call(http.MethodPatch, "/mongo/documents", obj{
		"collection": coll, "filter": `{ name: "Ann" }`, "update": `{ $set: { touched: "` + secretMarker + `" } }`,
	}, http.StatusOK, &written)
	if written.Matched != 1 || written.Modified != 1 {
		t.Errorf("update = %+v", written)
	}
	if d := m.audited("database.document.update"); !strings.Contains(d, `"matched":1`) || !strings.Contains(d, `name`) || strings.Contains(d, secretMarker) {
		t.Errorf("update audit = %s", d)
	}
	for _, guard := range []obj{{"expectedDigest": ann.Digest}, {"expected": ann.Canonical}} {
		body := obj{"collection": coll, "id": ann.ID, "document": ann.Canonical}
		for k, v := range guard {
			body[k] = v
		}
		rec = m.call(http.MethodPut, "/mongo/documents", body, http.StatusConflict, nil)
		if mongoErrorCode(rec) != "document_changed" {
			t.Errorf("a stale replace: %s", rec.Body.String())
		}
	}
	m.call(http.MethodPut, "/mongo/documents", obj{
		"collection": coll, "id": ann.ID, "document": ann.Canonical, "expectedDigest": ann.Digest, "expected": ann.Canonical,
	}, http.StatusBadRequest, nil)

	// A dry run counts and writes nothing, and is not on the audit trail.
	var before int
	_ = m.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log`).Scan(&before)
	m.call(http.MethodPatch, "/mongo/documents", obj{
		"collection": coll, "filter": `{}`, "update": `{ $set: { all: true } }`, "many": true, "dryRun": true,
	}, http.StatusOK, &written)
	if !written.DryRun || written.Matched != 3 {
		t.Errorf("dry run of an update of everything = %+v", written)
	}
	m.call(http.MethodDelete, "/mongo/documents", obj{"collection": coll, "filter": `{ n: { $gte: 2 } }`, "many": true, "dryRun": true}, http.StatusOK, &written)
	if !written.DryRun || written.Matched != 3 || written.Deleted != 0 {
		t.Errorf("dry run of a delete = %+v", written)
	}
	var after int
	_ = m.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log`).Scan(&after)
	if after != before {
		t.Errorf("the dry runs and reads wrote %d audit entries", after-before)
	}

	var cloned struct {
		Document dbx.MongoDoc `json:"document"`
	}
	m.call(http.MethodPost, "/mongo/documents/clone", obj{"collection": coll, "id": `3`}, http.StatusOK, &cloned)
	if cloned.Document.ID == "" || cloned.Document.ID == `{"$numberInt":"3"}` || !strings.Contains(cloned.Document.Canonical, `"name":"Cy"`) {
		t.Errorf("clone = %+v", cloned.Document)
	}
	if d := m.audited("database.document.clone"); strings.Contains(d, secretMarker) {
		t.Errorf("clone audit = %s", d)
	}

	m.call(http.MethodDelete, "/mongo/documents", obj{"collection": coll, "id": `"text-id"`}, http.StatusOK, &written)
	if written.Deleted != 1 {
		t.Errorf("delete by id = %+v", written)
	}
	m.call(http.MethodDelete, "/mongo/documents", obj{"collection": coll, "many": true, "all": true}, http.StatusOK, &written)
	if written.Deleted != 3 {
		t.Errorf("delete of everything = %+v", written)
	}
	if d := m.audited("database.document.delete"); !strings.Contains(d, `"everyDocument":true`) || !strings.Contains(d, `"deleted":3`) {
		t.Errorf("delete audit = %s", d)
	}

	// Password verifiers are not documents to browse, whoever asks.
	for _, path := range []string{"/mongo/find", "/mongo/schema", "/mongo/explain"} {
		rec = m.call(http.MethodPost, path, obj{"database": "admin", "collection": "system.users"}, http.StatusForbidden, nil)
		if mongoErrorCode(rec) != "credentials_withheld" {
			t.Errorf("%s on admin.system.users: %s", path, rec.Body.String())
		}
	}
	m.call(http.MethodGet, "/mongo/export?database=admin&collection=system.users", nil, http.StatusForbidden, nil)
	if rec := mongoDo(m.router, http.MethodGet, m.base+"/browse?schema=admin&table=system.users", ""); rec.Code == http.StatusOK {
		t.Errorf("the shared browse route opened admin.system.users: %s", rec.Body.String())
	}

	// The server refusing what was sent is the request's fault, in the
	// server's words.
	rec = m.call(http.MethodPost, "/mongo/find", obj{"collection": coll, "filter": `{ a: { $nope: 1 } }`}, http.StatusBadRequest, nil)
	if !strings.Contains(rec.Body.String(), "$nope") {
		t.Errorf("an unknown operator: %s", rec.Body.String())
	}
	m.call(http.MethodPost, "/mongo/documents", obj{"collection": coll, "documents": `{ _id: 1 }`}, http.StatusOK, nil)
	rec = m.call(http.MethodPost, "/mongo/find", obj{"collection": coll, "filter": `{ $where: "sleep(500) || true" }`, "maxTimeMS": 50, "count": false}, http.StatusGatewayTimeout, nil)
	if mongoErrorCode(rec) != "query_timeout" {
		t.Errorf("a query past its max time: %s", rec.Body.String())
	}
}

func TestLiveAPIMongoStructure(t *testing.T) {
	m := liveMongoAPI(t, auth.RoleAdmin)
	const coll, view, renamed = "jd_api_structure", "jd_api_view", "jd_api_renamed"
	for _, c := range []string{coll, view, renamed} {
		m.drop(c)
	}
	t.Cleanup(func() {
		for _, c := range []string{coll, view, renamed} {
			m.drop(c)
		}
	})
	rule := `{ $jsonSchema: { required: ["name"], description: "` + secretMarker + `" } }`

	m.call(http.MethodPost, "/mongo/collections", obj{"collection": coll, "validator": rule, "validationAction": "warn"}, http.StatusOK, nil)
	m.call(http.MethodPost, "/mongo/collections", obj{"collection": view, "viewOn": coll, "pipeline": `[ { $match: { n: { $gt: 1 } } } ]`}, http.StatusOK, nil)
	if d := m.audited("database.collection.create"); !strings.Contains(d, `"kind":"view"`) || !strings.Contains(d, coll) {
		t.Errorf("create audit = %s", d)
	}
	m.call(http.MethodPost, "/mongo/documents", obj{"collection": coll, "documents": `[ {name: "a", n: 1}, {name: "b", n: 2}, {n: 3} ]`}, http.StatusOK, nil)

	var list dbx.MongoCollectionList
	m.call(http.MethodGet, "/mongo/collections", nil, http.StatusOK, &list)
	seen := map[string]dbx.MongoCollection{}
	for _, c := range list.Collections {
		seen[c.Name] = c
	}
	if list.Database != m.db || seen[coll].Type != "collection" || seen[coll].Count != 3 || seen[view].Type != "view" || seen[view].ViewOn != coll {
		t.Errorf("list = %+v / %+v", seen[coll], seen[view])
	}
	var one struct {
		Collection dbx.MongoCollection `json:"collection"`
	}
	m.call(http.MethodGet, "/mongo/collection?collection="+coll, nil, http.StatusOK, &one)
	if one.Collection.ValidationAction != "warn" || !strings.Contains(one.Collection.Validator, "$jsonSchema") {
		t.Errorf("collection = %+v", one.Collection)
	}
	rec := m.call(http.MethodGet, "/mongo/collection?collection=jd_api_missing", nil, http.StatusNotFound, nil)
	if mongoErrorCode(rec) != "collection_not_found" {
		t.Errorf("a missing collection: %s", rec.Body.String())
	}

	// Validation: read, check, change.
	var validation dbx.MongoValidation
	m.call(http.MethodGet, "/mongo/validation?collection="+coll, nil, http.StatusOK, &validation)
	if validation.Action != "warn" || validation.Level != "strict" || validation.Validator == "" {
		t.Errorf("validation = %+v", validation)
	}
	var check dbx.MongoValidationCheck
	m.call(http.MethodPost, "/mongo/validation/check", obj{"collection": coll}, http.StatusOK, &check)
	if check.Failing != 1 || check.Proposed || len(check.Samples) != 1 || check.Total != 3 {
		t.Errorf("check of the current rule = %+v", check)
	}
	m.call(http.MethodPost, "/mongo/validation/check", obj{"collection": coll, "validator": `{ n: { $lt: 2 } }`}, http.StatusOK, &check)
	if check.Failing != 2 || !check.Proposed {
		t.Errorf("check of a proposed rule = %+v", check)
	}
	m.call(http.MethodPut, "/mongo/validation", obj{"collection": coll, "level": "moderate", "action": "error"}, http.StatusOK, &validation)
	if validation.Level != "moderate" || validation.Action != "error" || validation.Validator == "" {
		t.Errorf("validation after changing the level = %+v", validation)
	}
	m.call(http.MethodPut, "/mongo/validation", obj{"collection": coll, "validator": ""}, http.StatusOK, &validation)
	if validation.Validator != "" {
		t.Errorf("validation after removing the rule = %+v", validation)
	}
	if d := m.audited("database.validation.set"); !strings.Contains(d, `"validator":""`) {
		t.Errorf("validation audit = %s", d)
	}

	// Indexes.
	var created struct {
		Name string `json:"name"`
	}
	m.call(http.MethodPost, "/mongo/indexes", obj{
		"collection": coll, "keys": []obj{{"field": "name", "type": "asc"}, {"field": "n", "type": "desc"}}, "sparse": true,
	}, http.StatusOK, &created)
	if created.Name != "name_1_n_-1" {
		t.Errorf("index name = %q", created.Name)
	}
	if d := m.audited("database.index.create"); !strings.Contains(d, created.Name) || !strings.Contains(d, `"name:asc"`) {
		t.Errorf("index audit = %s", d)
	}
	m.call(http.MethodPatch, "/mongo/indexes", obj{"collection": coll, "name": created.Name, "hidden": true}, http.StatusOK, nil)
	var indexes dbx.MongoIndexList
	m.call(http.MethodGet, "/mongo/indexes?collection="+coll, nil, http.StatusOK, &indexes)
	if len(indexes.Indexes) != 2 || !indexes.Indexes[0].Primary || !indexes.Indexes[1].Hidden || !indexes.Indexes[1].Sparse ||
		indexes.Indexes[1].Size <= 0 || indexes.Indexes[1].Usage == nil {
		t.Errorf("indexes = %+v", indexes.Indexes)
	}
	rec = m.call(http.MethodDelete, "/mongo/indexes", obj{"collection": coll, "name": "_id_"}, http.StatusBadRequest, nil)
	if !strings.Contains(rec.Body.String(), "_id index") {
		t.Errorf("dropping the _id index: %s", rec.Body.String())
	}
	m.call(http.MethodDelete, "/mongo/indexes", obj{"collection": coll, "name": created.Name}, http.StatusOK, nil)
	if d := m.audited("database.index.drop"); !strings.Contains(d, created.Name) {
		t.Errorf("index drop audit = %s", d)
	}
	// The first browser's route still answers in its own shape.
	var legacy struct {
		Indexes []dbx.Index    `json:"indexes"`
		Stats   map[string]any `json:"stats"`
	}
	m.call(http.MethodGet, "/collections/indexes?schema="+m.db+"&table="+coll, nil, http.StatusOK, &legacy)
	if len(legacy.Indexes) != 1 || !legacy.Indexes[0].Primary || legacy.Stats["count"] == nil {
		t.Errorf("legacy indexes = %+v", legacy)
	}

	// collMod, rename, drop.
	m.call(http.MethodPatch, "/mongo/collections", obj{"collection": view, "viewOn": coll, "pipeline": `[ { $match: { n: 3 } } ]`}, http.StatusOK, nil)
	if d := m.audited("database.collection.modify"); !strings.Contains(d, `"view"`) {
		t.Errorf("collMod audit = %s", d)
	}
	var page dbx.MongoFindResult
	m.call(http.MethodPost, "/mongo/find", obj{"collection": view}, http.StatusOK, &page)
	if page.Returned != 1 {
		t.Errorf("the redefined view returned %d documents", page.Returned)
	}
	m.call(http.MethodPost, "/mongo/collections/rename", obj{"collection": coll, "to": renamed}, http.StatusOK, nil)
	if d := m.audited("database.collection.rename"); !strings.Contains(d, renamed) {
		t.Errorf("rename audit = %s", d)
	}
	m.call(http.MethodDelete, "/mongo/collections", obj{"collection": renamed}, http.StatusOK, nil)
	m.call(http.MethodGet, "/mongo/collection?collection="+renamed, nil, http.StatusNotFound, nil)

	var leaked int
	_ = m.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE detail LIKE ?`, "%"+secretMarker+"%").Scan(&leaked)
	// Creating a collection records what kind it is, not the rule it was
	// given; a rule is recorded only where it is the thing being set.
	if leaked != 0 {
		var detail string
		_ = m.s.Store.DB.QueryRow(`SELECT action || ' ' || detail FROM audit_log WHERE detail LIKE ? LIMIT 1`, "%"+secretMarker+"%").Scan(&detail)
		t.Errorf("%d audit entries carry a document or rule body, first: %s", leaked, detail)
	}
}

func TestLiveAPIMongoQueries(t *testing.T) {
	m := liveMongoAPI(t, auth.RoleAdmin)
	const coll, out = "jd_api_queries", "jd_api_queries_out"
	m.drop(coll)
	m.drop(out)
	t.Cleanup(func() { m.drop(coll); m.drop(out) })
	docs := []string{}
	for i := 0; i < 30; i++ {
		docs = append(docs, fmt.Sprintf(`{ _id: %d, kind: "k%d", n: %d }`, i, i%3, i))
	}
	m.call(http.MethodPost, "/mongo/documents", obj{"collection": coll, "documents": "[" + strings.Join(docs, ",") + "]"}, http.StatusOK, nil)
	pipeline := `[ { $match: { n: { $gte: 6 } } }, { $group: { _id: "$kind", total: { $sum: "$n" } } }, { $sort: { _id: 1 } } ]`

	// The aggregate route answers in the shape it always had, and in the
	// typed one beside it.
	var run struct {
		Result    dbx.QueryResult       `json:"result"`
		Writes    bool                  `json:"writes"`
		Pipeline  dbx.MongoPipelineInfo `json:"pipeline"`
		Documents []dbx.MongoDoc        `json:"documents"`
		Returned  int                   `json:"returned"`
		HasMore   bool                  `json:"hasMore"`
	}
	m.call(http.MethodPost, "/aggregate", obj{"collection": coll, "pipeline": pipeline, "limit": 200}, http.StatusOK, &run)
	if run.Result.RowCount != 3 || run.Writes || run.Returned != 3 || len(run.Documents) != 3 || strings.Join(run.Pipeline.Stages, ",") != "$match,$group,$sort" {
		t.Errorf("aggregate = %+v", run)
	}
	if d := m.audited("database.aggregate"); !strings.Contains(d, `"writes":false`) || !strings.Contains(d, `"$group"`) || strings.Contains(d, "$gte") {
		t.Errorf("aggregate audit = %s", d)
	}
	// $mergeObjects is an expression. It used to be taken for $merge.
	m.call(http.MethodPost, "/aggregate", obj{"collection": coll, "pipeline": `[ { $group: { _id: null, doc: { $mergeObjects: "$$ROOT" } } } ]`}, http.StatusOK, &run)
	if run.Writes {
		t.Error("a pipeline using $mergeObjects was reported as writing")
	}
	m.call(http.MethodPost, "/aggregate", obj{"collection": coll, "pipeline": `[ { $match: { kind: "k0" } }, { $out: "` + out + `" } ]`}, http.StatusOK, &run)
	if !run.Writes || run.Pipeline.WriteStage != "$out" {
		t.Errorf("a writing pipeline = %+v", run.Pipeline)
	}
	if d := m.audited("database.aggregate"); !strings.Contains(d, `"writes":true`) {
		t.Errorf("writing aggregate audit = %s", d)
	}

	var preview dbx.MongoPreviewResult
	m.call(http.MethodPost, "/mongo/aggregate/preview", obj{"collection": coll, "pipeline": pipeline, "stage": 0, "sample": 4}, http.StatusOK, &preview)
	if preview.Returned != 4 || preview.Stages != 3 || preview.Stage != 0 {
		t.Errorf("preview of stage 0 = %+v", preview)
	}
	m.call(http.MethodPost, "/mongo/aggregate/preview", obj{"collection": coll, "pipeline": pipeline, "stage": 2}, http.StatusOK, &preview)
	if preview.Returned != 3 || !preview.InputLimited || preview.Documents[0].Canonical != `{"_id":"k0","total":{"$numberInt":"132"}}` {
		t.Errorf("preview of stage 2 = %+v", preview)
	}

	var plan dbx.MongoExplainResult
	m.call(http.MethodPost, "/mongo/explain", obj{"collection": coll, "filter": `{ n: { $gte: 20 } }`, "verbosity": "executionStats"}, http.StatusOK, &plan)
	if !plan.Summary.Executed || !plan.Summary.CollectionScan || plan.Summary.Returned == nil || *plan.Summary.Returned != 10 ||
		plan.Summary.DocsExamined == nil || *plan.Summary.DocsExamined != 30 || plan.Plan.Stage == "" || plan.Raw == "" {
		t.Errorf("explain of a find = %+v", plan.Summary)
	}
	m.call(http.MethodPost, "/mongo/explain", obj{"collection": coll, "pipeline": pipeline}, http.StatusOK, &plan)
	if plan.Summary.Executed || plan.Summary.Verbosity != "queryPlanner" || plan.Plan.Stage == "" {
		t.Errorf("explain of a pipeline = %+v", plan.Summary)
	}

	var schema dbx.MongoSchema
	m.call(http.MethodPost, "/mongo/schema", obj{"collection": coll, "sample": 100}, http.StatusOK, &schema)
	if schema.Sampled != 30 || len(schema.Fields) != 3 || schema.Fields[0].Path != "_id" || !schema.Fields[0].Indexed {
		t.Errorf("schema = %+v", schema)
	}

	// Export: documents as documents, and a query that cannot run is an
	// error before it is a file.
	rec := m.call(http.MethodGet, "/mongo/export?collection="+coll+"&filter="+url.QueryEscape(`{ kind: "k1" }`)+"&sort="+url.QueryEscape(`{ _id: 1 }`), nil, http.StatusOK, nil)
	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(lines) != 10 || lines[0] != `{"_id":{"$numberInt":"1"},"kind":"k1","n":{"$numberInt":"1"}}` {
		t.Errorf("export: %d lines, first %s", len(lines), lines[0])
	}
	h := rec.Header()
	if h.Get("Content-Type") != "application/x-ndjson" || !strings.Contains(h.Get("Content-Disposition"), coll+".ndjson") ||
		h.Get("X-Export-Total") != "10" || h.Get("X-Export-Truncated") != "false" {
		t.Errorf("export headers = %v", h)
	}
	rec = m.call(http.MethodGet, "/mongo/export?collection="+coll+"&format=json&relaxed=1&limit=2&sort="+url.QueryEscape(`{ _id: 1 }`)+"&filter="+url.QueryEscape(`{ n: { $lt: 5 } }`), nil, http.StatusOK, nil)
	var exported []obj
	if err := json.Unmarshal(rec.Body.Bytes(), &exported); err != nil || len(exported) != 2 || exported[1]["n"] != float64(1) {
		t.Errorf("json export = %s (%v)", rec.Body.String(), err)
	}
	if rec.Header().Get("X-Export-Truncated") != "true" || rec.Header().Get("X-Export-Total") != "5" {
		t.Errorf("truncated export headers = %v", rec.Header())
	}
	rec = m.call(http.MethodGet, "/mongo/export?collection="+coll+"&format=csv&limit=3&sort="+url.QueryEscape(`{ _id: 1 }`), nil, http.StatusOK, nil)
	if got := rec.Body.String(); got != "_id,kind,n\n0,k0,0\n1,k1,1\n2,k2,2\n" {
		t.Errorf("csv export = %q", got)
	}
	rec = m.call(http.MethodGet, "/mongo/export?collection="+coll+"&filter="+url.QueryEscape(`{ n: `), nil, http.StatusBadRequest, nil)
	if rec.Header().Get("Content-Disposition") != "" {
		t.Error("a refused export still announced a file")
	}
	var exports int
	_ = m.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'database.export'`).Scan(&exports)
	if exports != 3 {
		t.Errorf("%d exports on the audit trail, want the 3 that ran", exports)
	}
	if d := m.audited("database.export"); !strings.Contains(d, `"collection":"`+coll+`"`) || !strings.Contains(d, `"format":"csv"`) || !strings.Contains(d, `"limit":3`) {
		t.Errorf("export audit = %s", d)
	}

	// An export that breaks off after its first byte is on the trail twice:
	// once as begun, when it was, and once as the failure it became.
	broken := &breakingWriter{ResponseRecorder: httptest.NewRecorder()}
	func() {
		defer func() {
			if recovered := recover(); recovered != http.ErrAbortHandler {
				t.Errorf("an export whose reader went away ended with %v, want the response broken off", recovered)
			}
		}()
		m.router.ServeHTTP(broken, httptest.NewRequest(http.MethodGet, m.base+"/mongo/export?collection="+coll, nil))
	}()
	var (
		failures int
		detail   string
		status   int
	)
	_ = m.s.Store.DB.QueryRow(`SELECT COUNT(*), COALESCE(MAX(detail), ''), COALESCE(MAX(status), 0) FROM audit_log WHERE action = 'database.export' AND success = 0`).
		Scan(&failures, &detail, &status)
	if failures != 1 || status != http.StatusBadGateway || !strings.Contains(detail, `"error":"the reader went away"`) || !strings.Contains(detail, `"rows"`) {
		t.Errorf("%d failed exports on the trail, status %d, detail %s", failures, status, detail)
	}
	_ = m.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'database.export' AND success = 1`).Scan(&exports)
	if exports != 4 {
		t.Errorf("%d exports recorded as begun, want 4", exports)
	}
}

// breakingWriter is a client that reads nothing of a download.
type breakingWriter struct{ *httptest.ResponseRecorder }

func (*breakingWriter) Write([]byte) (int, error) { return 0, errors.New("the reader went away") }

func TestLiveAPIMongoServer(t *testing.T) {
	m := liveMongoAPI(t, auth.RoleAdmin)

	// The shared stats route: the keys it had, and the counters beside them.
	var stats struct {
		Server map[string]any `json:"server"`
	}
	m.call(http.MethodGet, "/stats", nil, http.StatusOK, &stats)
	for _, key := range []string{"host", "version", "uptime", "connections", "network", "opcounters", "mem", "timestamp", "documents", "cache", "role"} {
		if _, ok := stats.Server[key]; !ok {
			t.Errorf("/stats lacks %q", key)
		}
	}
	var server dbx.MongoServer
	m.call(http.MethodGet, "/mongo/server", nil, http.StatusOK, &server)
	if server.Version == "" || server.Timestamp == 0 || server.Opcounters["command"] == 0 || server.Connections.Current == 0 || server.Role == "" {
		t.Errorf("server = %+v", server)
	}
	var dbs struct {
		Databases []dbx.MongoDatabaseStat `json:"databases"`
	}
	m.call(http.MethodGet, "/mongo/databases", nil, http.StatusOK, &dbs)
	if len(dbs.Databases) == 0 {
		t.Error("no databases listed")
	}
	var ops struct {
		Operations []dbx.MongoOperation `json:"operations"`
	}
	m.call(http.MethodGet, "/mongo/ops", nil, http.StatusOK, &ops)
	m.call(http.MethodGet, "/mongo/ops?all=1", nil, http.StatusOK, &ops)
	if len(ops.Operations) == 0 {
		t.Error("no operations listed with idle connections included")
	}
	// An operation that is not running: the server says so, and it is the
	// request's fault, not a failure.
	rec := m.call(http.MethodPost, "/mongo/killop", obj{"opId": "not-an-id"}, http.StatusBadRequest, nil)
	if !strings.Contains(rec.Body.String(), "operation id") {
		t.Errorf("killop of a bad id: %s", rec.Body.String())
	}
	m.call(http.MethodPost, "/mongo/killop", obj{"opId": "2147480000"}, http.StatusOK, nil)
	if d := m.audited("database.mongo.killop"); !strings.Contains(d, "2147480000") {
		t.Errorf("killop audit = %s", d)
	}

	var profile dbx.MongoProfile
	m.call(http.MethodGet, "/mongo/profiler", nil, http.StatusOK, &profile)
	if profile.Database != m.db || profile.Entries == nil || profile.SlowMs <= 0 {
		t.Errorf("profiler = %+v", profile.MongoProfiler)
	}
	var set dbx.MongoProfiler
	m.call(http.MethodPut, "/mongo/profiler", obj{"level": profile.Level, "slowMs": profile.SlowMs}, http.StatusOK, &set)
	if set.Level != profile.Level || set.SlowMs != profile.SlowMs {
		t.Errorf("profiler set to what it was = %+v", set)
	}
	if d := m.audited("database.mongo.profiler"); !strings.Contains(d, `"level"`) {
		t.Errorf("profiler audit = %s", d)
	}
	m.call(http.MethodPut, "/mongo/profiler", obj{"level": 7}, http.StatusBadRequest, nil)
	// A change that names no level leaves the level alone. It used to be
	// read as level 0, so moving the threshold turned the profiler off.
	m.call(http.MethodPut, "/mongo/profiler", obj{"level": 1, "slowMs": profile.SlowMs}, http.StatusOK, &set)
	t.Cleanup(func() {
		m.call(http.MethodPut, "/mongo/profiler", obj{"level": profile.Level, "slowMs": profile.SlowMs}, http.StatusOK, nil)
		// Turning the profiler on made the collection it writes to; a server
		// that had it off is left without one.
		if profile.Level == 0 {
			m.drop("system.profile")
		}
	})
	m.call(http.MethodPut, "/mongo/profiler", obj{"slowMs": profile.SlowMs + 1}, http.StatusOK, &set)
	if set.Level != 1 || set.SlowMs != profile.SlowMs+1 {
		t.Errorf("moving only the threshold = %+v, want level 1 kept", set)
	}
	m.call(http.MethodPut, "/mongo/profiler", obj{}, http.StatusBadRequest, nil)

	var repl dbx.MongoReplication
	m.call(http.MethodGet, "/mongo/replication", nil, http.StatusOK, &repl)
	if repl.Members == nil || repl.ReplicaSet != (server.Topology == "replicaset") {
		t.Errorf("replication = %+v, topology %s", repl, server.Topology)
	}

	var users struct {
		Users []dbx.MongoUser `json:"users"`
	}
	rec = m.call(http.MethodGet, "/mongo/users", nil, http.StatusOK, &users)
	if users.Users == nil || strings.Contains(rec.Body.String(), "credentials") {
		t.Errorf("users = %s", rec.Body.String())
	}
	var roles struct {
		Database string          `json:"database"`
		Roles    []dbx.MongoRole `json:"roles"`
	}
	m.call(http.MethodGet, "/mongo/roles", nil, http.StatusOK, &roles)
	if roles.Database != m.db || len(roles.Roles) < 4 {
		t.Errorf("roles of %s: %d", roles.Database, len(roles.Roles))
	}
	// An account that does not exist: the server's refusal, as a 400.
	rec = m.call(http.MethodPut, "/mongo/users", obj{"database": m.db, "user": "jd_api_nobody", "password": "pw-" + secretMarker}, http.StatusBadRequest, nil)
	if !strings.Contains(rec.Body.String(), "jd_api_nobody") {
		t.Errorf("password of a missing account: %s", rec.Body.String())
	}
	var leaked int
	_ = m.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE detail LIKE ?`, "%"+secretMarker+"%").Scan(&leaked)
	if leaked != 0 {
		t.Error("a password reached the audit log")
	}
}

func TestLiveAPIMongoConsole(t *testing.T) {
	m := liveMongoAPI(t, auth.RoleAdmin)
	const coll = "jd_api_console"
	m.drop(coll)
	t.Cleanup(func() { m.drop(coll) })

	var answer struct {
		Verdict  dbx.MongoVerdict      `json:"verdict"`
		Database string                `json:"database"`
		Reply    dbx.MongoCommandReply `json:"reply"`
	}
	m.call(http.MethodPost, "/mongo/command", obj{"command": `{ insert: "` + coll + `", documents: [ { _id: 1, at: ISODate("2024-01-01"), note: "` + secretMarker + `" } ] }`}, http.StatusOK, &answer)
	if answer.Verdict.Class != dbx.MongoClassWrite || answer.Database != m.db || !strings.Contains(answer.Reply.Canonical, `"n":{"$numberInt":"1"}`) {
		t.Errorf("insert = %+v", answer)
	}
	d := m.audited("database.mongo.command")
	if !strings.Contains(d, `"command":"insert"`) || !strings.Contains(d, `"target":"`+coll+`"`) || strings.Contains(d, secretMarker) {
		t.Errorf("console audit = %s", d)
	}
	m.call(http.MethodPost, "/mongo/command", obj{"command": `{ find: "` + coll + `" }`}, http.StatusOK, &answer)
	if answer.Verdict.Class != dbx.MongoClassRead || !strings.Contains(answer.Reply.Canonical, `"at":{"$date":{"$numberLong":"1704067200000"}}`) || !json.Valid(answer.Reply.Relaxed) {
		t.Errorf("find = %+v", answer.Reply)
	}
	m.call(http.MethodPost, "/mongo/command", obj{"command": `{ listDatabases: 1 }`, "database": "admin"}, http.StatusOK, &answer)
	if answer.Database != "admin" || !strings.Contains(answer.Reply.Canonical, `"databases"`) {
		t.Errorf("listDatabases = %+v", answer)
	}
	m.call(http.MethodPost, "/mongo/command", obj{"command": `{ drop: "` + coll + `" }`}, http.StatusOK, &answer)
	if answer.Verdict.Class != dbx.MongoClassDestructive {
		t.Errorf("drop = %+v", answer.Verdict)
	}
	// The server's refusal of a command is the server's sentence.
	rec := m.call(http.MethodPost, "/mongo/command", obj{"command": `{ collStats: "jd_api_no_such_collection_` + "x" + `", scale: "big" }`}, http.StatusBadRequest, nil)
	if mongoErrorCode(rec) != "bad_request" {
		t.Errorf("a refused command: %s", rec.Body.String())
	}
}

// What the console's table once read as harmless, on a server that reads it
// otherwise: each of these is refused for a role without the capability its
// real effect needs, and shown not to have happened. The last part runs the
// same command as an administrator, to show the server does what the table
// now says it does.
func TestLiveAPIMongoConsoleFlags(t *testing.T) {
	admin := liveMongoAPI(t, auth.RoleAdmin)
	limited := liveMongoAPI(t, auth.RoleLimited)
	const coll = "jd_api_console_flags"
	admin.drop(coll)
	t.Cleanup(func() { admin.drop(coll) })
	admin.call(http.MethodPost, "/mongo/documents", obj{"collection": coll, "documents": `{ _id: 1 }`}, http.StatusOK, nil)

	var before dbx.MongoProfile
	admin.call(http.MethodGet, "/mongo/profiler", nil, http.StatusOK, &before)
	t.Cleanup(func() {
		admin.call(http.MethodPut, "/mongo/profiler", obj{"level": before.Level, "slowMs": before.SlowMs}, http.StatusOK, nil)
		if before.Level == 0 {
			admin.drop("system.profile")
		}
	})
	admin.call(http.MethodPut, "/mongo/profiler", obj{"level": 1}, http.StatusOK, nil)

	for _, text := range []string{
		`{ findAndModify: "` + coll + `", query: { _id: 1 }, remove: NumberDecimal("1") }`,
		`{ validate: "` + coll + `", repair: "yes" }`,
		`{ profile: -1, slowms: ` + fmt.Sprint(before.SlowMs+7) + ` }`,
		`{ profile: NaN }`,
	} {
		limited.call(http.MethodPost, "/mongo/command", obj{"command": text}, http.StatusForbidden, nil)
	}
	var found dbx.MongoFindResult
	admin.call(http.MethodPost, "/mongo/find", obj{"collection": coll}, http.StatusOK, &found)
	if found.Returned != 1 {
		t.Errorf("the refused findAndModify removed the document: %d left", found.Returned)
	}
	var after dbx.MongoProfile
	admin.call(http.MethodGet, "/mongo/profiler", nil, http.StatusOK, &after)
	if after.Level != 1 || after.SlowMs != before.SlowMs {
		t.Errorf("the refused profile commands left level %d and threshold %d, want 1 and %d", after.Level, after.SlowMs, before.SlowMs)
	}
	// Asking is still a read for the same role.
	limited.call(http.MethodPost, "/mongo/command", obj{"command": `{ profile: -1 }`}, http.StatusOK, nil)

	// A level that is not a number is level 0 to the server: the profiler
	// goes off, which is why it takes what turning it off takes.
	admin.call(http.MethodPost, "/mongo/command", obj{"command": `{ profile: NaN }`}, http.StatusOK, nil)
	admin.call(http.MethodGet, "/mongo/profiler", nil, http.StatusOK, &after)
	if after.Level != 0 {
		t.Errorf("{ profile: NaN } left the profiler at level %d; the table classes it as the write that turns it off", after.Level)
	}
}

// What any role can read, read for real: a read-only role gets documents,
// plans, the schema and the server's state, and none of the writes.
func TestLiveAPIMongoReadOnlyRole(t *testing.T) {
	admin := liveMongoAPI(t, auth.RoleAdmin)
	const coll = "jd_api_readonly"
	admin.drop(coll)
	t.Cleanup(func() { admin.drop(coll) })
	admin.call(http.MethodPost, "/mongo/documents", obj{"collection": coll, "documents": `[ {n: 1}, {n: 2} ]`}, http.StatusOK, nil)

	ro := liveMongoAPI(t, auth.RoleReadOnly)
	var page dbx.MongoFindResult
	ro.call(http.MethodPost, "/mongo/find", obj{"collection": coll}, http.StatusOK, &page)
	if page.Returned != 2 {
		t.Errorf("a read-only role found %d documents", page.Returned)
	}
	ro.call(http.MethodPost, "/mongo/explain", obj{"collection": coll, "verbosity": "executionStats"}, http.StatusOK, nil)
	ro.call(http.MethodPost, "/mongo/schema", obj{"collection": coll}, http.StatusOK, nil)
	ro.call(http.MethodPost, "/mongo/aggregate/preview", obj{"collection": coll, "pipeline": `[ { $match: {} } ]`, "stage": 0}, http.StatusOK, nil)
	ro.call(http.MethodGet, "/mongo/collections", nil, http.StatusOK, nil)
	ro.call(http.MethodGet, "/mongo/indexes?collection="+coll, nil, http.StatusOK, nil)
	ro.call(http.MethodGet, "/mongo/server", nil, http.StatusOK, nil)
	ro.call(http.MethodGet, "/mongo/ops", nil, http.StatusOK, nil)
	ro.call(http.MethodGet, "/mongo/export?collection="+coll, nil, http.StatusOK, nil)
	// A preview is a read even of a pipeline that would write: the stage
	// that writes is not run.
	var preview dbx.MongoPreviewResult
	ro.call(http.MethodPost, "/mongo/aggregate/preview", obj{"collection": coll, "pipeline": `[ { $out: "jd_api_readonly_out" } ]`, "stage": 0}, http.StatusOK, &preview)
	if preview.WriteStage != "$out" || preview.Returned != 2 {
		t.Errorf("preview of a writing stage = %+v", preview)
	}
	admin.call(http.MethodGet, "/mongo/collection?collection=jd_api_readonly_out", nil, http.StatusNotFound, nil)

	for _, rq := range []mongoRoute{
		{http.MethodPost, "/mongo/documents", `{"collection":"` + coll + `","documents":"{}"}`},
		{http.MethodPatch, "/mongo/documents", `{"collection":"` + coll + `","filter":"{\"n\":1}","update":"{\"$set\":{\"x\":1}}"}`},
		{http.MethodDelete, "/mongo/documents", `{"collection":"` + coll + `","filter":"{\"n\":1}"}`},
		{http.MethodPost, "/mongo/command", `{"command":"{ ping: 1 }"}`},
		{http.MethodPost, "/aggregate", `{"collection":"` + coll + `","pipeline":"[]"}`},
	} {
		if rec := mongoDo(ro.router, rq.method, ro.base+rq.path, rq.body); rec.Code != http.StatusForbidden {
			t.Errorf("a read-only role: %s %s answered %d", rq.method, rq.path, rec.Code)
		}
	}
	var left dbx.MongoFindResult
	admin.call(http.MethodPost, "/mongo/find", obj{"collection": coll}, http.StatusOK, &left)
	if left.Returned != 2 {
		t.Errorf("the refused writes changed the collection: %d documents", left.Returned)
	}
}

// The credential guards, against a server that has credentials to guard: a
// real account, and a view somebody made over admin.system.users outside the
// dashboard. Nothing the routes answer may carry a password verifier.
func TestLiveAPIMongoCredentialGuards(t *testing.T) {
	m := liveMongoPrivate(t, auth.RoleAdmin)
	const user, view, coll = "jd_api_guard_user", "jd_api_guard_view", "jd_api_guard"
	command := func(m *mongoLive, database, text string, want int) *httptest.ResponseRecorder {
		t.Helper()
		return m.call(http.MethodPost, "/mongo/command", obj{"database": database, "command": text}, want, nil)
	}
	cleanup := func() {
		mongoDo(m.router, http.MethodDelete, m.base+"/mongo/users", `{"user":"`+user+`"}`)
		mongoDo(m.router, http.MethodPost, m.base+"/mongo/command", `{"database":"admin","command":"{ drop: '`+view+`' }"}`)
		m.drop(coll)
	}
	cleanup()
	t.Cleanup(cleanup)
	m.call(http.MethodPost, "/mongo/users", obj{"user": user, "password": "pw-" + secretMarker, "roles": []obj{{"role": "read", "db": m.db}}}, http.StatusOK, nil)
	m.call(http.MethodPost, "/mongo/documents", obj{"collection": coll, "documents": `{ _id: 1 }`}, http.StatusOK, nil)

	// The console refuses to make the view, in either spelling of making one.
	for _, text := range []string{
		`{ create: "` + view + `", viewOn: "system.users", pipeline: [] }`,
		`{ create: "` + view + `", viewOn: "` + coll + `", pipeline: [ { $unionWith: "system.users" } ] }`,
	} {
		if rec := command(m, "admin", text, http.StatusBadRequest); mongoErrorCode(rec) != "command_blocked" {
			t.Errorf("%s: %s", text, rec.Body.String())
		}
	}
	// So it is made the way somebody with a shell would have made it.
	client, err := dbx.MongoClient(context.Background(), os.Getenv("JD_TEST_MONGO_RS_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	if err := client.Database("admin").RunCommand(context.Background(), bson.D{
		{Key: "create", Value: view}, {Key: "viewOn", Value: "system.users"}, {Key: "pipeline", Value: bson.A{}},
	}).Err(); err != nil {
		t.Fatalf("making the view: %v", err)
	}
	// The view is there and the server reads through it: this is what the
	// guard stands in front of.
	raw, err := client.Database("admin").Collection(view).FindOne(context.Background(), bson.D{{Key: "user", Value: user}}).Raw()
	if err != nil || !strings.Contains(raw.String(), "storedKey") {
		t.Fatalf("the view does not show credentials, so there is nothing to guard: %v", err)
	}

	withheld := func(rec *httptest.ResponseRecorder, what string) {
		t.Helper()
		if mongoErrorCode(rec) != "credentials_withheld" {
			t.Errorf("%s: %s", what, rec.Body.String())
		}
		for _, secret := range []string{"storedKey", "serverKey", "SCRAM-SHA"} {
			if strings.Contains(rec.Body.String(), secret) {
				t.Errorf("%s answered with %q", what, secret)
			}
		}
	}
	ro := liveMongoPrivate(t, auth.RoleReadOnly)
	for _, path := range []string{"/mongo/find", "/mongo/count", "/mongo/schema", "/mongo/explain", "/mongo/validation/check"} {
		withheld(ro.call(http.MethodPost, path, obj{"database": "admin", "collection": view}, http.StatusForbidden, nil), path+" on the view")
	}
	withheld(ro.call(http.MethodPost, "/mongo/aggregate/preview", obj{
		"database": "admin", "collection": "system.version", "pipeline": `[ { $unionWith: "` + view + `" } ]`, "stage": 0,
	}, http.StatusForbidden, nil), "a preview joining the view")
	withheld(ro.call(http.MethodGet, "/mongo/export?database=admin&collection="+view, nil, http.StatusForbidden, nil), "export of the view")
	if rec := mongoDo(ro.router, http.MethodGet, ro.base+"/browse?schema=admin&table="+view, ""); rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "storedKey") {
		t.Errorf("the shared browse route opened the view: %d %s", rec.Code, rec.Body.String())
	}
	withheld(m.call(http.MethodPost, "/aggregate", obj{
		"database": "admin", "collection": "system.version", "pipeline": `[ { $lookup: { from: "` + view + `", as: "u", pipeline: [] } } ]`,
	}, http.StatusForbidden, nil), "a pipeline joining the view")
	for _, text := range []string{
		`{ find: "` + view + `" }`,
		`{ count: "` + view + `", query: { "credentials.SCRAM-SHA-256.storedKey": /^a/ } }`,
		`{ aggregate: "system.version", pipeline: [ { $unionWith: "` + view + `" } ], cursor: {} }`,
		`{ explain: { find: "` + view + `" }, verbosity: "executionStats" }`,
	} {
		withheld(command(m, "admin", text, http.StatusForbidden), text)
	}
	// The replication log records the account being made, verifier and all.
	withheld(ro.call(http.MethodPost, "/mongo/find", obj{"database": "local", "collection": "oplog.rs"}, http.StatusForbidden, nil), "find on the replication log")
	withheld(ro.call(http.MethodPost, "/mongo/aggregate/preview", obj{
		"collection": coll, "stage": 0,
		"pipeline": `[ { $lookup: { from: { db: "local", coll: "oplog.rs" }, as: "log", pipeline: [] } } ]`,
	}, http.StatusForbidden, nil), "a preview joining the replication log")
	// And the first browser's writers no longer take the collection either.
	for _, rq := range []mongoRoute{
		{http.MethodPost, "/documents", `{"database":"admin","collection":"system.users","document":"{\"user\":\"x\"}"}`},
		{http.MethodPatch, "/documents", `{"database":"admin","collection":"system.users","filter":"{\"user\":\"` + user + `\"}","document":"{}"}`},
		{http.MethodDelete, "/documents", `{"database":"admin","collection":"system.users","filter":"{\"user\":\"` + user + `\"}"}`},
		{http.MethodDelete, "/collections", `{"database":"admin","collection":"system.users"}`},
	} {
		rec := mongoDo(m.router, rq.method, m.base+rq.path, rq.body)
		if rec.Code != http.StatusForbidden || mongoErrorCode(rec) != "credentials_withheld" {
			t.Errorf("%s %s on admin.system.users: %d %s", rq.method, rq.path, rec.Code, rec.Body.String())
		}
	}

	// What the console's table read as harmless when a flag was not a
	// boolean, on a server that takes the flag that way.
	if rec := command(m, "admin", `{ usersInfo: "`+user+`", showCredentials: NumberDecimal("1") }`, http.StatusBadRequest); mongoErrorCode(rec) != "command_blocked" || strings.Contains(rec.Body.String(), "storedKey") {
		t.Errorf("usersInfo with showCredentials as a decimal: %s", rec.Body.String())
	}
	limited := liveMongoPrivate(t, auth.RoleLimited)
	for _, text := range []string{
		`{ findAndModify: "` + coll + `", query: { _id: 1 }, remove: NumberDecimal("1") }`,
		`{ validate: "` + coll + `", repair: "yes" }`,
	} {
		command(limited, "", text, http.StatusForbidden)
	}
	command(limited, "", `{ profile: -1, slowms: 123 }`, http.StatusForbidden)
	var found dbx.MongoFindResult
	m.call(http.MethodPost, "/mongo/find", obj{"collection": coll}, http.StatusOK, &found)
	if found.Returned != 1 {
		t.Errorf("the refused findAndModify removed the document: %d left", found.Returned)
	}
	var profile dbx.MongoProfile
	m.call(http.MethodGet, "/mongo/profiler", nil, http.StatusOK, &profile)
	if profile.SlowMs == 123 {
		t.Error("the refused profile command moved the slow threshold")
	}
	// The account list still answers, without them.
	var users struct {
		Users []dbx.MongoUser `json:"users"`
	}
	rec := ro.call(http.MethodGet, "/mongo/users", nil, http.StatusOK, &users)
	if len(users.Users) == 0 || strings.Contains(rec.Body.String(), "storedKey") || strings.Contains(rec.Body.String(), secretMarker) {
		t.Errorf("users = %s", rec.Body.String())
	}
}
