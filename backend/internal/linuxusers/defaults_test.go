package linuxusers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const loginDefsFixture = `#
# /etc/login.defs - Configuration control definitions for the login package.
#
MAIL_DIR        /var/mail
#PASS_MAX_DAYS  1
PASS_MAX_DAYS	99999
PASS_MIN_DAYS	0
PASS_WARN_AGE	7
UID_MIN                  1000
`

const pwqualityFixture = `# Configuration for systemwide password quality limits
#
# minlen = 8
# minclass = 0
retry = 3
`

func defaultsHost(t *testing.T) (*Service, string) {
	t.Helper()
	s, _, etc := fixtureHost(t, map[string]string{
		"login.defs":              loginDefsFixture,
		"default/useradd":         "# useradd defaults file\nGROUP=100\nSHELL=/bin/sh\nINACTIVE=-1\nSKEL=/etc/skel\n",
		"security/pwquality.conf": pwqualityFixture,
		"skel/.bashrc":            "# bashrc\n",
		"skel/.profile":           "# profile\n",
		"skel/.config/x":          "x",
		"shells":                  "/bin/sh\n/bin/bash\n",
	})
	return s, etc
}

func TestDefaultsAreReadFromTheirThreeFiles(t *testing.T) {
	s, _ := defaultsHost(t)
	got := s.Defaults()
	if got.PasswordMaxDays != -1 || got.PasswordMinDays != 0 || got.PasswordWarnDays != 7 {
		t.Fatalf("login.defs: %+v", got)
	}
	if got.Shell != "/bin/sh" || got.InactiveDays != -1 {
		t.Fatalf("useradd: %+v", got)
	}
	if !got.Quality.Available || got.Quality.MinLength != 8 || got.Quality.MinClasses != 0 {
		t.Fatalf("pwquality: %+v", got.Quality)
	}
	names := []string{}
	for _, file := range got.Skel {
		names = append(names, file.Name)
	}
	if strings.Join(names, " ") != ".bashrc .config .profile" {
		t.Fatalf("skel = %v", names)
	}
	if len(got.Shells) != 2 {
		t.Fatalf("shells = %v", got.Shells)
	}
}

func TestAHostWithoutTheFilesReadsAsTheToolsOwnDefaults(t *testing.T) {
	s, _, _ := fixtureHost(t, map[string]string{})
	got := s.Defaults()
	if got.PasswordMaxDays != -1 || got.PasswordWarnDays != 7 || got.Quality.Available || len(got.Skel) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestEditingOneKeyKeepsEveryOtherLineAndComment(t *testing.T) {
	s, etc := defaultsHost(t)
	maxDays, warn, minLength := 90, 14, 12
	shell := "/bin/bash"
	if err := s.SetDefaults(DefaultsChange{PasswordMaxDays: &maxDays, PasswordWarnDays: &warn, Shell: &shell, MinLength: &minLength}); err != nil {
		t.Fatal(err)
	}
	login, _ := os.ReadFile(filepath.Join(etc, "login.defs"))
	want := strings.NewReplacer("PASS_MAX_DAYS\t99999", "PASS_MAX_DAYS\t90", "PASS_WARN_AGE\t7", "PASS_WARN_AGE\t14").Replace(loginDefsFixture)
	if string(login) != want {
		t.Fatalf("login.defs:\n%s\nwant:\n%s", login, want)
	}
	useradd, _ := os.ReadFile(filepath.Join(etc, "default", "useradd"))
	if !strings.Contains(string(useradd), "SHELL=/bin/bash\n") || !strings.Contains(string(useradd), "# useradd defaults file\nGROUP=100\n") {
		t.Fatalf("useradd = %q", useradd)
	}
	quality, _ := os.ReadFile(filepath.Join(etc, "security", "pwquality.conf"))
	// A commented-out default stays the comment it was, and the live line is added.
	if !strings.HasPrefix(string(quality), pwqualityFixture) || !strings.HasSuffix(string(quality), "minlen = 12\n") {
		t.Fatalf("pwquality = %q", quality)
	}
	got := s.Defaults()
	if got.PasswordMaxDays != 90 || got.PasswordWarnDays != 14 || got.Shell != "/bin/bash" || got.Quality.MinLength != 12 {
		t.Fatalf("read back = %+v", got)
	}
}

func TestNeverIsWrittenAsTheNumberTheFileUses(t *testing.T) {
	s, etc := defaultsHost(t)
	never := -1
	if err := s.SetDefaults(DefaultsChange{PasswordMaxDays: &never}); err != nil {
		t.Fatal(err)
	}
	login, _ := os.ReadFile(filepath.Join(etc, "login.defs"))
	if !strings.Contains(string(login), "PASS_MAX_DAYS\t99999") {
		t.Fatalf("login.defs = %q", login)
	}
}

func TestEditingKeepsTheFilesMode(t *testing.T) {
	s, etc := defaultsHost(t)
	path := filepath.Join(etc, "login.defs")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	days := 30
	if err := s.SetDefaults(DefaultsChange{PasswordMinDays: &days}); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode())
	}
	entries, _ := os.ReadDir(etc)
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".jd-") {
			t.Fatalf("left %s behind", entry.Name())
		}
	}
}

func TestDefaultsRefuseWhatTheyCannotMean(t *testing.T) {
	s, _ := defaultsHost(t)
	tooMany, tooShort, classes := 100000, 2, 9
	bad := "/bin/nope"
	for name, change := range map[string]DefaultsChange{
		"days":    {PasswordMaxDays: &tooMany},
		"length":  {MinLength: &tooShort},
		"classes": {MinClasses: &classes},
		"shell":   {Shell: &bad},
	} {
		if err := s.SetDefaults(change); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	// pwquality.conf is only there on a host that reads it.
	s, _, _ = fixtureHost(t, map[string]string{"login.defs": loginDefsFixture})
	length := 12
	if err := s.SetDefaults(DefaultsChange{MinLength: &length}); err == nil {
		t.Fatal("wrote a quality rule nothing would read")
	}
}
