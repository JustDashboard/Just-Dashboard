package proxysvc

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// maxSiteFileBytes bounds a site file brought in by an import or a restore.
// A real server block is a few kilobytes; anything near this is not one.
const maxSiteFileBytes = 256 << 10

// siteFileDirs are the directories a site file is imported into and restored
// in. sites-enabled is not one: nginx includes everything there, so a file
// written into it is live with no switch, and a backup there is read as a
// site.
var siteFileDirs = []string{"sites-available", "conf.d"}

// SiteDownload returns a site's file as it is on disk, by the name the Sites
// list shows. Only a file inside the proxy's directories, and never a
// password file, is handed out, as ReadConfig would.
func (s *Service) SiteDownload(name string) (string, []byte, error) {
	if err := linkName(name); err != nil {
		return "", nil, err
	}
	for _, candidate := range []string{
		filepath.Join(s.nginxDir, "sites-available", name),
		s.confdPath(name),
		filepath.Join(s.nginxDir, "sites-enabled", name),
	} {
		if _, err := os.Stat(candidate); err != nil {
			continue
		}
		full, err := s.allowedPath(candidate)
		if err != nil {
			return "", nil, err
		}
		if s.isPasswordFile(full) {
			return "", nil, fmt.Errorf("%w: %s", ErrProtectedFile, name)
		}
		content, err := os.ReadFile(full)
		if err != nil {
			return "", nil, err
		}
		return filepath.Base(candidate), content, nil
	}
	return "", nil, fmt.Errorf("no such site: %s", name)
}

// exportEntry is one site in an export's manifest.
type exportEntry struct {
	Name        string   `json:"name"`
	Layout      string   `json:"layout"`
	File        string   `json:"file,omitempty"`
	Path        string   `json:"path"`
	Enabled     bool     `json:"enabled"`
	LinkedAs    []string `json:"linkedAs,omitempty"`
	ServerNames []string `json:"serverNames"`
	Bytes       int      `json:"bytes,omitempty"`
	SHA256      string   `json:"sha256,omitempty"`
	// Skipped says why the file is not in the archive: it resolves outside
	// the proxy's directories, or it is gone.
	Skipped string `json:"skipped,omitempty"`
}

