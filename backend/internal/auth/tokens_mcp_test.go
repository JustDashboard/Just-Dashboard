package auth

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

// connectionsFor saves the named connections, for grants to point at.
func connectionsFor(t *testing.T, svc *Service, names ...string) []int64 {
	t.Helper()
	ids := make([]int64, 0, len(names))
	for _, name := range names {
		res, err := svc.st.DB.ExecContext(t.Context(),
			`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?, 'postgres', 'sealed', 0)`, name)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		ids = append(ids, id)
	}
	return ids
}

func TestAnMCPKeyCarriesItsOwnersRoleAndItsDatabases(t *testing.T) {
	svc, owner := newTestService(t, false)
	ids := connectionsFor(t, svc, "shop", "analytics")
	tok, secret, err := svc.CreateMCPToken(t.Context(), owner, " claude-desktop ", "", 30*24*time.Hour, []TokenConnection{
		{ConnectionID: ids[1], Access: AccessWrite},
		{ConnectionID: ids[0], Access: AccessRead},
	}, DeployNone, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []TokenConnection{{ids[0], AccessRead}, {ids[1], AccessWrite}}
	if tok.Name != "claude-desktop" || tok.Scope != ScopeMCP || tok.Role != RoleAdmin || !reflect.DeepEqual(tok.Connections, want) {
		t.Fatalf("created key = %+v", tok)
	}
	resolved, _, role, err := svc.ResolveAPIToken(t.Context(), secret)
	if err != nil || resolved.Scope != ScopeMCP || role != RoleAdmin {
		t.Fatalf("resolved = %+v %s %v", resolved, role, err)
	}
	granted, err := svc.TokenConnections(t.Context(), tok.ID)
	if err != nil || !reflect.DeepEqual(granted, want) {
		t.Fatalf("grants = %+v %v", granted, err)
	}

	plain, _, err := svc.CreateAPIToken(t.Context(), owner, "github-actions", RoleReadOnly, 0)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := svc.ListAPITokens(t.Context(), owner.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range listed {
		switch k.ID {
		case tok.ID:
			if !reflect.DeepEqual(k.Connections, want) {
				t.Errorf("the list gives the MCP key %+v", k.Connections)
			}
		case plain.ID:
			if k.Scope != "" || k.Connections != nil {
				t.Errorf("a key for the whole API was listed as %q with %+v", k.Scope, k.Connections)
			}
		}
	}
}

func TestAnMCPKeysGrantsAreChecked(t *testing.T) {
	svc, admin := newTestService(t, false)
	ids := connectionsFor(t, svc, "shop")
	viewer, err := svc.CreateUser(t.Context(), "viewer", testPassword, RoleReadOnly, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		owner       *User
		grants      []TokenConnection
		deployments DeployAccess
	}{
		{"no database and no deployments", admin, nil, DeployNone},
		{"twice", admin, []TokenConnection{{ids[0], AccessRead}, {ids[0], AccessWrite}}, DeployNone},
		{"an access nobody spells", admin, []TokenConnection{{ids[0], "admin"}}, DeployNone},
		{"no id", admin, []TokenConnection{{0, AccessRead}}, DeployNone},
		{"changes through a read-only account", viewer, []TokenConnection{{ids[0], AccessWrite}}, DeployNone},
		{"a deployment grant nobody spells", admin, []TokenConnection{{ids[0], AccessRead}}, "admin"},
		{"deploys through a read-only account", viewer, nil, DeployRun},
	} {
		if _, _, err := svc.CreateMCPToken(t.Context(), tc.owner, "key", "", 0, tc.grants, tc.deployments, nil, false); err == nil {
			t.Errorf("%s: created", tc.name)
		}
	}
	if _, _, err := svc.CreateMCPToken(t.Context(), viewer, "key", "", 0, []TokenConnection{{ids[0], AccessRead}}, DeployNone, nil, false); err != nil {
		t.Errorf("a read-only account cannot make a key that reads: %v", err)
	}
	var keys int
	if err := svc.st.DB.QueryRow(`SELECT COUNT(*) FROM api_tokens`).Scan(&keys); err != nil || keys != 1 {
		t.Fatalf("%d keys stored after the refusals, want the one that was allowed (%v)", keys, err)
	}
}

func TestAnMCPKeyIsRepointedOnlyByItsOwnerWhileItWorks(t *testing.T) {
	svc, owner := newTestService(t, false)
	ids := connectionsFor(t, svc, "shop", "analytics")
	tok, secret, err := svc.CreateMCPToken(t.Context(), owner, "cursor", "", 0, []TokenConnection{{ids[0], AccessRead}}, DeployNone, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	next := []TokenConnection{{ids[1], AccessWrite}}
	updated, err := svc.SetTokenConnections(t.Context(), owner, tok.ID, next, nil, nil, nil)
	if err != nil || !reflect.DeepEqual(updated.Connections, next) {
		t.Fatalf("repointed = %+v %v", updated, err)
	}
	if _, _, _, err := svc.ResolveAPIToken(t.Context(), secret); err != nil {
		t.Fatalf("repointing a key stopped it working: %v", err)
	}

	other, err := svc.CreateUser(t.Context(), "maria", testPassword, RoleAdmin, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetTokenConnections(t.Context(), other, tok.ID, next, nil, nil, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("another account repointed the key: %v", err)
	}
	plain, _, err := svc.CreateAPIToken(t.Context(), owner, "github-actions", RoleLimited, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetTokenConnections(t.Context(), owner, plain.ID, next, nil, nil, nil); !errors.Is(err, ErrNotMCPToken) {
		t.Errorf("a key for the whole API was given databases: %v", err)
	}
	if err := svc.RevokeAPIToken(t.Context(), tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetTokenConnections(t.Context(), owner, tok.ID, next, nil, nil, nil); !errors.Is(err, ErrTokenInactive) {
		t.Errorf("a revoked key was repointed: %v", err)
	}
	if granted, _ := svc.TokenConnections(t.Context(), tok.ID); !reflect.DeepEqual(granted, next) {
		t.Errorf("the refusals changed the grants: %+v", granted)
	}
}

// The grants go with the record they belong to, and with the connection they
// name: neither is left pointing at something that is gone.
func TestAnMCPKeysGrantsGoWithTheKeyAndTheConnection(t *testing.T) {
	svc, owner := newTestService(t, false)
	ids := connectionsFor(t, svc, "shop", "analytics")
	tok, _, err := svc.CreateMCPToken(t.Context(), owner, "claude", "", 0, []TokenConnection{
		{ids[0], AccessRead}, {ids[1], AccessRead},
	}, DeployNone, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.st.DB.Exec(`DELETE FROM db_connections WHERE id = ?`, ids[1]); err != nil {
		t.Fatal(err)
	}
	if granted, _ := svc.TokenConnections(t.Context(), tok.ID); !reflect.DeepEqual(granted, []TokenConnection{{ids[0], AccessRead}}) {
		t.Fatalf("after forgetting a connection the key reaches %+v", granted)
	}
	if err := svc.RevokeAPIToken(t.Context(), tok.ID); err != nil {
		t.Fatal(err)
	}
	if deleted, err := svc.DeleteInactiveAPIToken(t.Context(), tok.ID); err != nil || !deleted {
		t.Fatalf("deleted = %v %v", deleted, err)
	}
	var left int
	if err := svc.st.DB.QueryRow(`SELECT COUNT(*) FROM api_token_connections`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("%d grants outlived their key (%v)", left, err)
	}
}

// The deployments are a grant of their own: a key made without it reaches
// none, a key may reach the deployments and no database, and re-pointing the
// databases keeps the grant unless it is named.
func TestAnMCPKeysDeploymentGrantIsItsOwn(t *testing.T) {
	svc, owner := newTestService(t, false)
	ids := connectionsFor(t, svc, "shop")
	plain, _, err := svc.CreateMCPToken(t.Context(), owner, "claude", "", 0, []TokenConnection{{ids[0], AccessRead}}, DeployNone, nil, false)
	if err != nil || plain.Deployments != DeployNone {
		t.Fatalf("a key made without the deployments = %+v %v", plain, err)
	}
	deployer, secret, err := svc.CreateMCPToken(t.Context(), owner, "cursor", "", 0, nil, DeployRun, nil, false)
	if err != nil || deployer.Deployments != DeployRun || len(deployer.Connections) != 0 {
		t.Fatalf("a key for the deployments alone = %+v %v", deployer, err)
	}
	if resolved, _, _, err := svc.ResolveAPIToken(t.Context(), secret); err != nil || resolved.Deployments != DeployRun {
		t.Fatalf("resolved = %+v %v", resolved, err)
	}
	kept, err := svc.SetTokenConnections(t.Context(), owner, deployer.ID, []TokenConnection{{ids[0], AccessWrite}}, nil, nil, nil)
	if err != nil || kept.Deployments != DeployRun {
		t.Fatalf("re-pointing the databases changed the deployments: %+v %v", kept, err)
	}
	read := DeployRead
	if narrowed, err := svc.SetTokenConnections(t.Context(), owner, deployer.ID, nil, &read, nil, nil); err != nil || narrowed.Deployments != DeployRead {
		t.Fatalf("narrowed = %+v %v", narrowed, err)
	}
	none := DeployNone
	if _, err := svc.SetTokenConnections(t.Context(), owner, deployer.ID, nil, &none, nil, nil); err == nil {
		t.Error("a key was left reaching nothing")
	}
	listed, err := svc.ListAPITokens(t.Context(), owner.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range listed {
		if k.ID == deployer.ID && k.Deployments != DeployRead {
			t.Errorf("the list gives the key %q", k.Deployments)
		}
	}

	// An owner demoted since may not give a key the right to deploy, though
	// the key itself was made at a role that could.
	if _, err := svc.st.DB.Exec(`UPDATE users SET role = ? WHERE id = ?`, string(RoleReadOnly), owner.ID); err != nil {
		t.Fatal(err)
	}
	owner.Role = RoleReadOnly
	run := DeployRun
	if _, err := svc.SetTokenConnections(t.Context(), owner, deployer.ID, nil, &run, nil, nil); err == nil {
		t.Error("a read-only owner gave a key the right to deploy")
	}
}

// An MCP key's deployment grant is narrowed to chosen deployments as a
// deploy key is: the list is read with the key on every request, kept when
// the key is re-pointed without it, replaced with it, and cleared with a
// grant of none; a key given no deployments is not narrowed to some.
func TestAnMCPKeysDeploymentsAreNarrowedAsADeployKeysAre(t *testing.T) {
	svc, owner := newTestService(t, false)
	grants := []TokenConnection{{connectionsFor(t, svc, "shop")[0], AccessRead}}
	if _, _, err := svc.CreateMCPToken(t.Context(), owner, "x", "", 0, grants, DeployNone, []int64{4}, false); err == nil {
		t.Error("a key given no deployments was narrowed to some")
	}
	if _, _, err := svc.CreateMCPToken(t.Context(), owner, "x", "", 0, nil, DeployRead, []int64{4, 4}, false); err == nil {
		t.Error("a deployment was named twice")
	}
	tok, secret, err := svc.CreateMCPToken(t.Context(), owner, "claude", "", 0, nil, DeployRun, []int64{9, 4}, false)
	if err != nil || !reflect.DeepEqual(tok.DeploymentIDs, []int64{4, 9}) {
		t.Fatalf("a narrowed key = %+v %v", tok, err)
	}
	resolved, _, _, err := svc.ResolveAPIToken(t.Context(), secret)
	if err != nil || !resolved.ReachesDeployment(4) || resolved.ReachesDeployment(5) || resolved.EveryDeployment() {
		t.Fatalf("resolved = %+v %v", resolved, err)
	}
	kept, err := svc.SetTokenConnections(t.Context(), owner, tok.ID, nil, nil, nil, nil)
	if err != nil || !reflect.DeepEqual(kept.DeploymentIDs, []int64{4, 9}) {
		t.Fatalf("re-pointed without a list = %+v %v", kept, err)
	}
	one := []int64{5}
	if replaced, err := svc.SetTokenConnections(t.Context(), owner, tok.ID, nil, nil, &one, nil); err != nil || !reflect.DeepEqual(replaced.DeploymentIDs, []int64{5}) {
		t.Fatalf("re-pointed with a list = %+v %v", replaced, err)
	}
	every := []int64{}
	widened, err := svc.SetTokenConnections(t.Context(), owner, tok.ID, nil, nil, &every, nil)
	if err != nil || len(widened.DeploymentIDs) != 0 {
		t.Fatalf("widened = %+v %v", widened, err)
	}
	if resolved, _, _, _ := svc.ResolveAPIToken(t.Context(), secret); !resolved.EveryDeployment() || !resolved.ReachesDeployment(77) {
		t.Errorf("a key reaching every deployment = %+v", resolved)
	}
	if _, err := svc.SetTokenConnections(t.Context(), owner, tok.ID, nil, nil, &one, nil); err != nil {
		t.Fatal(err)
	}
	none := DeployNone
	if _, err := svc.SetTokenConnections(t.Context(), owner, tok.ID, grants, &none, &one, nil); err == nil {
		t.Error("a key given no deployments was narrowed to some")
	}
	cleared, err := svc.SetTokenConnections(t.Context(), owner, tok.ID, grants, &none, nil, nil)
	if err != nil || len(cleared.DeploymentIDs) != 0 {
		t.Fatalf("a grant of none kept its list: %+v %v", cleared, err)
	}
	if ids, _ := svc.TokenDeployments(t.Context(), tok.ID); len(ids) != 0 {
		t.Errorf("a key given no deployments keeps rows for %v", ids)
	}
	// Only a deploy key and an MCP key are narrowed at all.
	plain := &APIToken{Scope: "", DeploymentIDs: nil}
	if plain.ReachesDeployment(1) || plain.EveryDeployment() {
		t.Error("an ordinary key reads as narrowed to deployments")
	}
}

// Whether an MCP key is offered the generated tools is the key's own flag:
// off unless it is asked for, read with the key, kept when the key is
// re-pointed without it, and changed only when it is sent.
func TestAnMCPKeysGeneratedToolsAreItsOwnFlag(t *testing.T) {
	svc, owner := newTestService(t, false)
	plain, secret, err := svc.CreateMCPToken(t.Context(), owner, "plain", "", 0, nil, DeployRead, nil, false)
	if err != nil || plain.Operations {
		t.Fatalf("a key made without the generated tools = %+v %v", plain, err)
	}
	on, onSecret, err := svc.CreateMCPToken(t.Context(), owner, "tools", "", 0, nil, DeployRun, nil, true)
	if err != nil || !on.Operations {
		t.Fatalf("a key made with them = %+v %v", on, err)
	}
	for key, want := range map[string]bool{secret: false, onSecret: true} {
		if resolved, _, _, err := svc.ResolveAPIToken(t.Context(), key); err != nil || resolved.Operations != want {
			t.Errorf("resolved = %+v %v, want operations %v", resolved, err, want)
		}
	}
	if kept, err := svc.SetTokenConnections(t.Context(), owner, on.ID, nil, nil, nil, nil); err != nil || !kept.Operations {
		t.Errorf("re-pointed without the flag = %+v %v", kept, err)
	}
	off := false
	if changed, err := svc.SetTokenConnections(t.Context(), owner, on.ID, nil, nil, nil, &off); err != nil || changed.Operations {
		t.Errorf("turned off = %+v %v", changed, err)
	}
	if _, err := svc.st.DB.Exec(`UPDATE api_tokens SET mcp_operations = 0`); err != nil {
		t.Fatal(err)
	}
	listed, err := svc.ListAPITokens(t.Context(), owner.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range listed {
		if k.Operations {
			t.Errorf("%s lists the generated tools on", k.Name)
		}
	}
}
