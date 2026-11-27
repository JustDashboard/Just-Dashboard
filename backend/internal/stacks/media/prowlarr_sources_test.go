package media

// The source picker against a stand-in for Prowlarr. Its schema is
// testdata/prowlarr-indexer-schema.json: eight entries of what the pinned
// Prowlarr 3.1.0 answered, each kind the picker meets once (the generic
// Torznab and a preset sharing its definition, a usenet one, a public site, a
// public one with optional settings, a private one with an account and a
// captcha, and the two RSS feeds), renamed so that no real site is named.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks/stackstest"
)

type sourcesFake struct {
	t   *testing.T
	key string

	mu       sync.Mutex
	schema   []byte
	indexers []map[string]any
	nextID   int
	// failTest answers the test with Prowlarr's refusal, echoing the body's
	// apiKey back as a site's error would.
	failTest bool
	// fail answers a request ("DELETE indexer/4") with a status instead.
	fail     map[string]int
	calls    []string
	saved    []map[string]any
	commands int
	schemas  atomic.Int32
	server   *httptest.Server
}

func newSourcesFake(t *testing.T) (*sourcesFake, *stackstest.Fake) {
	t.Helper()
	fake := &sourcesFake{t: t, key: strings.Repeat("p", 32), schema: arrFixture(t, "prowlarr-indexer-schema.json"), nextID: 10, fail: map[string]int{}}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.server.Close)
	env := &stackstest.Fake{
		PresetID: "complete",
		Services: map[string]string{"prowlarr": fake.server.URL},
		Vars:     map[string]string{VariableProwlarrAPIKey: fake.key},
	}
	return fake, env
}

func (f *sourcesFake) answer(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if raw, ok := value.([]byte); ok {
		_, _ = w.Write(raw)
		return
	}
	if value != nil {
		_ = json.NewEncoder(w).Encode(value)
	}
}

