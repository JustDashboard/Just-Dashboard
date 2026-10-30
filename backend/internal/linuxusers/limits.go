package linuxusers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// Per-account limits lean on what systemd already does: every logged-in account
// runs under user-<uid>.slice, and a drop-in on that slice caps the lot, its
// sessions and its services together, and survives a reboot. Disk is the
// filesystem's own quota, where the mount has them turned on.

// AccountLimits is what the account may use. Zero is no limit.
type AccountLimits struct {
	CPUPercent  int   `json:"cpuPercent"`
	MemoryBytes int64 `json:"memoryBytes"`
	Tasks       int   `json:"tasks"`
	// Systemd says whether this host can apply any of the above.
	Systemd bool `json:"systemd"`
	// Quota is the disk half, present where the home's mount has quotas on.
	Quota *DiskQuota `json:"quota,omitempty"`
}

// DiskQuota is the filesystem's limit for the account on one mount.
type DiskQuota struct {
	Mount     string `json:"mount"`
	UsedBytes int64  `json:"usedBytes"`
	SoftBytes int64  `json:"softBytes"`
	HardBytes int64  `json:"hardBytes"`
}

const limitsDropIn = "50-just-dashboard.conf"

func (s *Service) limitsFile(uid int) string {
	return s.path(filepath.Join("systemd", "system", fmt.Sprintf("user-%d.slice.d", uid), limitsDropIn))
}

// Limits reads the drop-in and the quota.
func (s *Service) Limits(ctx context.Context, username string) (*AccountLimits, error) {
	u, err := s.lookup(username)
	if err != nil {
		return nil, err
	}
	out := &AccountLimits{}
	if _, err := s.run(ctx, "", "systemctl", "--version"); err == nil {
		out.Systemd = true
	}
	if b, err := os.ReadFile(s.limitsFile(u.UID)); err == nil {
		parseLimitsFile(string(b), out)
	}
	out.Quota = s.readQuota(ctx, *u)
	return out, nil
}

// ExistingHome is the directory useradd would give this name, if something is
// already there. A kept home from an earlier delete is the usual reason.
func (s *Service) ExistingHome(username string) string {
	base := "/home"
	if useradd, ok := readFileString(s.path(filepath.Join("default", "useradd"))); ok {
		if m := useraddLine("HOME").FindStringSubmatch(useradd); m != nil {
			if value := strings.TrimSpace(m[1]); filepath.IsAbs(value) {
				base = value
			}
		}
	}
	home := filepath.Join(base, username)
	if _, err := os.Lstat(hostexec.HostPath(home)); err == nil {
		return home
	}
	return ""
}

// Lookup is one account's passwd entry, with no reading of anything else, for a
// route that only needs to know who the name is.
func (s *Service) Lookup(username string) (*User, error) {
	u, err := s.lookup(username)
	if err != nil {
		return nil, err
	}
	u.System = u.UID < 1000 || u.UID == 65534
	return u, nil
}

func (s *Service) lookup(username string) (*User, error) {
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	users, err := parsePasswd(s.path("passwd"))
	if err != nil {
		return nil, err
	}
	for i := range users {
		if users[i].Username == username {
			return &users[i], nil
		}
	}
	return nil, ErrNotFound
}

var cpuQuotaRe = regexp.MustCompile(`^(\d+)%$`)

func parseLimitsFile(content string, out *AccountLimits) {
	for _, line := range strings.Split(content, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "CPUQuota":
			if m := cpuQuotaRe.FindStringSubmatch(value); m != nil {
				out.CPUPercent, _ = strconv.Atoi(m[1])
			}
		case "MemoryMax":
			out.MemoryBytes, _ = strconv.ParseInt(value, 10, 64)
		case "TasksMax":
			out.Tasks, _ = strconv.Atoi(value)
		}
	}
}

