package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// The catch-all default site is what nginx answers when a request's Host
// names no site — a scanner on the bare IP, a domain pointed at the box
// before its site exists. Without one, nginx hands those to whichever server
// it read first on the port, so the first site in alphabetical order serves
// every stranger, and on 443 shows them its certificate.
//
// The dashboard keeps it as one owned file, jd-default, which the Sites list
// leaves out (dashboardOwned). It only ever claims sockets nginx already
// listens on, apart from plain :80: taking a new port would make a reload
// fail to bind where `nginx -t` said nothing, and the test does not bind.

const defaultSiteName = "jd-default"

// DefaultChoice is what the catch-all does with a request.
type DefaultChoice string

const (
	DefaultClose    DefaultChoice = "close"
	DefaultNotFound DefaultChoice = "not_found"
	DefaultRedirect DefaultChoice = "redirect"
	DefaultPage     DefaultChoice = "page"
)

// DefaultListener is who answers an unknown Host on one address and port
// today: the server that claims default_server there, or else the first one
// nginx read.
type DefaultListener struct {
	Listen      string   `json:"listen"`
	File        string   `json:"file"`
	Line        int      `json:"line"`
	ServerNames []string `json:"serverNames"`
	// Claimed is a default_server on the listen line; otherwise the server
	// answers only because nginx read it first.
	Claimed bool `json:"claimed"`
	Ours    bool `json:"ours"`
}

// DefaultClaim is another file's default_server on a socket the catch-all
// would claim. nginx refuses two on one socket, so each has to go first.
type DefaultClaim struct {
	Listen string `json:"listen"`
	File   string `json:"file"`
	Line   int    `json:"line"`
}

// DefaultSite is the catch-all as it stands and what an Apply would write.
type DefaultSite struct {
	Installed  bool          `json:"installed"`
	Path       string        `json:"path,omitempty"`
	Content    string        `json:"content,omitempty"`
	Choice     DefaultChoice `json:"choice,omitempty"`
	RedirectTo string        `json:"redirectTo,omitempty"`
	// PageDir is where the "page" choice serves index.html from.
	PageDir string `json:"pageDir"`
	// Covers are the sockets an Apply would claim, in listen spelling.
	Covers    []string          `json:"covers"`
	Answering []DefaultListener `json:"answering"`
	Others    []DefaultClaim    `json:"others"`
	// Uncovered are sockets on a named address, which nginx matches before
	// the wildcard one the catch-all listens on.
	Uncovered []string `json:"uncovered"`
	// TLSSkipped says why 443 is not covered although a site listens there.
	TLSSkipped string `json:"tlsSkipped,omitempty"`
	// Error is why nginx's configuration could not be read, in which case
	// only Installed, Path, Content and the choice are known.
	Error string `json:"error,omitempty"`
}

// DefaultConflictError is an Apply refused before anything was written,
// because other files already claim default_server where it would.
type DefaultConflictError struct {
	Claims []DefaultClaim
}

func (e *DefaultConflictError) Error() string {
	files := []string{}
	for _, c := range e.Claims {
		files = appendNew(files, c.File)
	}
	return "default_server is already claimed by " + strings.Join(files, ", ") +
		" — disable that site, or take default_server off its listen lines, before applying the catch-all"
}

// DefaultSiteResult is what an Apply or a removal did.
type DefaultSiteResult struct {
	Path    string      `json:"path"`
	Content string      `json:"content,omitempty"`
	Reload  *LinkReload `json:"-"`
}

func (s *Service) defaultPageDir() string {
	return filepath.Join(s.nginxDir, "jd-pages", "default")
}

// defaultSitePaths are the owned file and, on a sites-available host, its
// link. The layout is the host's, as for any site the form writes.
func (s *Service) defaultSitePaths() (file, link string, err error) {
	if _, err := os.Stat(filepath.Join(s.nginxDir, "sites-available")); err == nil {
		return filepath.Join(s.nginxDir, "sites-available", defaultSiteName),
			filepath.Join(s.nginxDir, "sites-enabled", defaultSiteName), nil
	}
	if _, err := os.Stat(filepath.Join(s.nginxDir, "conf.d")); err == nil {
		return s.confdPath(defaultSiteName), "", nil
	}
	return "", "", fmt.Errorf("%s has neither a sites-available nor a conf.d directory — set JD_NGINX_DIR to where this host keeps its nginx configuration", s.nginxDir)
}