func (f *sourcesFake) serve(w http.ResponseWriter, r *http.Request) {
	path, isAPI := strings.CutPrefix(r.URL.Path, "/api/v1/")
	if !isAPI {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("X-Api-Key") != f.key {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	call := r.Method + " " + path
	if path == "indexer/schema" {
		f.schemas.Add(1)
		f.mu.Lock()
		schema := f.schema
		f.mu.Unlock()
		f.answer(w, http.StatusOK, schema)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	if status, found := f.fail[call]; found {
		f.answer(w, status, map[string]string{"message": "Prowlarr is having a bad time"})
		return
	}
	var body map[string]any
	if r.Method == http.MethodPost {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			f.answer(w, http.StatusBadRequest, map[string]string{"message": "not JSON"})
			return
		}
	}
	parts := strings.Split(path, "/")
	switch {
	case call == "GET appprofile":
		f.answer(w, http.StatusOK, []map[string]any{{"id": 3, "name": "Another"}, {"id": 1, "name": "Standard"}})
	case call == "GET indexer":
		f.answer(w, http.StatusOK, append([]map[string]any{}, f.indexers...))
	case call == "POST indexer/test":
		if f.failTest {
			key := sourcesFieldValue(body, "apiKey")
			f.answer(w, http.StatusBadRequest, []map[string]any{{
				"isWarning": false, "propertyName": "", "severity": "error",
				"errorMessage":        "Unable to connect to indexer, check the log above the ValidationFailure for more details. HTTP request failed: [403:Forbidden] [GET] at [https://torznab.example.org/api?t=caps&apikey=" + url.QueryEscape(key) + "]",
				"detailedDescription": "The site said the key " + key + " is not known",
			}})
			return
		}
		f.answer(w, http.StatusOK, map[string]any{})
	case call == "POST indexer":
		if r.URL.Query().Get("forceSave") != "true" {
			f.t.Errorf("an add was not saved with forceSave after its test: %s", r.URL.RawQuery)
		}
		for _, indexer := range f.indexers {
			if strings.EqualFold(indexer["name"].(string), body["name"].(string)) {
				f.answer(w, http.StatusBadRequest, []map[string]any{{"propertyName": "Name", "errorMessage": "Should be unique"}})
				return
			}
		}
		body["id"] = f.nextID
		f.nextID++
		f.saved = append(f.saved, body)
		f.indexers = append(f.indexers, body)
		f.answer(w, http.StatusCreated, body)
	case call == "POST command":
		f.commands++
		if body["name"] != "ApplicationIndexerSync" {
			f.t.Errorf("command %v", body["name"])
		}
		f.answer(w, http.StatusCreated, map[string]any{"id": 7, "name": body["name"], "status": "queued"})
	case call == "GET command/7":
		f.answer(w, http.StatusOK, map[string]any{"id": 7, "name": "ApplicationIndexerSync", "status": "completed"})
	case parts[0] == "indexer" && len(parts) == 2:
		id, _ := strconv.Atoi(parts[1])
		index := slices.IndexFunc(f.indexers, func(entry map[string]any) bool { return sourcesID(entry) == id })
		if index < 0 {
			f.answer(w, http.StatusNotFound, map[string]string{"message": "NotFound"})
			return
		}
		switch r.Method {
		case http.MethodGet:
			f.answer(w, http.StatusOK, f.indexers[index])
		case http.MethodDelete:
			f.indexers = slices.Delete(f.indexers, index, index+1)
			f.answer(w, http.StatusOK, nil)
		}
	default:
		f.t.Errorf("Prowlarr: unexpected request %s", call)
		http.NotFound(w, r)
	}
}

func sourcesID(entry map[string]any) int {
	switch id := entry["id"].(type) {
	case float64:
		return int(id)
	case int:
		return id
	}
	return 0
}

func sourcesFieldValue(resource map[string]any, name string) string {
	fields, _ := resource["fields"].([]any)
	for _, raw := range fields {
		field, _ := raw.(map[string]any)
		if field["name"] == name {
			value, _ := field["value"].(string)
			return value
		}
	}
	return ""
}

func sourcesCatalogue(t *testing.T, env stacks.Env) *MediaSourceCatalogue {
	t.Helper()
	catalogue, err := FetchSourceCatalogue(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	return catalogue
}

// The catalogue is Prowlarr's torrent definitions only, by name, each keyed
// by its definition name, or by that and its own when presets share one; what
// is kept of each is trimmed of what an add does not send.
func TestSourceCatalogueKeepsTorrentDefinitionsTrimmedByName(t *testing.T) {
	_, env := newSourcesFake(t)
	catalogue := sourcesCatalogue(t, env)
	var keys, names []string
	for _, definition := range catalogue.Definitions {
		keys = append(keys, definition.Definition)
		names = append(names, definition.Name)
	}
	wantNames := []string{"Example Optional", "Example Private", "Example Public", "Example RSS Preset", "Example Torznab Preset", "Generic Torznab", "Torrent RSS Feed"}
	if !slices.Equal(names, wantNames) {
		t.Fatalf("names = %q; want %q", names, wantNames)
	}
	wantKeys := []string{"exampleoptional", "exampleprivate", "examplepublic", "Torrent RSS Feed:Example RSS Preset", "Torznab:Example Torznab Preset", "Torznab:Generic Torznab", "Torrent RSS Feed:Torrent RSS Feed"}
	if !slices.Equal(keys, wantKeys) {
		t.Fatalf("keys = %q; want %q", keys, wantKeys)
	}
	if catalogue.FetchedAt.IsZero() {
		t.Error("the catalogue does not say when it was read")
	}
	for key, entry := range catalogue.entries {
		if _, kept := entry.resource["capabilities"]; kept {
			t.Errorf("%s keeps its capabilities", key)
		}
		if _, kept := entry.resource["presets"]; kept {
			t.Errorf("%s keeps its presets", key)
		}
		if _, kept := entry.resource["fields"]; !kept {
			t.Errorf("%s lost its fields", key)
		}
	}
	public := catalogue.Definitions[slices.IndexFunc(catalogue.Definitions, func(d MediaSourceDefinition) bool { return d.Definition == "examplepublic" })]
	if public.Privacy != SourcePublic || public.Language == "" || public.Description != "A public site with nothing to fill in." {
		t.Errorf("examplepublic = %+v", public)
	}
}

// A definition needs input when it asks for an account, a key, a cookie, an
// address or a captcha with nothing in it; an empty number, a checkbox, a
// select with choices and a setting whose words say it is optional do not.
func TestSourceCatalogueSaysWhichDefinitionsNeedInput(t *testing.T) {
	_, env := newSourcesFake(t)
	catalogue := sourcesCatalogue(t, env)
	want := map[string]bool{
		"examplepublic":                       false, // an address select with choices, an empty seed ratio
		"exampleoptional":                     false, // an empty uploader filter
		"exampleprivate":                      true,  // username, password and a captcha
		"Torznab:Generic Torznab":             true,  // an empty address
		"Torznab:Example Torznab Preset":      true,  // its address is filled in, its key is not
		"Torrent RSS Feed:Torrent RSS Feed":   true,  // an empty feed address
		"Torrent RSS Feed:Example RSS Preset": false, // a cookie only "if your site requires" one
	}
	for _, definition := range catalogue.Definitions {
		if definition.NeedsInput != want[definition.Definition] {
			t.Errorf("%s: needsInput = %v", definition.Definition, definition.NeedsInput)
		}
	}

	optional := &prowlarrSchemaEntry{Fields: []prowlarrField{{Name: "apikey", Label: "API Key (Optional)", Type: "textbox"}}}
	if optional.needsInput() {
		t.Error("a key marked optional needs input")
	}
	advanced := &prowlarrSchemaEntry{Fields: []prowlarrField{{Name: "cookie", Type: "textbox", Advanced: true}}}
	if advanced.needsInput() {
		t.Error("an advanced setting needs input")
	}
	hidden := &prowlarrSchemaEntry{Fields: []prowlarrField{{Name: "password", Type: "password", Hidden: "hidden"}}}
	if hidden.needsInput() {
		t.Error("a hidden setting needs input")
	}
	blank := &prowlarrSchemaEntry{Fields: []prowlarrField{{Name: "username", Type: "textbox", Value: json.RawMessage(`"  "`)}}}
	if !blank.needsInput() {
		t.Error("a username of spaces is a default")
	}
	choiceless := &prowlarrSchemaEntry{Fields: []prowlarrField{{Name: "category", Type: "select"}}}
	if !choiceless.needsInput() {
		t.Error("a select with no choice and no value is a default")
	}
}

// A definition's form: what Prowlarr would show, the address chosen from the
// definition's own, a key Prowlarr keeps secret masked as a password, and
// nothing hidden, informational or a captcha.
func TestSourceDefinitionMapsProwlarrFieldsToAForm(t *testing.T) {
	_, env := newSourcesFake(t)
	catalogue := sourcesCatalogue(t, env)
	if _, found := catalogue.Lookup("Generic Newznab"); found {
		t.Fatal("a usenet definition is in the catalogue")
	}
	if _, found := catalogue.Lookup("Torznab"); found {
		t.Fatal("a definition name shared by presets names one of them")
	}

	private, found := catalogue.Lookup("exampleprivate")
	if !found {
		t.Fatal("exampleprivate is not in the catalogue")
	}
	if private.Name != "Example Private" || private.Privacy != SourcePrivate || !slices.Equal(private.URLs, []string{"https://private.example.org/"}) {
		t.Fatalf("detail = %+v", private)
	}
	fields := map[string]MediaSourceField{}
	var order []string
	for _, field := range private.Fields {
		fields[field.Name] = field
		order = append(order, field.Name)
		if field.Label == "" || field.Type == "" {
			t.Errorf("%s has no label or type: %+v", field.Name, field)
		}
	}
	for _, left := range []string{"definitionFile", "info", "info_activity", "info_captcha", "cardigannCaptcha"} {
		if _, shown := fields[left]; shown {
			t.Errorf("the form shows %s", left)
		}
	}
	if order[0] != "baseUrl" {
		t.Errorf("the form is not in Prowlarr's order: %q", order)
	}
	address := fields["baseUrl"]
	if address.Type != SourceFieldSelect || address.Value != "https://private.example.org/" || len(address.Options) != 1 ||
		address.Options[0] != (MediaSourceOption{Value: "https://private.example.org/", Label: "https://private.example.org/"}) || address.Advanced {
		t.Errorf("baseUrl = %+v", address)
	}
	if user := fields["username"]; user.Type != SourceFieldText || user.Value != nil || user.Label != "Username" {
		t.Errorf("username = %+v", user)
	}
	if password := fields["password"]; password.Type != SourceFieldPassword {
		t.Errorf("password = %+v", password)
	}
	if ratio := fields["torrentBaseSettings.seedRatio"]; ratio.Type != SourceFieldNumber || ratio.Advanced || !strings.Contains(ratio.Help, "ratio") {
		t.Errorf("seed ratio = %+v", ratio)
	}
	if seedTime := fields["torrentBaseSettings.seedTime"]; !seedTime.Advanced || !strings.HasSuffix(seedTime.Help, "In minutes.") {
		t.Errorf("seed time = %+v", seedTime)
	}
	if freeleech := fields["freeleech"]; freeleech.Type != SourceFieldCheckbox || freeleech.Value != false {
		t.Errorf("freeleech = %+v", freeleech)
	}
	sort := fields["sort"]
	if sort.Type != SourceFieldSelect || len(sort.Options) < 2 || sort.Options[0].Label == "" {
		t.Errorf("sort = %+v", sort)
	}
	if limits := fields["baseSettings.limitsUnit"]; limits.Value != json.Number("0") || limits.Options[1] != (MediaSourceOption{Value: json.Number("1"), Label: "Hour"}) {
		t.Errorf("limits unit = %+v", limits)
	}

	torznab, _ := catalogue.Lookup("Torznab:Generic Torznab")
	for _, field := range torznab.Fields {
		switch field.Name {
		case "apiKey":
			if field.Type != SourceFieldPassword {
				t.Errorf("Torznab's key is asked for as %s", field.Type)
			}
		case "baseUrl":
			if field.Type != SourceFieldText || field.Value != "" {
				t.Errorf("Torznab's address = %+v", field)
			}
		}
	}
	// Most sites' keys and cookies are plain textboxes in the schema; their
	// names say what they are.
	feed, _ := catalogue.Lookup("Torrent RSS Feed:Example RSS Preset")
	for _, field := range feed.Fields {
		if field.Name == "cookie" && field.Type != SourceFieldPassword {
			t.Errorf("a cookie is asked for as %s", field.Type)
		}
		if field.Name == "baseUrl" && field.Type != SourceFieldText {
			t.Errorf("the feed's address is asked for as %s", field.Type)
		}
	}
	for name, secret := range map[string]bool{"apikey": true, "passkey": true, "cookie": true, "pin": true, "username": false, "uploader": false, "baseUrl": false} {
		if got := (prowlarrField{Name: name, Type: "textbox", Privacy: "normal"}).formType() == SourceFieldPassword; got != secret {
			t.Errorf("a textbox named %s is a secret: %v", name, got)
		}
	}
	encoded, _ := json.Marshal(private)
	if !strings.Contains(string(encoded), `"value":0`) || strings.Contains(string(encoded), "cardigannCaptcha") {
		t.Errorf("detail encodes as %s", encoded)
	}
}

// What the person types is checked against the form: text trimmed of a
// paste's spaces (a password as typed), numbers and checkboxes from a form's
// text, a select only to one of its choices, and nothing the form does not
// ask for.
func TestPrepareSourceChecksAndTrimsWhatWasTyped(t *testing.T) {
	_, env := newSourcesFake(t)
	catalogue := sourcesCatalogue(t, env)

	prepared, err := catalogue.Prepare("exampleprivate", map[string]any{
		"username":                      "  someone\n",
		"password":                      " pass word ",
		"freeleech":                     "true",
		"sort":                          float64(1),
		"torrentBaseSettings.seedRatio": "1.5",
		"baseSettings.queryLimit":       "",
		"type":                          nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !prepared.Secret || prepared.Name != "Example Private" || prepared.Definition != "exampleprivate" {
		t.Fatalf("prepared = %+v", prepared)
	}
	values := map[string]any{}
	for _, field := range prepared.fields {
		values[field["name"].(string)] = field["value"]
	}
	if values["username"] != "someone" {
		t.Errorf("username = %q", values["username"])
	}
	if values["password"] != " pass word " {
		t.Errorf("password = %q; a password is sent as typed", values["password"])
	}
	if values["freeleech"] != true || values["torrentBaseSettings.seedRatio"] != 1.5 {
		t.Errorf("freeleech %v, seed ratio %v", values["freeleech"], values["torrentBaseSettings.seedRatio"])
	}
	if sort, ok := values["sort"].(json.Number); !ok || sort != "1" {
		t.Errorf("sort = %#v; want Prowlarr's own choice", values["sort"])
	}
	if values["baseUrl"] != "https://private.example.org/" {
		t.Errorf("baseUrl = %v; want the definition's first address", values["baseUrl"])
	}
	if values["baseSettings.queryLimit"] != nil {
		t.Errorf("an empty number was sent as %v", values["baseSettings.queryLimit"])
	}
	if values["definitionFile"] != "exampleprivate" {
		t.Errorf("the hidden definition file is %v", values["definitionFile"])
	}
	resource := prepared.resourceFor(1)
	if resource["appProfileId"] != 1 || resource["enable"] != true {
		t.Errorf("resource = %v %v", resource["appProfileId"], resource["enable"])
	}

	public, err := catalogue.Prepare("examplepublic", map[string]any{"baseUrl": "https://mirror.example.org/"})
	if err != nil {
		t.Fatal(err)
	}
	if public.Secret || sourcesFieldValue(map[string]any{"fields": anySlice(public.fields)}, "baseUrl") != "https://mirror.example.org/" {
		t.Errorf("public = %+v", public)
	}

	for name, values := range map[string]map[string]any{
		"a setting it does not have":   {"nothing": "x"},
		"a hidden setting":             {"definitionFile": "another"},
		"an information line":          {"info": "x"},
		"text that is not text":        {"username": float64(4)},
		"a number that is not one":     {"torrentBaseSettings.seedRatio": "lots"},
		"a fraction of a whole number": {"baseSettings.queryLimit": 1.5},
		"a checkbox that is not one":   {"freeleech": "maybe"},
		"a choice it does not have":    {"sort": float64(99)},
		"an address not its own":       {"baseUrl": "https://elsewhere.example.org/"},
	} {
		_, err := catalogue.Prepare("exampleprivate", values)
		var invalid *SourceInvalidError
		if !errors.As(err, &invalid) || invalid.Field == "" || invalid.Message == "" {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := catalogue.Prepare("nothing", nil); !errors.Is(err, ErrSourceDefinitionNotFound) {
		t.Errorf("an unknown definition: %v", err)
	}
	feed, err := catalogue.Prepare("Torrent RSS Feed:Example RSS Preset", map[string]any{"cookie": "session=abc123"})
	if err != nil || !feed.Secret {
		t.Errorf("a cookie is a secret: %+v %v", feed, err)
	}
	torznab, err := catalogue.Prepare("Torznab:Generic Torznab", map[string]any{"baseUrl": " http://torznab:8080 ", "apiKey": " key-value-1 "})
	if err != nil {
		t.Fatal(err)
	}
	if !torznab.Secret || sourcesFieldValue(map[string]any{"fields": anySlice(torznab.fields)}, "apiKey") != "key-value-1" {
		t.Errorf("a key Prowlarr keeps secret is a secret, trimmed: %+v", torznab)
	}
}

func anySlice(fields []map[string]any) []any {
	out := make([]any, len(fields))
	for index, field := range fields {
		out[index] = field
	}
	return out
}

// An add is Prowlarr's test, then the save in its first app profile, then the
// sync to the applications.
func TestAddSourceTestsSavesAndSyncs(t *testing.T) {
	fake, env := newSourcesFake(t)
	catalogue := sourcesCatalogue(t, env)
	prepared, err := catalogue.Prepare("Torznab:Generic Torznab", map[string]any{"baseUrl": "http://torznab:8080", "apiKey": "torznab-key-value"})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := AddSource(context.Background(), env, prepared)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Synced || outcome.Source.ID != 10 || outcome.Source.Name != "Generic Torznab" || outcome.Source.Definition != "Torznab" ||
		outcome.Source.Protocol != "torrent" || !outcome.Source.Enabled {
		t.Fatalf("outcome = %+v", outcome)
	}
	want := []string{"GET indexer", "GET appprofile", "POST indexer/test", "POST indexer", "POST command", "GET command/7"}
	if !slices.Equal(fake.calls, want) {
		t.Fatalf("calls = %q; want %q", fake.calls, want)
	}
	saved := fake.saved[0]
	if saved["appProfileId"] != float64(1) || saved["enable"] != true || saved["implementation"] != "Torznab" || saved["configContract"] != "TorznabSettings" {
		t.Errorf("saved = %v", saved)
	}
	if sourcesFieldValue(saved, "apiKey") != "torznab-key-value" || sourcesFieldValue(saved, "apiPath") != "/api" {
		t.Errorf("saved fields: key %q, path %q", sourcesFieldValue(saved, "apiKey"), sourcesFieldValue(saved, "apiPath"))
	}
	if _, sent := saved["capabilities"]; sent {
		t.Error("the save sent the schema's capabilities")
	}

	// The same source again is refused before Prowlarr is asked anything.
	fake.calls = nil
	_, err = AddSource(context.Background(), env, prepared)
	var exists *SourceExistsError
	if !errors.As(err, &exists) || exists.Name != "Generic Torznab" || !slices.Equal(fake.calls, []string{"GET indexer"}) {
		t.Fatalf("a second add: %v, calls %q", err, fake.calls)
	}

	sources, err := ListSources(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0] != outcome.Source {
		t.Fatalf("sources = %+v", sources)
	}
}

// Prowlarr's reason for a failed test is the person's to read, with every
// text they typed taken out of it, as typed and as a URL carries it.
func TestAddSourceMasksWhatWasTypedOutOfAFailure(t *testing.T) {
	fake, env := newSourcesFake(t)
	fake.failTest = true
	catalogue := sourcesCatalogue(t, env)
	key := "typed key+value/1"
	prepared, err := catalogue.Prepare("Torznab:Example Torznab Preset", map[string]any{"apiKey": key})
	if err != nil {
		t.Fatal(err)
	}
	_, err = AddSource(context.Background(), env, prepared)
	var failed *SourceTestFailedError
	if !errors.As(err, &failed) {
		t.Fatalf("err = %v", err)
	}
	for _, form := range []string{key, url.QueryEscape(key), url.PathEscape(key), "typed key"} {
		if strings.Contains(failed.Message, form) {
			t.Errorf("the failure carries %q: %s", form, failed.Message)
		}
	}
	if !strings.Contains(failed.Message, "Example Torznab Preset") || !strings.Contains(failed.Message, "403:Forbidden") || !strings.Contains(failed.Message, "••••••") {
		t.Errorf("the failure does not say why: %s", failed.Message)
	}
	if slices.Contains(fake.calls, "POST indexer") {
		t.Error("a source whose test failed was saved")
	}

	// Any other answer is an error, masked the same way.
	fake.failTest = false
	fake.fail["POST indexer"] = http.StatusInternalServerError
	_, err = AddSource(context.Background(), env, prepared)
	if err == nil || errors.As(err, &failed) {
		t.Fatalf("a save Prowlarr fails: %v", err)
	}

	masked := (&PreparedSource{typed: []string{"ab", "secret-value", "secret"}}).Mask("ab secret-value secret " + url.QueryEscape("secret-value"))
	if masked != "ab •••••• •••••• ••••••" {
		t.Errorf("masked = %q", masked)
	}
}

// The schema is read bounded: a longer answer is an error that says so, never
// a list cut short.
func TestSourceCatalogueReadIsBounded(t *testing.T) {
	fake, env := newSourcesFake(t)
	limit := prowlarrSchemaLimit
	t.Cleanup(func() { prowlarrSchemaLimit = limit })

	prowlarrSchemaLimit = int64(len(fake.schema))
	if _, err := FetchSourceCatalogue(context.Background(), env); err != nil {
		t.Fatalf("a schema exactly at the limit: %v", err)
	}
	prowlarrSchemaLimit = int64(len(fake.schema)) - 1
	if _, err := FetchSourceCatalogue(context.Background(), env); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("a schema one byte over: %v", err)
	}
	prowlarrSchemaLimit = 1024
	if _, err := FetchSourceCatalogue(context.Background(), env); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("a schema far over: %v", err)
	}
	prowlarrSchemaLimit = limit

	fake.schema = []byte(`[{"definitionName":"examplepublic","name":"Example Public","protocol":"torrent","fields":[]}] trailing`)
	if _, err := FetchSourceCatalogue(context.Background(), env); err != nil {
		t.Fatalf("a document followed by more: %v", err)
	}
	fake.schema = []byte(`{"message":"not a list"}`)
	if _, err := FetchSourceCatalogue(context.Background(), env); err == nil {
		t.Fatal("an answer that is not a list was read")
	}
}

// A project's catalogue is read once per TTL, by one request when several ask
// at once, and a project's is never another's.
func TestSourceCacheKeepsEachProjectsCatalogueForItsTTL(t *testing.T) {
	fake, env := newSourcesFake(t)
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	var clock sync.Mutex
	cache := NewSourceCache()
	cache.Now = func() time.Time { clock.Lock(); defer clock.Unlock(); return now }
	advance := func(by time.Duration) { clock.Lock(); now = now.Add(by); clock.Unlock() }
	ctx := context.Background()

	var wg sync.WaitGroup
	results := make([]*MediaSourceCatalogue, 5)
	for index := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			catalogue, err := cache.Catalogue(ctx, "1/5", env)
			if err != nil {
				t.Error(err)
			}
			results[index] = catalogue
		}()
	}
	wg.Wait()
	if fake.schemas.Load() != 1 {
		t.Fatalf("five requests at once read the schema %d times", fake.schemas.Load())
	}
	for _, catalogue := range results {
		if catalogue != results[0] {
			t.Fatal("requests at once were given different catalogues")
		}
	}
	advance(SourceCatalogueTTL - time.Second)
	if again, _ := cache.Catalogue(ctx, "1/5", env); again != results[0] || fake.schemas.Load() != 1 {
		t.Fatalf("within the TTL the schema was read %d times", fake.schemas.Load())
	}
	if _, err := cache.Catalogue(ctx, "2/9", env); err != nil || fake.schemas.Load() != 2 {
		t.Fatalf("another project's catalogue: %v, %d reads", err, fake.schemas.Load())
	}
	advance(2 * time.Second)
	if fresh, _ := cache.Catalogue(ctx, "1/5", env); fresh == results[0] || fake.schemas.Load() != 3 {
		t.Fatalf("after the TTL the schema was read %d times", fake.schemas.Load())
	}

	// A failed read is not kept.
	fake.mu.Lock()
	fake.schema = []byte(`{}`)
	fake.mu.Unlock()
	if _, err := cache.Catalogue(ctx, "3/1", env); err == nil {
		t.Fatal("a failed read answered")
	}
	fake.mu.Lock()
	fake.schema = arrFixture(t, "prowlarr-indexer-schema.json")
	fake.mu.Unlock()
	if _, err := cache.Catalogue(ctx, "3/1", env); err != nil {
		t.Fatalf("a read after a failed one: %v", err)
	}

	// At most sourceCacheSize projects are kept; the oldest goes.
	for index := range sourceCacheSize + 2 {
		advance(time.Second)
		if _, err := cache.Catalogue(ctx, "many/"+strconv.Itoa(index), env); err != nil {
			t.Fatal(err)
		}
	}
	if kept := len(cache.slots); kept > sourceCacheSize {
		t.Fatalf("%d catalogues kept", kept)
	}
}

// Removing is by Prowlarr's id; one it does not have says so.
func TestRemoveSourceByProwlarrsID(t *testing.T) {
	fake, env := newSourcesFake(t)
	fake.indexers = []map[string]any{
		{"id": 4, "name": "zeta", "definitionName": "examplepublic", "privacy": "public", "protocol": "torrent", "enable": false},
		{"id": 5, "name": "Alpha", "definitionName": "Torznab", "privacy": "private", "protocol": "torrent", "enable": true},
	}
	sources, err := ListSources(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 || sources[0].Name != "Alpha" || sources[1] != (MediaSource{ID: 4, Name: "zeta", Definition: "examplepublic", Privacy: SourcePublic, Protocol: "torrent"}) {
		t.Fatalf("sources = %+v", sources)
	}
	if _, err := RemoveSource(context.Background(), env, 99); !errors.Is(err, ErrSourceNotFound) {
		t.Fatalf("an unknown id: %v", err)
	}
	removed, err := RemoveSource(context.Background(), env, 4)
	if err != nil || removed.Name != "zeta" {
		t.Fatalf("removed %+v: %v", removed, err)
	}
	if len(fake.indexers) != 1 || sourcesID(fake.indexers[0]) != 5 {
		t.Fatalf("left = %v", fake.indexers)
	}
	fake.fail["DELETE indexer/5"] = http.StatusInternalServerError
	if _, err := RemoveSource(context.Background(), env, 5); err == nil || errors.Is(err, ErrSourceNotFound) {
		t.Fatalf("a removal Prowlarr fails: %v", err)
	}
}

// The bound holds while catalogues are being read: more projects read at once
// than there is room for still leave at most sourceCacheSize kept, rather
// than every one of them for good.
func TestSourceCacheBoundHoldsWhileReadsAreUnderWay(t *testing.T) {
	cache := NewSourceCache()
	entered, release := make(chan struct{}), make(chan struct{})
	cache.Fetch = func(context.Context, stacks.Env) (*MediaSourceCatalogue, error) {
		entered <- struct{}{}
		<-release
		return &MediaSourceCatalogue{}, nil
	}
	var wg sync.WaitGroup
	for index := range sourceCacheSize + 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := cache.Catalogue(context.Background(), "busy/"+strconv.Itoa(index), nil); err != nil {
				t.Error(err)
			}
		}()
		<-entered
	}
	close(release)
	wg.Wait()
	if kept := len(cache.slots); kept > sourceCacheSize {
		t.Fatalf("%d catalogues kept after %d were read at once", kept, sourceCacheSize+2)
	}
}
