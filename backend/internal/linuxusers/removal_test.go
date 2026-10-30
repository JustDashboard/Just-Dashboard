package linuxusers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAHomeTooNearTheRootOrSharedIsNeverDeleted(t *testing.T) {
	users := []User{{Username: "ion", Home: "/home/ion"}, {Username: "mira", Home: "/srv/shared"}, {Username: "bob", Home: "/srv/shared/"}}
	for _, c := range []struct {
		home, user string
		unsafe     bool
	}{
		{"/home/ion", "ion", false},
		{"/data/people/x", "x", false},
		{"/", "x", true},
		{"/root", "x", true},
		{"/home", "x", true},
		{"/home/", "x", true},
		{"/var", "x", true},
		{"/var/lib", "x", true},
		{"/usr/sbin/..", "x", true},
		{"/nonexistent", "x", true},
		{"relative/path", "x", true},
		{"/srv/shared", "mira", true},
		{"/home/ion", "other", true},
	} {
		if got := homeIsUnsafe(c.home, c.user, users); got != c.unsafe {
			t.Errorf("%s for %s: %v, want %v", c.home, c.user, got, c.unsafe)
		}
	}
}

func removalHost(t *testing.T) (*Service, *fakeHost, string) {
	t.Helper()
	dir := t.TempDir()
	home := filepath.Join(dir, "people", "ion")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	s, fake, etc := fixtureHost(t, map[string]string{
		"passwd": "root:x:0:0:root:/root:/bin/bash\n" +
			"ion:x:1000:1000::" + home + ":/bin/bash\n" +
			"mira:x:1001:1001::" + filepath.Join(dir, "people", "mira") + ":/bin/bash\n" +
			"wrong:x:1002:1002::/var:/bin/bash\n",
		"group":            "root:x:0:\nion:x:1000:\nmira:x:1001:\nwrong:x:1002:\n",
		"shadow":           "root:$y$x:20000:0:99999:7:::\nion:$y$x:20000:0:99999:7:::\nmira:!:20000:0:99999:7:::\nwrong:!:20000:0:99999:7:::\n",
		"sudoers":          "@includedir sudoers.d\n",
		"sudoers.d/jd-ion": "ion ALL=(ALL:ALL) ALL\n",
		"sudoers.d/other":  "ion, mira ALL=(ALL) /bin/ls\n",
	})
	fake.answer = func(_, name string, _ ...string) (string, error) {
		if strings.HasSuffix(name, "sshd") {
			return "permitrootlogin yes\n", nil
		}
		return "", nil
	}
	return s, fake, etc
}

func TestThePreviewCountsWhatWouldGo(t *testing.T) {
	s, fake, _ := removalHost(t)
	fake.answer = func(_, name string, args ...string) (string, error) {
		switch {
		case strings.HasSuffix(name, "sshd"):
			return "permitrootlogin yes\n", nil
		case name == "du":
			return "4096\t" + args[len(args)-1] + "\n", nil
		case name == "ps":
			return "10 0.0 0.0 5 bash\n11 0.0 0.0 6 sleep\n", nil
		case name == "crontab":
			return "0 1 * * * /bin/true\n", nil
		case name == "find":
			return "/tmp/ion-file\nfind: '/x': Permission denied\n/var/spool/ion\n", nil
		}
		return "", nil
	}
	preview, err := s.PreviewRemoval(context.Background(), "ion")
	if err != nil {
		t.Fatal(err)
	}
	if !preview.HomeExists || preview.HomeBytes != 4096 || preview.HomeUnsafe || preview.Blocked != "" {
		t.Fatalf("preview = %+v", preview)
	}
	if preview.Processes != 2 || !preview.CronJobs {
		t.Fatalf("preview = %+v", preview)
	}
	if len(preview.SudoRules) != 2 {
		t.Fatalf("rules = %+v", preview.SudoRules)
	}
	if strings.Join(preview.Elsewhere, ",") != "/tmp/ion-file,/var/spool/ion" || preview.ElsewhereTruncated {
		t.Fatalf("elsewhere = %+v, %v", preview.Elsewhere, preview.ElsewhereTruncated)
	}
	call := fake.lastCall("find")
	joined := strings.Join(call.args, " ")
	if !strings.Contains(joined, "-uid 1000") || !strings.Contains(joined, "-xdev") || !strings.Contains(joined, "-path "+preview.Home) {
		t.Fatalf("find = %s", joined)
	}
}

