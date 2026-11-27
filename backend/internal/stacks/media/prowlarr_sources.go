package media

// The media-source picker: the dashboard lists the sources Prowlarr can add,
// shows one's settings as a form, and adds what the person chose with what
// they typed, so nobody has to reach Prowlarr itself to finish a stack. Which
// source is the person's to decide, since it has to be one they are allowed
// to use: nothing here picks, suggests or ships one.
//
// Every request here is in Prowlarr's own OpenAPI document at the pinned tag
// (src/Prowlarr.Api.V1/openapi.json at v3.1.0.5799) and was run against the
// pinned image (lscr.io/linuxserver/prowlarr:3.1.0.5799-ls170):
//
//	GET    /api/v1/indexer/schema  every definition Prowlarr can add, with its fields (about 6 MB)
//	GET    /api/v1/appprofile      the app profiles; an indexer must name one
//	GET    /api/v1/indexer         the indexers added
//	GET    /api/v1/indexer/{id}    one of them
//	POST   /api/v1/indexer/test    Prowlarr's own test of an indexer before it is saved
//	POST   /api/v1/indexer         save one (?forceSave=true: the test above has just passed)
//	DELETE /api/v1/indexer/{id}    remove one
//	POST   /api/v1/command         {"name":"ApplicationIndexerSync"}: share the indexers with the applications now
//	GET    /api/v1/command/{id}    whether that command finished

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
)

// SourcesService is the service a media stack keeps its sources in. A preset
// without it (Essential) has no sources to pick.
const SourcesService = "prowlarr"

// MediaSourcePrivacy is who a source is open to, in Prowlarr's words.
type MediaSourcePrivacy string

const (
	SourcePublic      MediaSourcePrivacy = "public"
	SourceSemiPrivate MediaSourcePrivacy = "semiPrivate"
	SourcePrivate     MediaSourcePrivacy = "private"
)

// MediaSourceFieldType is how a setting is asked for in the form.
type MediaSourceFieldType string

const (
	SourceFieldText     MediaSourceFieldType = "text"
	SourceFieldPassword MediaSourceFieldType = "password"
	SourceFieldNumber   MediaSourceFieldType = "number"
	SourceFieldCheckbox MediaSourceFieldType = "checkbox"
	SourceFieldSelect   MediaSourceFieldType = "select"
)

// MediaSource is one of the sources (indexers) Prowlarr has.
type MediaSource struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	// Definition is Prowlarr's name for what it was made from ("Torznab",
	// or a site's definition file).
	Definition string             `json:"definition"`
	Privacy    MediaSourcePrivacy `json:"privacy"`
	Protocol   string             `json:"protocol"`
	Enabled    bool               `json:"enabled"`
}

// MediaSourceDefinition is one kind of source Prowlarr can add, as the catalogue
// lists it.
type MediaSourceDefinition struct {
	// Definition names it in the routes: Prowlarr's definition name, or for
	// the few presets that share one ("Torznab"), that name, a colon and the
	// preset's ("Torznab:Generic Torznab").
	Definition  string             `json:"definition"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Privacy     MediaSourcePrivacy `json:"privacy"`
	Language    string             `json:"language"`
	// NeedsInput: the form asks for something Prowlarr cannot do without
	// (an account, a key, a cookie, the address), so the source cannot be
	// added as it comes.
	NeedsInput bool `json:"needsInput"`
}

// MediaSourceDetail is one definition with the settings its form asks for.
type MediaSourceDetail struct {
	Definition  string             `json:"definition"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Privacy     MediaSourcePrivacy `json:"privacy"`
	URLs        []string           `json:"urls"`
	Fields      []MediaSourceField `json:"fields"`
}

// MediaSourceField is one setting. Value is Prowlarr's default, never anything a
// person typed before.
type MediaSourceField struct {
	Name     string               `json:"name"`
	Label    string               `json:"label"`
	Help     string               `json:"help"`
	Type     MediaSourceFieldType `json:"type"`
	Value    any                  `json:"value"`
	Options  []MediaSourceOption  `json:"options,omitempty"`
	Advanced bool                 `json:"advanced"`
}

// MediaSourceOption is one choice of a select.
type MediaSourceOption struct {
	Value any    `json:"value"`
	Label string `json:"label"`
}

