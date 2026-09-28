package proxysvc

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
)

// A site's request record, read where the site says it writes one.
//
// A deployment's route is addressed by a name this dashboard chose, so its
// record is at a path the dashboard wrote. A site is not: a hand-written file
// logs wherever its author put the directive, and a guess at the managed
// spelling read an empty file and reported a site nobody visits. So the path
// comes from the site's own file, and — because that file is something an
// operator can edit to point anywhere — it is held to the log roots the logs
// page is, generations included, before a byte of it is read.

var (
	// ErrSiteNotFound is a site this host has no file or route for.
	ErrSiteNotFound = errors.New("no such site")
	// ErrNoSiteAccessLog is a site that logs no requests of its own: off,
	// to syslog, or — with no directive — to nginx's shared log, whose
	// combined lines do not say which site answered them.
	ErrNoSiteAccessLog = errors.New("this site writes no access log of its own")
	// ErrOutsideLogRoots is a site whose access log is somewhere the logs
	// page may not read either.
	ErrOutsideLogRoots = errors.New("the site's access log is outside the log roots")
)

// siteRecordPrefix marks a record that is the file a site's access_log names.
// A deployment's route is a site name the renderer chose and can hold no
// colon, so the two can never name the same record.
const siteRecordPrefix = "file:"

// SiteConfig reads a site's file by its name, in either layout: the listing
// on a conf.d host reports a name that already ends in .conf, and a host set
// up by hand may have a file without it. It reads through the config
// editor's own allowlist, so a name that turns out to be a path is refused
// there rather than joined here.
func (s *Service) SiteConfig(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, "/\\") {
		return "", ErrSiteNotFound
	}
	for _, candidate := range []string{
		filepath.Join(s.nginxDir, "sites-available", name),
		filepath.Join(s.nginxDir, "conf.d", name),
		filepath.Join(s.nginxDir, "conf.d", name+".conf"),
	} {
		if content, err := s.ReadConfig(candidate); err == nil {
			return content, nil
		}
	}
	return "", ErrSiteNotFound
}

// SiteRequestRoute names the record a site's requests are held under, and is
// asked on every read rather than once. The record is the file, not the site:
// an edit — the form, the raw sheet, or an editor over SSH — that moves the
// site's access_log is a new record, where a record held under the site's
// name went on reading the old file for as long as anyone was looking. And a
// name with no site behind it is refused here, before the store holds
// anything for it.
//
// An nginx site is the file its access_log names, refused unless allow
// accepts it. A deployment's own file, and a route on the shared Docker
// Caddy ingress that has no file on the host, are the deployment's route —
// the record its Logs page already holds, rather than a second copy of it.
// A name shaped like a deployment's is one only when the ingress has its
// route: any reader can type the shape, and each name would otherwise start
// a record and a lookup of its own.
func (s *Service) SiteRequestRoute(ctx context.Context, name string, allow func(string) error) (string, error) {
	content, err := s.SiteConfig(name)
	if err != nil {
		if !deploymentRoute(name) {
			return "", err
		}
		if ok, err := s.ingressRoute(ctx, name); err != nil || !ok {
			return "", cmp.Or(err, ErrSiteNotFound)
		}
		return name, nil
	}
	spec, _ := ParseSiteSpec(name, content)
	path := spec.AccessLogPath
	if path == "" {
		return "", ErrNoSiteAccessLog
	}
	if err := allow(path); err != nil {
		return "", fmt.Errorf("%w: %v", ErrOutsideLogRoots, err)
	}
	if deploymentRoute(name) && path == nginxAccessLogPath(name) {
		return name, nil
	}
	return siteRecordPrefix + path, nil
}

// ingressRoute reports whether the Docker Caddy ingress holds a route of this
// name. On an nginx host a deployment's route is a file in sites-available,
// which SiteConfig has already looked for, so there is nothing more to find.
func (s *Service) ingressRoute(ctx context.Context, name string) (bool, error) {
	edge, err := s.dockerCaddy(ctx)
	if err != nil || edge == nil {
		return false, err
	}
	path, err := dockerCaddyRoutePath(name)
	if err != nil {
		return false, nil
	}
	_, exists, err := edge.read(ctx, path)
	return exists, err
}

// deploymentRoute reports whether a name is one the deployment renderer
// writes, which the deployment reader resolves on its own.
func deploymentRoute(name string) bool {
	_, err := dockerCaddyRoutePath(name)
	return err == nil && siteNameRe.MatchString(name)
}

// SiteRecordReader opens a record SiteRequestRoute named by its file, and
// reports false for any other route. allow is asked again before every open
// of the live file and of each rotated generation: the route was resolved
// once, and a generation beside the file can be a link to anywhere.
func SiteRecordReader(route string, allow func(string) error) (accesslog.Reader, accesslog.Facts, bool) {
	path, ok := strings.CutPrefix(route, siteRecordPrefix)
	if !ok {
		return nil, accesslog.Facts{}, false
	}
	facts := accesslog.Facts{Driver: accessDriverNginx, Format: accesslog.FormatCombined, Latency: false, Path: path}
	return &fileAccessLog{path: path, allow: allow}, facts, true
}