// DefaultSite reads the catch-all and, from nginx's own dump, who answers
// unknown hosts now. It dumps the configuration, so it must not be called
// with s.mu held.
func (s *Service) DefaultSite(ctx context.Context) (*DefaultSite, error) {
	out := &DefaultSite{PageDir: s.defaultPageDir(), Covers: []string{}, Answering: []DefaultListener{},
		Others: []DefaultClaim{}, Uncovered: []string{}}
	file, _, err := s.defaultSitePaths()
	if err != nil {
		out.Error = err.Error()
		return out, nil
	}
	if content, ok := readIfPresent(file); ok && dashboardOwned(defaultSiteName, file) {
		out.Installed, out.Path, out.Content = true, file, content
		out.Choice, out.RedirectTo = parseDefaultChoice(file, content)
	}
	files, err := s.EffectiveConfig(ctx)
	if err != nil {
		out.Error = "nginx's configuration could not be read: " + err.Error()
		return out, nil
	}
	tree, err := NginxTree(files)
	if err != nil {
		out.Error = "nginx's configuration could not be read: " + err.Error()
		return out, nil
	}
	s.planDefaultSite(out, tree, resolvedFile(file))
	return out, nil
}

// defaultServer is one http server block as the plan needs it.
type defaultServer struct {
	file    string
	line    int
	names   []string
	listens []defaultListen
}

type defaultListen struct {
	key     string // "*:80", "[::]:443", "10.0.0.1:80"
	params  []string
	line    int
	claimed bool
}

// httpServers are the server blocks in the http context, in the order nginx
// reads them, which is what decides the answer on a socket nothing claims.
func httpServers(tree []Directive) []defaultServer {
	var out []defaultServer
	var walk func([]Directive)
	walk = func(dirs []Directive) {
		for _, d := range dirs {
			if d.Name == "server" && len(d.Context) > 0 && d.Context[len(d.Context)-1] == "http" {
				out = append(out, readServer(d))
				continue
			}
			if d.Name == "stream" || d.Name == "mail" {
				continue
			}
			walk(d.Block)
		}
	}
	walk(tree)
	return out
}

func readServer(d Directive) defaultServer {
	srv := defaultServer{file: resolvedFile(d.File), line: d.Line, names: []string{}}
	for _, c := range d.Block {
		switch c.Name {
		case "server_name":
			srv.names = append(srv.names, c.Args...)
		case "listen":
			if l, ok := parseListen(c); ok {
				srv.listens = append(srv.listens, l)
			}
		}
	}
	if !slices.ContainsFunc(d.Block, func(c Directive) bool { return c.Name == "listen" }) {
		// A server with no listen at all is on *:80 when nginx runs as root.
		srv.listens = append(srv.listens, defaultListen{key: "*:80", line: d.Line})
	}
	return srv
}

// parseListen reads a TCP listen line. A unix socket is no address a
// stranger reaches, and a quic one is UDP, which the catch-all does not
// claim: taking it means taking reuseport from the site that has it.
func parseListen(d Directive) (defaultListen, bool) {
	if len(d.Args) == 0 || strings.HasPrefix(d.Args[0], "unix:") || slices.Contains(d.Args[1:], "quic") {
		return defaultListen{}, false
	}
	addr := d.Args[0]
	host, port := "*", "80"
	switch {
	case isDigits(addr):
		port = addr
	case strings.HasPrefix(addr, "["):
		end := strings.Index(addr, "]")
		if end < 0 {
			return defaultListen{}, false
		}
		host = addr[:end+1]
		if rest := addr[end+1:]; strings.HasPrefix(rest, ":") {
			port = rest[1:]
		}
	case strings.Contains(addr, ":"):
		i := strings.LastIndex(addr, ":")
		host, port = addr[:i], addr[i+1:]
	default:
		host = addr
	}
	if host == "0.0.0.0" {
		host = "*"
	}
	l := defaultListen{key: host + ":" + port, params: d.Args[1:], line: d.Line}
	l.claimed = slices.Contains(l.params, "default_server") || slices.Contains(l.params, "default")
	return l, true
}

