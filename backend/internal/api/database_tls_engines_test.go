package api

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A reload runs in the database's container, whose processes are the host's:
// any account on the host can read their argv. Each profile's reload is run
// here through a real /bin/sh against stand-in clients that write down the
// argv and environment they were started with, and the root password the
// image holds must reach the client only through its environment.
func TestTLSReloadsHandTheRootPasswordOverInTheEnvironment(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	const password = "Root-sentinel-Password"
	bin := t.TempDir()
	stub := "#!/bin/sh\n" +
		`{ printf 'argv'; for a in "$0" "$@"; do printf ' [%s]' "$a"; done; printf '\n'; env; } > "$JD_STUB_OUT"` + "\n" +
		// redis-cli is asked whether the reload took.
		"echo OK\n"
	for _, client := range []string{"mysql", "mariadb", "mongosh", "psql", "redis-cli", "valkey-cli"} {
		if err := os.WriteFile(filepath.Join(bin, client), []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// What the client must find in its environment, by profile; empty for
	// one that signs in by the container's own trust.
	handedOver := map[string]string{
		"mysql": "MYSQL_PWD=" + password, "mariadb": "MYSQL_PWD=" + password,
		"redis": "REDISCLI_AUTH=" + password, "valkey": "REDISCLI_AUTH=" + password,
		"mongodb": "MONGO_INITDB_ROOT_PASSWORD=" + password,
	}
	for key, engine := range dbTLSEngines {
		out := filepath.Join(t.TempDir(), "seen")
		cmd := exec.Command("/bin/sh", "-c", engine.reload)
		cmd.Env = []string{
			"PATH=" + bin + ":/usr/bin:/bin", "JD_STUB_OUT=" + out,
			"MYSQL_ROOT_PASSWORD=" + password, "MARIADB_ROOT_PASSWORD=" + password,
			"MONGO_INITDB_ROOT_USERNAME=root", "MONGO_INITDB_ROOT_PASSWORD=" + password,
			"REDIS_PASSWORD=" + password, "POSTGRES_USER=postgres",
		}
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: the reload did not run: %v %s", key, err, output)
		}
		raw, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("%s: the reload started no client: %v", key, err)
		}
		argv, environment, _ := strings.Cut(string(raw), "\n")
		if strings.Contains(argv, password) {
			t.Errorf("%s: the root password is an argument: %s", key, argv)
		}
		if want := handedOver[key]; want != "" && !slicesContainsLine(environment, want) {
			t.Errorf("%s: the client was not given %s in its environment", key, strings.SplitN(want, "=", 2)[0])
		}
		if key == "mongodb" && !strings.Contains(argv, "process.env.MONGO_INITDB_ROOT_PASSWORD") {
			t.Errorf("mongodb: the script does not sign in from the environment: %s", argv)
		}
	}
}

func slicesContainsLine(text, line string) bool {
	for _, got := range strings.Split(text, "\n") {
		if got == line {
			return true
		}
	}
	return false
}
