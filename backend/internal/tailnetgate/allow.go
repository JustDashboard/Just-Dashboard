package tailnetgate

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// An allow-list names who may open a tailnet-only deployment: Tailscale
// logins, alice@example.com, and ACL tags, tag:ci. A login is the user's
// login name as the tailnet's identity provider spells it — GitHub accounts
// are alice@github, passkey accounts alice@passkey, so the part after the @
// need not be a domain with a dot. A tag is tag: followed by the name the
// tailnet's policy file gives it.
//
// Logins are compared without regard to case, because identity providers do
// not agree on one and alice@Example.com is the same person. Tags are
// compared exactly, as the tailnet's policy compares them: tag:CI and tag:ci
// are two tags there, and so they are here.

// MaxAllow bounds an allow-list. A tailnet's people are a group, not a
// directory; a longer list is a paste of the wrong thing.
const MaxAllow = 64

var (
	loginRE = regexp.MustCompile(`^[A-Za-z0-9._%+'=-]{1,128}@[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	tagRE   = regexp.MustCompile(`^tag:[A-Za-z][A-Za-z0-9-]{0,62}$`)
)

// Canonical reads one entry of an allow-list and returns it as the plan keeps
// it: a login in lower case, a tag as written. It says which it is.
func Canonical(raw string) (entry string, tag bool, err error) {
	value := strings.TrimSpace(raw)
	switch {
	case value == "":
		return "", false, fmt.Errorf("an empty entry names nobody")
	case strings.HasPrefix(strings.ToLower(value), "tag:"):
		if !tagRE.MatchString(value) {
			return "", true, fmt.Errorf("%s is not a tag: tag: is followed by a letter, then letters, digits and dashes", value)
		}
		return value, true, nil
	case loginRE.MatchString(value):
		return strings.ToLower(value), false, nil
	default:
		return "", false, fmt.Errorf("%s is neither a Tailscale login (user@example.com) nor a tag (tag:name)", value)
	}
}

// CanonicalList checks a whole allow-list: every entry well formed, none
// twice, no more than MaxAllow. It returns the list as the plan keeps it.
func CanonicalList(raw []string) ([]string, error) {
	if len(raw) > MaxAllow {
		return nil, fmt.Errorf("at most %d logins and tags", MaxAllow)
	}
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		entry, _, err := Canonical(value)
		if err != nil {
			return nil, err
		}
		if slices.Contains(out, entry) {
			return nil, fmt.Errorf("%s is listed twice", entry)
		}
		out = append(out, entry)
	}
	return out, nil
}

// Admits says whether the list lets the peer in, and which entry did: the
// peer's login, compared without case, or one of its machine's tags,
// compared exactly. A tagged machine is never admitted by a login, and a
// person's machine never by a tag it does not carry.
func Admits(allow []string, peer Peer) (string, bool) {
	for _, entry := range allow {
		if strings.HasPrefix(entry, "tag:") {
			if slices.Contains(peer.Tags, entry) {
				return entry, true
			}
			continue
		}
		if !peer.Tagged() && peer.Login != "" && strings.EqualFold(entry, peer.Login) {
			return entry, true
		}
	}
	return "", false
}
