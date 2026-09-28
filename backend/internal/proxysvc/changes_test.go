package proxysvc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type changeLog struct {
	changes []Change
	err     error
}

func (l *changeLog) Record(_ context.Context, c Change) error {
	l.changes = append(l.changes, c)
	return l.err
}

// recordingService is a Debian-layout nginx directory behind an nginx that
// accepts everything, with a recorder attached.
func recordingService(t *testing.T) (*Service, *changeLog, string) {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"sites-available", "sites-enabled", "bin"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "nginx"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	// allowedPath resolves symlinks, so the recorded paths are the resolved
	// ones; a temporary directory behind a symlink must not fail the test.
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	service := New(resolved, filepath.Join(resolved, "Caddyfile"))
	log := &changeLog{}
	service.SetRecorder(log)
	return service, log, resolved
}

func TestRecorderReceivesPriorContent(t *testing.T) {
	service, log, root := recordingService(t)
	ctx := WithActor(context.Background(), "operator")

	existing := filepath.Join(root, "sites-available", "legacy")
	if err := os.WriteFile(existing, []byte("server { listen 80; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := service.WriteConfig(ctx, KindNginx, existing, "server { listen 8080; }\n"); err != nil {
		t.Fatal(err)
	}
	spec := proxySpec()
	res, err := service.ApplySite(ctx, spec, true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	site := filepath.Join(root, "sites-available", spec.Name)
	if err := service.SetVHostEnabled(ctx, spec.Name, false); err != nil {
		t.Fatal(err)
	}
	if err := service.SetVHostEnabled(ctx, spec.Name, true); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteSite(ctx, spec.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyStream(ctx, tcpStream(), "", false); err != nil {
		t.Fatal(err)
	}
	stream := filepath.Join(service.streamDir(), tcpStream().Name+".conf")
	streamContent, err := os.ReadFile(stream)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.DeleteStream(ctx, tcpStream().Name); err != nil {
		t.Fatal(err)
	}

	rendered := []byte(res.Content)
	want := []Change{
		{Path: existing, Action: ChangeWrite, Actor: "operator",
			Before: []byte("server { listen 80; }\n"), BeforeExisted: true, After: []byte("server { listen 8080; }\n")},
		{Path: site, Action: ChangeWrite, Actor: "operator", Before: []byte{}, After: rendered},
		{Path: site, Action: ChangeDisable, Actor: "operator", Before: rendered, BeforeExisted: true, After: rendered},
		{Path: site, Action: ChangeEnable, Actor: "operator", Before: rendered, BeforeExisted: true, After: rendered},
		{Path: site, Action: ChangeDelete, Actor: "operator", Before: rendered, BeforeExisted: true},
		{Path: stream, Action: ChangeWrite, Actor: "operator", Before: []byte{}, After: streamContent},
		{Path: stream, Action: ChangeDelete, Actor: "operator", Before: streamContent, BeforeExisted: true},
	}
	if !reflect.DeepEqual(log.changes, want) {
		t.Fatalf("recorded\n%s\nwant\n%s", describeChanges(log.changes), describeChanges(want))
	}
}

// A change nobody asked for through the API — a deployment cutover — is
// recorded with no actor rather than borrowing one.
func TestRecorderLeavesTheActorEmptyWithoutOne(t *testing.T) {
	service, log, _ := recordingService(t)
	if _, err := service.ApplySite(context.Background(), proxySpec(), true, false, false); err != nil {
		t.Fatal(err)
	}
	if len(log.changes) != 1 || log.changes[0].Actor != "" {
		t.Fatalf("got %s", describeChanges(log.changes))
	}
}

func TestRecorderSkipsPasswordFiles(t *testing.T) {
	service, log, root := recordingService(t)
	ctx := WithActor(context.Background(), "operator")
	if _, err := service.WriteConfig(ctx, KindNginx, filepath.Join(root, ".htpasswd"), "operator:$2y$10$x\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetAuthUser("team", "operator", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.WriteConfig(ctx, KindNginx, filepath.Join(service.authDir(), "team"), "operator:$2y$10$y\n"); err != nil {
		t.Fatal(err)
	}
	// A site whose file is a link to a password file: toggling it reads the
	// file, and must not hand what it read to the recorder.
	if err := os.Symlink(filepath.Join(service.authDir(), "team"), filepath.Join(root, "sites-available", "app")); err != nil {
		t.Fatal(err)
	}
	if err := service.SetVHostEnabled(ctx, "app", true); err != nil {
		t.Fatal(err)
	}
	if err := service.SetVHostEnabled(ctx, "app", false); err != nil {
		t.Fatal(err)
	}
	if len(log.changes) != 0 {
		t.Fatalf("password material was recorded: %s", describeChanges(log.changes))
	}
}

// A site file that links out of the proxy's directories is one ReadConfig
// refuses to show, so its toggle is recorded without what the file says.
func TestRecorderLeavesOutAFileOutsideTheProxyDirectory(t *testing.T) {
	service, log, root := recordingService(t)
	ctx := WithActor(context.Background(), "operator")
	outside := filepath.Join(t.TempDir(), "app.conf")
	if err := os.WriteFile(outside, []byte("server { proxy_set_header X-Api-Key s3cr3t; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	site := filepath.Join(root, "sites-available", "app")
	if err := os.Symlink(outside, site); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReadConfig(site); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("ReadConfig showed a file outside the proxy directory: %v", err)
	}
	if err := service.SetVHostEnabled(ctx, "app", true); err != nil {
		t.Fatal(err)
	}
	if err := service.SetVHostEnabled(ctx, "app", false); err != nil {
		t.Fatal(err)
	}
	want := []Change{
		{Path: site, Action: ChangeEnable, Actor: "operator", BeforeExisted: true},
		{Path: site, Action: ChangeDisable, Actor: "operator", BeforeExisted: true},
	}
	if !reflect.DeepEqual(log.changes, want) {
		t.Fatalf("recorded\n%s\nwant\n%s", describeChanges(log.changes), describeChanges(want))
	}
}

// Switching a site to the state it is already in changes nothing on disk, in
// either direction, so the history must not say it did.
func TestRecorderSkipsAToggleThatChangesNothing(t *testing.T) {
	service, log, _ := recordingService(t)
	ctx := WithActor(context.Background(), "operator")
	spec := proxySpec()
	if _, err := service.ApplySite(ctx, spec, true, false, false); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{true, true, false, false} {
		if err := service.SetVHostEnabled(ctx, spec.Name, enabled); err != nil {
			t.Fatal(err)
		}
	}
	var actions []ChangeAction
	for _, c := range log.changes {
		actions = append(actions, c.Action)
	}
	if want := []ChangeAction{ChangeWrite, ChangeDisable}; !reflect.DeepEqual(actions, want) {
		t.Fatalf("recorded %v, want %v", actions, want)
	}
}

func TestRecorderFailureDoesNotFailTheWrite(t *testing.T) {
	service, log, root := recordingService(t)
	log.err = errors.New("disk full")
	path := filepath.Join(root, "sites-available", "app")
	if _, err := service.WriteConfig(context.Background(), KindNginx, path, "server {}\n"); err != nil {
		t.Fatalf("a recorder failure failed the save: %v", err)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "server {}\n" {
		t.Fatalf("the file was not written: %q %v", b, err)
	}
	if len(log.changes) != 1 {
		t.Fatalf("the recorder was not asked: %s", describeChanges(log.changes))
	}
}

func describeChanges(changes []Change) string {
	out := ""
	for _, c := range changes {
		out += "  " + string(c.Action) + " " + c.Path + " by " + c.Actor +
			" before=" + string(c.Before) + " after=" + string(c.After) + "\n"
	}
	return out
}