// MediaSourceCatalogue is what Prowlarr can add: torrent definitions only (the
// stack's download client is qBittorrent), sorted by name.
type MediaSourceCatalogue struct {
	Definitions []MediaSourceDefinition `json:"definitions"`
	FetchedAt   time.Time               `json:"fetchedAt,omitzero"`
	entries     map[string]*prowlarrSchemaEntry
}

// Errors the routes turn into words.
var (
	ErrSourceDefinitionNotFound = errors.New("Prowlarr has no such source to add")
	ErrSourceNotFound           = errors.New("Prowlarr has no such source")
)

// SourceExistsError is a source whose name Prowlarr already has: it keeps
// names unique, whatever their case.
type SourceExistsError struct{ Name string }

func (e *SourceExistsError) Error() string {
	return fmt.Sprintf("Prowlarr already has a source named %q", e.Name)
}

// SourceInvalidError is a value the definition's form does not take.
type SourceInvalidError struct {
	Field   string
	Message string
}

func (e *SourceInvalidError) Error() string { return e.Message }

// SourceTestFailedError is Prowlarr's own test saying no, in its words, with
// every text the person typed taken out.
type SourceTestFailedError struct{ Message string }

func (e *SourceTestFailedError) Error() string { return e.Message }

// prowlarrSchemaLimit bounds the schema read: about 6 MB at the pinned
// version, and Prowlarr adds definitions with every release. A variable so a
// test can lower it.
var prowlarrSchemaLimit int64 = 32 << 20

// prowlarrSyncWait bounds how long an add waits for Prowlarr to share the new
// source with Radarr and Sonarr. The source is added either way.
var prowlarrSyncWait = time.Minute

// prowlarrField is one setting as the schema describes it.
type prowlarrField struct {
	Name            string          `json:"name"`
	Label           string          `json:"label"`
	HelpText        string          `json:"helpText"`
	HelpTextWarning string          `json:"helpTextWarning"`
	Unit            string          `json:"unit"`
	Value           json.RawMessage `json:"value"`
	Type            string          `json:"type"`
	Advanced        bool            `json:"advanced"`
	Hidden          string          `json:"hidden"`
	Privacy         string          `json:"privacy"`
	IsFloat         bool            `json:"isFloat"`
	SelectOptions   []struct {
		Value json.RawMessage `json:"value"`
		Name  string          `json:"name"`
	} `json:"selectOptions"`
	// SelectOptionsProviderAction names a select whose choices Prowlarr
	// computes; "getUrls" is the definition's own addresses (indexerUrls),
	// which is what POST indexer/action/getUrls answers.
	SelectOptionsProviderAction string `json:"selectOptionsProviderAction"`
}

// prowlarrSchemaEntry is one definition of GET indexer/schema, kept as what
// the catalogue lists and the resource an add starts from.
type prowlarrSchemaEntry struct {
	key            string
	DefinitionName string             `json:"definitionName"`
	Name           string             `json:"name"`
	Description    string             `json:"description"`
	Language       string             `json:"language"`
	Privacy        MediaSourcePrivacy `json:"privacy"`
	Protocol       string             `json:"protocol"`
	IndexerURLs    []string           `json:"indexerUrls"`
	// Fields are read to list the entry, then dropped: the catalogue keeps
	// them once, in resource, and schemaFields reads them from there.
	Fields []prowlarrField `json:"fields"`
	// resource is the entry as Prowlarr gave it, trimmed of what an add
	// does not send: its capabilities (most of its size, recomputed by
	// Prowlarr from the definition) and its presets.
	resource map[string]json.RawMessage
}

