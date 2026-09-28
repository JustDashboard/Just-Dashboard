package proxysvc

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	// The store already links the driver in; imported here too so this file
	// does not depend on some other package having done it.
	_ "modernc.org/sqlite"
)

// Nginx Proxy Manager keeps everything it serves in one SQLite file:
// database.sqlite under its /data volume. Reading that file is how a host
// moves off NPM without retyping every proxy host. The file is opened
// read-only from a private temporary copy, mapped onto the same SiteSpec and
// StreamSpec the forms write, and held as a plan behind a token; nothing on
// the host changes until the plan is applied, and then every selected item
// goes in behind one `nginx -t`.
//
// What does not carry over is said per item rather than dropped quietly:
// NPM's own certificates live inside its container, its advanced
// configuration names files only its image has, and a few of its switches
// have no equivalent in the form. Anything that would come out *weaker* than
// NPM had it — an access rule the form cannot write, a password that cannot
// be carried — skips the item instead of importing it open.

// MaxNPMDatabaseBytes bounds an upload. A database with a few hundred hosts
// is well under a megabyte; the rest is headroom for audit-log rows.
const MaxNPMDatabaseBytes = 50 << 20

const (
	npmPreviewTTL = 15 * time.Minute
	// npmMaxRows bounds each table read, so a crafted file cannot make one
	// preview hold the service for long.
	npmMaxRows  = 2000
	npmMaxPlans = 8
	// npmAuthRealm is the prompt NPM itself shows.
	npmAuthRealm = "Authorization required"
)

// NPMItem is one thing an import can add: a site (from an NPM proxy host or
// redirection host), a stream, or a password file (from an access list with
// users).
type NPMItem struct {
	ID string `json:"id"`
	// Kind is proxy, redirect, stream or access.
	Kind   string `json:"kind"`
	Source string `json:"source"`
	// Name is what it is called here: the site, the stream, or the password
	// file under jd-auth.
	Name    string   `json:"name"`
	Domains []string `json:"domains"`
	// Target is where it sends traffic, in one line.
	Target string `json:"target"`
	// Enabled is how NPM had it, and how the import leaves it.
	Enabled  bool     `json:"enabled"`
	TLS      bool     `json:"tls"`
	CertPath string   `json:"certPath,omitempty"`
	Users    []string `json:"users,omitempty"`
	// Requires names the items this one cannot work without; applying it
	// applies them too.
	Requires []string `json:"requires"`
	// Content is the file the import writes.
	Content string `json:"content,omitempty"`
	// Advanced is NPM's custom nginx configuration for the host, shown for
	// copying by hand and never written.
	Advanced  string   `json:"advanced,omitempty"`
	Notes     []string `json:"notes"`
	Conflicts []string `json:"conflicts"`
	// Skipped says why this item cannot be imported at all.
	Skipped string `json:"skipped,omitempty"`
}

// NPMPreview is what the database maps to, behind a token Apply takes.
type NPMPreview struct {
	Token   string    `json:"token"`
	Expires time.Time `json:"expires"`
	// Layout is where sites go: sites-available or conf.d.
	Layout string `json:"layout"`
	// StreamsIncluded says nginx.conf reads stream.d; without it a stream
	// file is written and ignored.
	StreamsIncluded bool      `json:"streamsIncluded"`
	Items           []NPMItem `json:"items"`
}

// NPMApplyResult is what an apply added.
type NPMApplyResult struct {
	Sites []string `json:"sites"`
	// Disabled are sites written to sites-available and left unlinked, as
	// NPM had them off.
	Disabled  []string `json:"disabled"`
	Streams   []string `json:"streams"`
	AuthFiles []string `json:"authFiles"`
	// Added are items applied because a selected one requires them.
	Added []string `json:"added"`
}

type npmPlanned struct {
	item   NPMItem
	site   *SiteSpec
	stream *StreamSpec
	// authLines are user:bcrypt lines; the plain passwords NPM stores are
	// hashed while the preview is built and never kept.
	authLines []string
}

type npmPlan struct {
	actor   string
	expires time.Time
	layout  string
	order   []string
	items   map[string]*npmPlanned
}

var npmPlans = struct {
	sync.Mutex
	m map[string]*npmPlan
}{m: map[string]*npmPlan{}}

var npmSlugRe = regexp.MustCompile(`[^a-z0-9._-]+`)