func isDigits(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

// planDefaultSite fills in who answers each socket, what an Apply would
// claim and who is in its way. ours is the catch-all's own resolved path.
func (s *Service) planDefaultSite(out *DefaultSite, tree []Directive, ours string) {
	servers := httpServers(tree)
	answering := map[string]int{}
	type socket struct {
		seen, plain bool
	}
	sockets := map[string]*socket{}
	for _, srv := range servers {
		for _, l := range srv.listens {
			entry := DefaultListener{Listen: l.key, File: srv.file, Line: l.line, ServerNames: srv.names,
				Claimed: l.claimed, Ours: srv.file == ours}
			if i, ok := answering[l.key]; !ok {
				answering[l.key] = len(out.Answering)
				out.Answering = append(out.Answering, entry)
			} else if l.claimed && !out.Answering[i].Claimed {
				out.Answering[i] = entry
			}
			if srv.file == ours {
				continue
			}
			sk := sockets[l.key]
			if sk == nil {
				sk = &socket{}
				sockets[l.key] = sk
			}
			sk.seen = true
			if !slices.Contains(l.params, "ssl") {
				sk.plain = true
			}
		}
	}

	out.Covers = append(out.Covers, "*:80")
	if sockets["[::]:80"] != nil || s.oursListens(servers, ours, "[::]:80") {
		out.Covers = append(out.Covers, "[::]:80")
	}
	for _, key := range []string{"*:443", "[::]:443"} {
		sk := sockets[key]
		switch {
		case sk == nil && !s.oursListens(servers, ours, key):
		case sk != nil && sk.plain:
			// ssl on one listen line turns the whole socket to TLS, so a
			// site serving plain HTTP on 443 would stop working.
			out.TLSSkipped = "a site listens on " + key + " without ssl, and a TLS catch-all there would turn that socket to TLS for it too"
		default:
			out.Covers = append(out.Covers, key)
		}
	}

	for _, srv := range servers {
		if srv.file == ours {
			continue
		}
		for _, l := range srv.listens {
			if l.claimed && slices.Contains(out.Covers, l.key) {
				out.Others = append(out.Others, DefaultClaim{Listen: l.key, File: srv.file, Line: l.line})
			}
		}
	}
	for _, a := range out.Answering {
		host, port := a.Listen[:strings.LastIndex(a.Listen, ":")], a.Listen[strings.LastIndex(a.Listen, ":")+1:]
		if (port == "80" || port == "443") && host != "*" && host != "[::]" {
			out.Uncovered = appendNew(out.Uncovered, a.Listen)
		}
	}
}

// oursListens keeps a socket the catch-all already holds, so applying it
// again writes the same file.
func (s *Service) oursListens(servers []defaultServer, ours, key string) bool {
	for _, srv := range servers {
		if srv.file == ours && slices.ContainsFunc(srv.listens, func(l defaultListen) bool { return l.key == key }) {
			return true
		}
	}
	return false
}

// validDefaultRedirect is an origin: the request's own path is appended,
// so a path, query or fragment here would be doubled up. `$` is refused
// because nginx would expand it as a variable.
func validDefaultRedirect(raw string) (string, error) {
	if err := validRedirect(raw); err != nil {
		return "", err
	}
	if strings.ContainsAny(raw, "$'\\`") {
		return "", fmt.Errorf("the redirect target contains characters that are not allowed")
	}
	u, _ := url.Parse(strings.TrimSpace(raw))
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", fmt.Errorf("the redirect target must be a scheme and host only, for example https://example.com — the path asked for is added to it")
	}
	return u.Scheme + "://" + u.Host, nil
}

func listenSpelling(key string) string {
	if rest, ok := strings.CutPrefix(key, "*:"); ok {
		return rest
	}
	return key
}

