package linuxusers

import (
	"bufio"
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"
)

// What is behind an account's password field. Locked and "none" are the two the
// old reading could not tell apart, and only one of them has a password to come
// back to.
const (
	// PasswordSet is a hash that signs in today.
	PasswordSet = "set"
	// PasswordLocked is a hash behind a "!": it stops working, and unlocking
	// restores the same password.
	PasswordLocked = "locked"
	// PasswordNone is "!", "*" or "!!" with nothing behind it. The account has
	// no password and unlocking has nothing to restore.
	PasswordNone = "none"
	// PasswordEmpty is an empty field: no password is asked for at all.
	PasswordEmpty = "empty"
	// PasswordUnknown is a shadow file this process cannot read.
	PasswordUnknown = "unknown"
)

type shadowEntry struct {
	Hash                                         string
	LastChange, Min, Max, Warn, Inactive, Expire int
}

// parseShadow reads every field but the reserved one. An empty numeric field is
// -1, which is how the shadow suite itself writes "not set".
func parseShadow(path string) (map[string]shadowEntry, bool) {
	out := map[string]shadowEntry{}
	f, err := os.Open(path)
	if err != nil {
		return out, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), ":")
		if len(fields) < 2 {
			continue
		}
		for len(fields) < 8 {
			fields = append(fields, "")
		}
		out[fields[0]] = shadowEntry{
			Hash:       fields[1],
			LastChange: shadowDays(fields[2]),
			Min:        shadowDays(fields[3]),
			Max:        shadowDays(fields[4]),
			Warn:       shadowDays(fields[5]),
			Inactive:   shadowDays(fields[6]),
			Expire:     shadowDays(fields[7]),
		}
	}
	return out, true
}

func shadowDays(field string) int {
	n, err := strconv.Atoi(strings.TrimSpace(field))
	if err != nil {
		return -1
	}
	return n
}

func (e shadowEntry) locked() bool {
	return strings.HasPrefix(e.Hash, "!") || strings.HasPrefix(e.Hash, "*")
}

func (e shadowEntry) passwordState() string {
	if e.Hash == "" {
		return PasswordEmpty
	}
	behind := strings.TrimLeft(e.Hash, "!")
	switch {
	case behind == "", strings.HasPrefix(behind, "*"):
		return PasswordNone
	case behind != e.Hash:
		return PasswordLocked
	}
	return PasswordSet
}

// expired is an account whose expiry date has passed. The shadow suite counts
// days from the epoch, and an account expires at the start of the day named.
func (e shadowEntry) expired(today int) bool {
	return e.Expire >= 0 && e.Expire <= today
}

// PasswordAging is the shadow file's rules about when a password must change,
// with "never" as nil rather than the 99999 the file spells it as.
type PasswordAging struct {
	LastChanged string `json:"lastChanged,omitempty"`
	// MustChange is a password that has to be replaced at the next sign-in.
	MustChange   bool   `json:"mustChange"`
	MinDays      *int   `json:"minDays,omitempty"`
	MaxDays      *int   `json:"maxDays,omitempty"`
	WarnDays     *int   `json:"warnDays,omitempty"`
	InactiveDays *int   `json:"inactiveDays,omitempty"`
	ExpiresOn    string `json:"expiresOn,omitempty"`
}

// passwordDead is an account closed by its password's age: past the last change,
// the maximum and the grace after it, PAM refuses it outright, keys included.
func (e shadowEntry) passwordDead(today int) bool {
	return e.LastChange > 0 && e.Max >= 0 && e.Max < 10000 && e.Inactive >= 0 &&
		e.LastChange+e.Max+e.Inactive < today
}

func (e shadowEntry) aging() PasswordAging {
	a := PasswordAging{MustChange: e.LastChange == 0}
	if e.LastChange > 0 {
		a.LastChanged = dayString(e.LastChange)
	}
	if e.Min > 0 {
		a.MinDays = intPtr(e.Min)
	}
	// 99999 is how the shadow suite writes "never"; anything near it is a date
	// centuries away, which is the same thing and reads worse.
	if e.Max >= 0 && e.Max < 10000 {
		a.MaxDays = intPtr(e.Max)
		if e.LastChange > 0 {
			a.ExpiresOn = dayString(e.LastChange + e.Max)
		}
	}
	if e.Warn > 0 {
		a.WarnDays = intPtr(e.Warn)
	}
	if e.Inactive >= 0 {
		a.InactiveDays = intPtr(e.Inactive)
	}
	return a
}

