package githubrepos

import (
	"errors"
	"fmt"
	"time"
)

var (
	// ErrNotConfigured means nobody has created this dashboard's GitHub App
	// yet, so there is nothing to connect an account through.
	ErrNotConfigured = errors.New("GitHub is not set up on this dashboard yet")
	// ErrConnectionNotFound is a connection id that is not one of the signed-in
	// account's own. Another account's connection answers exactly this, so an
	// id reveals nothing about whose it is.
	ErrConnectionNotFound = errors.New("that GitHub account is not connected to your dashboard account")
)

// ReconnectError is a connection GitHub no longer accepts: the refresh token
// expired or was refused, or a call answered 401. The person has to sign in
// again; nothing on this side can repair it.
type ReconnectError struct {
	ConnectionID int64
	Login        string
	// Status is the connection's status after the refusal: expired or revoked.
	Status string
	Reason string
}

func (e *ReconnectError) Error() string {
	if e.Reason != "" {
		return e.Reason
	}
	return fmt.Sprintf("GitHub no longer accepts the sign-in for %s; connect it again", e.Login)
}

// NotPermittedError is a refusal decided here, before GitHub is asked: the
// person's role on the repository or the App's grant on the installation
// does not cover the operation. GitHub would refuse it too; asking first
// lets the answer say which of the two is missing.
type NotPermittedError struct {
	Reason string
}

func (e *NotPermittedError) Error() string { return e.Reason }

// Owner is the account a repository or installation belongs to.
type Owner struct {
	Login string `json:"login"`
	// Type is GitHub's: "User" or "Organization".
	Type string `json:"type"`
}

// Installation is one account where the App is installed and the signed-in
// person can see it, with exactly what it grants.
type Installation struct {
	ID      int64 `json:"id"`
	Account Owner `json:"account"`
	// RepositorySelection is "all" or "selected".
	RepositorySelection string `json:"repositorySelection"`
	// Permissions is the App's grant on this installation, GitHub's names
	// and levels ("administration": "write").
	Permissions map[string]string `json:"permissions"`
	// SettingsURL is where the installation's repositories and permissions
	// are changed on GitHub.
	SettingsURL string `json:"settingsUrl"`
	Suspended   bool   `json:"suspended"`
}

// Grants reports whether the installation was granted permission at least
// at level ("read" < "write" < "admin").
func (i Installation) Grants(permission, level string) bool {
	return levelRank(i.Permissions[permission]) >= levelRank(level)
}

func levelRank(level string) int {
	switch level {
	case "read":
		return 1
	case "write":
		return 2
	case "admin":
		return 3
	}
	return 0
}

// Repository is one repository as the GitHub page lists it.
type Repository struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	FullName string `json:"fullName"`
	Owner    Owner  `json:"owner"`
	// Description is empty when GitHub has none.
	Description string `json:"description,omitempty"`
	// Visibility is "public", "private" or "internal".
	Visibility    string    `json:"visibility"`
	Private       bool      `json:"private"`
	Language      string    `json:"language,omitempty"`
	Stars         int       `json:"stars"`
	Forks         int       `json:"forks"`
	OpenIssues    int       `json:"openIssues"`
	Archived      bool      `json:"archived"`
	Fork          bool      `json:"fork"`
	IsTemplate    bool      `json:"isTemplate"`
	DefaultBranch string    `json:"defaultBranch"`
	HTMLURL       string    `json:"htmlUrl"`
	CloneURL      string    `json:"cloneUrl"`
	SSHURL        string    `json:"sshUrl"`
	Topics        []string  `json:"topics,omitempty"`
	CreatedAt     time.Time `json:"createdAt,omitzero"`
	UpdatedAt     time.Time `json:"updatedAt,omitzero"`
	PushedAt      time.Time `json:"pushedAt,omitzero"`
	// InstallationID is the installation this repository was read through.
	InstallationID int64 `json:"installationId"`
	// Role is the person's own on the repository: admin, maintain, write,
	// triage or read, from GitHub's permissions hash.
	Role string `json:"role"`
	// Relation is how the repository stands to the connected account: owned
	// by it, by an organisation, or by somebody else who added it as a
	// collaborator.
	Relation string `json:"relation"`
	// Can is what the page may offer, computed by Abilities.
	Can Abilities `json:"can"`
}

// Relations.
const (
	RelationOwned        = "owned"
	RelationOrganization = "organization"
	RelationCollaborator = "collaborator"
)

// RoleFromPermissions reads GitHub's permissions hash (admin, maintain, push,
// triage, pull) as the highest role it grants.
func RoleFromPermissions(permissions map[string]bool) string {
	switch {
	case permissions["admin"]:
		return "admin"
	case permissions["maintain"]:
		return "maintain"
	case permissions["push"]:
		return "write"
	case permissions["triage"]:
		return "triage"
	default:
		return "read"
	}
}

