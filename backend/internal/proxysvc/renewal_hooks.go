package proxysvc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Reloading nginx after a renewal.
//
// nginx reads a certificate when it starts or reloads, and serves that one
// until it does again. certbot reloads nginx after a renewal only for a
// lineage it installed with its nginx plugin; one issued with webroot,
// standalone or a DNS plugin is renewed on disk while nginx keeps serving the
// old certificate — until the day it expires, which is how a "renewed" site
// goes down. certbot runs every executable in renewal-hooks/deploy after each
// renewal, and one managed file there closes the gap for every lineage at
// once. The dashboard's own renewal and issuance jobs reload nginx themselves
// (SitesServingLineages), since certbot runs no deploy hook for a brand-new
// lineage.

// renewalHookName sorts after a hook an operator numbered lower, so theirs
// run first, and says whose it is to anybody listing the directory.
const renewalHookName = "50-just-dashboard-reload-nginx"

// renewalHookMarker is how the file is recognised as this dashboard's, so a
// file somebody else put at the same name is never replaced or removed.
const renewalHookMarker = "# Managed by Just Dashboard"

// renewalHookScript tests before it reloads, so a configuration broken since
// the last reload is reported in certbot's output rather than taking nginx
// down; a certificate renewed on disk is still renewed. A host without nginx
// has nothing to reload. The system directories come after PATH, not before,
// so a PATH that names a particular nginx keeps it.
const renewalHookScript = `#!/bin/sh
` + renewalHookMarker + `. The Certificates page's "Reload nginx after
# every renewal" switch installed this file, and turning it off removes it.
#
# certbot runs each executable in this directory after it renews a
# certificate, with RENEWED_LINEAGE naming the lineage. nginx serves the
# certificate it read when it last reloaded, so without a reload a renewed
# certificate reaches no browser until it expires.
PATH="$PATH:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
command -v nginx >/dev/null 2>&1 || exit 0
if ! nginx -t -q; then
	echo "nginx -t failed, so nginx was not reloaded for $RENEWED_LINEAGE" >&2
	exit 1
fi
systemctl reload nginx 2>/dev/null || nginx -s reload
`

// RenewalHook is the managed deploy hook and what else runs beside it.
type RenewalHook struct {
	Path string `json:"path"`
	// State is one of:
	//   installed  the file is this dashboard's, unchanged and executable
	//   missing    there is no file
	//   modified   it is this dashboard's, but changed by hand or no longer
	//              executable, so certbot may not run what it says
	//   foreign    a file of somebody else's has the name; it is left alone
	State string `json:"state"`
	// Others are the other hooks certbot runs after a renewal, by name. One
	// of them may reload nginx already; what each does is not read here.
	Others []string `json:"others"`
}

// ErrRenewalHookForeign is a file at the hook's name that this dashboard did
// not write.
var ErrRenewalHookForeign = errors.New("a file this dashboard did not write is at the hook's path")

// ErrRenewalHookMissing is removing a hook that is not there.
var ErrRenewalHookMissing = errors.New("the hook is not installed")

func renewalHookDir() string {
	return filepath.Join(letsencryptDir, "renewal-hooks", "deploy")
}

// RenewalHookStatus reads the hook's state.
func RenewalHookStatus() (RenewalHook, error) {
	dir := renewalHookDir()
	hook := RenewalHook{Path: filepath.Join(dir, renewalHookName), State: "missing", Others: executablesIn(dir, renewalHookName)}
	if hook.Others == nil {
		hook.Others = []string{}
	}
	sort.Strings(hook.Others)
	info, err := os.Lstat(hook.Path)
	if os.IsNotExist(err) {
		return hook, nil
	}
	if err != nil {
		return hook, err
	}
	if !info.Mode().IsRegular() {
		hook.State = "foreign"
		return hook, nil
	}
	content, err := os.ReadFile(hook.Path)
	if err != nil {
		return hook, err
	}
	switch {
	case !strings.Contains(string(content), renewalHookMarker):
		hook.State = "foreign"
	case string(content) != renewalHookScript || info.Mode().Perm()&0o111 == 0:
		hook.State = "modified"
	default:
		hook.State = "installed"
	}
	return hook, nil
}

