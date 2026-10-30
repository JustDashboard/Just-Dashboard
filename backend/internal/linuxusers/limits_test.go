package linuxusers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLimitsFile(t *testing.T) {
	var out AccountLimits
	parseLimitsFile("# Written by Just Dashboard\n[Slice]\nCPUQuota=150%\nMemoryMax=2147483648\nTasksMax=512\n", &out)
	if out.CPUPercent != 150 || out.MemoryBytes != 2147483648 || out.Tasks != 512 {
		t.Fatalf("got %+v", out)
	}
	var empty AccountLimits
	parseLimitsFile("[Slice]\nCPUQuota=lots\n", &empty)
	if empty.CPUPercent != 0 {
		t.Fatalf("got %+v", empty)
	}
}

func TestParseQuotaReport(t *testing.T) {
	report := `Disk quotas for user ion (uid 1000):
     Filesystem   Mount   space   quota   limit   grace   files   quota   limit   grace
      /dev/sda1   /home   102400*  51200  204800          1200       0       0
      /dev/sdb1   /data   10      20      30              1          0       0
`
	got := parseQuotaReport(report, "/home")
	if got.UsedBytes != 102400<<10 || got.SoftBytes != 51200<<10 || got.HardBytes != 204800<<10 {
		t.Fatalf("got %+v", got)
	}
	if got := parseQuotaReport("nonsense\n", "/home"); got.HardBytes != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestHasQuotaOption(t *testing.T) {
	for options, want := range map[string]bool{
		"rw,relatime":                          false,
		"rw,usrquota,grpquota":                 true,
		"rw,usrjquota=aquota.user,jqfmt=vfsv1": true,
		"rw,uquota":                            true,
		"rw,grpquota":                          false,
	} {
		if got := hasQuotaOption(options); got != want {
			t.Errorf("%s = %v", options, got)
		}
	}
}

func limitsHost(t *testing.T) (*Service, *fakeHost, string) {
	t.Helper()
	return fixtureHost(t, map[string]string{
		"passwd": "root:x:0:0:root:/root:/bin/bash\nion:x:1000:1000::/home/ion:/bin/bash\n",
	})
}

func TestLimitsAreADropInOnTheAccountsSlice(t *testing.T) {
	s, fake, etc := limitsHost(t)
	ctx := context.Background()
	if err := s.SetLimits(ctx, "ion", AccountLimits{CPUPercent: 150, MemoryBytes: 1 << 30, Tasks: 512}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(etc, "systemd", "system", "user-1000.slice.d", limitsDropIn)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[Slice]", "CPUQuota=150%", "MemoryMax=1073741824", "TasksMax=512"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("drop-in = %q", b)
		}
	}
	if call := fake.lastCall("systemctl"); call == nil || call.args[0] != "daemon-reload" {
		t.Fatalf("systemctl = %+v", call)
	}
	got, err := s.Limits(ctx, "ion")
	if err != nil {
		t.Fatal(err)
	}
	if got.CPUPercent != 150 || got.MemoryBytes != 1<<30 || got.Tasks != 512 || !got.Systemd {
		t.Fatalf("read back = %+v", got)
	}
	if err := s.SetLimits(ctx, "ion", AccountLimits{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("no limits should mean no drop-in")
	}
	if _, err := os.Stat(filepath.Dir(path)); err == nil {
		t.Fatal("the drop-in's directory stayed")
	}
}

func TestALimitTooLowForALoginIsRefused(t *testing.T) {
	s, _, etc := limitsHost(t)
	ctx := context.Background()
	for name, limits := range map[string]AccountLimits{
		"memory":   {MemoryBytes: 1 << 20},
		"tasks":    {Tasks: 2},
		"cpu":      {CPUPercent: 1},
		"negative": {CPUPercent: -1},
	} {
		if err := s.SetLimits(ctx, "ion", limits); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if err := s.SetLimits(ctx, "root", AccountLimits{CPUPercent: 50}); !errors.Is(err, ErrProtected) {
		t.Fatalf("root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(etc, "systemd")); err == nil {
		t.Fatal("wrote something")
	}
}

func TestAQuotaIsOnlyOfferedOnAMountThatHasThem(t *testing.T) {
	s, fake, _ := limitsHost(t)
	ctx := context.Background()
	fake.answer = func(_, name string, _ ...string) (string, error) {
		if name == "findmnt" {
			return "/ rw,relatime\n", nil
		}
		return "", nil
	}
	got, err := s.Limits(ctx, "ion")
	if err != nil || got.Quota != nil {
		t.Fatalf("got %+v, %v", got, err)
	}
	if err := s.SetQuota(ctx, "ion", 1<<30, 2<<30); err == nil {
		t.Fatal("set a quota on a mount without them")
	}
	if fake.ran("setquota") {
		t.Fatal("setquota was asked")
	}
	fake.answer = func(_, name string, _ ...string) (string, error) {
		switch name {
		case "findmnt":
			return "/home rw,usrquota\n", nil
		case "quota":
			return "Disk quotas for user ion (uid 1000):\n Filesystem Mount space quota limit grace files quota limit grace\n /dev/sda1 /home 100 200 300 5 0 0\n", nil
		}
		return "", nil
	}
	got, err = s.Limits(ctx, "ion")
	if err != nil || got.Quota == nil || got.Quota.Mount != "/home" || got.Quota.HardBytes != 300<<10 {
		t.Fatalf("got %+v, %v", got.Quota, err)
	}
	if err := s.SetQuota(ctx, "ion", 1<<30, 2<<30); err != nil {
		t.Fatal(err)
	}
	if call := fake.lastCall("setquota"); call == nil || strings.Join(call.args, " ") != "-u ion 1048576 2097152 0 0 /home" {
		t.Fatalf("setquota = %+v", call)
	}
	if err := s.SetQuota(ctx, "ion", 2<<30, 1<<30); err == nil {
		t.Fatal("soft above hard was accepted")
	}
}

func TestALimitSystemdDidNotTakeIsNotKept(t *testing.T) {
	s, fake, etc := limitsHost(t)
	ctx := context.Background()
	path := filepath.Join(etc, "systemd", "system", "user-1000.slice.d", limitsDropIn)
	fake.answer = func(_, name string, _ ...string) (string, error) {
		if name == "systemctl" {
			return "", errors.New("System has not been booted with systemd")
		}
		return "", nil
	}
	if err := s.SetLimits(ctx, "ion", AccountLimits{Tasks: 64}); err == nil {
		t.Fatal("accepted")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("the file stayed")
	}
	fake.answer = nil
	if err := s.SetLimits(ctx, "ion", AccountLimits{Tasks: 64}); err != nil {
		t.Fatal(err)
	}
	fake.answer = func(_, name string, _ ...string) (string, error) {
		if name == "systemctl" {
			return "", errors.New("no")
		}
		return "", nil
	}
	if err := s.SetLimits(ctx, "ion", AccountLimits{Tasks: 128}); err == nil {
		t.Fatal("accepted")
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "TasksMax=64") {
		t.Fatalf("the old limit was not put back: %q", b)
	}
}
