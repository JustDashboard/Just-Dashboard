package linuxusers

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// A snapshot is what the host alert keeps between passes, so it can say what
// changed: the accounts, the keys that open them, the sudo rules that make them
// root, and the last time root signed in. Reading it from the host each pass is
// what catches a change made outside the dashboard, which is the change an
// operator most wants to hear about.

// Snapshot is the part of the account database worth being told about.
type Snapshot struct {
	Accounts     []string  `json:"accounts"`
	Keys         []string  `json:"keys"`
	SudoRules    []string  `json:"sudoRules"`
	RootSignIn   time.Time `json:"rootSignIn"`
	RootSignInIP string    `json:"rootSignInIp,omitempty"`
}

// alertTextCap keeps a message within what a channel will take. A key's label is
// free text, and one long enough would get the alert about it refused.
const alertTextCap = 200

func clipForAlert(text string) string {
	if len(text) > alertTextCap {
		return text[:alertTextCap] + "…"
	}
	return text
}

// AccountChange is one difference between two snapshots.
type AccountChange struct {
	// Subject identifies the change, so one change is told once.
	Subject string
	Label   string
	Detail  string
}

// TakeSnapshot reads the host. System accounts are in it: a package creating
// one is rare enough to be worth a line, and an attacker's is the point.
func (s *Service) TakeSnapshot(ctx context.Context) (*Snapshot, error) {
	st, err := s.read(ctx)
	if err != nil {
		return nil, err
	}
	out := &Snapshot{Accounts: []string{}, Keys: []string{}, SudoRules: []string{}}
	for _, u := range st.users {
		out.Accounts = append(out.Accounts, u.Username)
		if keys, _, keyErr := s.ListKeys(u.Username); keyErr == nil {
			for _, key := range keys {
				out.Keys = append(out.Keys, u.Username+" "+key.Fingerprint+" "+clipForAlert(key.Comment))
			}
		}
		other, _ := otherKeys(st.policy, u)
		for _, key := range other {
			out.Keys = append(out.Keys, u.Username+" "+key.Fingerprint+" "+clipForAlert(key.Comment))
		}
		// A file that cannot be read is the change an attacker would make: a link in
		// place of the file reads as no keys at all, and so as nothing new.
		if u.KeysUnknown && !st.policy.KeysCommand {
			out.Keys = append(out.Keys, u.Username+" unreadable-key-file")
		}
		if u.UID == 0 && u.LastLogin != nil {
			out.RootSignIn = *u.LastLogin
			out.RootSignInIP = u.LastFrom
		}
	}
	for _, rule := range st.rules {
		if rule.AsRoot {
			out.SudoRules = append(out.SudoRules, rule.ID+" "+rule.File+" "+clipForAlert(rule.Text))
		}
	}
	sort.Strings(out.Accounts)
	sort.Strings(out.Keys)
	sort.Strings(out.SudoRules)
	return out, nil
}

// DiffSnapshots names what appeared. What disappeared is not an alarm: nobody
// gets into a server through an account that is no longer there.
func DiffSnapshots(before, after *Snapshot) []AccountChange {
	out := []AccountChange{}
	for _, name := range added(before.Accounts, after.Accounts) {
		out = append(out, AccountChange{
			Subject: "account:" + name,
			Label:   name,
			Detail:  fmt.Sprintf("A new account, %s, appeared on this host.", name),
		})
	}
	// A key is its account and fingerprint. Relabelling one is not a new way in.
	knownKeys := map[string]bool{}
	for _, entry := range before.Keys {
		user, fingerprint, _ := splitKeyEntry(entry)
		knownKeys[user+" "+fingerprint] = true
	}
	for _, entry := range after.Keys {
		user, fingerprint, comment := splitKeyEntry(entry)
		if knownKeys[user+" "+fingerprint] {
			continue
		}
		detail := fmt.Sprintf("A new SSH key can sign in as %s (%s).", user, fingerprint)
		if comment != "" {
			detail = fmt.Sprintf("A new SSH key, %s, can sign in as %s (%s).", comment, user, fingerprint)
		}
		out = append(out, AccountChange{Subject: "key:" + user + ":" + fingerprint, Label: user, Detail: detail})
	}
	for _, entry := range added(before.SudoRules, after.SudoRules) {
		id, file, text := splitKeyEntry(entry)
		out = append(out, AccountChange{
			Subject: "sudo:" + id,
			Label:   file,
			Detail:  fmt.Sprintf("A new sudo rule reaches root: %s (in %s).", text, file),
		})
	}
	if after.RootSignIn.After(before.RootSignIn) {
		detail := "root signed in over SSH."
		if after.RootSignInIP != "" {
			detail = fmt.Sprintf("root signed in from %s.", after.RootSignInIP)
		}
		out = append(out, AccountChange{
			Subject: "root:" + after.RootSignIn.UTC().Format(time.RFC3339),
			Label:   "root",
			Detail:  detail,
		})
	}
	return out
}

func added(before, after []string) []string {
	known := make(map[string]bool, len(before))
	for _, item := range before {
		known[item] = true
	}
	out := []string{}
	for _, item := range after {
		if !known[item] {
			out = append(out, item)
		}
	}
	return out
}

// splitKeyEntry undoes the "a b rest" form the snapshot's lists are kept in.
func splitKeyEntry(entry string) (first, second, rest string) {
	parts := splitN(entry, 3)
	return parts[0], parts[1], parts[2]
}

func splitN(entry string, n int) []string {
	out := make([]string, 0, n)
	for len(out) < n-1 {
		at := -1
		for i := 0; i < len(entry); i++ {
			if entry[i] == ' ' {
				at = i
				break
			}
		}
		if at < 0 {
			break
		}
		out = append(out, entry[:at])
		entry = entry[at+1:]
	}
	out = append(out, entry)
	for len(out) < n {
		out = append(out, "")
	}
	return out
}
