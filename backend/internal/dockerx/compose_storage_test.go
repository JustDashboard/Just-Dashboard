package dockerx

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestComposePersistentSourcesIncludesEveryServiceAndResolvedVolumeIdentity(t *testing.T) {
	sources, err := composePersistentSources([]byte(`{
		"services": {
			"web": {"volumes": [{"type":"bind","source":"/srv/uploads"}, {"type":"bind","source":"/etc/app","read_only":true}]},
			"db": {"volumes": [{"type":"volume","source":"db"}, {"type":"volume","source":"shared"}]},
			"worker": {"volumes": [{"type":"volume","source":"shared"}, {"type":"tmpfs","target":"/tmp"}]}
		},
		"volumes": {"db":{"name":"jd-e42_db"},"shared":{"name":"existing-external-data"}}
	}`))
	want := []string{"/srv/uploads", "existing-external-data", "jd-e42_db"}
	if err != nil || !reflect.DeepEqual(sources, want) {
		t.Fatalf("persistent sources = %v, %v; want %v", sources, err, want)
	}
}

func TestLiveDeploymentComposeStorageUsesMergedMountsAndFrozenVariables(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to use the real Docker Compose parser")
	}
	dir := t.TempDir()
	for name, body := range map[string]string{
		"compose.yml": `services:
  web:
    image: nginx:alpine
    volumes:
      - ./uploads:/uploads
  db:
    image: postgres:16
    environment:
      POSTGRES_PASSWORD: ${DB_PASSWORD}
    volumes:
      - db:/var/lib/postgresql/data
volumes:
  db: {}
  extra:
    external: true
    name: ${EXTERNAL_NAME}
`,
		"override.yml": `services:
  web:
    volumes:
      - extra:/extra
`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := New("").ComposePersistentSources(t.Context(), ComposeReleaseSpec{
		ProjectName: "jd-storage-fixture", ProjectDirectory: dir, Files: []string{"compose.yml", "override.yml"},
		Environment: map[string]string{"DB_PASSWORD": "private-fixture-value", "EXTERNAL_NAME": "resolved-external"},
	})
	want := []string{filepath.Join(dir, "uploads"), "jd-storage-fixture_db", "resolved-external"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("real Compose storage = %v, %v; want %v", got, err, want)
	}
	leaks, err := filepath.Glob(filepath.Join(dir, ".just-dashboard-env-*"))
	if err != nil || len(leaks) != 0 {
		t.Fatalf("private Compose env files remain: %v, %v", leaks, err)
	}
}

func TestComposePersistentSourcesFailsClosedForUnknownStorage(t *testing.T) {
	for _, mount := range []string{
		`{"type":"volume","target":"/data"}`,
		`{"type":"volume","source":"unresolved"}`,
		`{"type":"bind","source":"./unresolved"}`,
		`{"type":"cluster","source":"unknown"}`,
	} {
		_, err := composePersistentSources([]byte(`{"services":{"app":{"volumes":[` + mount + `]}}}`))
		if err == nil {
			t.Fatalf("unresolved storage was ignored: %s", mount)
		}
	}
}
