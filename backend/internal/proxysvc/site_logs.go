package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
)

// Where an nginx site writes, read back from the site's own file.
//
// A site's traffic page reads the files its configuration names rather than a
// path built from its name, because a hand-written site logs wherever its
// author pointed it and the form's convention is only one of those places.
// What a configuration names is still not trusted as a path to open: every
// log this reads must resolve inside nginxLogRoot, so a site file cannot turn
// the traffic page into a reader of /etc/shadow.

// nginxLogRoot is the only directory a site's logs are read from. It is where
// the site form writes them and where every distribution's nginx package puts
// its own.
var nginxLogRoot = "/var/log/nginx"

// ErrSiteNotFound is a name the sites list does not have.
var ErrSiteNotFound = errors.New("no nginx site has that name")

// SiteLogs is where one nginx site writes. Access or Error is empty when the
// site names none of its own, and the note beside it says why, in words the
// traffic page shows as they are.
type SiteLogs struct {
	Site        string   `json:"site"`
	File        string   `json:"file"`
	ServerNames []string `json:"serverNames"`
	Access      string   `json:"access,omitempty"`
	AccessNote  string   `json:"accessNote,omitempty"`
	Error       string   `json:"error,omitempty"`
	ErrorNote   string   `json:"errorNote,omitempty"`
}

// AllSiteLogs resolves every nginx site's logs in one pass over the sites
// directory, for the overview's per-site figures.
func (s *Service) AllSiteLogs() []SiteLogs {
	vhosts := s.nginxVHosts()
	out := make([]SiteLogs, 0, len(vhosts))
	for _, v := range vhosts {
		out = append(out, siteLogsOf(v))
	}
	return out
}

// SiteLogsFor resolves one site's logs. The name is looked up among the
// listed sites, never joined onto a path.
func (s *Service) SiteLogsFor(name string) (SiteLogs, error) {
	for _, v := range s.nginxVHosts() {
		if v.Name == name {
			return siteLogsOf(v), nil
		}
	}
	return SiteLogs{}, fmt.Errorf("%w: %s", ErrSiteNotFound, name)
}

func siteLogsOf(v VHost) SiteLogs {
	out := SiteLogs{Site: v.Name, File: v.Path, ServerNames: v.ServerNames}
	content, err := os.ReadFile(v.Path)
	if err != nil {
		out.AccessNote = "The site's file could not be read."
		out.ErrorNote = out.AccessNote
		return out
	}
	directives, err := ParseNginxFile(v.Path, string(content), []string{"http"})
	if err != nil {
		out.AccessNote = "The site's file does not parse, so where it logs cannot be told."
		out.ErrorNote = out.AccessNote
		return out
	}
	out.Access, out.AccessNote = resolveSiteLog(serverLogDirective(directives, "access_log"), "access_log")
	out.Error, out.ErrorNote = resolveSiteLog(serverLogDirective(directives, "error_log"), "error_log")
	return out
}

// serverLogDirective finds the first log directive at the level of a server
// block, or at the top of the file, which nginx applies to every server in
// it. One inside a location covers only that path and is not the site's log.
func serverLogDirective(directives []Directive, name string) *Directive {
	for i := range directives {
		d := &directives[i]
		if d.Name == name {
			return d
		}
	}
	for _, d := range directives {
		if d.Name != "server" {
			continue
		}
		for i := range d.Block {
			if d.Block[i].Name == name {
				return &d.Block[i]
			}
		}
	}
	return nil
}

// resolveSiteLog turns a log directive into a file this process may read, or
// the reason it will not.
func resolveSiteLog(d *Directive, name string) (string, string) {
	if d == nil || len(d.Args) == 0 {
		if name == "access_log" {
			return "", "This site writes no access log of its own, so its requests are mixed into nginx's shared one, which does not say which site each was for."
		}
		return "", ""
	}
	target := d.Args[0]
	switch {
	case target == "off":
		return "", "Request logging is off for this site."
	case strings.HasPrefix(target, "syslog:"):
		return "", "This site logs to syslog, which the dashboard does not read."
	case target == "stderr" || strings.HasPrefix(target, "memory:"):
		return "", "This site logs somewhere other than a file."
	}
	path, err := confineLog(target)
	if err != nil {
		return "", fmt.Sprintf("This site logs to %s, outside %s, which the dashboard does not read.", target, nginxLogRoot)
	}
	return path, ""
}

// confineLog is the containment check for a log path a configuration names.
// A variable in the path is refused rather than guessed at: nginx expands it
// per request, so there is no one file it names.
func confineLog(path string) (string, error) {
	if !filepath.IsAbs(path) || strings.Contains(path, "$") {
		return "", fmt.Errorf("%w: %s", ErrUnsafePath, path)
	}
	clean := filepath.Clean(path)
	root := filepath.Clean(nginxLogRoot)
	resolved := clean
	if r, err := filepath.EvalSymlinks(clean); err == nil {
		resolved = r
	}
	// Both spellings must be inside: the path as written, so a link the root
	// holds cannot point out of it, and what it resolves to.
	for _, p := range []string{clean, resolved} {
		if !strings.HasPrefix(p, root+string(os.PathSeparator)) {
			return "", fmt.Errorf("%w: %s", ErrUnsafePath, path)
		}
	}
	return resolved, nil
}

// SiteAccessLogReader is the opener for the per-site traffic store. Its route
// is the log path SiteLogs resolved, so two sites that share a file share one
// held record, and a site repointed at another file starts a record of its
// own rather than going on reading the old one. The path is confined again
// here because the store will open whatever it is handed.
func (s *Service) SiteAccessLogReader(_ context.Context, route string) (accesslog.Reader, accesslog.Facts, error) {
	path, err := confineLog(route)
	if err != nil {
		return nil, accesslog.Facts{}, err
	}
	facts := accesslog.Facts{Driver: accessDriverNginx, Format: accesslog.FormatCombined, Latency: false, Path: path}
	return &fileAccessLog{path: path}, facts, nil
}
