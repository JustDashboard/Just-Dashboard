package proxysvc

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// What a save from the form would lose.
//
// The form reads a file into a SiteSpec and saves by writing the spec out
// again, so anything the spec has no field for — a proxy_set_header added by
// hand, a listen on a second port, a whole location the form cannot express —
// is gone after the next save, and nothing said so. The answer here is the
// file compared with what the form would write in its place, statement by
// statement as nginx reads them: every statement of the file that is not
// written back where it was.

// ContentDigest names a site file's bytes, so a form that read one version of
// a file can tell that the file is still that version when it saves.
func ContentDigest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// ErrSiteChanged refuses a save over a file that changed after the form read
// it: somebody else's edit, or the raw editor's, which the save would undo.
var ErrSiteChanged = errors.New("the site's file changed after the form read it")

// DroppedLine is a statement of a site file that saving the form in its place
// does not write back.
type DroppedLine struct {
	// Line is where the statement starts in the file, and Lines how many
	// lines Text takes.
	Line  int `json:"line"`
	Lines int `json:"lines"`
	// Text is the statement: its own lines of the file, dedented, when it
	// has them to itself, or the statement written out on one line when it
	// shares a line with others.
	Text string `json:"text"`
	// Context is where it sits: "server", "location /api/", "server on port
	// 80" for a server block other than the one the form writes into, or
	// "outside any server".
	Context string `json:"context"`
	// Movable says it sits directly in the server block the form writes, and
	// that the form's extra configuration, which goes there, keeps it as it
	// is. Anything inside a location, in another server block or outside
	// them all has nowhere in the form to go.
	Movable bool `json:"movable"`
	// Reason says why a statement of that server block cannot move: the form
	// writes a directive of its own that nginx refuses to see twice, or the
	// same statement moves from another line.
	Reason string `json:"reason,omitempty"`
}

// DroppedLines are the statements of content, the site file at path, that
// saving spec in its place does not write back. included lists the names of
// the directives the files an include pattern matches set, where they can be
// read, and may be nil.
//
// Statements are compared as nginx reads them, so spacing, quoting and
// comments do not count, and a few spellings the form writes differently
// with the same effect are counted as the same statement: `listen 443 ssl
// http2` for `listen 443 ssl` and `http2 on`, a proxy_pass whose path is
// the location's own, a folder alias with or without its trailing slash, and
// a server_name split over several lines. Comments are not statements, and
// a save drops them too; the whole difference is the form's Changes view.
func DroppedLines(path, content string, spec *SiteSpec, included func(pattern string) []string) ([]DroppedLine, error) {
	file, err := readDriftFile(path, content)
	if err != nil {
		return nil, err
	}
	stmts, err := file.against(spec, included)
	if err != nil {
		return nil, err
	}
	return file.lines(stmts), nil
}

// SiteDrift is what saving current over the site file at path, whose content
// is content, drops of what the form cannot hold: the statements the form's
// own reading of the file leaves out — which is what a save of the file as
// read would drop — less any that current now writes back itself, such as
// lines moved into its extra configuration. An edit made in the form is not
// a dropped line; the file's Changes are where it shows. Without current,
// it is what a save of the file as read drops.
func (s *Service) SiteDrift(name, path, content string, current *SiteSpec) ([]DroppedLine, error) {
	file, err := readDriftFile(path, content)
	if err != nil {
		return nil, err
	}
	read, _ := ParseSiteSpec(name, content)
	base, err := file.against(read, s.includedNames)
	if err != nil || current == nil {
		return file.lines(base), err
	}
	now, err := file.against(current, s.includedNames)
	if err != nil {
		return nil, err
	}
	missing := map[*Directive][]int{}
	for _, st := range base {
		missing[st.directive] = st.missing
	}
	kept := now[:0]
	for _, st := range now {
		before, ok := missing[st.directive]
		if !ok {
			continue
		}
		// A server_name is dropped name by name: only the names both
		// comparisons leave out are still dropped.
		if st.missing != nil || before != nil {
			if st.missing = common(st.missing, before); len(st.missing) == 0 {
				continue
			}
		}
		kept = append(kept, st)
	}
	return file.lines(kept), nil
}