// PreviewNPMImport reads an NPM database at path and returns what importing
// it would add. The file is the caller's private copy; it is opened
// read-only and immutable, so SQLite writes nothing beside it either.
func (s *Service) PreviewNPMImport(ctx context.Context, path string) (*NPMPreview, error) {
	if err := checkSQLiteHeader(path); err != nil {
		return nil, err
	}
	layout, err := s.siteLayout()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tables, err := npmTables(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("the file is not a readable SQLite database: %v", err)
	}
	if !tables["proxy_host"] {
		return nil, fmt.Errorf("this is not an Nginx Proxy Manager database — it has no proxy_host table")
	}
	read := func(table string) ([]npmRow, error) {
		if !tables[table] {
			return nil, nil
		}
		return npmRead(ctx, db, table)
	}
	var lists, auths, clients, proxies, redirects, streams []npmRow
	for _, t := range []struct {
		name string
		into *[]npmRow
	}{
		{"access_list", &lists}, {"access_list_auth", &auths}, {"access_list_client", &clients},
		{"proxy_host", &proxies}, {"redirection_host", &redirects}, {"stream", &streams},
	} {
		rows, err := read(t.name)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %v", t.name, err)
		}
		*t.into = rows
	}

	b := &npmBuilder{s: s, plan: &npmPlan{layout: layout, items: map[string]*npmPlanned{}},
		names: map[string]bool{}, lists: map[int64]*npmAccess{}}
	b.loadCertificates(ctx)
	b.accessLists(lists, auths, clients)
	for _, row := range proxies {
		b.host(row, "proxy")
	}
	for _, row := range redirects {
		b.host(row, "redirect")
	}
	for _, row := range streams {
		b.streamRows(row)
	}

	b.plan.actor = ActorFrom(ctx)
	b.plan.expires = time.Now().Add(npmPreviewTTL)
	s.npmConflicts(b.plan, b.plan.order)
	token, err := keepNPMPlan(b.plan)
	if err != nil {
		return nil, err
	}
	out := &NPMPreview{Token: token, Expires: b.plan.expires, Layout: layout,
		StreamsIncluded: streamIncludeFound(s.nginxDir, s.streamDir()), Items: []NPMItem{}}
	for _, id := range b.plan.order {
		out.Items = append(out.Items, b.plan.items[id].item)
	}
	return out, nil
}

func checkSQLiteHeader(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, 16)
	if _, err := io.ReadFull(f, head); err != nil || !bytes.Equal(head, []byte("SQLite format 3\x00")) {
		return fmt.Errorf("the file is not a SQLite database — NPM keeps its data in database.sqlite under its /data volume")
	}
	return nil
}

// siteLayout is where a new site goes on this host.
func (s *Service) siteLayout() (string, error) {
	for _, dir := range []string{"sites-available", "conf.d"} {
		if _, err := os.Stat(filepath.Join(s.nginxDir, dir)); err == nil {
			return dir, nil
		}
	}
	return "", fmt.Errorf("%s has neither a sites-available nor a conf.d directory — set JD_NGINX_DIR to where this host keeps its nginx configuration", s.nginxDir)
}

func keepNPMPlan(plan *npmPlan) (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	npmPlans.Lock()
	defer npmPlans.Unlock()
	now := time.Now()
	for t, p := range npmPlans.m {
		if now.After(p.expires) {
			delete(npmPlans.m, t)
		}
	}
	// Each plan holds password hashes; a handful is all a person uses.
	for len(npmPlans.m) >= npmMaxPlans {
		oldest := ""
		for t, p := range npmPlans.m {
			if oldest == "" || p.expires.Before(npmPlans.m[oldest].expires) {
				oldest = t
			}
		}
		delete(npmPlans.m, oldest)
	}
	npmPlans.m[token] = plan
	return token, nil
}

type npmRow map[string]any

