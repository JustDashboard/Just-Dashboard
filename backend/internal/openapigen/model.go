package openapigen

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// The model both emitters write from: every named type the document
// declares or reaches, and every operation, with names already chosen so the
// Go and TypeScript clients call each thing the same.

// Kind is what a value is, as JSON Schema says it.
type Kind int

const (
	KindAny Kind = iota
	KindString
	KindInt64
	KindInt32
	KindFloat
	KindBool
	KindTime
	KindBytes
	// KindNamed is a declaration: a struct or an enum, by its name.
	KindNamed
	KindArray
	KindMap
	// KindUnion is one of several alternatives (anyOf, or a oneOf that is
	// not a value-or-null).
	KindUnion
)

// Type is one value's type.
type Type struct {
	Kind     Kind
	Nullable bool
	Name     string
	Elem     *Type
	Options  []*Type
}

// Decl is a named type: a struct (Fields) or an enum (Enum, its values).
type Decl struct {
	Name string
	// Source is the component the declaration is, or where an inline object
	// was met, for its comment.
	Source string
	Doc    string
	Enum   []string
	Fields []Field
	// Inline is an object the document spells out where it is used (an
	// answer a handler assembles, an anonymous struct) rather than a
	// component.
	Inline bool
}

// Field is one property of a struct.
type Field struct {
	JSON      string
	Go        string
	Type      *Type
	Doc       string
	WriteOnly bool
}

// Param is a path or query parameter.
type Param struct {
	Name string
	Go   string
	TS   string
	Type *Type
	Doc  string
}

// Operation is one route.
type Operation struct {
	ID          string
	Go          string
	Method      string
	Path        string
	Summary     string
	Description string
	PathParams  []Param
	QueryParams []Param
	// Body is the JSON request body's type; nil when the route takes none.
	Body         *Type
	BodyRequired bool
	Status       int
	// Response is a JSON answer's type; nil when the answer has no body, is
	// not JSON (Content) or is a WebSocket upgrade (Stream).
	Response    *Type
	Content     string
	Stream      bool
	DeployKey   string
	Destructive bool
	Session     bool // only a session may call it: no key scheme is listed
}

// API is the whole document as the emitters read it.
type API struct {
	Title    string
	BasePath string
	Decls    []*Decl
	Ops      []*Operation
	// ErrorFields are the error envelope's properties.
	ErrorFields []Field
}

// Decl finds a declaration by name.
func (a *API) Decl(name string) *Decl {
	for _, d := range a.Decls {
		if d.Name == name {
			return d
		}
	}
	return nil
}

type parser struct {
	doc        map[string]any
	components map[string]any
	// names is each component's declaration name, and enumNames each
	// vocabulary title's.
	names     map[string]string
	enumNames map[string]string
	decls     map[string]*Decl
	// taken is every declaration name in use, with what took it.
	taken map[string]string
}

// reserved are names the generated runtime declares itself.
var reserved = map[string]bool{
	"Client": true, "Error": true, "RequestOption": true, "BasePath": true, "WithHeader": true,
	"NewClient": true, "ApiError": true, "ClientOptions": true, "RequestOptions": true,
}

// Parse reads an OpenAPI 3.1 document into the model.
func Parse(document []byte) (*API, error) {
	var doc map[string]any
	if err := json.Unmarshal(document, &doc); err != nil {
		return nil, fmt.Errorf("the document is not JSON: %w", err)
	}
	p := &parser{doc: doc, names: map[string]string{}, enumNames: map[string]string{}, decls: map[string]*Decl{}, taken: map[string]string{}}
	components, _ := doc["components"].(map[string]any)
	p.components, _ = components["schemas"].(map[string]any)
	if err := p.name(); err != nil {
		return nil, err
	}
	api := &API{BasePath: "/api/v1"}
	if info, ok := doc["info"].(map[string]any); ok {
		api.Title, _ = info["title"].(string)
	}
	if servers, ok := doc["servers"].([]any); ok && len(servers) > 0 {
		if server, ok := servers[0].(map[string]any); ok {
			if u, ok := server["url"].(string); ok && strings.HasPrefix(u, "/") {
				api.BasePath = strings.TrimRight(u, "/")
			}
		}
	}
	for _, name := range sortedKeys(p.components) {
		schema, _ := p.components[name].(map[string]any)
		decl := p.decls[p.names[name]]
		decl.Doc, _ = schema["description"].(string)
		fields, err := p.fields(decl.Name, schema)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		decl.Fields = fields
	}
	ops, err := p.operations()
	if err != nil {
		return nil, err
	}
	api.Ops = ops
	api.ErrorFields, err = p.errorFields()
	if err != nil {
		return nil, err
	}
	for _, name := range sortedKeys(p.decls) {
		api.Decls = append(api.Decls, p.decls[name])
	}
	return api, nil
}