// ExportSites packs every nginx site the Sites list shows into a tar.gz:
// each file under <layout>/<name>, and a manifest.json saying where each
// came from, whether it was enabled, and its checksum. Password files are
// never in it — a site names its auth_basic_user_file, and the manifest says
// they were left out — nor is anything that resolves outside the proxy's
// directories, which the manifest lists as skipped.
func (s *Service) ExportSites(at time.Time) ([]byte, error) {
	hosts := s.nginxVHosts()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	entries := []exportEntry{}
	for _, v := range hosts {
		e := exportEntry{
			Name: v.Name, Layout: v.Layout, Path: v.Path, Enabled: v.Enabled,
			LinkedAs: v.LinkedAs, ServerNames: v.ServerNames,
		}
		if e.ServerNames == nil {
			e.ServerNames = []string{}
		}
		content, reason := s.exportContent(v.Path)
		if reason != "" {
			e.Skipped = reason
			entries = append(entries, e)
			continue
		}
		sum := sha256.Sum256(content)
		e.File, e.Bytes, e.SHA256 = v.Layout+"/"+v.Name, len(content), hex.EncodeToString(sum[:])
		if err := writeTarFile(tw, e.File, content, at); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	manifest, err := json.MarshalIndent(map[string]any{
		"exportedAt": at.UTC().Format(time.RFC3339),
		"nginxDir":   s.nginxDir,
		"sites":      entries,
		"note":       "Password files (auth_basic_user_file) and files outside the nginx directory are never included.",
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := writeTarFile(tw, "manifest.json", append(manifest, '\n'), at); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// exportContent is a listed site's file, or why it is not exported.
func (s *Service) exportContent(path string) ([]byte, string) {
	if path == "" {
		return nil, "its link in sites-enabled points at nothing"
	}
	full, err := s.allowedPath(path)
	if err != nil {
		return nil, "it resolves outside the nginx directory"
	}
	if s.isPasswordFile(full) {
		return nil, "it is a password file"
	}
	content, err := os.ReadFile(full)
	if err != nil {
		return nil, "it could not be read"
	}
	return content, ""
}

func writeTarFile(tw *tar.Writer, name string, content []byte, at time.Time) error {
	if err := tw.WriteHeader(&tar.Header{
		Name: name, Mode: 0o644, Size: int64(len(content)), ModTime: at, Typeflag: tar.TypeReg,
	}); err != nil {
		return err
	}
	_, err := tw.Write(content)
	return err
}

// SitePlacement is what an import or a restore did, or — for a preview —
// would do: where the file lands, whether nginx accepts the configuration
// with it in place, and what is worth knowing before it goes live.
type SitePlacement struct {
	Name        string            `json:"name"`
	Path        string            `json:"path"`
	Layout      string            `json:"layout"`
	Enabled     bool              `json:"enabled"`
	ServerNames []string          `json:"serverNames"`
	Validation  *ValidationResult `json:"validation"`
	// RefusedBefore says nginx refuses the configuration without the file
	// too, so a refusal is not the file's doing.
	RefusedBefore bool     `json:"refusedBefore,omitempty"`
	Warnings      []string `json:"warnings"`
}

// ImportSite puts a pasted or dropped server block in as a new site, in
// sites-available where the host has it and conf.d where it does not. The
// file is written, linked into sites-enabled, and `nginx -t` runs over the
// whole configuration — linked even when it is to stay disabled, as a file
// outside the include tree tests valid whatever it says. A refusal, or a
// preview (commit false), takes the file and the link back out. An existing
// site, or an existing name in sites-enabled, is never overwritten.
func (s *Service) ImportSite(ctx context.Context, name, content string, enable, commit, reload bool) (*SitePlacement, *LinkReload, error) {
	dir := "sites-available"
	if _, err := os.Stat(filepath.Join(s.nginxDir, dir)); err != nil {
		dir = "conf.d"
		if _, err := os.Stat(filepath.Join(s.nginxDir, dir)); err != nil {
			return nil, nil, fmt.Errorf("%s has neither a sites-available nor a conf.d directory — set JD_NGINX_DIR to where this host keeps its nginx configuration", s.nginxDir)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.placeSiteLocked(ctx, dir, name, content, enable, commit, reload)
}

// placeSiteLocked writes content as a new site called name in dir, tests it
// in place and keeps it only when commit is set and nginx accepts it. Must
// be called with s.mu held.
func (s *Service) placeSiteLocked(ctx context.Context, dir, name, content string, enable, commit, reload bool) (*SitePlacement, *LinkReload, error) {
	if !siteNameRe.MatchString(name) || isBackupFile(name) {
		return nil, nil, fmt.Errorf("a site name is up to 64 lower-case letters, digits, dots, dashes and underscores, starting with a letter or digit, and not ending like a backup")
	}
	if strings.HasPrefix(name, deploymentRoutePrefix) {
		return nil, nil, fmt.Errorf("%s is the name a deployment gives its route — a deploy writes that file by name and would replace it", name)
	}
	if err := checkSiteContent(content); err != nil {
		return nil, nil, err
	}
	link, path := "", s.confdPath(name)
	if dir == "sites-available" {
		path = filepath.Join(s.nginxDir, "sites-available", name)
		link = filepath.Join(s.nginxDir, "sites-enabled", name)
	}
	full, err := s.allowedPath(path)
	if err != nil {
		return nil, nil, err
	}
	// Both layouts: a name in sites-available and conf.d at once is one the
	// list cannot tell apart, and the site verbs refuse it.
	for _, taken := range []string{full, filepath.Join(s.nginxDir, "sites-available", name), s.confdPath(name)} {
		if _, err := os.Lstat(taken); err == nil {
			return nil, nil, fmt.Errorf("a site called %s already exists", filepath.Base(taken))
		}
	}
	if link != "" {
		if _, err := os.Lstat(link); err == nil {
			return nil, nil, fmt.Errorf("sites-enabled/%s already exists, and the new site's link would take its place", name)
		}
	}

	out := &SitePlacement{
		Name: filepath.Base(full), Path: full, Layout: dir,
		ServerNames: contentServerNames(content), Warnings: s.placementWarnings(full, content),
	}
	s.keepLoaded(full, link)
	if err := writeAtomic(full, content); err != nil {
		return nil, nil, err
	}
	undo := func() {
		if link != "" {
			_ = os.Remove(link)
		}
		_ = os.Remove(full)
		s.forgetEffective()
	}
	if link != "" {
		if err := os.Symlink(full, link); err != nil {
			undo()
			return nil, nil, err
		}
	}
	s.forgetEffective()
	out.Validation = runValidator(ctx, "nginx", "-t")
	if !out.Validation.Valid || !commit {
		undo()
		if !out.Validation.Valid {
			before := runValidator(ctx, "nginx", "-t")
			out.RefusedBefore = !before.Valid && FailureHeadline(before) == FailureHeadline(out.Validation)
		}
		if !commit {
			return out, nil, nil
		}
		refused := &RefusedError{Validation: out.Validation, Lead: fmt.Sprintf("nginx refuses the configuration with %s added", out.Name)}
		if out.RefusedBefore {
			refused.Lead = "nginx already refuses the configuration as it is"
		}
		return nil, nil, refused
	}
	out.Enabled = link == "" || enable
	if link != "" && !enable {
		_ = os.Remove(link)
		s.forgetEffective()
	}
	s.recordChange(ctx, Change{Path: full, Action: ChangeWrite, After: []byte(content)})
	if !out.Enabled {
		return out, nil, nil
	}
	return out, s.reloadLocked(ctx, reload), nil
}

// checkSiteContent refuses what cannot be a site file: nothing, something
// too large, or bytes that are not text.
func checkSiteContent(content string) error {
	switch {
	case strings.TrimSpace(content) == "":
		return fmt.Errorf("the file is empty")
	case len(content) > maxSiteFileBytes:
		return fmt.Errorf("the file is larger than %d KiB, which no server block is", maxSiteFileBytes>>10)
	case !utf8.ValidString(content) || strings.ContainsRune(content, 0):
		return fmt.Errorf("the file is not text")
	}
	return nil
}

// contentServerNames are the hostnames content's server_name lines give.
func contentServerNames(content string) []string {
	names := []string{}
	for _, m := range serverNameRe.FindAllStringSubmatch(content, -1) {
		for _, name := range strings.Fields(m[1]) {
			if name = unquote(name); !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	return names
}

// placementWarnings are what a new site file does not stop nginx accepting
// but the operator should hear before it goes live.
func (s *Service) placementWarnings(full, content string) []string {
	warnings := []string{}
	if strings.Contains(firstLine(content), OwnedMarker) {
		warnings = append(warnings, "the first line marks the file as the dashboard's own, so the Sites list will not show it — remove that line to manage it here")
	}
	if directives, err := ParseNginxFile(full, content, []string{"http"}); err == nil {
		server := false
		for _, d := range directives {
			server = server || (d.Name == "server" && d.Block != nil)
		}
		if !server {
			warnings = append(warnings, "the file declares no server block, so it is not a site — in conf.d the Sites list will not show it")
		}
	}
	names := contentServerNames(content)
	for _, v := range s.nginxVHosts() {
		for _, name := range v.ServerNames {
			if name != "_" && name != "" && slices.Contains(names, name) {
				warnings = append(warnings, fmt.Sprintf("%s is already served by %s; nginx answers it from whichever it reads first", name, v.Name))
			}
		}
	}
	return warnings
}

// SiteBackup is a copy nginx does not read beside a site: what a delete
// keeps as <name>.bak, or an editor's or package manager's leftover.
type SiteBackup struct {
	File     string    `json:"file"`
	Layout   string    `json:"layout"`
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	// Site is the name a restore gives it back, and SiteExists whether a
	// site of that name is there now — a restore then needs another name.
	Site       string `json:"site,omitempty"`
	SiteExists bool   `json:"siteExists"`
	// Restorable is false, with Reason, for a copy that cannot become a
	// site again: not text, too large, or a password file.
	Restorable bool   `json:"restorable"`
	Reason     string `json:"reason,omitempty"`
}

// ListSiteBackups lists the backup files in sites-available and conf.d,
// newest first. Dotfiles are left out: they are the editor's swap files
// and this service's own temporary files mid-write.
func (s *Service) ListSiteBackups() []SiteBackup {
	out := []SiteBackup{}
	for _, dir := range siteFileDirs {
		entries, err := os.ReadDir(filepath.Join(s.nginxDir, dir))
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !e.Type().IsRegular() || strings.HasPrefix(name, ".") || !isBackupFile(name) {
				continue
			}
			b, err := s.siteBackup(dir, name)
			if err != nil {
				continue
			}
			out = append(out, *b)
		}
	}
	slices.SortFunc(out, func(a, b SiteBackup) int { return b.Modified.Compare(a.Modified) })
	return out
}

// siteBackup describes one backup file, refusing any name or place a backup
// cannot have.
func (s *Service) siteBackup(dir, file string) (*SiteBackup, error) {
	if !slices.Contains(siteFileDirs, dir) {
		return nil, fmt.Errorf("backups are kept in sites-available or conf.d, not %s", dir)
	}
	if err := linkName(file); err != nil {
		return nil, err
	}
	if !isBackupFile(file) {
		return nil, fmt.Errorf("%s is not a backup", file)
	}
	path := filepath.Join(s.nginxDir, dir, file)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("no such backup: %s/%s", dir, file)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s/%s is not a plain file", dir, file)
	}
	full, err := s.allowedPath(path)
	if err != nil {
		return nil, err
	}
	b := &SiteBackup{File: file, Layout: dir, Path: full, Size: info.Size(), Modified: info.ModTime()}
	b.Site = backupSite(file)
	if b.Site != "" {
		target := filepath.Join(s.nginxDir, dir, b.Site)
		if dir == "conf.d" {
			target = s.confdPath(b.Site)
		}
		_, err := os.Lstat(target)
		b.SiteExists = err == nil
	}
	switch {
	case s.isPasswordFile(full):
		b.Reason = "it is a password file"
	case info.Size() > maxSiteFileBytes:
		b.Reason = "it is too large to be a site file"
	default:
		content, err := os.ReadFile(full)
		if err != nil || checkSiteContent(string(content)) != nil {
			b.Reason = "it is empty or not text"
		}
	}
	b.Restorable = b.Reason == ""
	return b, nil
}

// backupSite is the site a backup file was made from: its name with the
// backup suffixes taken off, or "" when what is left is not a site name.
func backupSite(file string) string {
	name := file
	for isBackupFile(name) {
		trimmed := strings.TrimSuffix(name, "~")
		// The longest suffix, so app.dpkg-old is app and not app.dpkg.
		longest := ""
		for _, suffix := range backupSuffixes {
			if strings.HasSuffix(strings.ToLower(trimmed), suffix) && len(suffix) > len(longest) {
				longest = suffix
			}
		}
		trimmed = trimmed[:len(trimmed)-len(longest)]
		if trimmed == name {
			return ""
		}
		name = trimmed
	}
	if !siteNameRe.MatchString(name) {
		return ""
	}
	return name
}

// RestoreSiteBackup puts a backup back as a site in the directory it was
// kept in, under the name it was made from or under as when that is taken.
// It goes through the same test an import does, and the backup stays where
// it is: the restored site is a copy of it.
func (s *Service) RestoreSiteBackup(ctx context.Context, dir, file, as string, enable, reload bool) (*SitePlacement, *LinkReload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.siteBackup(dir, file)
	if err != nil {
		return nil, nil, err
	}
	if !b.Restorable {
		return nil, nil, fmt.Errorf("%s cannot be restored: %s", file, b.Reason)
	}
	name := strings.TrimSpace(as)
	if name == "" {
		name = b.Site
	}
	if name == "" {
		return nil, nil, fmt.Errorf("%s does not say which site it was — name the site to restore it as", file)
	}
	content, err := os.ReadFile(b.Path)
	if err != nil {
		return nil, nil, err
	}
	return s.placeSiteLocked(ctx, dir, name, string(content), enable, true, reload)
}

// PurgeSiteBackup deletes one backup file for good.
func (s *Service) PurgeSiteBackup(dir, file string) (*SiteBackup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.siteBackup(dir, file)
	if err != nil {
		return nil, err
	}
	return b, os.Remove(b.Path)
}