func npmTables(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	// Tables only: a view is a query, and a crafted file's view can be made
	// to take as long as it likes.
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

// npmRead reads a table by column name, since columns arrived over NPM's
// releases and an older database lacks some of them.
func npmRead(ctx context.Context, db *sql.DB, table string) ([]npmRow, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`SELECT * FROM %q LIMIT %d`, table, npmMaxRows))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []npmRow{}
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := npmRow{}
		for i, c := range cols {
			row[c] = values[i]
		}
		if row.flag("is_deleted") {
			continue
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (r npmRow) str(col string) string {
	switch v := r[col].(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func (r npmRow) num(col string) int64 {
	switch v := r[col].(type) {
	case int64:
		return v
	case bool:
		if v {
			return 1
		}
		return 0
	case float64:
		return int64(v)
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(r.str(col)), 10, 64)
	return n
}

// flag reads a knex boolean, which SQLite stores as 0/1 and some versions
// wrote as "true"/"false".
func (r npmRow) flag(col string) bool {
	switch r[col].(type) {
	case int64, bool:
		return r.num(col) != 0
	}
	v := strings.ToLower(strings.TrimSpace(r.str(col)))
	return v == "1" || v == "true"
}

// has reports whether the column exists, to tell an old database's missing
// "enabled" (everything was on) from a host switched off.
func (r npmRow) has(col string) bool {
	_, ok := r[col]
	return ok
}

// npmAccess is an NPM access list as the form can write it.
type npmAccess struct {
	id         string
	name       string
	file       string
	allow      []string
	deny       []string
	users      []string
	lines      []string
	satisfyAny bool
	passAuth   bool
	// broken says the list cannot be written faithfully, and why; a site
	// using it is skipped rather than imported with less protection.
	broken string
}

type npmBuilder struct {
	s     *Service
	plan  *npmPlan
	names map[string]bool
	lists map[int64]*npmAccess
	certs []Certificate
	keys  map[string]string
}

func (b *npmBuilder) add(p *npmPlanned) {
	if p.item.Domains == nil {
		p.item.Domains = []string{}
	}
	if p.item.Requires == nil {
		p.item.Requires = []string{}
	}
	if p.item.Notes == nil {
		p.item.Notes = []string{}
	}
	p.item.Conflicts = []string{}
	b.plan.items[p.item.ID] = p
	b.plan.order = append(b.plan.order, p.item.ID)
}

// unique returns base, or base-2, base-3… — the first no other item in this
// import has taken.
func (b *npmBuilder) unique(base string) string {
	name := base
	for i := 2; b.names[name]; i++ {
		suffix := "-" + strconv.Itoa(i)
		name = strings.TrimRight(base[:min(len(base), 64-len(suffix))], ".-_") + suffix
	}
	b.names[name] = true
	return name
}

func npmSlug(raw, fallback string) string {
	slug := strings.Trim(npmSlugRe.ReplaceAllString(strings.ToLower(raw), "-"), ".-_")
	if len(slug) > 56 {
		slug = strings.TrimRight(slug[:56], ".-_")
	}
	if slug == "" || !siteNameRe.MatchString(slug) || isBackupFile(slug) || strings.HasPrefix(slug, deploymentRoutePrefix) {
		return fallback
	}
	return slug
}

func (b *npmBuilder) loadCertificates(ctx context.Context) {
	b.keys = map[string]string{}
	for _, v := range b.s.nginxVHosts() {
		if v.CertPath == "" || v.Path == "" {
			continue
		}
		if content, err := os.ReadFile(v.Path); err == nil {
			if spec, _ := ParseSiteSpec(v.Name, string(content)); spec != nil && spec.KeyPath != "" {
				b.keys[v.CertPath] = spec.KeyPath
			}
		}
	}
	b.certs, _ = b.s.ListCertificates(ctx)
}

// localCertificate is a certificate and key on this host covering every
// domain — NPM's own are inside its container and never reach here.
func (b *npmBuilder) localCertificate(domains []string) (string, string, bool) {
	for _, c := range b.certs {
		if c.Error != "" || c.Expired || !certificateCoversAll(c.Domains, domains) {
			continue
		}
		key := b.keys[c.Path]
		if key == "" {
			key = filepath.Join(filepath.Dir(c.Path), "privkey.pem")
		}
		if !absPathRe.MatchString(c.Path) || !absPathRe.MatchString(key) {
			continue
		}
		if _, err := os.Stat(c.Path); err != nil {
			continue
		}
		if _, err := os.Stat(key); err != nil {
			continue
		}
		return c.Path, key, true
	}
	return "", "", false
}

func (b *npmBuilder) accessLists(lists, auths, clients []npmRow) {
	for _, row := range lists {
		id := row.num("id")
		a := &npmAccess{
			id: fmt.Sprintf("access-%d", id), name: row.str("name"),
			allow: []string{}, deny: []string{}, users: []string{},
			satisfyAny: row.flag("satisfy_any"),
			// NPM added pass_auth later; before it the header always went through.
			passAuth: !row.has("pass_auth") || row.flag("pass_auth"),
		}
		b.lists[id] = a
	}
	for _, row := range clients {
		a := b.lists[row.num("access_list_id")]
		if a == nil || a.broken != "" {
			continue
		}
		addr := strings.TrimSpace(row.str("address"))
		if err := validACLEntry(addr); err != nil {
			a.broken = fmt.Sprintf("its rule for %q is not an address or range the form can write", addr)
			continue
		}
		switch row.str("directive") {
		case "allow":
			a.allow = append(a.allow, addr)
		case "deny":
			a.deny = append(a.deny, addr)
		default:
			a.broken = fmt.Sprintf("its rule for %s is neither allow nor deny", addr)
		}
	}
	type user struct{ name, password string }
	byList := map[int64][]user{}
	for _, row := range auths {
		id := row.num("access_list_id")
		byList[id] = append(byList[id], user{row.str("username"), row.str("password")})
	}
	for _, row := range lists {
		id := row.num("id")
		a := b.lists[id]
		users := byList[id]
		notes := []string{}
		for _, u := range users {
			switch {
			case !authUserRe.MatchString(u.name):
				notes = append(notes, fmt.Sprintf("User %q is left out: the name has characters an htpasswd file cannot hold.", u.name))
				continue
			case u.password == "":
				notes = append(notes, fmt.Sprintf("User %s is left out: NPM has no password stored for it.", u.name))
				continue
			case len(u.password) > 72:
				notes = append(notes, fmt.Sprintf("User %s is left out: the password is longer than bcrypt's 72 bytes.", u.name))
				continue
			}
			hash, err := bcrypt.GenerateFromPassword([]byte(u.password), bcrypt.DefaultCost)
			if err != nil {
				notes = append(notes, fmt.Sprintf("User %s is left out: %v.", u.name, err))
				continue
			}
			a.users = append(a.users, u.name)
			a.lines = append(a.lines, u.name+":"+string(hash))
		}
		// Some users left out is a lock-out for them rather than a hole, so
		// the list still goes in with the ones it can hold; none at all
		// would be a site with no password.
		if len(users) > 0 && len(a.users) == 0 && a.broken == "" {
			a.broken = "none of its users could be carried over"
		}
		if len(users) == 0 {
			continue
		}
		a.file = b.unique("npm-" + npmSlug(a.name, fmt.Sprintf("list-%d", id)))
		item := NPMItem{
			ID: a.id, Kind: "access", Source: fmt.Sprintf("Access list #%d %s", id, a.name),
			Name: a.file, Target: filepath.Join(b.s.authDir(), a.file), Enabled: true,
			Users: a.users, Notes: notes,
		}
		if a.broken != "" {
			item.Skipped = "The list " + a.broken + "."
		}
		b.add(&npmPlanned{item: item, authLines: a.lines})
	}
}

// host maps a proxy_host or redirection_host row onto a site.
func (b *npmBuilder) host(row npmRow, kind string) {
	id := row.num("id")
	label := "Proxy host"
	if kind == "redirect" {
		label = "Redirection host"
	}
	item := NPMItem{ID: fmt.Sprintf("%s-%d", kind, id), Kind: kind, Source: fmt.Sprintf("%s #%d", label, id),
		Enabled: !row.has("enabled") || row.flag("enabled"), Advanced: strings.TrimSpace(row.str("advanced_config"))}
	p := &npmPlanned{item: item}
	defer b.add(p)
	skip := func(format string, args ...any) { p.item.Skipped = fmt.Sprintf(format, args...) }
	note := func(format string, args ...any) { p.item.Notes = append(p.item.Notes, fmt.Sprintf(format, args...)) }

	var domains []string
	if err := json.Unmarshal([]byte(row.str("domain_names")), &domains); err != nil || len(domains) == 0 {
		p.item.Name = b.unique(fmt.Sprintf("npm-%s-%d", kind, id))
		skip("Its domain names could not be read.")
		return
	}
	for i, d := range domains {
		domains[i] = strings.ToLower(strings.TrimSpace(d))
	}
	p.item.Domains = domains
	p.item.Name = b.unique(npmSlug(strings.Replace(domains[0], "*", "wildcard", 1), fmt.Sprintf("npm-%s-%d", kind, id)))
	for _, d := range domains {
		if !domainRe.MatchString(d) {
			skip("%q is not a domain name a server block here can carry.", d)
			return
		}
	}
	if b.plan.layout == "conf.d" && !p.item.Enabled {
		skip("It is off in NPM, and this host keeps sites in conf.d, where every file is served.")
		return
	}

	spec := &SiteSpec{
		Name: p.item.Name, Domains: domains, Kind: kind,
		BlockExploits: row.flag("block_exploits"), AccessLog: true,
		AllowFrom: []string{}, DenyFrom: []string{}, Locations: []SiteLocation{},
	}
	if kind == "proxy" {
		scheme := row.str("forward_scheme")
		if scheme != "https" {
			scheme = "http"
		}
		spec.Upstream = scheme + "://" + net.JoinHostPort(row.str("forward_host"), row.str("forward_port"))
		spec.WebSockets = row.flag("allow_websocket_upgrade")
		p.item.Target = spec.Upstream
		if row.flag("caching_enabled") {
			note("NPM cached assets for this host; the form has no cache, so every request reaches the upstream.")
		}
		b.locations(row, spec, note)
		if !b.access(row, spec, p, note, skip) {
			return
		}
	} else {
		scheme := row.str("forward_scheme")
		if scheme != "http" && scheme != "https" {
			scheme = "https"
			note("NPM kept the visitor's scheme (auto); the redirect here always goes to https.")
		}
		spec.RedirectTo = scheme + "://" + strings.TrimSpace(row.str("forward_domain_name"))
		code := row.num("forward_http_code")
		spec.Permanent = code == 0 || code == 301 || code == 308
		if code != 0 && code != 301 && code != 302 {
			note("NPM answered with %d; the form writes %s.", code, map[bool]string{true: "301", false: "302"}[spec.Permanent])
		}
		if row.has("preserve_path") && !row.flag("preserve_path") {
			note("NPM dropped the path; the redirect here keeps it, sending /a/b to the same path on the target.")
		}
		p.item.Target = spec.RedirectTo
	}

	if row.num("certificate_id") > 0 {
		if cert, key, ok := b.localCertificate(domains); ok {
			spec.TLS, spec.CertPath, spec.KeyPath = true, cert, key
			spec.ForceHTTPS = row.flag("ssl_forced")
			spec.HTTP2 = row.flag("http2_support")
			spec.HSTS = row.flag("hsts_enabled")
			p.item.TLS, p.item.CertPath = true, cert
			if spec.HSTS && !row.flag("hsts_subdomains") {
				note("HSTS here always includes subdomains; NPM had it for this name only.")
			}
		} else {
			note("NPM served it over HTTPS with its certificate #%d, which stays inside NPM's container, and no certificate on this host covers every one of its names — it is imported on plain HTTP. Issue one under Certificates, then turn TLS on in the site form.", row.num("certificate_id"))
		}
	}
	if p.item.Advanced != "" {
		note("NPM's custom nginx configuration is not written: it names files that only NPM's image has. It is shown here to copy across by hand.")
	}
	if err := ValidateSpec(spec); err != nil {
		skip("It does not map onto a site: %v.", err)
		return
	}
	content, err := RenderNginx(spec)
	if err != nil {
		skip("It does not map onto a site: %v.", err)
		return
	}
	p.site, p.item.Content = spec, content
}

// locations carries NPM's custom locations that the form can write.
func (b *npmBuilder) locations(row npmRow, spec *SiteSpec, note func(string, ...any)) {
	raw := row.str("locations")
	if strings.TrimSpace(raw) == "" || raw == "null" {
		return
	}
	var locs []struct {
		Path           string `json:"path"`
		ForwardScheme  string `json:"forward_scheme"`
		ForwardHost    string `json:"forward_host"`
		ForwardPort    any    `json:"forward_port"`
		AdvancedConfig string `json:"advanced_config"`
	}
	if err := json.Unmarshal([]byte(raw), &locs); err != nil {
		note("Its custom locations could not be read and are not imported.")
		return
	}
	for _, l := range locs {
		scheme := l.ForwardScheme
		if scheme != "https" {
			scheme = "http"
		}
		// NPM keeps a location's path on the host field: "backend/api".
		host, path, _ := strings.Cut(l.ForwardHost, "/")
		upstream := scheme + "://" + net.JoinHostPort(host, fmt.Sprint(l.ForwardPort))
		if path != "" {
			upstream += "/" + path
		}
		loc := SiteLocation{Path: l.Path, Upstream: upstream, WebSockets: spec.WebSockets}
		trial := *spec
		trial.Locations = append(slices.Clone(spec.Locations), loc)
		if err := ValidateSpec(&trial); err != nil {
			note("Location %s is not imported: %v.", l.Path, err)
			continue
		}
		spec.Locations = trial.Locations
		if strings.TrimSpace(l.AdvancedConfig) != "" {
			note("Location %s had custom configuration, which is not written.", l.Path)
		}
	}
}

// access puts a proxy host's access list on the site. It reports false when
// the host is skipped for it.
func (b *npmBuilder) access(row npmRow, spec *SiteSpec, p *npmPlanned, note, skip func(string, ...any)) bool {
	id := row.num("access_list_id")
	if id <= 0 {
		return true
	}
	a := b.lists[id]
	if a == nil {
		skip("It uses access list #%d, which is not in the database; importing it without would leave it open.", id)
		return false
	}
	if a.broken != "" {
		skip("Its access list %s cannot be carried over: %s. Importing it without would leave it open.", a.name, a.broken)
		return false
	}
	spec.AllowFrom = slices.Clone(a.allow)
	spec.DenyFrom = slices.Clone(a.deny)
	// NPM closes every list with deny all. The form writes that fence after
	// an allow list by itself; a list of denials alone needs it named.
	if len(a.allow) == 0 && len(a.deny) > 0 && !containsDenyAll(a.deny) {
		spec.DenyFrom = append(spec.DenyFrom, "all")
	}
	if len(a.allow) > 0 && len(a.deny) > 0 {
		note("Denials are written before the allow list here, so an address both allowed and denied by %s is refused.", a.name)
	}
	if a.file != "" {
		spec.BasicAuthFile = filepath.Join(b.s.authDir(), a.file)
		spec.BasicAuthRealm = npmAuthRealm
		p.item.Requires = []string{a.id}
		if a.satisfyAny && (len(a.allow) > 0 || len(a.deny) > 0) {
			note("NPM let a visitor in by address or by password (satisfy any); here both are required.")
		}
		if !a.passAuth {
			note("NPM stripped the Authorization header before the upstream; here the upstream receives it.")
		}
	}
	return true
}

func (b *npmBuilder) streamRows(row npmRow) {
	id := row.num("id")
	listen := int(row.num("incoming_port"))
	upstream := net.JoinHostPort(strings.TrimSpace(row.str("forwarding_host")), row.str("forwarding_port"))
	enabled := !row.has("enabled") || row.flag("enabled")
	protocols := []string{}
	if !row.has("tcp_forwarding") || row.flag("tcp_forwarding") {
		protocols = append(protocols, "tcp")
	}
	if row.flag("udp_forwarding") {
		protocols = append(protocols, "udp")
	}
	for _, proto := range protocols {
		name := b.unique(fmt.Sprintf("npm-%d-%s", listen, proto))
		p := &npmPlanned{item: NPMItem{
			ID: fmt.Sprintf("stream-%d-%s", id, proto), Kind: "stream",
			Source: fmt.Sprintf("Stream #%d", id), Name: name, Enabled: enabled,
			Target: fmt.Sprintf("%s %d → %s", proto, listen, upstream),
		}}
		spec := &StreamSpec{Name: name, Listen: listen, Protocol: proto, Upstream: upstream, AllowFrom: []string{}}
		switch {
		case !enabled:
			p.item.Skipped = "It is off in NPM, and a stream here has no off switch — importing it would put it live."
		case row.num("certificate_id") > 0:
			p.item.Skipped = "NPM terminates TLS on this stream; a stream here forwards the bytes as they come, so the backend would receive TLS it does not expect."
		default:
			if err := ValidateStream(spec); err != nil {
				p.item.Skipped = fmt.Sprintf("It does not map onto a stream: %v.", err)
			} else if content, err := RenderStream(spec); err != nil {
				p.item.Skipped = fmt.Sprintf("It does not map onto a stream: %v.", err)
			} else {
				p.stream, p.item.Content = spec, content
				p.item.Notes = streamWarnings(spec)
			}
		}
		b.add(p)
	}
}

// npmConflicts fills in, for the given items, what already on this host
// stands in their way. Called for a preview and again under the lock at
// apply, since the host may have changed in between.
func (s *Service) npmConflicts(plan *npmPlan, ids []string) {
	served := map[string]string{}
	for _, v := range s.nginxVHosts() {
		for _, name := range v.ServerNames {
			served[strings.ToLower(name)] = v.Name
		}
	}
	ports := map[string]string{}
	for _, st := range s.Streams(context.Background()).Streams {
		ports[fmt.Sprintf("%s/%d", st.Protocol, st.Listen)] = st.Name
	}
	exists := func(path string) bool {
		_, err := os.Lstat(path)
		return err == nil
	}
	for _, id := range ids {
		p := plan.items[id]
		p.item.Conflicts = []string{}
		add := func(format string, args ...any) {
			p.item.Conflicts = append(p.item.Conflicts, fmt.Sprintf(format, args...))
		}
		switch {
		case p.site != nil:
			name := p.item.Name
			for _, taken := range []string{
				filepath.Join(s.nginxDir, "sites-available", name), s.confdPath(name),
				filepath.Join(s.nginxDir, "sites-enabled", name),
			} {
				if exists(taken) {
					add("%s already exists.", strings.TrimPrefix(taken, s.nginxDir+"/"))
					break
				}
			}
			for _, d := range p.item.Domains {
				if site, ok := served[d]; ok {
					add("%s is already served by %s.", d, site)
				}
			}
		case p.stream != nil:
			if exists(filepath.Join(s.streamDir(), p.item.Name+".conf")) {
				add("stream.d/%s.conf already exists.", p.item.Name)
			}
			if other, ok := ports[fmt.Sprintf("%s/%d", p.stream.Protocol, p.stream.Listen)]; ok {
				add("%s port %d is already forwarded by %s.", p.stream.Protocol, p.stream.Listen, other)
			}
		case p.item.Kind == "access":
			if exists(filepath.Join(s.authDir(), p.item.Name)) {
				add("The password file %s already exists.", p.item.Name)
			}
		}
	}
	for _, id := range ids {
		p := plan.items[id]
		for _, req := range p.item.Requires {
			if r := plan.items[req]; r != nil && len(r.item.Conflicts) > 0 {
				p.item.Conflicts = append(p.item.Conflicts,
					fmt.Sprintf("It needs the password file %s, which cannot be added.", r.item.Name))
			}
		}
	}
}

// ErrNPMPreviewGone is an apply against a token that has expired, was used,
// or belongs to someone else's preview.
var ErrNPMPreviewGone = errors.New("the preview has expired or was already applied — upload the database again")

type npmStep struct {
	undo   func()
	change *Change
}

// ApplyNPMImport adds the selected items of a preview, and whatever they
// require, as one change: every file is written and every site linked, then
// `nginx -t` runs once. A refusal takes every file back out. Sites NPM had
// off are unlinked again after the test, which is why they are linked for
// it at all: a file outside the include tree tests valid whatever it says.
func (s *Service) ApplyNPMImport(ctx context.Context, token string, ids []string, reload bool) (*NPMApplyResult, *LinkReload, error) {
	npmPlans.Lock()
	plan := npmPlans.m[token]
	npmPlans.Unlock()
	if plan == nil || time.Now().After(plan.expires) || plan.actor != ActorFrom(ctx) {
		return nil, nil, ErrNPMPreviewGone
	}
	if len(ids) == 0 {
		return nil, nil, fmt.Errorf("nothing was selected")
	}
	out := &NPMApplyResult{Sites: []string{}, Disabled: []string{}, Streams: []string{}, AuthFiles: []string{}, Added: []string{}}
	selected := map[string]bool{}
	for _, id := range ids {
		selected[id] = true
	}
	chosen := []string{}
	seen := map[string]bool{}
	var pick func(id string) error
	pick = func(id string) error {
		if seen[id] {
			return nil
		}
		p := plan.items[id]
		if p == nil {
			return fmt.Errorf("%s is not in this preview", id)
		}
		if p.item.Skipped != "" {
			return fmt.Errorf("%s cannot be imported: %s", p.item.Source, p.item.Skipped)
		}
		seen[id] = true
		for _, req := range p.item.Requires {
			if err := pick(req); err != nil {
				return err
			}
		}
		chosen = append(chosen, id)
		if !selected[id] {
			out.Added = append(out.Added, p.item.Name)
		}
		return nil
	}
	for _, id := range ids {
		if err := pick(id); err != nil {
			return nil, nil, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.npmConflicts(plan, chosen)
	blocked := []string{}
	for _, id := range chosen {
		for _, c := range plan.items[id].item.Conflicts {
			blocked = append(blocked, plan.items[id].item.Source+": "+c)
		}
	}
	if len(blocked) > 0 {
		return nil, nil, fmt.Errorf("nothing was imported — %s", strings.Join(blocked, " "))
	}

	var steps []npmStep
	undoAll := func() {
		for i := len(steps) - 1; i >= 0; i-- {
			steps[i].undo()
		}
		s.forgetEffective()
	}
	unlink := []string{}
	for _, id := range chosen {
		p := plan.items[id]
		step, link, err := s.npmWrite(plan.layout, p)
		if err != nil {
			undoAll()
			return nil, nil, fmt.Errorf("%s: %w — nothing was imported", p.item.Source, err)
		}
		steps = append(steps, step)
		if link != "" && !p.item.Enabled {
			unlink = append(unlink, link)
		}
	}
	s.forgetEffective()
	if res := runValidator(ctx, "nginx", "-t"); !res.Valid {
		undoAll()
		refused := &RefusedError{Validation: res, Lead: fmt.Sprintf("nginx refuses the configuration with the %d imported items added", len(chosen))}
		if before := runValidator(ctx, "nginx", "-t"); !before.Valid && FailureHeadline(before) == FailureHeadline(res) {
			refused.Lead = "nginx already refuses the configuration as it is"
		}
		return nil, nil, refused
	}
	for _, link := range unlink {
		_ = os.Remove(link)
	}
	for _, step := range steps {
		if step.change != nil {
			s.recordChange(ctx, *step.change)
		}
	}
	for _, id := range chosen {
		p := plan.items[id]
		switch {
		case p.site != nil && p.item.Enabled:
			out.Sites = append(out.Sites, p.item.Name)
		case p.site != nil:
			out.Disabled = append(out.Disabled, p.item.Name)
		case p.stream != nil:
			out.Streams = append(out.Streams, p.item.Name)
		default:
			out.AuthFiles = append(out.AuthFiles, p.item.Name)
		}
	}
	npmPlans.Lock()
	delete(npmPlans.m, token)
	npmPlans.Unlock()
	if len(out.Sites) == 0 && len(out.Streams) == 0 {
		return out, nil, nil
	}
	return out, s.reloadLocked(ctx, reload), nil
}

// npmWrite puts one item on disk and returns how to take it back out, and
// for a site in sites-available the link it made.
func (s *Service) npmWrite(layout string, p *npmPlanned) (npmStep, string, error) {
	switch {
	case p.site != nil:
		path, link := s.confdPath(p.item.Name), ""
		if layout == "sites-available" {
			path = filepath.Join(s.nginxDir, "sites-available", p.item.Name)
			link = filepath.Join(s.nginxDir, "sites-enabled", p.item.Name)
		}
		full, err := s.allowedPath(path)
		if err != nil {
			return npmStep{}, "", err
		}
		s.keepLoaded(full, link)
		if err := writeAtomic(full, p.item.Content); err != nil {
			return npmStep{}, "", err
		}
		undo := func() {
			if link != "" {
				_ = os.Remove(link)
			}
			_ = os.Remove(full)
		}
		if link != "" {
			if err := os.Symlink(full, link); err != nil {
				undo()
				return npmStep{}, "", err
			}
		}
		return npmStep{undo: undo, change: &Change{Path: full, Action: ChangeWrite, After: []byte(p.item.Content)}}, link, nil
	case p.stream != nil:
		dir := s.streamDir()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return npmStep{}, "", err
		}
		path := filepath.Join(dir, p.item.Name+".conf")
		s.keepLoaded(path)
		if err := writeAtomic(path, p.item.Content); err != nil {
			return npmStep{}, "", err
		}
		return npmStep{undo: func() { _ = os.Remove(path) },
			change: &Change{Path: path, Action: ChangeWrite, After: []byte(p.item.Content)}}, "", nil
	default:
		// Password files are never recorded: the change history is readable
		// by accounts that may not read hashes.
		path := filepath.Join(s.authDir(), p.item.Name)
		if err := s.writeAuthFile(path, p.authLines); err != nil {
			return npmStep{}, "", err
		}
		return npmStep{undo: func() { _ = os.Remove(path) }}, "", nil
	}
}
