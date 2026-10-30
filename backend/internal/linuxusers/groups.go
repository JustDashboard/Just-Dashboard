package linuxusers

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Group is one entry of the group database, with what membership hands over.
type Group struct {
	Name string `json:"name"`
	GID  int    `json:"gid"`
	// Members are supplementary members; Primary are the accounts that have this
	// as their main group, which /etc/group does not list.
	Members []string `json:"members"`
	Primary []string `json:"primary"`
	System  bool     `json:"system"`
	// RootEquivalent is a group that is root by another name.
	RootEquivalent bool `json:"rootEquivalent"`
	// SudoRules are the sudoers rules granted to the group by name.
	SudoRules []SudoRule `json:"sudoRules"`
	Protected bool       `json:"protected"`
}

// protectedGroups underpin the system, the way protected does for accounts.
// Renaming `sudo` would silently orphan the rule that names it.
var protectedGroups = map[string]bool{
	"root": true, "daemon": true, "bin": true, "sys": true, "adm": true, "tty": true, "disk": true,
	"mail": true, "shadow": true, "utmp": true, "sudo": true, "wheel": true, "admin": true,
	"users": true, "nogroup": true, "systemd-journal": true, "ssh": true, "sshd": true,
}

// ListGroups is the whole database.
func (s *Service) ListGroups(ctx context.Context) ([]Group, error) {
	entries, err := parseGroupFile(s.path("group"))
	if err != nil {
		return nil, err
	}
	users, err := parsePasswd(s.path("passwd"))
	if err != nil {
		return nil, err
	}
	rules := s.sudoRules()
	out := make([]Group, 0, len(entries))
	for _, g := range entries {
		g.System = g.GID < 1000 || g.GID == 65534
		g.RootEquivalent = rootEquivalentGroups[g.Name]
		g.Protected = protectedGroups[g.Name] || g.GID == 0
		g.SudoRules = []SudoRule{}
		for _, u := range users {
			if u.GID == g.GID {
				g.Primary = append(g.Primary, u.Username)
			}
		}
		sort.Strings(g.Primary)
		for _, rule := range rules {
			if contains(rule.Who, "%"+g.Name) {
				g.SudoRules = append(g.SudoRules, rule)
			}
		}
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].System != out[j].System {
			return !out[i].System
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func parseGroupFile(path string) ([]Group, error) {
	members, byGID := parseGroups(path)
	out := make([]Group, 0, len(byGID))
	for gid, name := range byGID {
		g := Group{Name: name, GID: gid, Members: []string{}, Primary: []string{}}
		for user, groups := range members {
			if contains(groups, name) {
				g.Members = append(g.Members, user)
			}
		}
		sort.Strings(g.Members)
		out = append(out, g)
	}
	return out, nil
}

// CreateGroup adds a group; a negative gid lets the host pick one.
func (s *Service) CreateGroup(ctx context.Context, name string, gid int, system bool) error {
	if err := ValidateUsername(name); err != nil {
		return fmt.Errorf("invalid group name %q", name)
	}
	args := []string{}
	if gid >= 0 {
		args = append(args, "--gid", strconv.Itoa(gid))
	}
	if system {
		args = append(args, "--system")
	}
	_, err := s.run(ctx, "", "groupadd", append(args, name)...)
	return err
}

// Group is one group by name.
func (s *Service) Group(ctx context.Context, name string) (*Group, error) {
	return s.groupByName(ctx, name)
}

func (s *Service) groupByName(ctx context.Context, name string) (*Group, error) {
	if err := ValidateUsername(name); err != nil {
		return nil, fmt.Errorf("invalid group name %q", name)
	}
	groups, err := s.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	for i := range groups {
		if groups[i].Name == name {
			return &groups[i], nil
		}
	}
	return nil, ErrNotFound
}

// RenameGroup refuses a group a sudoers rule names: the rule would keep the old
// name, and whoever next made a group with it would hold what the rule grants.
func (s *Service) RenameGroup(ctx context.Context, name, newName string) error {
	g, err := s.groupByName(ctx, name)
	if err != nil {
		return err
	}
	if err := ValidateUsername(newName); err != nil {
		return fmt.Errorf("invalid group name %q", newName)
	}
	if g.Protected {
		return fmt.Errorf("%w: %s", ErrProtected, name)
	}
	if len(g.SudoRules) > 0 {
		return fmt.Errorf("a sudo rule in %s names this group: remove it first", g.SudoRules[0].File)
	}
	_, err = s.run(ctx, "", "groupmod", "--new-name", newName, name)
	return err
}

// SetGroupID changes a group's number. Files keep the old one, which is why it
// is offered at all only for groups the system does not rest on.
func (s *Service) SetGroupID(ctx context.Context, name string, gid int) error {
	g, err := s.groupByName(ctx, name)
	if err != nil {
		return err
	}
	if g.Protected {
		return fmt.Errorf("%w: %s", ErrProtected, name)
	}
	if gid <= 0 {
		return fmt.Errorf("a group id must be a positive number")
	}
	_, err = s.run(ctx, "", "groupmod", "--gid", strconv.Itoa(gid), name)
	return err
}

// SetGroupMembers replaces the supplementary members. Each must be an account,
// and the last way in is judged as for any other change that can close it.
func (s *Service) SetGroupMembers(ctx context.Context, name string, members []string) error {
	if _, err := s.groupByName(ctx, name); err != nil {
		return err
	}
	for _, member := range members {
		if err := ValidateUsername(member); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guard(ctx, Change{Group: name, Members: &members}); err != nil {
		return err
	}
	_, err := s.run(ctx, "", "gpasswd", "--members", strings.Join(members, ","), name)
	return err
}

// DeleteGroup removes a group, which the host refuses while it is any account's
// primary group. Saying so first is a better sentence than groupdel's.
func (s *Service) DeleteGroup(ctx context.Context, name string) error {
	g, err := s.groupByName(ctx, name)
	if err != nil {
		return err
	}
	if g.Protected {
		return fmt.Errorf("%w: %s", ErrProtected, name)
	}
	if len(g.Primary) > 0 {
		return fmt.Errorf("%s is the main group of %s", name, strings.Join(g.Primary, ", "))
	}
	if len(g.SudoRules) > 0 {
		return fmt.Errorf("a sudo rule in %s names this group: remove it first", g.SudoRules[0].File)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guard(ctx, Change{Group: name, Members: &[]string{}}); err != nil {
		return err
	}
	_, err = s.run(ctx, "", "groupdel", name)
	return err
}