// includedNames are the directives set by the files an include pattern
// matches, resolved against the configuration directory the way nginx
// resolves a relative include. Only the names leave here, and only to say
// that a moved include would repeat a directive the form writes; a file that
// cannot be read contributes nothing.
func (s *Service) includedNames(pattern string) []string {
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(s.nginxDir, pattern)
	}
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	var names []string
	for _, f := range files {
		// A glob skips dotfiles the way nginx's include does.
		if strings.HasPrefix(filepath.Base(f), ".") && !strings.HasPrefix(filepath.Base(pattern), ".") {
			continue
		}
		info, err := os.Stat(f)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		directives, err := ParseNginxFile(f, string(raw), []string{"http", "server"})
		if err != nil {
			continue
		}
		for _, d := range directives {
			names = append(names, d.Name)
		}
	}
	return names
}

// refusedTwice are the directives the form writes into its server block that
// nginx refuses to see twice there ("directive is duplicate"), checked
// against nginx 1.26. The others it writes — listen, server_name, add_header,
// the log paths, ssl_protocols, ssl_session_cache, index, return, allow and
// deny — nginx takes twice, adding or merging.
var refusedTwice = map[string]bool{
	"http2": true, "ssl_prefer_server_ciphers": true, "ssl_session_timeout": true,
	"ssl_session_tickets": true, "client_max_body_size": true, "gzip": true, "gzip_vary": true,
	"auth_basic": true, "auth_basic_user_file": true, "root": true,
}

// driftFile is a site file as nginx reads it, with where each statement sits
// in its text.
type driftFile struct {
	path   string
	source []string
	tokens []nginxToken
	tree   []Directive
	spans  map[*Directive]tokenSpan
}

// tokenSpan is the first and last token of a statement: its name to its ";",
// or to the "}" that closes its block.
type tokenSpan struct{ first, last int }

func readDriftFile(path, content string) (*driftFile, error) {
	tree, err := ParseNginxFile(path, content, []string{"http"})
	if err != nil {
		return nil, err
	}
	tokens, err := tokenizeNginx(path, content)
	if err != nil {
		return nil, err
	}
	// The statements in the order the parser meets them — a block before
	// what is inside it — which is the order of a walk of its tree.
	var spans []tokenSpan
	var open []int
	start := -1
	for i, tok := range tokens {
		if tok.quoted || (tok.text != ";" && tok.text != "{" && tok.text != "}") {
			if start < 0 {
				start = i
			}
			continue
		}
		switch tok.text {
		case ";":
			spans = append(spans, tokenSpan{start, i})
		case "{":
			open = append(open, len(spans))
			spans = append(spans, tokenSpan{first: start})
		case "}":
			if len(open) > 0 {
				spans[open[len(open)-1]].last = i
				open = open[:len(open)-1]
			}
		}
		start = -1
	}
	f := &driftFile{
		path: path, source: strings.Split(content, "\n"), tokens: tokens, tree: tree,
		spans: map[*Directive]tokenSpan{},
	}
	next := 0
	var walk func([]Directive)
	walk = func(ds []Directive) {
		for i := range ds {
			if next < len(spans) {
				f.spans[&ds[i]] = spans[next]
			}
			next++
			if ds[i].Block != nil {
				walk(ds[i].Block)
			}
		}
	}
	walk(tree)
	return f, nil
}

// droppedStmt is one statement of the file that a save does not write back:
// all of it, or — for a server_name — the names in missing.
type droppedStmt struct {
	directive *Directive
	missing   []int
	context   string
	movable   bool
	reason    string
}

// driftScope is where in the file a comparison is.
type driftScope struct {
	context string
	// server is the block of the written file this part of the file is
	// compared with; main says it is the one the form writes its extra
	// configuration into, and top that the statements sit directly in it.
	server *Directive
	main   bool
	top    bool
	// location is the path of the location the statements are in, which a
	// proxy_pass is read against.
	location string
}