// InstallRenewalHook writes the hook, or restores it over a changed copy of
// its own. The file goes in whole: written beside it under a name ending in
// "~", which certbot never runs, made executable, then renamed into place.
func InstallRenewalHook() (RenewalHook, error) {
	hook, err := RenewalHookStatus()
	if err != nil {
		return hook, err
	}
	if hook.State == "foreign" {
		return hook, ErrRenewalHookForeign
	}
	dir := renewalHookDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return hook, err
	}
	tmp, err := os.CreateTemp(dir, "."+renewalHookName+".*~")
	if err != nil {
		return hook, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(renewalHookScript); err != nil {
		tmp.Close()
		return hook, err
	}
	if err := tmp.Close(); err != nil {
		return hook, err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return hook, err
	}
	if err := os.Rename(tmp.Name(), hook.Path); err != nil {
		return hook, err
	}
	return RenewalHookStatus()
}

// RemoveRenewalHook deletes the hook, changed or not, and never a file of
// somebody else's.
func RemoveRenewalHook() (RenewalHook, error) {
	hook, err := RenewalHookStatus()
	if err != nil {
		return hook, err
	}
	switch hook.State {
	case "foreign":
		return hook, ErrRenewalHookForeign
	case "missing":
		return hook, ErrRenewalHookMissing
	}
	if err := os.Remove(hook.Path); err != nil {
		return hook, err
	}
	return RenewalHookStatus()
}

// SitesServingLineages names the enabled nginx sites whose certificate is
// one of these certbot lineages: a path in its live or archive directory,
// named directly or through a link. They serve the old certificate until
// nginx reloads.
func (s *Service) SitesServingLineages(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	var dirs []string
	for _, name := range names {
		dirs = append(dirs,
			filepath.Join(letsencryptDir, "live", name)+string(filepath.Separator),
			filepath.Join(letsencryptDir, "archive", name)+string(filepath.Separator))
	}
	within := func(path string) bool {
		return slices.ContainsFunc(dirs, func(dir string) bool { return strings.HasPrefix(path, dir) })
	}
	var sites []string
	for _, v := range s.nginxVHosts() {
		if !v.Enabled || v.CertPath == "" {
			continue
		}
		path := filepath.Clean(v.CertPath)
		resolved, err := filepath.EvalSymlinks(path)
		if within(path) || (err == nil && within(resolved)) {
			sites = appendOnce(sites, v.Name)
		}
	}
	sort.Strings(sites)
	return sites
}

// ChangedLineages names the lineages whose serial differs between two
// readings of CertbotSerials, a new one included: what a certbot run renewed
// or issued.
func ChangedLineages(before, after map[string]string) []string {
	var changed []string
	for name, serial := range after {
		if before[name] != serial {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)
	return changed
}

// describeSites is a list of site names in a sentence.
func describeSites(sites []string) string {
	switch len(sites) {
	case 0:
		return ""
	case 1:
		return sites[0]
	}
	return strings.Join(sites[:len(sites)-1], ", ") + " and " + sites[len(sites)-1]
}

// ReloadedFor says what a reload after a renewal did for the sites serving
// it.
func ReloadedFor(sites []string) string {
	verb := "serve"
	if len(sites) == 1 {
		verb = "serves"
	}
	return fmt.Sprintf("Reloaded nginx, so %s %s the new certificate.", describeSites(sites), verb)
}

// NotReloadedFor says why a renewed certificate is not what the sites serve.
func NotReloadedFor(sites []string, why string) error {
	verb := "keep"
	if len(sites) == 1 {
		verb = "keeps"
	}
	return fmt.Errorf("the certificate was renewed, but nginx was not reloaded: %s. %s %s serving the previous certificate until nginx reloads",
		why, describeSites(sites), verb)
}