// FetchSourceCatalogue reads Prowlarr's schema, bounded, and keeps the torrent
// definitions.
func FetchSourceCatalogue(ctx context.Context, env stacks.Env) (*MediaSourceCatalogue, error) {
	client, err := newArrClient(ctx, env, arrProwlarr)
	if err != nil {
		return nil, err
	}
	var entries []*prowlarrSchemaEntry
	err = client.stream(ctx, http.MethodGet, "indexer/schema", prowlarrSchemaLimit, func(body io.Reader) error {
		decoder := json.NewDecoder(body)
		if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
			return fmt.Errorf("Prowlarr's list of sources was not the JSON list its API documents")
		}
		for decoder.More() {
			var resource map[string]json.RawMessage
			if err := decoder.Decode(&resource); err != nil {
				return fmt.Errorf("Prowlarr's list of sources could not be read: %w", err)
			}
			entry, keep, err := prowlarrTrim(resource)
			if err != nil {
				return err
			}
			if keep {
				entries = append(entries, entry)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return newSourceCatalogue(entries, time.Now().UTC()), nil
}

// prowlarrTrim reads one schema entry, keeping it only when it is a torrent
// source the stack can use.
func prowlarrTrim(resource map[string]json.RawMessage) (*prowlarrSchemaEntry, bool, error) {
	delete(resource, "capabilities")
	delete(resource, "presets")
	encoded, err := json.Marshal(resource)
	if err != nil {
		return nil, false, err
	}
	var entry prowlarrSchemaEntry
	if err := json.Unmarshal(encoded, &entry); err != nil {
		return nil, false, fmt.Errorf("a source in Prowlarr's list was not what its API documents: %w", err)
	}
	if entry.Protocol != "torrent" || entry.DefinitionName == "" || entry.Name == "" {
		return nil, false, nil
	}
	entry.resource = resource
	return &entry, true, nil
}

func newSourceCatalogue(entries []*prowlarrSchemaEntry, fetched time.Time) *MediaSourceCatalogue {
	named := map[string]int{}
	for _, entry := range entries {
		named[entry.DefinitionName]++
	}
	catalogue := &MediaSourceCatalogue{Definitions: []MediaSourceDefinition{}, FetchedAt: fetched, entries: map[string]*prowlarrSchemaEntry{}}
	for _, entry := range entries {
		key := entry.DefinitionName
		if named[key] > 1 {
			key += ":" + entry.Name
		}
		for n := 2; catalogue.entries[key] != nil; n++ {
			key = fmt.Sprintf("%s:%s (%d)", entry.DefinitionName, entry.Name, n)
		}
		entry.key = key
		catalogue.entries[key] = entry
		catalogue.Definitions = append(catalogue.Definitions, MediaSourceDefinition{
			Definition: key, Name: entry.Name, Description: strings.TrimSpace(entry.Description),
			Privacy: entry.Privacy, Language: entry.Language, NeedsInput: entry.needsInput(),
		})
		entry.Fields = nil
	}
	slices.SortStableFunc(catalogue.Definitions, func(a, b MediaSourceDefinition) int {
		if order := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); order != 0 {
			return order
		}
		return strings.Compare(a.Definition, b.Definition)
	})
	return catalogue
}

// credentialName is a setting's name that says it holds what a site gives
// its members: an account, a key, a cookie, a code.
var credentialName = regexp.MustCompile(`(?i)(user(name)?|email|pass(word|key)?|pin|pid|cookie|api_?key|rss_?key|token|secret|uid|2fa_?code)$`)

// secretName is a setting's name that says it holds a secret, which the
// schema often does not: most sites' keys, cookies and passkeys are plain
// textboxes there ("apikey", "cookie", "passkey").
var secretName = regexp.MustCompile(`(?i)(pass(word|key)?|pin|pid|cookie|api_?key|rss_?key|token|secret)$`)

// optionalWords are what a setting's label or help says when it may be left
// empty ("API Key (Optional)", "If your site requires a login cookie").
var optionalWords = []string{"optional", "leave empty", "leave blank", "if your site", "if the site"}

// needsInput reports whether the form asks for something with no usable
// default. The schema has no "required" mark, so it is read from what each
// shown setting is: an empty account, key, cookie or address is one Prowlarr
// cannot work without, as is a captcha, which only Prowlarr can show; an empty
// number or checkbox means Prowlarr's own default, a select with choices has
// its first, and a setting whose words say it is optional is.
func (e *prowlarrSchemaEntry) needsInput() bool {
	for _, field := range e.Fields {
		if !field.shown() && field.Type != "cardigannCaptcha" {
			continue
		}
		if field.Advanced || field.hasValue() || field.optional() {
			continue
		}
		switch field.Type {
		case "cardigannCaptcha":
			return true
		case "number", "checkbox":
		case "select":
			if len(field.SelectOptions) == 0 && field.SelectOptionsProviderAction == "" {
				return true
			}
		default:
			if field.formType() == SourceFieldPassword || field.Privacy == "userName" ||
				credentialName.MatchString(strings.ReplaceAll(field.Name, ".", "")) || field.Name == "baseUrl" {
				return true
			}
		}
	}
	return false
}

// shown is a setting the form asks for: not hidden, not an information line,
// not a captcha (which only Prowlarr's own page can show).
func (f prowlarrField) shown() bool {
	return f.Hidden != "hidden" && f.Type != "info" && f.Type != "cardigannCaptcha"
}

func (f prowlarrField) hasValue() bool {
	var value any
	if len(f.Value) == 0 || json.Unmarshal(f.Value, &value) != nil || value == nil {
		return false
	}
	text, isText := value.(string)
	return !isText || strings.TrimSpace(text) != ""
}

func (f prowlarrField) optional() bool {
	words := strings.ToLower(f.Label + " " + f.HelpText)
	for _, word := range optionalWords {
		if strings.Contains(words, word) {
			return true
		}
	}
	return false
}

// formType is how the form asks for a setting. A textbox Prowlarr keeps as a
// secret, or whose name says it is one (a key, a cookie, a passkey), is
// masked as a password is, and is the person's to type.
func (f prowlarrField) formType() MediaSourceFieldType {
	switch f.Type {
	case "password":
		return SourceFieldPassword
	case "number":
		return SourceFieldNumber
	case "checkbox":
		return SourceFieldCheckbox
	case "select":
		return SourceFieldSelect
	}
	if f.Privacy == "password" || f.Privacy == "apiKey" || secretName.MatchString(strings.ReplaceAll(f.Name, ".", "")) {
		return SourceFieldPassword
	}
	return SourceFieldText
}

func (f prowlarrField) urlChoices() bool {
	return f.SelectOptionsProviderAction == "getUrls"
}

// schemaFields are the entry's settings, read from what the catalogue keeps.
func (e *prowlarrSchemaEntry) schemaFields() ([]prowlarrField, error) {
	if e.Fields != nil {
		return e.Fields, nil
	}
	var fields []prowlarrField
	if err := json.Unmarshal(e.resource["fields"], &fields); err != nil {
		return nil, fmt.Errorf("%s's settings could not be read: %w", e.Name, err)
	}
	return fields, nil
}

// Lookup is one definition of the catalogue by its name in the routes.
func (c *MediaSourceCatalogue) Lookup(definition string) (MediaSourceDetail, bool) {
	entry, found := c.entries[definition]
	if !found {
		return MediaSourceDetail{}, false
	}
	detail := MediaSourceDetail{
		Definition: entry.key, Name: entry.Name, Description: strings.TrimSpace(entry.Description),
		Privacy: entry.Privacy, URLs: entry.urls(), Fields: []MediaSourceField{},
	}
	// The fields were read once already, when the catalogue was made.
	fields, _ := entry.schemaFields()
	for _, field := range fields {
		if !field.shown() {
			continue
		}
		detail.Fields = append(detail.Fields, entry.formField(field))
	}
	return detail, true
}

func (e *prowlarrSchemaEntry) urls() []string {
	urls := []string{}
	for _, address := range e.IndexerURLs {
		if strings.TrimSpace(address) != "" {
			urls = append(urls, address)
		}
	}
	return urls
}

func (e *prowlarrSchemaEntry) formField(field prowlarrField) MediaSourceField {
	out := MediaSourceField{Name: field.Name, Label: strings.TrimSpace(field.Label), Help: strings.TrimSpace(field.HelpText),
		Type: field.formType(), Value: rawValue(field.Value), Advanced: field.Advanced}
	if out.Label == "" {
		out.Label = field.Name
	}
	if field.Unit != "" {
		out.Help = strings.TrimSpace(out.Help + " In " + field.Unit + ".")
	}
	if field.HelpTextWarning != "" {
		out.Help = strings.TrimSpace(out.Help + " " + field.HelpTextWarning)
	}
	if out.Type == SourceFieldSelect {
		out.Options = []MediaSourceOption{}
		for _, option := range field.SelectOptions {
			out.Options = append(out.Options, MediaSourceOption{Value: rawValue(option.Value), Label: option.Name})
		}
		if field.urlChoices() {
			for _, address := range e.urls() {
				out.Options = append(out.Options, MediaSourceOption{Value: address, Label: address})
			}
			if out.Value == nil && len(out.Options) > 0 {
				out.Value = out.Options[0].Value
			}
		}
	}
	return out
}

func rawValue(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil
	}
	return value
}

// PreparedSource is a definition with the person's values, checked against its
// form and ready to be tested and added.
type PreparedSource struct {
	Definition string
	Name       string
	// Secret: a value went into a setting the form masks (a password, a key,
	// a cookie).
	Secret   bool
	typed    []string
	resource map[string]json.RawMessage
	fields   []map[string]any
}

// Prepare checks the person's values against the definition's form. Text is
// trimmed of the spaces a paste brings with it, except in a password, where a
// space may be part of it. A setting not given keeps Prowlarr's default; an
// address chosen from the definition's own keeps its first.
func (c *MediaSourceCatalogue) Prepare(definition string, values map[string]any) (*PreparedSource, error) {
	entry, found := c.entries[definition]
	if !found {
		return nil, ErrSourceDefinitionNotFound
	}
	schema, err := entry.schemaFields()
	if err != nil {
		return nil, err
	}
	byName := map[string]prowlarrField{}
	for _, field := range schema {
		byName[field.Name] = field
	}
	prepared := &PreparedSource{Definition: entry.key, Name: entry.Name, resource: entry.resource}
	given := map[string]any{}
	for name, value := range values {
		field, known := byName[name]
		if !known || !field.shown() {
			return nil, &SourceInvalidError{Field: name, Message: fmt.Sprintf("%s has no setting named %q", entry.Name, name)}
		}
		converted, set, err := field.convert(value, entry.urls())
		if err != nil {
			return nil, &SourceInvalidError{Field: name, Message: fmt.Sprintf("%s: %s", labelOf(field), err)}
		}
		if !set {
			continue
		}
		given[name] = converted
		if text, isText := converted.(string); isText && field.formType() != SourceFieldSelect && text != "" {
			prepared.typed = append(prepared.typed, text)
			if field.formType() == SourceFieldPassword {
				prepared.Secret = true
			}
		}
	}

	var fields []map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(entry.resource["fields"])))
	decoder.UseNumber()
	if err := decoder.Decode(&fields); err != nil {
		return nil, fmt.Errorf("%s's settings could not be read: %w", entry.Name, err)
	}
	for _, field := range fields {
		name, _ := field["name"].(string)
		if value, found := given[name]; found {
			field["value"] = value
			continue
		}
		if schema := byName[name]; schema.urlChoices() && !schema.hasValue() {
			if urls := entry.urls(); len(urls) > 0 {
				field["value"] = urls[0]
			}
		}
	}
	prepared.fields = fields
	return prepared, nil
}