// name chooses every component's and every vocabulary's declaration name
// before any is used: the type's own name without its package, unless two
// share it, or it is one the runtime declares, when the package is kept.
func (p *parser) name() error {
	type candidate struct {
		source string
		enum   []string
	}
	byShort := map[string][]candidate{}
	for name := range p.components {
		byShort[exported(shortName(name))] = append(byShort[exported(shortName(name))], candidate{source: name})
	}
	enums := map[string][]string{}
	var walkErr error
	walk(p.doc, func(schema map[string]any) {
		title, _ := schema["title"].(string)
		values, ok := schema["enum"].([]any)
		if !ok || title == "" {
			return
		}
		list := make([]string, 0, len(values))
		for _, v := range values {
			s, ok := v.(string)
			if !ok {
				walkErr = fmt.Errorf("vocabulary %s has a value that is not a string: %v", title, v)
				return
			}
			list = append(list, s)
		}
		if seen, ok := enums[title]; ok && strings.Join(seen, "\x00") != strings.Join(list, "\x00") {
			walkErr = fmt.Errorf("vocabulary %s is spelled with two different value lists", title)
			return
		}
		enums[title] = list
	})
	if walkErr != nil {
		return walkErr
	}
	for title, values := range enums {
		short := exported(shortName(title))
		byShort[short] = append(byShort[short], candidate{source: title, enum: values})
	}
	for _, short := range sortedKeys(byShort) {
		group := byShort[short]
		sort.Slice(group, func(i, j int) bool { return group[i].source < group[j].source })
		for _, c := range group {
			name := short
			if len(group) > 1 || reserved[short] {
				name = exported(strings.ReplaceAll(c.source, ".", " "))
			}
			if other, ok := p.taken[name]; ok {
				return fmt.Errorf("%s and %s would both be named %s", other, c.source, name)
			}
			p.taken[name] = c.source
			if c.enum != nil {
				p.enumNames[c.source] = name
				p.decls[name] = &Decl{Name: name, Source: c.source, Enum: c.enum}
			} else {
				p.names[c.source] = name
				p.decls[name] = &Decl{Name: name, Source: c.source}
			}
		}
	}
	return nil
}

