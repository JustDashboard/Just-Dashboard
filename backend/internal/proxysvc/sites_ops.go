package proxysvc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BulkAction is what a bulk change does to every site it names.
type BulkAction string

const (
	BulkEnable  BulkAction = "enable"
	BulkDisable BulkAction = "disable"
	BulkDelete  BulkAction = "delete"
)

// maxBulkSites bounds one request: every site is a file operation under the
// service lock, and a page lists far fewer.
const maxBulkSites = 200

// BulkResult is what a bulk change did: the sites it changed, in the order
// given, and the ones already as asked, which it left alone.
type BulkResult struct {
	Changed   []string
	Unchanged []string
}

// bulkStep is one site's change on disk, and how to put it back.
type bulkStep struct {
	name   string
	undo   func() error
	change Change
	// backup is the content a delete keeps as <file>.bak, written only
	// once the whole change has passed nginx's test.
	backup []byte
}

// BulkSites applies one action to several sites as a single change: every
// file is changed, `nginx -t` runs once over the result, and a refusal — or
// any site the action cannot apply to — puts every site back as it was and
// changes nothing. One test instead of one per site is the point: disabling
// ten sites one by one ran ten tests and ten reloads, and a refusal half-way
// left five off and five on. The reload, when asked for, runs inside the
// same hold of the lock, as a single toggle's does.
//
// Unlike a single disable, a bulk one does not go ahead over a
// configuration nginx was already refusing: all-or-nothing is what the
// operator was promised, and switching one site off is still there for
// repairing a broken tree.
func (s *Service) BulkSites(ctx context.Context, action BulkAction, names []string, reload bool) (*BulkResult, *LinkReload, error) {
	switch action {
	case BulkEnable, BulkDisable, BulkDelete:
	default:
		return nil, nil, fmt.Errorf("unknown bulk action %q", action)
	}
	if len(names) == 0 {
		return nil, nil, fmt.Errorf("no sites named")
	}
	if len(names) > maxBulkSites {
		return nil, nil, fmt.Errorf("at most %d sites at once", maxBulkSites)
	}
	seen := map[string]bool{}
	for _, name := range names {
		if err := linkName(name); err != nil {
			return nil, nil, err
		}
		if action == BulkDelete && (!siteNameRe.MatchString(name) || isBackupFile(name)) {
			return nil, nil, fmt.Errorf("%s cannot be deleted from here", name)
		}
		if seen[name] {
			return nil, nil, fmt.Errorf("%s is named twice", name)
		}
		seen[name] = true
	}
	available := filepath.Join(s.nginxDir, "sites-available")
	if action != BulkDelete {
		if _, err := os.Stat(available); err != nil {
			return nil, nil, fmt.Errorf("this host keeps its nginx sites in conf.d, where every file is active — there is no enable or disable to set")
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	var steps []bulkStep
	undoAll := func() error {
		var failed []string
		for i := len(steps) - 1; i >= 0; i-- {
			if err := steps[i].undo(); err != nil {
				failed = append(failed, fmt.Sprintf("%s: %v", steps[i].name, err))
			}
		}
		if len(failed) > 0 {
			return fmt.Errorf("putting the sites back failed — %s", strings.Join(failed, "; "))
		}
		return nil
	}
	out := &BulkResult{Changed: []string{}, Unchanged: []string{}}
	for _, name := range names {
		var step *bulkStep
		var err error
		switch action {
		case BulkEnable:
			step, err = s.bulkEnableStep(name, filepath.Join(available, name))
		case BulkDisable:
			step, err = s.bulkDisableStep(name, filepath.Join(available, name))
		case BulkDelete:
			step, err = s.bulkDeleteStep(name)
		}
		if err != nil {
			if undo := undoAll(); undo != nil {
				return nil, nil, fmt.Errorf("%s: %w, and %v", name, err, undo)
			}
			return nil, nil, fmt.Errorf("%s: %w — nothing was changed", name, err)
		}
		if step == nil {
			out.Unchanged = append(out.Unchanged, name)
			continue
		}
		steps = append(steps, *step)
		out.Changed = append(out.Changed, name)
	}
	if len(steps) == 0 {
		return out, nil, nil
	}
	s.forgetEffective()
	if res := runValidator(ctx, "nginx", "-t"); !res.Valid {
		if err := undoAll(); err != nil {
			return nil, nil, err
		}
		refused := &RefusedError{Validation: res, Lead: fmt.Sprintf("nginx refuses the configuration after this %s of %d sites", action, len(steps))}
		if before := runValidator(ctx, "nginx", "-t"); !before.Valid && FailureHeadline(before) == FailureHeadline(res) {
			refused.Lead = "nginx already refuses the configuration as it is"
		}
		return nil, nil, refused
	}
	for _, step := range steps {
		if step.backup != nil {
			// Kept for the reason a single delete keeps one: validation
			// catches a broken config, not a correct one that says the
			// wrong thing.
			_ = os.WriteFile(step.change.Path+".bak", step.backup, 0o644)
		}
		s.recordChange(ctx, step.change)
	}
	return out, s.reloadLocked(ctx, reload), nil
}

// bulkEnableStep links a site in sites-available into sites-enabled, or
// returns nil when it is already served there.
func (s *Service) bulkEnableStep(name, file string) (*bulkStep, error) {
	if _, err := os.Stat(file); err != nil {
		return nil, fmt.Errorf("no such site in sites-available")
	}
	enabledDir := filepath.Join(s.nginxDir, "sites-enabled")
	link := filepath.Join(enabledDir, name)
	aliases := otherNames(enabledLinks(enabledDir)[resolvedFile(file)], name)
	state, _ := readEnabledLink(link, file)
	if state == linkServes || len(aliases) > 0 {
		return nil, nil
	}
	if state == linkElsewhere {
		// A single enable asks before re-pointing a name that serves some
		// other file; a bulk one has no one to ask.
		return nil, fmt.Errorf("sites-enabled/%s serves another file — enable it on its own, which says what that file loses", name)
	}
	s.keepLoaded(link)
	undo, err := linkEnabled(link, file)
	if err != nil {
		return nil, err
	}
	change := siteChange(s, file, ChangeEnable)
	return &bulkStep{name: name, undo: func() error { undo(); return nil }, change: change}, nil
}

// bulkDisableStep takes a site's links out of sites-enabled — its own and
// any under another name — or returns nil when it has none.
func (s *Service) bulkDisableStep(name, file string) (*bulkStep, error) {
	if _, err := os.Stat(file); err != nil {
		return nil, fmt.Errorf("no such site in sites-available")
	}
	enabledDir := filepath.Join(s.nginxDir, "sites-enabled")
	link := filepath.Join(enabledDir, name)
	links := []string{}
	state, _ := readEnabledLink(link, file)
	switch state {
	case linkServes, linkDangling:
		links = append(links, link)
	case linkElsewhere:
		if info, err := os.Lstat(link); err == nil && info.Mode()&os.ModeSymlink == 0 {
			return nil, fmt.Errorf("%s is a file, not a link, and disabling it would delete that configuration", link)
		}
	}
	for _, alias := range otherNames(enabledLinks(enabledDir)[resolvedFile(file)], name) {
		links = append(links, filepath.Join(enabledDir, alias))
	}
	// What nginx loaded through the links is noted before they go, so an
	// undo leaves them as new as the change.
	s.keepLoaded(links...)
	undo, err := removeLinks(links)
	if err != nil || undo == nil {
		return nil, err
	}
	return &bulkStep{name: name, undo: undo, change: siteChange(s, file, ChangeDisable)}, nil
}

// bulkDeleteStep removes a site's file and its link, holding the content so
// the undo can write it back. The checks are a single delete's.
func (s *Service) bulkDeleteStep(name string) (*bulkStep, error) {
	if err := s.checkSiteDelete(name); err != nil {
		return nil, err
	}
	var full string
	for _, candidate := range []string{filepath.Join(s.nginxDir, "sites-available", name), s.confdPath(name)} {
		if f, err := s.allowedPath(candidate); err == nil {
			if _, err := os.Stat(f); err == nil {
				full = f
				break
			}
		}
	}
	if full == "" {
		return nil, fmt.Errorf("no such site")
	}
	info, err := os.Stat(full)
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	link := filepath.Join(s.nginxDir, "sites-enabled", name)
	var linkUndo func() error
	if _, err := os.Lstat(link); err == nil {
		s.keepLoaded(link)
		if linkUndo, err = removeLinks([]string{link}); err != nil {
			return nil, err
		}
	}
	if err := os.Remove(full); err != nil {
		if linkUndo != nil {
			_ = linkUndo()
		}
		return nil, err
	}
	undo := func() error {
		if err := os.WriteFile(full, content, info.Mode().Perm()); err != nil {
			return err
		}
		if linkUndo != nil {
			return linkUndo()
		}
		return nil
	}
	change := Change{Path: full, Action: ChangeDelete, Before: content, BeforeExisted: true}
	return &bulkStep{name: name, undo: undo, change: change, backup: content}, nil
}

// removeLinks takes symlinks out, refusing a real file, and returns how to
// put them back exactly — relative targets and all. Nothing present returns
// a nil undo.
func removeLinks(links []string) (func() error, error) {
	type removed struct{ link, target string }
	var present []removed
	for _, link := range links {
		info, err := os.Lstat(link)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return nil, fmt.Errorf("%s is a file, not a link", link)
		}
		target, err := os.Readlink(link)
		if err != nil {
			return nil, err
		}
		present = append(present, removed{link, target})
	}
	if len(present) == 0 {
		return nil, nil
	}
	restore := func() error {
		for _, r := range present {
			if _, err := os.Lstat(r.link); err == nil {
				continue
			}
			if err := os.Symlink(r.target, r.link); err != nil {
				return fmt.Errorf("putting %s back failed: %w", r.link, err)
			}
		}
		return nil
	}
	for _, r := range present {
		if err := os.Remove(r.link); err != nil {
			if undo := restore(); undo != nil {
				return nil, undo
			}
			return nil, err
		}
	}
	return restore, nil
}

// siteChange is the history record of a switch: the file, as it is, before
// and after — only its link moved. A file outside the proxy's directories
// is recorded without content, as ReadConfig refuses it.
func siteChange(s *Service, file string, action ChangeAction) Change {
	change := Change{Path: file, Action: action, BeforeExisted: true}
	if full, err := s.allowedPath(file); err == nil {
		content, _ := os.ReadFile(full)
		change.Path, change.Before, change.After = full, content, content
	}
	return change
}