func labelOf(field prowlarrField) string {
	if label := strings.TrimSpace(field.Label); label != "" {
		return label
	}
	return field.Name
}

// convert reads one value the way the form's setting holds it. set is false
// for a value that means "leave it": null, or an empty number.
func (f prowlarrField) convert(value any, urls []string) (converted any, set bool, err error) {
	if value == nil {
		return nil, false, nil
	}
	switch f.formType() {
	case SourceFieldText, SourceFieldPassword:
		text, isText := value.(string)
		if !isText {
			return nil, false, errors.New("must be text")
		}
		if f.Type != "password" {
			text = strings.TrimSpace(text)
		}
		return text, true, nil
	case SourceFieldNumber:
		var number float64
		switch typed := value.(type) {
		case float64:
			number = typed
		case json.Number:
			if number, err = typed.Float64(); err != nil {
				return nil, false, errors.New("must be a number")
			}
		case string:
			text := strings.TrimSpace(typed)
			if text == "" {
				return nil, false, nil
			}
			if number, err = strconv.ParseFloat(text, 64); err != nil {
				return nil, false, errors.New("must be a number")
			}
		default:
			return nil, false, errors.New("must be a number")
		}
		if f.IsFloat {
			return number, true, nil
		}
		if number != float64(int64(number)) {
			return nil, false, errors.New("must be a whole number")
		}
		return int64(number), true, nil
	case SourceFieldCheckbox:
		switch typed := value.(type) {
		case bool:
			return typed, true, nil
		case string:
			if parsed, err := strconv.ParseBool(strings.TrimSpace(typed)); err == nil {
				return parsed, true, nil
			}
		}
		return nil, false, errors.New("must be true or false")
	}
	// A select takes one of its own choices, as the choice itself.
	want := choiceText(value)
	for _, option := range f.SelectOptions {
		if choice := rawValue(option.Value); choiceText(choice) == want {
			return choice, true, nil
		}
	}
	if f.urlChoices() {
		for _, address := range urls {
			if address == want {
				return address, true, nil
			}
		}
	}
	return nil, false, fmt.Errorf("%q is not one of its choices", want)
}

