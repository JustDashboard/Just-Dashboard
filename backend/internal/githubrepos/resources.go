package githubrepos

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// What is inside one repository — branches, commits, pull requests, issues,
// workflows and collaborators — is read and changed by the methods on
// RepoAccess in branches.go, commits.go, pulls.go, issues.go, actions.go and
// collaborators.go. Each one checks what it can before GitHub is asked: a
// value GitHub would refuse is an InvalidError, an ability the person or the
// App lacks a NotPermittedError, so a refusal that is certain costs no
// request and says exactly why.

// InvalidError is a value refused here before any request: a branch name git
// would not accept, a pull request number that is not one, a title too long.
// The API answers it as a 400 with Reason.
type InvalidError struct {
	Reason string
}

func (e *InvalidError) Error() string { return e.Reason }

func invalid(format string, a ...any) error {
	return &InvalidError{Reason: fmt.Sprintf(format, a...)}
}

// notFoundBecause is the refusal GitHub would give, decided here: an issue number
// that GitHub answers with a pull request.
func notFoundBecause(path, message string) error {
	return &APIError{Status: http.StatusNotFound, Kind: KindNotFound, Message: message, Path: path}
}

// unreadable reports whether err is GitHub declining to show something to
// this person or this App (403 or 404), which several views draw as a
// sentence rather than as a failure. Rate limits, a revoked sign-in and an
// unreachable GitHub are not: those stop the whole view.
func unreadable(err error) bool {
	switch KindOf(err) {
	case KindForbidden, KindNotFound:
		return true
	}
	return false
}

// refusalWords is a 403 or 404 in a sentence for an "unreadable" field.
func refusalWords(err error, what string) string {
	var api *APIError
	if errors.As(err, &api) && api.Kind == KindForbidden && api.Message != "" {
		return fmt.Sprintf("GitHub would not show %s to this account: %s", what, api.Message)
	}
	return fmt.Sprintf("GitHub would not show %s to this account.", what)
}

var (
	fullSHARE = regexp.MustCompile(`^[0-9a-f]{40}$`)
	// shortSHARE is a commit named by a prefix, which the commit listing
	// accepts as where to start.
	shortSHARE = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
)

// IsFullSHA reports whether s is a full 40-hex commit name in lower case.
func IsFullSHA(s string) bool { return fullSHARE.MatchString(s) }

// ValidBranchName refuses a branch name GitHub's refs API would refuse or
// read as something else, before it reaches a URL: git's check-ref-format
// rules for one branch, no refs/ prefix (the name is always under
// refs/heads/), at most 255 bytes.
func ValidBranchName(name string) error {
	refuse := func(why string) error { return invalid("%q is not a branch name: %s", name, why) }
	switch {
	case name == "":
		return invalid("a branch name is required")
	case len(name) > 255:
		return refuse("it is longer than 255 characters")
	case strings.HasPrefix(name, "refs/"):
		return refuse("leave out refs/; branches are always under refs/heads/")
	case strings.HasPrefix(name, "-"):
		return refuse("it starts with a hyphen")
	case strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.Contains(name, "//"):
		return refuse("it has an empty part between slashes")
	case strings.HasSuffix(name, "."):
		return refuse("it ends with a dot")
	case strings.Contains(name, ".."):
		return refuse("it contains two dots in a row")
	case strings.Contains(name, "@{") || name == "@" || name == "HEAD":
		return refuse("git reads it as a revision")
	}
	// git's own character rule, not a narrower one: a branch someone made on
	// GitHub with a letter outside ASCII still has to be deletable here. The
	// name only ever reaches a URL through EscapeRef.
	for _, r := range name {
		if r < 0x20 || r == 0x7f || r == utf8.RuneError || strings.ContainsRune(" ~^:?*[\\", r) {
			return refuse(fmt.Sprintf("it contains %q", r))
		}
	}
	for _, segment := range strings.Split(name, "/") {
		if strings.HasPrefix(segment, ".") || strings.HasSuffix(segment, ".lock") {
			return refuse("a part starts with a dot or ends with .lock")
		}
	}
	return nil
}

// ParsePositive reads a path parameter that must be a positive integer: a
// pull request or issue number, a workflow, run or invitation id.
func ParsePositive(value, what string) (int64, error) {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		return 0, invalid("%s %q is not a positive number", what, value)
	}
	return n, nil
}

// pageOf is the page query value, 1 when absent or nonsense, and capped so
// a page number cannot walk GitHub's whole history one call at a time.
func pageOf(page int) int {
	if page < 1 {
		return 1
	}
	return min(page, 1000)
}

// pagedPath appends per_page and page to path (which may already carry a
// query) with the extra values in a stable order.
func pagedPath(path string, perPage, page int, extra url.Values) string {
	query := url.Values{}
	for key, values := range extra {
		for _, value := range values {
			if value != "" {
				query.Add(key, value)
			}
		}
	}
	query.Set("per_page", strconv.Itoa(perPage))
	query.Set("page", strconv.Itoa(pageOf(page)))
	return path + "?" + query.Encode()
}

// Label is one issue or pull request label.
type Label struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description,omitempty"`
}

type rawLabel struct {
	Name        string  `json:"name"`
	Color       string  `json:"color"`
	Description *string `json:"description"`
}

func labelsOf(raw []rawLabel) []Label {
	labels := make([]Label, 0, len(raw))
	for _, item := range raw {
		if item.Name == "" {
			continue
		}
		label := Label{Name: item.Name, Color: item.Color}
		if item.Description != nil {
			label.Description = *item.Description
		}
		labels = append(labels, label)
	}
	return labels
}

type rawUser struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

func loginOf(user *rawUser) string {
	if user == nil {
		return ""
	}
	return user.Login
}

// webURL keeps an address only when a browser would open it as a page. A
// check run's details address is supplied by whichever App reported it, so
// javascript: and other schemes are dropped rather than drawn as links.
func webURL(candidates ...string) string {
	for _, candidate := range candidates {
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.Host == "" {
			continue
		}
		if parsed.Scheme == "http" || parsed.Scheme == "https" {
			return candidate
		}
	}
	return ""
}