// against compares the file with what saving spec writes.
func (f *driftFile) against(spec *SiteSpec, included func(string) []string) ([]droppedStmt, error) {
	rendered, err := RenderNginx(spec)
	if err != nil {
		return nil, err
	}
	written, err := ParseNginxFile(f.path, rendered, []string{"http"})
	if err != nil {
		return nil, err
	}
	c := &driftCompare{included: included}
	c.top(f.tree, written)
	c.dedupe()
	return c.out, nil
}

type driftCompare struct {
	included func(string) []string
	out      []droppedStmt
}

// top compares the file's top level with the written one's. Each server
// block of the file is compared with the written block it shares the most
// with, and several may be compared with the same one: a site written as a
// block for port 80 and another for 443 is one block listening on both in
// the form, and that loses neither. A block with none of the site's names —
// a catch-all, or another site in the same file — shares nothing with it.
func (c *driftCompare) top(file, written []Directive) {
	var servers []*Directive
	for i := range written {
		if written[i].Name == "server" && written[i].Block != nil {
			servers = append(servers, &written[i])
		}
	}
	var main *Directive
	if len(servers) > 0 {
		main = servers[len(servers)-1]
	}
	rest := newDriftUnits(written, "")
	for i := range file {
		d := &file[i]
		if d.Name != "server" || d.Block == nil {
			c.compare(d, rest, driftScope{context: "outside any server"})
			continue
		}
		match := bestServer(d, servers, main)
		scope := driftScope{context: serverContext(d, match == main && match != nil), server: match, main: match == main, top: true}
		if match == nil {
			c.drop(d, nil, scope)
			continue
		}
		c.block(d.Block, match.Block, scope)
	}
}

// block compares the statements of a block with those of its written twin.
func (c *driftCompare) block(file, written []Directive, scope driftScope) {
	units := newDriftUnits(written, scope.location)
	for i := range file {
		c.compare(&file[i], units, scope)
	}
}

func (c *driftCompare) compare(d *Directive, units *driftUnits, scope driftScope) {
	if d.Block == nil {
		var missing []int
		for i, key := range unitKeys(d, scope.location) {
			if units.leaves[key] > 0 {
				units.leaves[key]--
			} else {
				missing = append(missing, i)
			}
		}
		if missing == nil {
			return
		}
		if d.Name != "server_name" {
			missing = nil
		}
		c.drop(d, missing, scope)
		return
	}
	twin := units.take(d)
	if twin == nil {
		c.drop(d, nil, scope)
		return
	}
	inner := scope
	inner.top = false
	inner.context = strings.TrimSpace(d.Name + " " + strings.Join(d.Args, " "))
	if d.Name == "location" && len(d.Args) > 0 {
		inner.location = d.Args[len(d.Args)-1]
	}
	c.block(d.Block, twin.Block, inner)
}

func (c *driftCompare) drop(d *Directive, missing []int, scope driftScope) {
	st := droppedStmt{directive: d, missing: missing, context: scope.context}
	if scope.main && scope.top {
		st.movable, st.reason = c.movable(d, scope.server)
	}
	c.out = append(c.out, st)
}

// movable says whether a statement of the server block the form writes can
// go into the form's extra configuration as it is: unless the form writes a
// directive of the same name there itself, which nginx either refuses to see
// twice or applies twice — two access logs, a second X-Frame-Options.
func (c *driftCompare) movable(d *Directive, server *Directive) (bool, string) {
	if d.Block != nil {
		return true, ""
	}
	writes := func(match func(Directive) bool) bool {
		for _, w := range server.Block {
			if w.Block == nil && match(w) {
				return true
			}
		}
		return false
	}
	switch d.Name {
	case "server_name", "ssl_certificate", "ssl_certificate_key":
		// Added to the form's, as a second name or a second certificate.
		return true, ""
	case "add_header":
		if len(d.Args) > 0 && writes(func(w Directive) bool {
			return w.Name == "add_header" && len(w.Args) > 0 && strings.EqualFold(w.Args[0], d.Args[0])
		}) {
			return false, "the form writes its own " + d.Args[0] + " header"
		}
		return true, ""
	case "listen":
		if len(d.Args) > 0 {
			address := listenAddress(d.Args[0])
			if writes(func(w Directive) bool {
				return w.Name == "listen" && len(w.Args) > 0 && listenAddress(w.Args[0]) == address
			}) {
				return false, "the form writes its own listen on " + address
			}
		}
		return true, ""
	case "include":
		if c.included == nil || len(d.Args) == 0 {
			return true, ""
		}
		for _, name := range c.included(d.Args[0]) {
			if refusedTwice[name] && writes(func(w Directive) bool { return w.Name == name }) {
				return false, "the included file sets " + name + ", which the form writes itself"
			}
		}
		return true, ""
	}
	if writes(func(w Directive) bool { return w.Name == d.Name }) {
		return false, "the form writes its own " + d.Name
	}
	return true, ""
}