// SetLimits writes the drop-in, or removes it when every limit is off, and tells
// systemd. A protected account is the machine's own and keeps what it has.
func (s *Service) SetLimits(ctx context.Context, username string, limits AccountLimits) error {
	u, err := s.lookup(username)
	if err != nil {
		return err
	}
	if protected[username] {
		return fmt.Errorf("%w: %s", ErrProtected, username)
	}
	if limits.CPUPercent < 0 || limits.MemoryBytes < 0 || limits.Tasks < 0 {
		return fmt.Errorf("a limit cannot be negative")
	}
	// Under these the account cannot start a shell, which reads like a broken
	// server rather than a limit.
	if limits.MemoryBytes > 0 && limits.MemoryBytes < 64<<20 {
		return fmt.Errorf("a memory limit under 64 MB leaves no room for a login")
	}
	if limits.Tasks > 0 && limits.Tasks < 16 {
		return fmt.Errorf("a process limit under 16 leaves no room for a login")
	}
	if limits.CPUPercent > 0 && limits.CPUPercent < 5 {
		return fmt.Errorf("a CPU limit under 5%% leaves no room for a login")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.limitsFile(u.UID)
	if limits.CPUPercent == 0 && limits.MemoryBytes == 0 && limits.Tasks == 0 {
		if err := s.removeLimits(ctx, u.UID); err != nil {
			return err
		}
		return nil
	}
	lines := []string{"# Written by Just Dashboard for " + username + ".", "[Slice]"}
	if limits.CPUPercent > 0 {
		lines = append(lines, fmt.Sprintf("CPUQuota=%d%%", limits.CPUPercent))
	}
	if limits.MemoryBytes > 0 {
		lines = append(lines, fmt.Sprintf("MemoryMax=%d", limits.MemoryBytes))
	}
	if limits.Tasks > 0 {
		lines = append(lines, fmt.Sprintf("TasksMax=%d", limits.Tasks))
	}
	previous, hadPrevious := readFileString(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := writeAtomic(path, []byte(strings.Join(lines, "\n")+"\n")); err != nil {
		return err
	}
	if _, err := s.run(ctx, "", "systemctl", "daemon-reload"); err != nil {
		// A file systemd never read would say a limit is in force when none is.
		if hadPrevious {
			_ = writeAtomic(path, []byte(previous))
		} else {
			_ = os.Remove(path)
			_ = os.Remove(filepath.Dir(path))
		}
		return fmt.Errorf("systemd did not take the limits, so they were not kept: %w", err)
	}
	return nil
}

func (s *Service) removeLimits(ctx context.Context, uid int) error {
	path := s.limitsFile(uid)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	_ = os.Remove(filepath.Dir(path))
	_, err := s.run(ctx, "", "systemctl", "daemon-reload")
	return err
}

// readQuota asks the mount under the home. It answers nil on a host without
// quota tools or on a mount that has none, so the page draws no control that
// could not take effect.
func (s *Service) readQuota(ctx context.Context, u User) *DiskQuota {
	out, err := s.run(ctx, "", "findmnt", "--noheadings", "--output", "TARGET,OPTIONS", "--target", u.Home)
	if err != nil {
		return nil
	}
	fields := strings.Fields(out)
	if len(fields) < 2 || !hasQuotaOption(fields[1]) {
		return nil
	}
	mount := fields[0]
	report, err := s.run(ctx, "", "quota", "--no-wrap", "--raw-grace", "--show-mntpoint", "--local-only", "-u", u.Username)
	if err != nil && strings.TrimSpace(report) == "" {
		return nil
	}
	quota := parseQuotaReport(report, mount)
	quota.Mount = mount
	return &quota
}

func hasQuotaOption(options string) bool {
	for _, option := range strings.Split(options, ",") {
		name, _, _ := strings.Cut(option, "=")
		switch name {
		case "usrquota", "usrjquota", "quota", "uquota":
			return true
		}
	}
	return false
}

// parseQuotaReport reads `quota -v`'s row for the mount: device, mount, then
// blocks, soft and hard in 1 KiB units. A used figure may end in * when over.
func parseQuotaReport(report, mount string) DiskQuota {
	out := DiskQuota{}
	for _, line := range strings.Split(report, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[1] != mount {
			continue
		}
		out.UsedBytes = quotaBytes(fields[2])
		out.SoftBytes = quotaBytes(fields[3])
		out.HardBytes = quotaBytes(fields[4])
	}
	return out
}

func quotaBytes(field string) int64 {
	n, err := strconv.ParseInt(strings.TrimSuffix(field, "*"), 10, 64)
	if err != nil {
		return 0
	}
	return n << 10
}

// SetQuota writes the account's disk limit on the mount under its home.
func (s *Service) SetQuota(ctx context.Context, username string, softBytes, hardBytes int64) error {
	u, err := s.lookup(username)
	if err != nil {
		return err
	}
	if protected[username] {
		return fmt.Errorf("%w: %s", ErrProtected, username)
	}
	if softBytes < 0 || hardBytes < 0 || (hardBytes > 0 && softBytes > hardBytes) {
		return fmt.Errorf("the soft limit cannot be above the hard limit")
	}
	current := s.readQuota(ctx, *u)
	if current == nil {
		return fmt.Errorf("quotas are not turned on for the filesystem that holds %s", u.Home)
	}
	_, err = s.run(ctx, "", "setquota", "-u", username,
		strconv.FormatInt(softBytes>>10, 10), strconv.FormatInt(hardBytes>>10, 10), "0", "0", current.Mount)
	return err
}
