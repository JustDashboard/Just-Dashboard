package githubrepos

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func errorsAs(err error, target any) bool { return errors.As(err, target) }

func TestCollaboratorsAndInvitations(t *testing.T) {
	fake := newFake(t)
	fake.json("GET /repos/octo-org/widgets/collaborators?affiliation=all&per_page=100", 200, `[
		{"login":"mona","type":"User","role_name":"admin"},
		{"login":"ci-bot","type":"Bot","permissions":{"pull":true,"push":true}},
		{"login":"auditor","type":"User","role_name":"security-reviewer"}]`)
	fake.json("GET /repos/octo-org/widgets/invitations?per_page=100", 200, `[
		{"id":5,"invitee":{"login":"newcomer"},"inviter":{"login":"mona"},"permissions":"push","created_at":"2026-11-01T00:00:00Z","expired":true}]`)
	got, err := testAccess(t, fake).Collaborators(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Readable || len(got.Collaborators) != 3 || got.Collaborators[1].Role != "write" || got.Collaborators[2].Role != "security-reviewer" {
		t.Fatalf("collaborators = %+v", got)
	}
	if !got.InvitationsReadable || len(got.Invitations) != 1 {
		t.Fatalf("invitations = %+v", got)
	}
	if inv := got.Invitations[0]; inv.Invitee != "newcomer" || inv.Role != "write" || !inv.Expired || inv.Inviter != "mona" || inv.CreatedAt.IsZero() {
		t.Fatalf("invitation = %+v", inv)
	}
}

func TestCollaboratorsAReaderGetsASentence(t *testing.T) {
	fake := newFake(t)
	fake.json("GET /repos/octo-org/widgets/collaborators?affiliation=all&per_page=100", 403, `{"message":"Must have push access to view repository collaborators."}`)
	got, err := withRole(testAccess(t, fake), "read").Collaborators(context.Background())
	if err != nil || got.Readable || got.Message == "" || got.Collaborators == nil || got.Invitations == nil || got.InvitationsReadable {
		t.Fatalf("collaborators = %+v %v", got, err)
	}
	if fake.callCount() != 1 {
		t.Fatalf("invitations were asked for: %v", fake.calls)
	}

	writer := newFake(t)
	writer.json("GET /repos/octo-org/widgets/collaborators?affiliation=all&per_page=100", 200, `[]`)
	writer.json("GET /repos/octo-org/widgets/invitations?per_page=100", 404, `{"message":"Not Found"}`)
	got, err = withRole(testAccess(t, writer), "write").Collaborators(context.Background())
	if err != nil || !got.Readable || got.InvitationsReadable || got.Invitations == nil {
		t.Fatalf("writer = %+v %v", got, err)
	}
}

func TestSetCollaboratorMapsRolesAndReadsTheStatus(t *testing.T) {
	fake := newFake(t)
	fake.json("PUT /repos/octo-org/widgets/collaborators/newcomer", 201, `{"id":6}`)
	fake.on("PUT /repos/octo-org/widgets/collaborators/mona", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	access := testAccess(t, fake)
	result, err := access.SetCollaborator(context.Background(), "newcomer", "read")
	if err != nil || !result.Invited {
		t.Fatalf("invite = %+v %v", result, err)
	}
	if body := fake.body("PUT /repos/octo-org/widgets/collaborators/newcomer"); body["permission"] != "pull" {
		t.Fatalf("body = %v", body)
	}
	result, err = access.SetCollaborator(context.Background(), "mona", "write")
	if err != nil || result.Invited {
		t.Fatalf("change = %+v %v", result, err)
	}
	if body := fake.body("PUT /repos/octo-org/widgets/collaborators/mona"); body["permission"] != "push" {
		t.Fatalf("body = %v", body)
	}
	calls := fake.callCount()
	_, err = access.SetCollaborator(context.Background(), "../admin", "read")
	wantInvalid(t, err)
	_, err = access.SetCollaborator(context.Background(), "mona", "owner")
	wantInvalid(t, err)
	writer := withRole(testAccess(t, fake), "maintain")
	_, err = writer.SetCollaborator(context.Background(), "mona", "read")
	wantNotPermitted(t, err, "needs admin")
	wantNotPermitted(t, writer.RemoveCollaborator(context.Background(), "mona"), "needs admin")
	wantNotPermitted(t, writer.CancelInvitation(context.Background(), 5), "needs admin")
	wantInvalid(t, access.CancelInvitation(context.Background(), 0))
	if fake.callCount() != calls {
		t.Fatalf("GitHub was asked: %v", fake.calls)
	}
}

func TestRemoveCollaboratorAndCancelInvitation(t *testing.T) {
	fake := newFake(t)
	noContent := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }
	fake.on("DELETE /repos/octo-org/widgets/collaborators/mona", noContent)
	fake.on("DELETE /repos/octo-org/widgets/invitations/5", noContent)
	access := testAccess(t, fake)
	if err := access.RemoveCollaborator(context.Background(), "mona"); err != nil {
		t.Fatal(err)
	}
	if err := access.CancelInvitation(context.Background(), 5); err != nil {
		t.Fatal(err)
	}
}