// shortName is a component's or a vocabulary's name without its package.
func shortName(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

// walk calls fn on every object in a decoded JSON tree.
func walk(node any, fn func(map[string]any)) {
	switch value := node.(type) {
	case map[string]any:
		fn(value)
		for _, k := range sortedKeys(value) {
			walk(value[k], fn)
		}
	case []any:
		for _, one := range value {
			walk(one, fn)
		}
	}
}

// inline declares an object spelled out where it is used, under a name built
// from where it was met, numbered if that name is taken.
func (p *parser) inline(hint, source string, schema map[string]any) (string, error) {
	name := hint
	if reserved[name] {
		name += "Value"
	}
	for n := 2; ; n++ {
		if _, ok := p.taken[name]; !ok {
			break
		}
		name = fmt.Sprintf("%s%d", hint, n)
	}
	p.taken[name] = source
	decl := &Decl{Name: name, Source: source, Inline: true}
	decl.Doc, _ = schema["description"].(string)
	p.decls[name] = decl
	fields, err := p.fields(name, schema)
	if err != nil {
		return "", err
	}
	decl.Fields = fields
	return name, nil
}

// fields reads a struct's properties, in the order of their names.
func (p *parser) fields(owner string, schema map[string]any) ([]Field, error) {
	props, _ := schema["properties"].(map[string]any)
	out := make([]Field, 0, len(props))
	used := map[string]bool{}
	for _, name := range sortedKeys(props) {
		prop, _ := props[name].(map[string]any)
		goName := exported(name)
		for n := 2; used[goName]; n++ {
			goName = fmt.Sprintf("%s%d", exported(name), n)
		}
		used[goName] = true
		t, err := p.typeOf(prop, owner+goName, owner+"."+name)
		if err != nil {
			return nil, err
		}
		doc, _ := prop["description"].(string)
		writeOnly, _ := prop["writeOnly"].(bool)
		out = append(out, Field{JSON: name, Go: goName, Type: t, Doc: doc, WriteOnly: writeOnly})
	}
	return out, nil
}

// typeOf reads one schema. hint names an object it has to declare.
func (p *parser) typeOf(schema map[string]any, hint, source string) (*Type, error) {
	if schema == nil {
		return &Type{Kind: KindAny}, nil
	}
	if ref, ok := schema["$ref"].(string); ok {
		const prefix = "#/components/schemas/"
		name, ok := p.names[strings.TrimPrefix(ref, prefix)]
		if !strings.HasPrefix(ref, prefix) || !ok {
			return nil, fmt.Errorf("%s: $ref %q names no component", source, ref)
		}
		return &Type{Kind: KindNamed, Name: name}, nil
	}
	if all, ok := schema["allOf"].([]any); ok {
		if len(all) != 1 {
			return nil, fmt.Errorf("%s: allOf of %d schemas is not described", source, len(all))
		}
		one, _ := all[0].(map[string]any)
		return p.typeOf(one, hint, source)
	}
	for _, key := range []string{"oneOf", "anyOf"} {
		alternatives, ok := schema[key].([]any)
		if !ok {
			continue
		}
		var options []map[string]any
		nullable := false
		for _, raw := range alternatives {
			alt, _ := raw.(map[string]any)
			if types := typeList(alt["type"]); len(types) == 1 && types[0] == "null" && len(alt) == 1 {
				nullable = true
				continue
			}
			options = append(options, alt)
		}
		if len(options) == 1 {
			t, err := p.typeOf(options[0], hint, source)
			if err != nil {
				return nil, err
			}
			t.Nullable = t.Nullable || nullable
			return t, nil
		}
		union := &Type{Kind: KindUnion, Nullable: nullable}
		for i, alt := range options {
			t, err := p.typeOf(alt, fmt.Sprintf("%sOption%d", hint, i+1), source)
			if err != nil {
				return nil, err
			}
			union.Options = append(union.Options, t)
		}
		return union, nil
	}
	types := typeList(schema["type"])
	nullable := false
	main := ""
	for _, t := range types {
		if t == "null" {
			nullable = true
		} else if main == "" {
			main = t
		} else {
			return nil, fmt.Errorf("%s: a value of types %v is not described", source, types)
		}
	}
	if values, ok := schema["enum"].([]any); ok {
		title, _ := schema["title"].(string)
		name, ok := p.enumNames[title]
		if !ok {
			// A vocabulary without a name is declared where it is used.
			list := make([]string, 0, len(values))
			for _, v := range values {
				s, ok := v.(string)
				if !ok {
					return nil, fmt.Errorf("%s: an enum value that is not a string: %v", source, v)
				}
				list = append(list, s)
			}
			name = hint
			for n := 2; ; n++ {
				if _, taken := p.taken[name]; !taken {
					break
				}
				name = fmt.Sprintf("%s%d", hint, n)
			}
			p.taken[name] = source
			p.decls[name] = &Decl{Name: name, Source: source, Enum: list, Inline: true}
		}
		return &Type{Kind: KindNamed, Name: name, Nullable: nullable}, nil
	}
	t := &Type{Nullable: nullable}
	switch main {
	case "string":
		format, _ := schema["format"].(string)
		encoding, _ := schema["contentEncoding"].(string)
		switch {
		case format == "date-time":
			t.Kind = KindTime
		case encoding == "base64" || format == "binary":
			t.Kind = KindBytes
		default:
			t.Kind = KindString
		}
	case "integer":
		if format, _ := schema["format"].(string); format == "int32" {
			t.Kind = KindInt32
		} else {
			t.Kind = KindInt64
		}
	case "number":
		t.Kind = KindFloat
	case "boolean":
		t.Kind = KindBool
	case "array":
		items, _ := schema["items"].(map[string]any)
		elem, err := p.typeOf(items, hint+"Item", source+"[]")
		if err != nil {
			return nil, err
		}
		t.Kind, t.Elem = KindArray, elem
	case "object":
		if _, ok := schema["properties"].(map[string]any); ok {
			name, err := p.inline(hint, source, schema)
			if err != nil {
				return nil, err
			}
			t.Kind, t.Name = KindNamed, name
			break
		}
		t.Kind, t.Elem = KindMap, &Type{Kind: KindAny}
		if values, ok := schema["additionalProperties"].(map[string]any); ok {
			elem, err := p.typeOf(values, hint+"Value", source+"{}")
			if err != nil {
				return nil, err
			}
			t.Elem = elem
		}
	case "":
		t.Kind = KindAny
	default:
		return nil, fmt.Errorf("%s: type %q is not described", source, main)
	}
	return t, nil
}

func typeList(v any) []string {
	switch typ := v.(type) {
	case string:
		return []string{typ}
	case []any:
		out := make([]string, 0, len(typ))
		for _, one := range typ {
			if s, ok := one.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

var methodOrder = []string{"get", "put", "post", "delete", "patch", "head", "options", "trace"}

// operations reads every operation, ordered by its operationId.
func (p *parser) operations() ([]*Operation, error) {
	paths, _ := p.doc["paths"].(map[string]any)
	var out []*Operation
	seen := map[string]string{}
	for _, path := range sortedKeys(paths) {
		item, _ := paths[path].(map[string]any)
		shared, _ := item["parameters"].([]any)
		for _, method := range methodOrder {
			raw, ok := item[method].(map[string]any)
			if !ok {
				continue
			}
			op, err := p.operation(strings.ToUpper(method), path, raw, shared)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", strings.ToUpper(method), path, err)
			}
			if other, ok := seen[op.Go]; ok {
				return nil, fmt.Errorf("%s %s and %s would both be called %s", strings.ToUpper(method), path, other, op.Go)
			}
			seen[op.Go] = op.Method + " " + op.Path
			out = append(out, op)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Go < out[j].Go })
	return out, nil
}

func (p *parser) operation(method, path string, raw map[string]any, shared []any) (*Operation, error) {
	id, _ := raw["operationId"].(string)
	if id == "" {
		return nil, fmt.Errorf("no operationId")
	}
	op := &Operation{ID: id, Go: exported(id), Method: method, Path: path}
	op.Summary, _ = raw["summary"].(string)
	op.Description, _ = raw["description"].(string)
	op.DeployKey, _ = raw["x-deploy-key"].(string)
	op.Destructive, _ = raw["x-destructive"].(bool)
	op.Session = true
	if security, ok := raw["security"].([]any); ok {
		for _, requirement := range security {
			for scheme := range requirement.(map[string]any) {
				if scheme != "session" {
					op.Session = false
				}
			}
		}
	}
	params := append(append([]any{}, shared...), anyList(raw["parameters"])...)
	usedGo, usedTS := map[string]bool{}, map[string]bool{}
	for _, rawParam := range params {
		param, _ := rawParam.(map[string]any)
		name, _ := param["name"].(string)
		in, _ := param["in"].(string)
		schema, _ := param["schema"].(map[string]any)
		t, err := p.typeOf(schema, op.Go+exported(name), id+"."+name)
		if err != nil {
			return nil, err
		}
		doc, _ := param["description"].(string)
		one := Param{Name: name, Type: t, Doc: doc, Go: unexported(name), TS: camel(name)}
		if in == "query" {
			one.Go = exported(name)
		}
		for goReserved[one.Go] || usedGo[one.Go] {
			one.Go += "Param"
		}
		for tsReserved[one.TS] || usedTS[one.TS] {
			one.TS += "Param"
		}
		usedGo[one.Go], usedTS[one.TS] = true, true
		switch in {
		case "path":
			op.PathParams = append(op.PathParams, one)
		case "query":
			op.QueryParams = append(op.QueryParams, one)
		default:
			return nil, fmt.Errorf("a parameter in %q is not described", in)
		}
	}
	// Path parameters are taken in the order the path names them.
	order := map[string]int{}
	for i, part := range strings.Split(path, "/") {
		order[strings.Trim(part, "{}")] = i
	}
	sort.SliceStable(op.PathParams, func(i, j int) bool { return order[op.PathParams[i].Name] < order[op.PathParams[j].Name] })
	if body, ok := raw["requestBody"].(map[string]any); ok {
		content, _ := body["content"].(map[string]any)
		if media, ok := content["application/json"].(map[string]any); ok {
			schema, _ := media["schema"].(map[string]any)
			t, err := p.typeOf(schema, op.Go+"Request", id+" request")
			if err != nil {
				return nil, err
			}
			op.Body = t
			op.BodyRequired, _ = body["required"].(bool)
		}
	}
	responses, _ := raw["responses"].(map[string]any)
	for _, code := range sortedKeys(responses) {
		status, err := strconv.Atoi(code)
		if err != nil || status >= 300 {
			continue
		}
		op.Status = status
		response, _ := responses[code].(map[string]any)
		if status == http.StatusSwitchingProtocols {
			op.Stream = true
			break
		}
		content, _ := response["content"].(map[string]any)
		for _, media := range sortedKeys(content) {
			one, _ := content[media].(map[string]any)
			schema, _ := one["schema"].(map[string]any)
			if media != "application/json" {
				op.Content = media
				continue
			}
			t, err := p.typeOf(schema, op.Go+"Response", id+" response")
			if err != nil {
				return nil, err
			}
			op.Response = t
		}
		if op.Response != nil {
			op.Content = ""
		}
		break
	}
	if op.Status == 0 {
		return nil, fmt.Errorf("no successful response")
	}
	return op, nil
}

// errorFields reads the error envelope every refusal is written in.
func (p *parser) errorFields() ([]Field, error) {
	components, _ := p.doc["components"].(map[string]any)
	responses, _ := components["responses"].(map[string]any)
	errResponse, _ := responses["Error"].(map[string]any)
	content, _ := errResponse["content"].(map[string]any)
	media, _ := content["application/json"].(map[string]any)
	schema, _ := media["schema"].(map[string]any)
	props, _ := schema["properties"].(map[string]any)
	inner, _ := props["error"].(map[string]any)
	fields := map[string]any{}
	if inner != nil {
		fields, _ = inner["properties"].(map[string]any)
	}
	if _, ok := fields["code"]; !ok || fields["message"] == nil {
		return nil, fmt.Errorf("components.responses.Error does not describe {\"error\": {\"code\", \"message\"}}")
	}
	var out []Field
	for _, name := range sortedKeys(fields) {
		prop, _ := fields[name].(map[string]any)
		t, err := p.typeOf(prop, "Error"+exported(name), "Error."+name)
		if err != nil {
			return nil, err
		}
		switch t.Kind {
		case KindString, KindBool, KindInt64, KindInt32, KindFloat:
		default:
			return nil, fmt.Errorf("the error envelope's %s is not a scalar", name)
		}
		doc, _ := prop["description"].(string)
		out = append(out, Field{JSON: name, Go: exported(name), Type: t, Doc: doc})
	}
	return out, nil
}

func anyList(v any) []any {
	list, _ := v.([]any)
	return list
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