// choiceText is a choice as text, so 1 from a form, "1" and Prowlarr's 1
// compare equal.
func choiceText(value any) string {
	switch typed := value.(type) {
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case json.Number:
		if number, err := typed.Float64(); err == nil {
			return strconv.FormatFloat(number, 'f', -1, 64)
		}
		return typed.String()
	case string:
		return strings.TrimSpace(typed)
	}
	return fmt.Sprint(value)
}

// Mask takes every text the person typed out of a message, as written and as
// a URL would carry it: a site's answer may echo a key back, and Prowlarr puts
// a Torznab key in the address it reports.
func (p *PreparedSource) Mask(text string) string {
	typed := slices.Clone(p.typed)
	// The longest first, so a value inside another is not masked half.
	slices.SortFunc(typed, func(a, b string) int { return len(b) - len(a) })
	for _, value := range typed {
		if len(value) < 3 {
			continue
		}
		for _, form := range []string{value, url.QueryEscape(value), url.PathEscape(value)} {
			text = strings.ReplaceAll(text, form, "••••••")
		}
	}
	return text
}

// resourceFor is the indexer Prowlarr is sent: the schema's entry with the
// person's values, switched on, in the app profile given.
func (p *PreparedSource) resourceFor(appProfile int) map[string]any {
	resource := make(map[string]any, len(p.resource))
	for name, value := range p.resource {
		resource[name] = value
	}
	resource["fields"] = p.fields
	resource["appProfileId"] = appProfile
	resource["enable"] = true
	return resource
}