// RenderDefaultSite is the owned file for a choice on the given sockets.
func RenderDefaultSite(choice DefaultChoice, redirectTo, pageDir string, covers []string) (string, error) {
	var action string
	switch choice {
	case DefaultClose:
		action = "        # 444 closes the connection without a response.\n        return 444;\n"
	case DefaultNotFound:
		action = "        return 404;\n"
	case DefaultRedirect:
		target, err := validDefaultRedirect(redirectTo)
		if err != nil {
			return "", err
		}
		action = "        return 301 " + target + "$request_uri;\n"
	case DefaultPage:
		action = "        root " + pageDir + ";\n        try_files /index.html =404;\n"
	default:
		return "", fmt.Errorf("choose close, not_found, redirect or page")
	}
	var b strings.Builder
	b.WriteString("# " + OwnedMarker + ": the catch-all default site, written from the Sites page. An Apply there replaces this file.\n")
	b.WriteString("# nginx sends a request here when its Host names no other site, or names the bare IP.\n")
	b.WriteString("server {\n")
	var tls []string
	for _, key := range covers {
		if strings.HasSuffix(key, ":443") {
			tls = append(tls, key)
			continue
		}
		b.WriteString("    listen " + listenSpelling(key) + " default_server;\n")
	}
	b.WriteString("    server_name _;\n\n")
	b.WriteString("    # certbot's webroot challenge for a name no site holds yet.\n")
	b.WriteString("    location ^~ " + acmeChallengePath + " {\n")
	b.WriteString("        root " + deploymentACMEWebroot + ";\n")
	b.WriteString("        default_type text/plain;\n")
	b.WriteString("        try_files $uri =404;\n")
	b.WriteString("    }\n\n")
	b.WriteString("    # In a location rather than the server: a server-level return runs\n")
	b.WriteString("    # before locations are matched and would answer the challenge too.\n")
	b.WriteString("    location / {\n" + action + "    }\n")
	b.WriteString("}\n")
	if len(tls) > 0 {
		b.WriteString("\n# TLS for a name no site holds a certificate for: the handshake is refused,\n")
		b.WriteString("# so a scanner never sees a certificate or a site.\n")
		b.WriteString("server {\n")
		for _, key := range tls {
			b.WriteString("    listen " + listenSpelling(key) + " ssl default_server;\n")
		}
		b.WriteString("    server_name _;\n")
		b.WriteString("    ssl_reject_handshake on;\n")
		b.WriteString("}\n")
	}
	return b.String(), nil
}

// parseDefaultChoice reads the choice back from the owned file's `location /`.
func parseDefaultChoice(path, content string) (DefaultChoice, string) {
	tree, err := ParseNginxFile(path, content, []string{"http"})
	if err != nil {
		return "", ""
	}
	for _, srv := range tree {
		if srv.Name != "server" {
			continue
		}
		for _, loc := range srv.Block {
			if loc.Name != "location" || !slices.Equal(loc.Args, []string{"/"}) {
				continue
			}
			for _, d := range loc.Block {
				switch {
				case d.Name == "try_files":
					return DefaultPage, ""
				case d.Name == "return" && slices.Equal(d.Args, []string{"444"}):
					return DefaultClose, ""
				case d.Name == "return" && slices.Equal(d.Args, []string{"404"}):
					return DefaultNotFound, ""
				case d.Name == "return" && len(d.Args) == 2 && d.Args[0] == "301":
					return DefaultRedirect, strings.TrimSuffix(d.Args[1], "$request_uri")
				}
			}
		}
	}
	return "", ""
}