func intPtr(v int) *int { return &v }

func dayString(days int) string {
	if days < 0 {
		return ""
	}
	return time.Unix(int64(days)*86400, 0).UTC().Format("2006-01-02")
}

func (s *Service) shadowEntry(username string) (shadowEntry, bool) {
	entries, readable := parseShadow(s.path("shadow"))
	if !readable {
		return shadowEntry{}, false
	}
	entry, ok := entries[username]
	return entry, ok
}

// minPasswordLength is the floor below which a dashboard will not set a
// password, whatever the host's PAM stack would accept. pwquality may ask for
// more, and chpasswd reports that in its own words.
const minPasswordLength = 8

// maxPasswordLength is well past any password and well short of chpasswd's line
// buffer, past which the rest of the line would be read as the next record.
const maxPasswordLength = 1024

func validatePassword(password string) error {
	if len(password) < minPasswordLength {
		return fmt.Errorf("a password needs at least %d characters", minPasswordLength)
	}
	if len(password) > maxPasswordLength {
		return fmt.Errorf("a password can be at most %d characters", maxPasswordLength)
	}
	if strings.ContainsAny(password, "\n\r\x00") {
		return fmt.Errorf("a password cannot contain a line break")
	}
	return nil
}

// CheckNewPassword is validatePassword for a form that may not have set one.
func CheckNewPassword(password string) error {
	if password == "" {
		return nil
	}
	return validatePassword(password)
}

// generatedAlphabet leaves out what reads ambiguously when a person has to copy
// it from a screen: 0 and O, 1, l and I. Punctuation is left out too, because
// the first thing a generated password usually meets is a shell.
const generatedAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// generatedLength gives about 117 bits at 57 symbols.
const generatedLength = 20

