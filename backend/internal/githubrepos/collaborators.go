package githubrepos

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Collaborator is one person (or bot) with access to a repository.
type Collaborator struct {
	Login string `json:"login"`
	// Type is User, Organization or Bot.
	Type string `json:"type"`
	// Role is GitHub's role name: admin, maintain, write, triage, read, or
	// an organisation's custom role.
	Role string `json:"role"`
}

// Invitation is a pending invitation to collaborate.
type Invitation struct {
	ID        int64     `json:"id"`
	Invitee   string    `json:"invitee"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"createdAt,omitzero"`
	Inviter   string    `json:"inviter,omitempty"`
	Expired   bool      `json:"expired,omitempty"`
}

// Collaborators is who has access, or a sentence for each half GitHub would
// not show this person.
type Collaborators struct {
	Collaborators       []Collaborator `json:"collaborators"`
	Readable            bool           `json:"readable"`
	Message             string         `json:"message,omitempty"`
	Invitations         []Invitation   `json:"invitations"`
	InvitationsReadable bool           `json:"invitationsReadable"`
}

// Collaborators reads who has access (every affiliation: direct, through an
// organisation, outside collaborators) and the pending invitations. GitHub
// lists collaborators only to people with write access and invitations only
// to administrators; either refusal is a sentence, not a failure.
func (a *RepoAccess) Collaborators(ctx context.Context) (*Collaborators, error) {
	out := &Collaborators{Collaborators: []Collaborator{}, Invitations: []Invitation{}}
	truncated, err := a.Client.Walk(ctx, a.Path("/collaborators?affiliation=all&per_page=100"), func(page []byte) error {
		var batch []struct {
			Login       string          `json:"login"`
			Type        string          `json:"type"`
			RoleName    string          `json:"role_name"`
			Permissions map[string]bool `json:"permissions"`
		}
		if err := json.Unmarshal(page, &batch); err != nil {
			return err
		}
		for _, item := range batch {
			role := item.RoleName
			if role == "" {
				role = RoleFromPermissions(item.Permissions)
			}
			out.Collaborators = append(out.Collaborators, Collaborator{Login: item.Login, Type: item.Type, Role: role})
		}
		return nil
	})
	switch {
	case err == nil:
		out.Readable = true
		if truncated {
			out.Message = fmt.Sprintf("Only the first %d collaborators are shown.", len(out.Collaborators))
		}
	case unreadable(err):
		out.Collaborators = []Collaborator{}
		out.Message = refusalWords(err, "who has access to "+a.Repository.FullName) + " GitHub lists collaborators to people with write access."
		// Invitations need more than collaborators do; not asking saves a
		// request whose answer is already known.
		return out, nil
	default:
		return nil, err
	}

	_, err = a.Client.Walk(ctx, a.Path("/invitations?per_page=100"), func(page []byte) error {
		var batch []struct {
			ID          int64      `json:"id"`
			Invitee     *rawUser   `json:"invitee"`
			Inviter     *rawUser   `json:"inviter"`
			Permissions string     `json:"permissions"`
			CreatedAt   *time.Time `json:"created_at"`
			Expired     bool       `json:"expired"`
		}
		if err := json.Unmarshal(page, &batch); err != nil {
			return err
		}
		for _, item := range batch {
			out.Invitations = append(out.Invitations, Invitation{
				ID: item.ID, Invitee: loginOf(item.Invitee), Role: invitationRole(item.Permissions),
				CreatedAt: timeOf(item.CreatedAt), Inviter: loginOf(item.Inviter), Expired: item.Expired,
			})
		}
		return nil
	})
	switch {
	case err == nil:
		out.InvitationsReadable = true
	case unreadable(err):
		out.Invitations = []Invitation{}
	default:
		return nil, err
	}
	return out, nil
}

// invitationRole is an invitation's permission in the role words the page
// uses: GitHub's older answers say pull and push.
func invitationRole(permission string) string {
	switch permission {
	case "pull":
		return "read"
	case "push":
		return "write"
	}
	return permission
}

// CollaboratorResult is whether setting a role sent an invitation (the
// person was not a collaborator yet) or changed an existing one's role.
type CollaboratorResult struct {
	Invited bool `json:"invited"`
}

// SetCollaborator invites login with role, or changes the role of someone
// who already collaborates.
func (a *RepoAccess) SetCollaborator(ctx context.Context, login, role string) (*CollaboratorResult, error) {
	if !ValidOwner(login) {
		return nil, invalid("%q is not a GitHub username", login)
	}
	permission := ""
	switch role {
	case "read":
		permission = "pull"
	case "write":
		permission = "push"
	case "triage", "maintain", "admin":
		permission = role
	default:
		return nil, invalid("role %q is not read, triage, write, maintain or admin", role)
	}
	if err := a.Require(a.Repository.Can.Administer, "administer"); err != nil {
		return nil, err
	}
	response, err := a.Client.Request(ctx, http.MethodPut, a.Path("/collaborators/"+url.PathEscape(login)), map[string]string{"permission": permission})
	if err != nil {
		return nil, err
	}
	// 201 is an invitation sent; 204 an existing collaborator's role changed.
	return &CollaboratorResult{Invited: response.Status == http.StatusCreated}, nil
}

// RemoveCollaborator takes login's direct access away.
func (a *RepoAccess) RemoveCollaborator(ctx context.Context, login string) error {
	if !ValidOwner(login) {
		return invalid("%q is not a GitHub username", login)
	}
	if err := a.Require(a.Repository.Can.Administer, "administer"); err != nil {
		return err
	}
	_, err := a.Client.Request(ctx, http.MethodDelete, a.Path("/collaborators/"+url.PathEscape(login)), nil)
	return err
}

// CancelInvitation withdraws a pending invitation.
func (a *RepoAccess) CancelInvitation(ctx context.Context, id int64) error {
	if id <= 0 {
		return invalid("an invitation id must be positive")
	}
	if err := a.Require(a.Repository.Can.Administer, "administer"); err != nil {
		return err
	}
	_, err := a.Client.Request(ctx, http.MethodDelete, a.Path(fmt.Sprintf("/invitations/%d", id)), nil)
	return err
}
