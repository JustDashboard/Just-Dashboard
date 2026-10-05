package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
)

func TestComposeAdoptionContainsEveryEnvironmentFileBeforeResolution(t *testing.T) {
	for _, name := range []string{"optional outside", "optional symlink", "required outside", "aliased service", "merged service", "aliased file", "merged extends", "merged include", "seventeenth file", "optional pipe", "optional dynamic", "optional missing", "optional contained", "safe alias", "seventeen contained files", "null override"} {
		t.Run(name, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			private := filepath.Join(outside, "private.env")
			if err := os.WriteFile(private, []byte("TOKEN=unrelated-private-value\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "local.env"), []byte("PORT=3000\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(private, filepath.Join(root, "escape.env")); err != nil {
				t.Fatal(err)
			}
			content := ""
			blocked := true
			switch name {
			case "optional outside":
				content = fmt.Sprintf("services:\n  web:\n    env_file: [{path: %q, required: false}]\n", private)
			case "optional symlink":
				content = "services:\n  web:\n    env_file: [{path: escape.env, required: false}]\n"
			case "required outside":
				content = fmt.Sprintf("services:\n  web:\n    env_file: [%q]\n", private)
			case "aliased service":
				content = fmt.Sprintf("x-app: &app\n  env_file: [%q]\nservices:\n  web: *app\n", private)
			case "merged service":
				content = fmt.Sprintf("x-app: &app\n  env_file: [%q]\nservices:\n  web:\n    <<: *app\n", private)
			case "aliased file":
				content = fmt.Sprintf("x-env: &env %q\nservices:\n  web:\n    env_file: [*env]\n", private)
			case "merged extends":
				content = fmt.Sprintf("x-app: &app\n  extends: {file: %q, service: external}\nservices:\n  web:\n    <<: *app\n", private)
			case "merged include":
				content = fmt.Sprintf("x-project: &project\n  include: [%q]\n<<: *project\nservices:\n  web: {image: example/web:latest}\n", private)
			case "seventeenth file":
				content = "services:\n  web:\n    env_file:\n" + strings.Repeat("      - local.env\n", 16) + fmt.Sprintf("      - %q\n", private)
			case "optional pipe":
				if err := syscall.Mkfifo(filepath.Join(root, "pipe.env"), 0600); err != nil {
					t.Fatal(err)
				}
				content = "services:\n  web:\n    env_file: [{path: pipe.env, required: false}]\n"
			case "optional dynamic":
				content = "services:\n  web:\n    env_file: [{path: '${ENV_FILE}', required: false}]\n"
			case "optional missing":
				content = "services:\n  web:\n    env_file: [{path: missing.env, required: false}]\n"
				blocked = false
			case "optional contained":
				content = "services:\n  web:\n    env_file: [{path: local.env, required: false}]\n"
				blocked = false
			case "safe alias":
				content = "x-app: &app\n  env_file: [local.env]\nservices:\n  web:\n    <<: *app\n"
				blocked = false
			case "seventeen contained files":
				content = "services:\n  web:\n    env_file:\n" + strings.Repeat("      - local.env\n", 17)
				blocked = false
			case "null override":
				content = "services:\n  web:\n    env_file: null\n"
				blocked = false
			}
			source := filepath.Join(root, "compose.yml")
			if err := os.WriteFile(source, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			capture := adoptionCaptureFixture(t, "web", true)
			capture.Inspection.Config.Labels = map[string]string{"com.docker.compose.project": "original", "com.docker.compose.service": "web", "com.docker.compose.container-number": "1", "com.docker.compose.project.working_dir": root, "com.docker.compose.project.config_files": source}
			reader := &adoptionReaderFake{captures: map[string]*dockerx.AdoptionContainer{capture.Inspection.ID: capture}, compose: []byte(`{"services":{"web":{"image":"example/web:latest"}}}`)}
			candidate := WorkloadCandidate{Key: "stack:original", Kind: "stack", Name: "original", ResourceID: "original", Total: 1, Running: 1, Services: []WorkloadService{{Name: "web", ResourceID: capture.Inspection.ID}}}
			result, err := RecoverDockerWorkload(context.Background(), candidate, reader, files.New([]string{root}), filepath.Join(root, "recovery"))
			if blocked {
				if !errors.Is(err, ErrRecoveryBlocked) || reader.reads != 0 {
					t.Fatalf("uncontained input reached Compose: error=%v reads=%d", err, reader.reads)
				}
				if _, err := os.Stat(filepath.Join(root, "recovery")); !os.IsNotExist(err) {
					t.Fatal("blocked recovery staged a runtime recipe")
				}
			} else if err != nil || reader.reads != 1 {
				t.Fatalf("contained input rejected: error=%v reads=%d blockers=%v", err, reader.reads, result.Adoption.Blockers)
			}
		})
	}
}

func TestComposeAdoptionRefusesSpecialSourceBeforeReading(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "compose.yml")
	if err := syscall.Mkfifo(source, 0600); err != nil {
		t.Fatal(err)
	}
	// No writer opens this pipe. Reading it with os.Open would hang instead
	// of returning the actionable recovery refusal.
	recovery := &dockerRecovery{paths: files.New([]string{root})}
	if err := recovery.checkOriginalReferences(source, root); err == nil {
		t.Fatal("special Compose source accepted")
	}
}