const defaultPageHTML = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Nothing here</title></head>
<body style="font-family: system-ui, sans-serif; margin: 20vh auto; max-width: 32rem; padding: 0 1rem; color: #333">
<h1>Nothing is served at this address</h1>
<p>No site on this server answers to the name you asked for.</p>
</body>
</html>
`

// ApplyDefaultSite writes the catch-all, tests the whole configuration with
// it in place, undoes everything if nginx refuses, and reloads when asked.
func (s *Service) ApplyDefaultSite(ctx context.Context, choice DefaultChoice, redirectTo string, reload bool) (*DefaultSiteResult, error) {
	// The request is checked before nginx is dumped, so a bad one is told
	// what is wrong with it rather than about somebody else's file.
	if _, err := RenderDefaultSite(choice, redirectTo, "", nil); err != nil {
		return nil, err
	}
	plan, err := s.DefaultSite(ctx)
	if err != nil {
		return nil, err
	}
	if plan.Error != "" {
		return nil, errors.New(plan.Error)
	}
	if len(plan.Others) > 0 {
		return nil, &DefaultConflictError{Claims: plan.Others}
	}
	content, err := RenderDefaultSite(choice, redirectTo, plan.PageDir, plan.Covers)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	file, link, err := s.defaultSitePaths()
	if err != nil {
		return nil, err
	}
	full, err := s.allowedPath(file)
	if err != nil {
		return nil, err
	}
	original, existed := readIfPresent(full)
	if existed && !dashboardOwned(defaultSiteName, full) {
		return nil, fmt.Errorf("%s exists and is not the dashboard's — rename it before applying the catch-all", full)
	}
	s.keepLoaded(full, link)

	undoPage := func() {}
	if choice == DefaultPage {
		if undoPage, err = s.ensureDefaultPage(); err != nil {
			return nil, err
		}
	}
	if err := writeAtomic(full, content); err != nil {
		undoPage()
		return nil, err
	}
	undoLink := func() {}
	if link != "" {
		if undoLink, err = linkEnabled(link, full); err != nil {
			restoreConfig(full, original, existed)
			undoPage()
			return nil, err
		}
	}
	if res := runValidator(ctx, "nginx", "-t"); !res.Valid {
		undoLink()
		restoreConfig(full, original, existed)
		undoPage()
		return nil, &RefusedError{Validation: res, Lead: "nginx refuses the configuration with the catch-all default site"}
	}
	s.recordChange(ctx, Change{Path: full, Action: ChangeWrite,
		Before: []byte(original), BeforeExisted: existed, After: []byte(content)})
	return &DefaultSiteResult{Path: full, Content: content, Reload: s.reloadLocked(ctx, reload)}, nil
}

// ensureDefaultPage writes a plain index.html when the page directory has
// none, so the "page" choice serves something rather than 404 until the
// operator writes their own. The undo removes only what it created.
func (s *Service) ensureDefaultPage() (func(), error) {
	dir := s.defaultPageDir()
	index := filepath.Join(dir, "index.html")
	if _, err := os.Stat(index); err == nil {
		return func() {}, nil
	}
	_, dirErr := os.Stat(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := writeAtomic(index, defaultPageHTML); err != nil {
		return nil, err
	}
	return func() {
		os.Remove(index)
		if dirErr != nil {
			os.Remove(dir)
		}
	}, nil
}

// RemoveDefaultSite takes the catch-all out and reloads when asked. Nothing
// else can refer to it, so its removal cannot be what makes nginx refuse the
// rest; the reload still tests first, as every reload does. The page
// directory is the operator's content and stays.
func (s *Service) RemoveDefaultSite(ctx context.Context, reload bool) (*DefaultSiteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, link, err := s.defaultSitePaths()
	if err != nil {
		return nil, err
	}
	full, err := s.allowedPath(file)
	if err != nil {
		return nil, err
	}
	content, existed := readIfPresent(full)
	if !existed || !dashboardOwned(defaultSiteName, full) {
		return nil, fmt.Errorf("there is no catch-all default site to remove")
	}
	if link != "" {
		if target, err := filepath.EvalSymlinks(link); err == nil && target == full {
			if err := os.Remove(link); err != nil {
				return nil, err
			}
		}
	}
	if err := os.Remove(full); err != nil {
		return nil, err
	}
	s.recordChange(ctx, Change{Path: full, Action: ChangeDelete, Before: []byte(content), BeforeExisted: true})
	return &DefaultSiteResult{Path: full, Reload: s.reloadLocked(ctx, reload)}, nil
}