// GeneratePassword makes one password from the system's CSPRNG.
func GeneratePassword() (string, error) {
	out := make([]byte, generatedLength)
	limit := big.NewInt(int64(len(generatedAlphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", err
		}
		out[i] = generatedAlphabet[n.Int64()]
	}
	return string(out), nil
}

// SetPassword gives an account a new password over chpasswd's stdin, so it is
// never in an argument vector, and confirms the file really changed.
func (s *Service) SetPassword(ctx context.Context, username, password string) error {
	if err := ValidateUsername(username); err != nil {
		return err
	}
	if err := validatePassword(password); err != nil {
		return err
	}
	before, known := s.shadowEntry(username)
	if _, err := s.run(ctx, username+":"+password+"\n", "chpasswd"); err != nil {
		return err
	}
	if after, ok := s.shadowEntry(username); known && ok && after.Hash == before.Hash {
		return fmt.Errorf("chpasswd reported success but %s's password did not change", username)
	}
	return nil
}

// RemovePassword takes an account back to keys only: the hash is gone, not
// hidden behind a "!", so nothing can bring the old password back. Root is allowed
// this, unlike a lock: root on keys alone is how a server is meant to be, and it is
// what a stock cloud image ships. The guard is what stops it being the last way in.
func (s *Service) RemovePassword(ctx context.Context, username string) error {
	if err := ValidateUsername(username); err != nil {
		return err
	}
	if protected[username] && username != "root" {
		return fmt.Errorf("%w: %s", ErrProtected, username)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guard(ctx, Change{Username: username, Password: stringPtr(PasswordNone)}); err != nil {
		return err
	}
	_, err := s.run(ctx, "", "usermod", "--password", "!", username)
	return err
}

// RequirePasswordChange makes the next sign-in replace the password. It needs a
// password to replace: on an account with none, PAM would refuse a key sign-in
// over an expired password that was never there.
func (s *Service) RequirePasswordChange(ctx context.Context, username string) error {
	if err := ValidateUsername(username); err != nil {
		return err
	}
	if entry, known := s.shadowEntry(username); known && entry.passwordState() != PasswordSet {
		return fmt.Errorf("%s has no usable password to change", username)
	}
	_, err := s.run(ctx, "", "chage", "--lastday", "0", username)
	return err
}

// SetDisabled ends every way in, keys included, by expiring the account, and
// brings it back by clearing the date. A lock would leave keys working.
func (s *Service) SetDisabled(ctx context.Context, username string, disabled bool) error {
	if err := ValidateUsername(username); err != nil {
		return err
	}
	if !disabled {
		_, err := s.run(ctx, "", "chage", "--expiredate", "-1", username)
		return err
	}
	if protected[username] {
		return fmt.Errorf("%w: %s", ErrProtected, username)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guard(ctx, Change{Username: username, Disabled: boolPtr(true)}); err != nil {
		return err
	}
	// Day one, not day zero: chage reads 0 as "1970-01-01", and some versions of
	// the suite treat a zero here as no expiry at all.
	_, err := s.run(ctx, "", "chage", "--expiredate", "1", username)
	return err
}

func boolPtr(v bool) *bool { return &v }

// SetExpiry schedules the end of an account, or clears it with an empty date.
func (s *Service) SetExpiry(ctx context.Context, username, date string) error {
	if err := ValidateUsername(username); err != nil {
		return err
	}
	if date == "" {
		_, err := s.run(ctx, "", "chage", "--expiredate", "-1", username)
		return err
	}
	day, err := time.Parse("2006-01-02", date)
	if err != nil {
		return fmt.Errorf("the expiry date must be YYYY-MM-DD")
	}
	if protected[username] {
		return fmt.Errorf("%w: %s", ErrProtected, username)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// A date that has already passed is disabling the account by another name.
	if int(day.Unix()/86400) <= s.today() {
		if err := s.guard(ctx, Change{Username: username, Disabled: boolPtr(true)}); err != nil {
			return err
		}
	}
	_, err = s.run(ctx, "", "chage", "--expiredate", date, username)
	return err
}

// AgingChange names the rules to change; a nil field is left alone and -1 clears
// a rule, which is how chage itself spells "never".
type AgingChange struct {
	MinDays      *int `json:"minDays,omitempty"`
	MaxDays      *int `json:"maxDays,omitempty"`
	WarnDays     *int `json:"warnDays,omitempty"`
	InactiveDays *int `json:"inactiveDays,omitempty"`
}

// SetAging writes the password's rules.
func (s *Service) SetAging(ctx context.Context, username string, change AgingChange) error {
	if err := ValidateUsername(username); err != nil {
		return err
	}
	args := []string{}
	add := func(flag string, value *int) {
		if value != nil {
			args = append(args, flag, strconv.Itoa(*value))
		}
	}
	for _, value := range []*int{change.MinDays, change.MaxDays, change.WarnDays, change.InactiveDays} {
		if value != nil && (*value < -1 || *value > 99999) {
			return fmt.Errorf("a password rule is a number of days between -1 and 99999")
		}
	}
	add("--mindays", change.MinDays)
	add("--maxdays", change.MaxDays)
	add("--warndays", change.WarnDays)
	add("--inactive", change.InactiveDays)
	if len(args) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// A maximum and a grace that have already run out close the account, keys and
	// all. Judged a day ahead: a password changed today is not dead until tomorrow,
	// and tomorrow is too late to find out.
	if entry, known := s.shadowEntry(username); known {
		next := entry
		if change.MaxDays != nil {
			next.Max = *change.MaxDays
		}
		if change.InactiveDays != nil {
			next.Inactive = *change.InactiveDays
		}
		if next.passwordDead(s.today()+1) && !entry.passwordDead(s.today()+1) {
			if err := s.guard(ctx, Change{Username: username, Disabled: boolPtr(true)}); err != nil {
				return err
			}
		}
	}
	_, err := s.run(ctx, "", "chage", append(args, username)...)
	return err
}

// FailedAttempts is pam_faillock's count of refused attempts for an account.
// Available is false where the host keeps no such record, so a page says nothing
// rather than reporting a clean zero it does not know.
type FailedAttempts struct {
	Available bool `json:"available"`
	Count     int  `json:"count"`
}

func (s *Service) FailedAttempts(ctx context.Context, username string) FailedAttempts {
	if err := ValidateUsername(username); err != nil {
		return FailedAttempts{}
	}
	out, err := s.run(ctx, "", "faillock", "--user", username)
	if err != nil {
		return FailedAttempts{}
	}
	return FailedAttempts{Available: true, Count: parseFaillock(out)}
}

// parseFaillock counts the valid rows. The first line is the account, the second
// the column names, and a row ends in V when it still counts toward a lockout.
func parseFaillock(out string) int {
	count := 0
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[len(fields)-1] == "V" {
			count++
		}
	}
	return count
}

// ResetFailedAttempts clears a faillock lockout.
func (s *Service) ResetFailedAttempts(ctx context.Context, username string) error {
	if err := ValidateUsername(username); err != nil {
		return err
	}
	_, err := s.run(ctx, "", "faillock", "--user", username, "--reset")
	return err
}
