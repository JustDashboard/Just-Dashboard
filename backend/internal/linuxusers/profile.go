package linuxusers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ProfileChange is the account's own facts. A nil field is left alone.
type ProfileChange struct {
	Comment      *string `json:"comment,omitempty"`
	PrimaryGroup *string `json:"primaryGroup,omitempty"`
	Home         *string `json:"home,omitempty"`
	// MoveHome carries the files along to a new home.
	MoveHome bool    `json:"moveHome,omitempty"`
	UID      *int    `json:"uid,omitempty"`
	NewName  *string `json:"newName,omitempty"`
}

// riskyProfileChange is what a protected account must keep: the system finds it
// by its name, its number and where its files are.
func (c ProfileChange) riskyProfileChange() bool {
	return c.Home != nil || c.UID != nil || c.NewName != nil || c.PrimaryGroup != nil
}

// UpdateProfile applies the change and returns the name the account now has. A
// rename goes last, so everything before it can still name the account.
func (s *Service) UpdateProfile(ctx context.Context, username string, change ProfileChange) (string, error) {
	if err := ValidateUsername(username); err != nil {
		return username, err
	}
	if protected[username] && change.riskyProfileChange() {
		return username, fmt.Errorf("%w: %s", ErrProtected, username)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if change.riskyProfileChange() {
		// A new home without its files leaves the keys behind, a new name can fall
		// outside AllowUsers, and a new main group can drop a %group rule: each can
		// close the last way in as surely as deleting the account.
		judged := Change{
			Username:     username,
			KeysLost:     change.Home != nil && !change.MoveHome,
			NewName:      change.NewName,
			PrimaryGroup: change.PrimaryGroup,
		}
		if err := s.guard(ctx, judged); err != nil {
			return username, err
		}
	}
	args := []string{}
	if change.Comment != nil {
		if err := validateComment(*change.Comment); err != nil {
			return username, err
		}
		args = append(args, "--comment", *change.Comment)
	}
	if change.PrimaryGroup != nil {
		if err := ValidateUsername(*change.PrimaryGroup); err != nil {
			return username, fmt.Errorf("invalid group %q", *change.PrimaryGroup)
		}
		args = append(args, "--gid", *change.PrimaryGroup)
	}
	if change.Home != nil {
		home := filepath.Clean(*change.Home)
		if !filepath.IsAbs(home) || home == "/" {
			return username, fmt.Errorf("a home directory must be an absolute path below /")
		}
		args = append(args, "--home", home)
		if change.MoveHome {
			args = append(args, "--move-home")
		}
	}
	if change.UID != nil {
		// A second uid 0 is a root account that does not look like one, and a
		// common way to hide a back door.
		if *change.UID <= 0 {
			return username, fmt.Errorf("an account's number must be above zero")
		}
		args = append(args, "--uid", strconv.Itoa(*change.UID))
	}
	if len(args) > 0 {
		if _, err := s.run(ctx, "", "usermod", append(args, username)...); err != nil {
			return username, err
		}
	}
	if change.NewName == nil || *change.NewName == username {
		return username, nil
	}
	return s.rename(ctx, username, *change.NewName)
}

// rename moves everything keyed by the account's name. A sudo rule in somebody
// else's file is refused rather than rewritten: leaving it names the old account,
// so the next person given that name would hold it.
func (s *Service) rename(ctx context.Context, from, to string) (string, error) {
	if err := ValidateUsername(to); err != nil {
		return from, err
	}
	if _, err := s.lookup(to); err == nil {
		return from, fmt.Errorf("an account named %s already exists", to)
	}
	managed := s.ManagedGrant(from)
	// The number stays with the account through a rename, so a rule by number still
	// means it. The private group is renamed with it, which would orphan %from.
	spellings := []string{from}
	if account, err := s.lookup(from); err == nil {
		for _, name := range s.ruleNames(*account) {
			if strings.HasPrefix(name, "%") && !strings.HasPrefix(name, "%#") {
				spellings = append(spellings, name)
			}
		}
	}
	for _, rule := range s.sudoRules() {
		if containsAny(rule.Who, spellings) && !(rule.Managed && filepath.Base(rule.File) == managedPrefix+from) {
			return from, fmt.Errorf("a sudo rule in %s names %s: remove it first", rule.File, from)
		}
	}
	crontab, hadCron := s.readCrontab(ctx, from)
	if hadCron {
		if _, err := s.run(ctx, "", "crontab", "-r", "-u", from); err != nil {
			return from, err
		}
	}
	if _, err := s.run(ctx, "", "usermod", "--login", to, from); err != nil {
		if hadCron {
			_, _ = s.run(ctx, crontab, "crontab", "-u", from, "-")
		}
		return from, err
	}
	// The private group that `useradd` made shares the name; a group that merely
	// happens to is somebody else's and keeps it.
	if s.hasPrivateGroup(ctx, from, to) {
		if _, err := s.run(ctx, "", "groupmod", "--new-name", to, from); err != nil {
			return to, fmt.Errorf("the account was renamed but its group was not: %w", err)
		}
	}
	if hadCron {
		if _, err := s.run(ctx, crontab, "crontab", "-u", to, "-"); err != nil {
			return to, fmt.Errorf("the account was renamed but its crontab could not be moved: %w", err)
		}
	}
	if managed.Level != SudoNone {
		if err := os.Remove(s.managedSudoPath(from)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return to, err
		}
		if err := s.setManagedUnlocked(ctx, to, managed); err != nil {
			return to, fmt.Errorf("the account was renamed but its sudo rule could not be moved: %w", err)
		}
	}
	return to, nil
}

func (s *Service) setManagedUnlocked(ctx context.Context, username string, grant SudoGrant) error {
	line, err := grant.line(username)
	if err != nil {
		return err
	}
	content := "# Written by Just Dashboard for " + username + ". Change it there, or delete this file.\n" + line + "\n"
	return s.writeSudoers(ctx, s.managedSudoPath(username), []byte(content))
}

func (s *Service) hasPrivateGroup(ctx context.Context, oldName, newName string) bool {
	users, err := parsePasswd(s.path("passwd"))
	if err != nil {
		return false
	}
	_, byGID := parseGroups(s.path("group"))
	for _, u := range users {
		if u.Username == newName {
			return byGID[u.GID] == oldName
		}
	}
	return false
}

func (s *Service) readCrontab(ctx context.Context, username string) (string, bool) {
	out, err := s.run(ctx, "", "crontab", "-l", "-u", username)
	if err != nil || strings.TrimSpace(out) == "" {
		return "", false
	}
	return out, true
}