func TestThePreviewSaysWhyAnAccountCannotGo(t *testing.T) {
	s, _, _ := removalHost(t)
	preview, err := s.PreviewRemoval(context.Background(), "root")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Blocked == "" {
		t.Fatalf("preview = %+v", preview)
	}
	wrong, err := s.PreviewRemoval(context.Background(), "wrong")
	if err != nil {
		t.Fatal(err)
	}
	if !wrong.HomeUnsafe {
		t.Fatalf("a home of /var: %+v", wrong)
	}
	if _, err := s.PreviewRemoval(context.Background(), "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestDeleteRefusals(t *testing.T) {
	s, fake, _ := removalHost(t)
	ctx := context.Background()
	if _, err := s.Delete(ctx, "root", RemovalOptions{}); !errors.Is(err, ErrProtected) {
		t.Fatalf("root: %v", err)
	}
	if _, err := s.Delete(ctx, "ghost", RemovalOptions{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ghost: %v", err)
	}
	if _, err := s.Delete(ctx, "ion", RemovalOptions{Home: "shred"}); err == nil {
		t.Fatal("an unknown home choice was accepted")
	}
	if _, err := s.Delete(ctx, "wrong", RemovalOptions{Home: HomeDelete}); err == nil ||
		!strings.Contains(err.Error(), "/var") {
		t.Fatalf("a home of /var: %v", err)
	}
	if _, err := s.Delete(ctx, "ion", RemovalOptions{TransferTo: "ion"}); err == nil {
		t.Fatal("an account handed its files to itself")
	}
	if _, err := s.Delete(ctx, "ion", RemovalOptions{TransferTo: "mira", Home: HomeDelete}); err == nil {
		t.Fatal("files were handed over and deleted")
	}
	if fake.ran("userdel") {
		t.Fatal("userdel was asked")
	}
}

func TestDeleteStopsAtRunningProcessesUnlessToldToEndThem(t *testing.T) {
	s, fake, _ := removalHost(t)
	ctx := context.Background()
	fake.answer = func(_, name string, _ ...string) (string, error) {
		switch {
		case strings.HasSuffix(name, "sshd"):
			return "permitrootlogin yes\n", nil
		case name == "ps":
			return "10 0.0 0.0 5 bash\n", nil
		}
		return "", nil
	}
	if _, err := s.Delete(ctx, "ion", RemovalOptions{Home: HomeKeep}); !errors.Is(err, ErrAccountBusy) {
		t.Fatalf("err = %v", err)
	}
	if fake.ran("userdel") {
		t.Fatal("userdel was asked")
	}
	if _, err := s.Delete(ctx, "ion", RemovalOptions{Home: HomeKeep, EndSessions: true}); err != nil {
		t.Fatal(err)
	}
	if !fake.ran("pkill") || !fake.ran("userdel") {
		t.Fatalf("calls = %+v", fake.calls)
	}
}

func TestDeletingTakesTheAccountsRulesCrontabAndLimitsWithIt(t *testing.T) {
	s, fake, etc := removalHost(t)
	ctx := context.Background()
	limits := s.limitsFile(1000)
	if err := os.MkdirAll(filepath.Dir(limits), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(limits, []byte("[Slice]\nCPUQuota=50%\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Delete(ctx, "ion", RemovalOptions{Home: HomeKeep}); err != nil {
		t.Fatal(err)
	}
	if call := fake.lastCall("userdel"); call == nil || strings.Join(call.args, " ") != "ion" {
		t.Fatalf("userdel = %+v", call)
	}
	if _, err := os.Stat(filepath.Join(etc, "sudoers.d", "jd-ion")); err == nil {
		t.Fatal("the managed rule stayed")
	}
	b, _ := os.ReadFile(filepath.Join(etc, "sudoers.d", "other"))
	if strings.Contains(string(b), "ion") || !strings.Contains(string(b), "mira ALL=(ALL) /bin/ls") {
		t.Fatalf("other = %q", b)
	}
	if call := fake.lastCall("crontab"); call == nil || strings.Join(call.args, " ") != "-r -u ion" {
		t.Fatalf("crontab = %+v", call)
	}
	if _, err := os.Stat(limits); err == nil {
		t.Fatal("the limit stayed, to land on whoever next gets the number")
	}
}

func TestDeletingWithTheHomeAsksUserdelToRemoveIt(t *testing.T) {
	s, fake, _ := removalHost(t)
	if _, err := s.Delete(context.Background(), "ion", RemovalOptions{Home: HomeDelete}); err != nil {
		t.Fatal(err)
	}
	if call := fake.lastCall("userdel"); strings.Join(call.args, " ") != "--remove ion" {
		t.Fatalf("userdel = %+v", call)
	}
}

func TestAFailedArchiveDeletesNothing(t *testing.T) {
	s, fake, _ := removalHost(t)
	inner := fake.answer
	fake.answer = func(stdin, name string, args ...string) (string, error) {
		if name == "tar" {
			return "", errors.New("tar: no space left on device")
		}
		return inner(stdin, name, args...)
	}
	_, err := s.Delete(context.Background(), "ion", RemovalOptions{Home: HomeArchive})
	if err == nil || !strings.Contains(err.Error(), "nothing was deleted") {
		t.Fatalf("err = %v", err)
	}
	if fake.ran("userdel") {
		t.Fatal("userdel was asked")
	}
	if call := fake.lastCall("rm"); call == nil || !strings.HasPrefix(call.args[len(call.args)-1], archiveRoot) {
		t.Fatalf("the partial archive was left: %+v", call)
	}
}

func TestAnArchiveIsProvedBeforeTheHomeGoes(t *testing.T) {
	s, fake, _ := removalHost(t)
	inner := fake.answer
	s.now = func() time.Time { return fixedNow }
	fake.answer = func(stdin, name string, args ...string) (string, error) {
		if name == "stat" {
			return "4096\n", nil
		}
		return inner(stdin, name, args...)
	}
	result, err := s.Delete(context.Background(), "ion", RemovalOptions{Home: HomeArchive})
	if err != nil {
		t.Fatal(err)
	}
	if result.Archive != archiveRoot+"/ion-20261030-120000.tar.gz" {
		t.Fatalf("archive = %q", result.Archive)
	}
	// The directory is made by a tool, because tar runs on the host and this
	// process's filesystem is a container's.
	if call := fake.lastCall("mkdir"); call == nil || call.args[len(call.args)-1] != archiveRoot {
		t.Fatalf("mkdir = %+v", call)
	}
	tar := fake.lastCall("tar")
	if tar == nil || !strings.Contains(strings.Join(tar.args, " "), "--file "+result.Archive) {
		t.Fatalf("tar = %+v", tar)
	}
	if call := fake.lastCall("chmod"); call == nil || call.args[0] != "0600" {
		t.Fatalf("chmod = %+v", call)
	}
	if call := fake.lastCall("userdel"); strings.Join(call.args, " ") != "--remove ion" {
		t.Fatalf("userdel = %+v", call)
	}

	// An empty archive is no archive, and one that cannot be read back is not proof.
	for _, stat := range []func() (string, error){
		func() (string, error) { return "0\n", nil },
		func() (string, error) { return "", errors.New("stat: no such file") },
	} {
		s, fake, _ = removalHost(t)
		inner = fake.answer
		fake.answer = func(stdin, name string, args ...string) (string, error) {
			if name == "stat" {
				return stat()
			}
			return inner(stdin, name, args...)
		}
		if _, err := s.Delete(context.Background(), "ion", RemovalOptions{Home: HomeArchive}); err == nil {
			t.Fatal("an archive that proves nothing was trusted")
		}
		if fake.ran("userdel") {
			t.Fatal("userdel was asked")
		}
	}
}

func TestHandingOverChownsWithoutFollowingLinks(t *testing.T) {
	s, fake, _ := removalHost(t)
	if _, err := s.Delete(context.Background(), "ion", RemovalOptions{Home: HomeKeep, TransferTo: "mira"}); err != nil {
		t.Fatal(err)
	}
	chown := fake.lastCall("chown")
	if chown == nil || chown.args[0] != "-hR" || chown.args[1] != "--from=1000" || chown.args[2] != "mira:" {
		t.Fatalf("chown = %+v", chown)
	}
	find := fake.lastCall("find")
	joined := strings.Join(find.args, " ")
	if !strings.Contains(joined, "-uid 1000 -execdir chown -h --from=1000 mira: {} +") {
		t.Fatalf("find = %s", joined)
	}
	if !fake.ran("userdel") {
		t.Fatal("userdel was not asked")
	}
}

func TestDeletingTheLastWayInIsRefused(t *testing.T) {
	s, fake, _ := fixtureHost(t, map[string]string{
		"passwd":  "ion:x:1000:1000::/nonexistent:/bin/bash\n",
		"group":   "ion:x:1000:\n",
		"shadow":  "ion:$y$x:20000:0:99999:7:::\n",
		"sudoers": "ion ALL=(ALL) ALL\n",
	})
	if _, err := s.Delete(context.Background(), "ion", RemovalOptions{}); !errors.Is(err, ErrLastWayIn) {
		t.Fatalf("err = %v", err)
	}
	if fake.ran("userdel") {
		t.Fatal("userdel was asked")
	}
	preview, err := s.PreviewRemoval(context.Background(), "ion")
	if err != nil || !strings.Contains(preview.Blocked, "last way in") {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
}