// prowlarrListed is the part of GET indexer the picker reads.
type prowlarrListed struct {
	ID             int                `json:"id"`
	Name           string             `json:"name"`
	DefinitionName string             `json:"definitionName"`
	Privacy        MediaSourcePrivacy `json:"privacy"`
	Protocol       string             `json:"protocol"`
	Enable         bool               `json:"enable"`
}

func (l prowlarrListed) source() MediaSource {
	return MediaSource{ID: l.ID, Name: l.Name, Definition: l.DefinitionName, Privacy: l.Privacy, Protocol: l.Protocol, Enabled: l.Enable}
}

// ListSources is every source Prowlarr has, by name, whoever added it.
func ListSources(ctx context.Context, env stacks.Env) ([]MediaSource, error) {
	client, err := newArrClient(ctx, env, arrProwlarr)
	if err != nil {
		return nil, err
	}
	var listed []prowlarrListed
	if err := client.call(ctx, http.MethodGet, "indexer", nil, &listed); err != nil {
		return nil, err
	}
	sources := make([]MediaSource, 0, len(listed))
	for _, entry := range listed {
		sources = append(sources, entry.source())
	}
	slices.SortStableFunc(sources, func(a, b MediaSource) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return sources, nil
}

// AddOutcome is what an add came to.
type AddOutcome struct {
	Source MediaSource
	// Synced: Prowlarr finished sharing its sources with Radarr and Sonarr
	// before the add answered. When not, it still shares the new one on its
	// own: both applications are on full sync.
	Synced bool
}

// AddSource has Prowlarr test the source, saves it in Prowlarr's lowest app
// profile (the "Standard" it makes on its first start), and asks Prowlarr to
// share its sources with the applications now. Every error is masked of what
// the person typed.
func AddSource(ctx context.Context, env stacks.Env, prepared *PreparedSource) (AddOutcome, error) {
	outcome, err := addSource(ctx, env, prepared)
	if err == nil {
		return outcome, nil
	}
	var failed *SourceTestFailedError
	var exists *SourceExistsError
	if errors.As(err, &failed) || errors.As(err, &exists) {
		return AddOutcome{}, err
	}
	return AddOutcome{}, maskedError{err: err, text: prepared.Mask(err.Error())}
}

func addSource(ctx context.Context, env stacks.Env, prepared *PreparedSource) (AddOutcome, error) {
	client, err := newArrClient(ctx, env, arrProwlarr)
	if err != nil {
		return AddOutcome{}, err
	}
	var listed []prowlarrListed
	if err := client.call(ctx, http.MethodGet, "indexer", nil, &listed); err != nil {
		return AddOutcome{}, err
	}
	for _, entry := range listed {
		if strings.EqualFold(entry.Name, prepared.Name) {
			return AddOutcome{}, &SourceExistsError{Name: entry.Name}
		}
	}
	var profiles []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := client.call(ctx, http.MethodGet, "appprofile", nil, &profiles); err != nil {
		return AddOutcome{}, err
	}
	profile := 0
	for _, candidate := range profiles {
		if candidate.ID > 0 && (profile == 0 || candidate.ID < profile) {
			profile = candidate.ID
		}
	}
	if profile == 0 {
		return AddOutcome{}, errors.New("Prowlarr has no app profile to put the source in")
	}
	resource := prepared.resourceFor(profile)
	if err := client.call(ctx, http.MethodPost, "indexer/test", resource, nil); err != nil {
		return AddOutcome{}, prepared.testFailure(err)
	}
	// Saved without a second test: the one above has just passed, and a
	// second sign-in to a site within a second can trip its limit on them.
	var created prowlarrListed
	if err := client.call(ctx, http.MethodPost, "indexer?forceSave=true", resource, &created); err != nil {
		return AddOutcome{}, prepared.testFailure(err)
	}
	if created.ID <= 0 {
		return AddOutcome{}, fmt.Errorf("Prowlarr saved %q but gave it no id", prepared.Name)
	}
	env.Logf("Prowlarr's source %q was added after its own test passed", created.Name)
	synced, err := client.prowlarrSync(ctx)
	if err != nil {
		env.Logf("Prowlarr did not confirm it shared its sources: %v", err)
	}
	return AddOutcome{Source: created.source(), Synced: synced}, nil
}

// testFailure turns Prowlarr's refusal of a test or a save (400, with its
// reasons) into the test failure, masked; anything else stays an error.
func (p *PreparedSource) testFailure(err error) error {
	var problem *arrProblem
	if !errors.As(err, &problem) || problem.Status != http.StatusBadRequest {
		return err
	}
	reason := strings.Join(problem.Details, "; ")
	if reason == "" {
		reason = "it gave no reason"
	}
	return &SourceTestFailedError{Message: p.Mask(fmt.Sprintf("Prowlarr's test of %s failed: %s", p.Name, reason))}
}

// maskedError is an error whose words were masked; it still unwraps to what
// it was, so a caller can tell a timeout from a refusal.
type maskedError struct {
	err  error
	text string
}

func (e maskedError) Error() string { return e.text }
func (e maskedError) Unwrap() error { return e.err }

// prowlarrSync runs ApplicationIndexerSync and waits for it, so the answer can
// say the source is already in Radarr and Sonarr.
func (c *arrClient) prowlarrSync(ctx context.Context) (bool, error) {
	var command struct {
		ID     int    `json:"id"`
		Status string `json:"status"`
	}
	if err := c.call(ctx, http.MethodPost, "command", map[string]string{"name": "ApplicationIndexerSync"}, &command); err != nil {
		return false, err
	}
	deadline := time.Now().Add(prowlarrSyncWait)
	for command.Status != "completed" {
		switch command.Status {
		case "failed", "aborted", "cancelled", "orphaned":
			return false, fmt.Errorf("Prowlarr's sync to its applications %s", command.Status)
		}
		if time.Now().After(deadline) {
			return false, fmt.Errorf("Prowlarr had not finished sharing its sources after %s", prowlarrSyncWait)
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(arrPollInterval):
		}
		if err := c.call(ctx, http.MethodGet, "command/"+strconv.Itoa(command.ID), nil, &command); err != nil {
			return false, err
		}
	}
	return true, nil
}

// RemoveSource removes one of Prowlarr's sources, whoever added it. Prowlarr
// then removes it from Radarr and Sonarr itself, since both are on full sync.
func RemoveSource(ctx context.Context, env stacks.Env, id int) (MediaSource, error) {
	client, err := newArrClient(ctx, env, arrProwlarr)
	if err != nil {
		return MediaSource{}, err
	}
	var listed prowlarrListed
	err = client.call(ctx, http.MethodGet, "indexer/"+strconv.Itoa(id), nil, &listed)
	var problem *arrProblem
	if errors.As(err, &problem) && problem.Status == http.StatusNotFound {
		return MediaSource{}, ErrSourceNotFound
	}
	if err != nil {
		return MediaSource{}, err
	}
	err = client.call(ctx, http.MethodDelete, "indexer/"+strconv.Itoa(id), nil, nil)
	if errors.As(err, &problem) && problem.Status == http.StatusNotFound {
		return MediaSource{}, ErrSourceNotFound
	}
	if err != nil {
		return MediaSource{}, err
	}
	env.Logf("Prowlarr's source %q was removed", listed.Name)
	return listed.source(), nil
}

// stream makes one request and hands its answer to read, bounded: an answer
// longer than limit is an error, never cut short and read as something else.
func (c *arrClient) stream(ctx context.Context, method, path string, limit int64, read func(io.Reader) error) error {
	apiPath := "/api/" + c.app.api + "/" + strings.TrimLeft(path, "/")
	request, err := http.NewRequestWithContext(ctx, method, stacks.JoinURL(c.base, apiPath, nil), nil)
	if err != nil {
		return err
	}
	request.Header.Set("X-Api-Key", c.key)
	request.Header.Set("Accept", "application/json")
	response, err := c.env.HTTP().Do(request)
	if err != nil {
		return fmt.Errorf("%s did not answer: %w", c.app.title, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		answer, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		problem := &arrProblem{App: c.app.title, Method: method, Path: apiPath, Status: response.StatusCode, Details: arrProblemDetails(answer)}
		if response.StatusCode == http.StatusUnauthorized {
			return stacks.Permanent(fmt.Errorf("%s does not accept the API key the deployment gave it (%s): %w", c.app.title, c.app.keyVariable, problem))
		}
		return problem
	}
	counted := &countingReader{reader: io.LimitReader(response.Body, limit+1)}
	err = read(counted)
	if counted.read > limit {
		return fmt.Errorf("%s's answer to %s %s is larger than %d MiB, more than the dashboard reads", c.app.title, method, apiPath, limit>>20)
	}
	if err != nil {
		return err
	}
	// Whatever follows the document is read too, so an answer longer than the
	// limit is told apart from one that ended.
	if _, err := io.Copy(io.Discard, counted); err != nil {
		return fmt.Errorf("%s's answer could not be read: %w", c.app.title, err)
	}
	if counted.read > limit {
		return fmt.Errorf("%s's answer to %s %s is larger than %d MiB, more than the dashboard reads", c.app.title, method, apiPath, limit>>20)
	}
	return nil
}

type countingReader struct {
	reader io.Reader
	read   int64
}

func (r *countingReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	r.read += int64(n)
	return n, err
}

// SourceCatalogueTTL is how long a project's catalogue is kept: it is read
// whole from Prowlarr, and only changes when Prowlarr is updated.
const SourceCatalogueTTL = 10 * time.Minute

// sourceCacheSize bounds how many projects' catalogues are kept at once:
// each is about 6 MB in memory at Prowlarr 3.1.0.
const sourceCacheSize = 4

// SourceCache keeps each project's catalogue for SourceCatalogueTTL. Two
// requests for one that is not kept share one read of Prowlarr's schema.
type SourceCache struct {
	TTL time.Duration
	Now func() time.Time
	// Fetch reads a catalogue; FetchSourceCatalogue unless a test sets it.
	Fetch func(ctx context.Context, env stacks.Env) (*MediaSourceCatalogue, error)

	mu    sync.Mutex
	slots map[string]*sourceCacheSlot
}

type sourceCacheSlot struct {
	mu        sync.Mutex
	catalogue *MediaSourceCatalogue
	kept      time.Time
}

func NewSourceCache() *SourceCache {
	return &SourceCache{TTL: SourceCatalogueTTL, Now: time.Now, Fetch: FetchSourceCatalogue, slots: map[string]*sourceCacheSlot{}}
}

// Catalogue is the catalogue kept under key (a project and its live release),
// read from Prowlarr when there is none or it is older than the TTL.
func (c *SourceCache) Catalogue(ctx context.Context, key string, env stacks.Env) (*MediaSourceCatalogue, error) {
	c.mu.Lock()
	slot := c.slots[key]
	if slot == nil {
		if len(c.slots) >= sourceCacheSize {
			c.evictLocked()
		}
		slot = &sourceCacheSlot{}
		c.slots[key] = slot
	}
	c.mu.Unlock()

	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.catalogue != nil && c.Now().Sub(slot.kept) < c.TTL {
		return slot.catalogue, nil
	}
	catalogue, err := c.Fetch(ctx, env)
	if err != nil {
		return nil, err
	}
	slot.catalogue, slot.kept = catalogue, c.Now()
	return catalogue, nil
}

// evictLocked drops the slot kept longest ago. Callers hold c.mu.
func (c *SourceCache) evictLocked() {
	oldest, oldestKey := time.Time{}, ""
	for key, slot := range c.slots {
		if !slot.mu.TryLock() {
			continue
		}
		kept := slot.kept
		slot.mu.Unlock()
		if oldestKey == "" || kept.Before(oldest) {
			oldest, oldestKey = kept, key
		}
	}
	if oldestKey != "" {
		delete(c.slots, oldestKey)
	}
}