// dedupe keeps one of the same statement moving from several places, since
// the second copy of most directives is one nginx refuses.
func (c *driftCompare) dedupe() {
	first := map[string]int{}
	for i := range c.out {
		st := &c.out[i]
		if !st.movable {
			continue
		}
		key := formatDirective(*st.directive)
		if line, ok := first[key]; ok {
			st.movable, st.reason = false, "the same as line "+strconv.Itoa(line)+", which moves"
			continue
		}
		first[key] = st.directive.Line
	}
}

// lines turns the dropped statements into what the form shows, in file order.
func (f *driftFile) lines(stmts []droppedStmt) []DroppedLine {
	out := make([]DroppedLine, 0, len(stmts))
	for _, st := range stmts {
		text, line, count := f.text(st)
		out = append(out, DroppedLine{
			Line: line, Lines: count, Text: text, Context: st.context,
			Movable: st.movable, Reason: st.reason,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out
}

// text is a dropped statement as the file has it: its own lines, when no
// other statement shares them, and otherwise written out on one line — as
// is the part of a server_name that is dropped while the rest is kept.
func (f *driftFile) text(st droppedStmt) (string, int, int) {
	d := st.directive
	if st.missing != nil {
		names := make([]string, 0, len(st.missing))
		for _, i := range st.missing {
			names = append(names, d.Args[i])
		}
		part := Directive{Name: d.Name, Args: names}
		return formatDirective(part), d.Line, 1
	}
	span, ok := f.spans[d]
	if ok && span.first >= 0 && span.last < len(f.tokens) {
		first, last := f.tokens[span.first], f.tokens[span.last]
		alone := (span.first == 0 || f.tokens[span.first-1].line < first.line) &&
			(span.last == len(f.tokens)-1 || f.tokens[span.last+1].line > last.line)
		if alone && first.line >= 1 && last.line <= len(f.source) {
			return dedent(f.source[first.line-1 : last.line]), first.line, last.line - first.line + 1
		}
	}
	return formatDirective(*d), d.Line, 1
}

// driftUnits are the statements of a written block, counted, for the file's
// to be matched against one at a time.
type driftUnits struct {
	leaves map[string]int
	blocks map[string][]*Directive
}

func newDriftUnits(written []Directive, location string) *driftUnits {
	u := &driftUnits{leaves: map[string]int{}, blocks: map[string][]*Directive{}}
	for i := range written {
		d := &written[i]
		if d.Block != nil {
			key := statementKey(d.Name, d.Args)
			u.blocks[key] = append(u.blocks[key], d)
			continue
		}
		for _, key := range unitKeys(d, location) {
			u.leaves[key]++
		}
	}
	return u
}

// take is the written block that d is: one with the same name and arguments,
// or — for a folder the form writes with the trailing slash it adds to both
// sides — the same location with a slash.
func (u *driftUnits) take(d *Directive) *Directive {
	keys := []string{statementKey(d.Name, d.Args)}
	if d.Name == "location" && len(d.Args) == 1 && !strings.HasSuffix(d.Args[0], "/") {
		keys = append(keys, statementKey(d.Name, []string{d.Args[0] + "/"}))
	}
	for _, key := range keys {
		if list := u.blocks[key]; len(list) > 0 {
			u.blocks[key] = list[1:]
			return list[0]
		}
	}
	return nil
}

// unitKeys are what a statement is compared by. Most are one statement; a
// server_name is one per name, since the form writes the names of every
// block of a file in one line.
func unitKeys(d *Directive, location string) []string {
	switch d.Name {
	case "server_name":
		keys := make([]string, 0, len(d.Args))
		for _, name := range d.Args {
			keys = append(keys, statementKey(d.Name, []string{strings.ToLower(name)}))
		}
		return keys
	case "listen":
		if len(d.Args) > 0 {
			// http2 as a listen parameter is the pre-1.25 spelling of the
			// `http2 on` the form writes; the parameters are a set.
			params := []string{}
			for _, p := range d.Args[1:] {
				if p != "http2" {
					params = append(params, p)
				}
			}
			sort.Strings(params)
			return []string{statementKey(d.Name, append([]string{listenAddress(d.Args[0])}, params...))}
		}
	case "proxy_pass":
		if len(d.Args) == 1 {
			upstream := d.Args[0]
			if socket, ok := strings.CutPrefix(upstream, "http://unix:"); ok {
				upstream = "unix:" + socket
			}
			return []string{statementKey(d.Name, []string{proxyPassTarget(location, upstream)})}
		}
	case "alias":
		if len(d.Args) == 1 {
			return []string{statementKey(d.Name, []string{strings.TrimSuffix(d.Args[0], "/") + "/"})}
		}
	}
	return []string{statementKey(d.Name, d.Args)}
}

func statementKey(name string, args []string) string {
	return name + "\x00" + strings.Join(args, "\x00")
}

// bestServer is the written server block a server block of the file is
// compared with: of those sharing at least one of its names, the one with
// the most statements in common, and the form's own on a tie.
func bestServer(block *Directive, servers []*Directive, main *Directive) *Directive {
	var best *Directive
	bestScore := -1
	for _, w := range servers {
		units := newDriftUnits(w.Block, "")
		named, score := false, 0
		for i := range block.Block {
			d := &block.Block[i]
			if d.Block != nil {
				if len(units.blocks[statementKey(d.Name, d.Args)]) > 0 {
					score++
				}
				continue
			}
			for _, key := range unitKeys(d, "") {
				if units.leaves[key] > 0 {
					score++
					named = named || d.Name == "server_name"
				}
			}
		}
		if !named {
			continue
		}
		if score > bestScore || (score == bestScore && w == main) {
			best, bestScore = w, score
		}
	}
	return best
}

// serverContext names a server block of the file: "server" for the one the
// form writes into, and by its first port for any other.
func serverContext(block *Directive, main bool) string {
	if main {
		return "server"
	}
	for _, d := range block.Block {
		if d.Name == "listen" && len(d.Args) > 0 {
			return "server on port " + listenPort(listenAddress(d.Args[0]))
		}
	}
	return "another server block"
}

// formatDirective writes a statement out on one line, quoting what nginx
// would otherwise read differently.
func formatDirective(d Directive) string {
	var b strings.Builder
	b.WriteString(d.Name)
	for _, arg := range d.Args {
		b.WriteByte(' ')
		b.WriteString(quoteNginx(arg))
	}
	if d.Block == nil {
		b.WriteByte(';')
		return b.String()
	}
	b.WriteString(" {")
	for _, inner := range d.Block {
		b.WriteByte(' ')
		b.WriteString(formatDirective(inner))
	}
	b.WriteString(" }")
	return b.String()
}

// quoteNginx writes a token so nginx reads it back as the same text: bare
// when nothing in it would end or start a token, and in double quotes, with
// the backslash and the quote escaped, when something would.
func quoteNginx(text string) string {
	if text != "" && !strings.ContainsAny(text, " \t\r\n;{}#\"'") {
		return text
	}
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(text)
	return `"` + escaped + `"`
}

// dedent drops the indentation lines share and the spaces they end with.
func dedent(lines []string) string {
	indent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		n := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent < 0 || n < indent {
			indent = n
		}
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		line = strings.TrimRight(line, " \t\r")
		if len(line) >= indent && indent > 0 {
			line = line[indent:]
		}
		out[i] = line
	}
	return strings.Join(out, "\n")
}

// common is the indices in both lists, where nil stands for all of them.
func common(a, b []int) []int {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	in := map[int]bool{}
	for _, i := range b {
		in[i] = true
	}
	out := []int{}
	for _, i := range a {
		if in[i] {
			out = append(out, i)
		}
	}
	return out
}