// RelationOf is how a repository owned by owner stands to the connected
// login.
func RelationOf(owner Owner, login string) string {
	switch {
	case owner.Type == "Organization":
		return RelationOrganization
	case equalFold(owner.Login, login):
		return RelationOwned
	default:
		return RelationCollaborator
	}
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

// Abilities is what the GitHub page may offer on one repository: the
// person's role there and the App's grant on the installation, both of which
// GitHub checks too. A false ability carries its reason in words, so a
// disabled control can say which of the two is missing.
type Abilities struct {
	// Administer covers the repository's settings, its name, description,
	// default branch, visibility, archiving and collaborators.
	Administer bool `json:"administer"`
	// Delete is deleting the repository.
	Delete bool `json:"delete"`
	// Push covers creating and deleting branches.
	Push bool `json:"push"`
	// Merge is merging a pull request.
	Merge bool `json:"merge"`
	// Pulls is closing and reopening pull requests.
	Pulls bool `json:"pulls"`
	// Issues is editing, labelling, closing and reopening issues.
	Issues bool `json:"issues"`
	// CreateIssues is opening a new issue.
	CreateIssues bool `json:"createIssues"`
	// Actions is running, re-running and cancelling workflows.
	Actions bool `json:"actions"`
	// Reasons holds the reason for every ability that is false, keyed by the
	// ability's JSON name.
	Reasons map[string]string `json:"reasons,omitempty"`
}

func roleRank(role string) int {
	switch role {
	case "read":
		return 1
	case "triage":
		return 2
	case "write":
		return 3
	case "maintain":
		return 4
	case "admin":
		return 5
	}
	return 0
}

// Abilities works out what the page may offer on repository through
// installation. hasIssues is the repository's own switch for issues.
//
// It is the one place this policy is written. The handlers refuse an
// operation whose ability is false with its reason (NotPermittedError)
// before GitHub is asked, and the page disables the control with the same
// sentence; GitHub still has the last word, and a refusal from it is shown
// as it is.
func ComputeAbilities(repository Repository, installation Installation, hasIssues bool) Abilities {
	abilities := Abilities{Reasons: map[string]string{}}
	role := repository.Role
	account := installation.Account.Login
	check := func(key string, minRole string, permission, level string, unarchived bool) bool {
		switch {
		case installation.Suspended:
			abilities.Reasons[key] = fmt.Sprintf("The GitHub App is suspended on %s.", account)
		case roleRank(role) < roleRank(minRole):
			abilities.Reasons[key] = fmt.Sprintf("Your role on %s is %s; this needs %s.", repository.FullName, role, minRole)
		case !installation.Grants(permission, level):
			abilities.Reasons[key] = fmt.Sprintf("The GitHub App was not granted %s on %s. Accept the permission on GitHub, then refresh.", permissionWords(permission, level), account)
		case unarchived && repository.Archived:
			abilities.Reasons[key] = fmt.Sprintf("%s is archived, so it is read-only until it is unarchived.", repository.FullName)
		default:
			return true
		}
		return false
	}
	abilities.Administer = check("administer", "admin", "administration", "write", false)
	abilities.Delete = check("delete", "admin", "administration", "write", false)
	abilities.Push = check("push", "write", "contents", "write", true)
	abilities.Merge = check("merge", "write", "contents", "write", true)
	abilities.Pulls = check("pulls", "triage", "pull_requests", "write", true)
	abilities.Issues = check("issues", "triage", "issues", "write", true)
	abilities.CreateIssues = check("createIssues", "read", "issues", "write", true)
	if abilities.CreateIssues && !hasIssues {
		abilities.CreateIssues = false
		abilities.Reasons["createIssues"] = fmt.Sprintf("Issues are switched off on %s.", repository.FullName)
	}
	abilities.Actions = check("actions", "write", "actions", "write", true)
	if len(abilities.Reasons) == 0 {
		abilities.Reasons = nil
	}
	return abilities
}

func permissionWords(permission, level string) string {
	names := map[string]string{
		"administration": "Administration", "contents": "Contents", "pull_requests": "Pull requests",
		"issues": "Issues", "actions": "Actions", "checks": "Checks", "metadata": "Metadata", "statuses": "Commit statuses",
	}
	name := names[permission]
	if name == "" {
		name = permission
	}
	return name + ": " + level
}

// RepoAccess is one repository resolved for one request: the signed-in
// account's own connection, a client acting as that GitHub account, and the
// repository as GitHub answered it with the abilities worked out. Every
// handler under /github/connections/{connection}/repos/{owner}/{repo} starts
// from one.
type RepoAccess struct {
	Client       *Client
	ConnectionID int64
	// Login is the connected GitHub account.
	Login string
	// Owner and Name are GitHub's own spelling, which may differ in case
	// from the address the page used, or follow a rename.
	Owner, Name  string
	Repository   Repository
	Installation Installation
}

// Path is the repository's API path with suffix appended ("" for the
// repository itself, "/branches" for its branches).
func (a *RepoAccess) Path(suffix string) string {
	return "/repos/" + a.Owner + "/" + a.Name + suffix
}

// Require refuses with the ability's reason when it is false.
func (a *RepoAccess) Require(allowed bool, ability string) error {
	if allowed {
		return nil
	}
	reason := a.Repository.Can.Reasons[ability]
	if reason == "" {
		reason = "You may not do this on " + a.Repository.FullName + "."
	}
	return &NotPermittedError{Reason: reason}
}
