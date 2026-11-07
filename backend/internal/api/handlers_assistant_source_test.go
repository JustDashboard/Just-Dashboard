package api

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/assistant"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// Asked what a project is, the assistant reads its code: read_source answers with
// the README of the commit a deployment would build, masked as a log is, and a
// conversation about one deployment reads another's by its id.
func TestTheAssistantReadsADeploymentsSource(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	const secret = "amber-harbour-lantern-52"
	checkout := t.TempDir()
	if err := os.WriteFile(filepath.Join(checkout, "README.md"), []byte("# Mugs\n\nA shop for hand-made mugs. Its token is "+secret+".\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		command := exec.Command("git", append([]string{"-C", checkout}, args...)...)
		command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "source-test@example.test")
	git("config", "user.name", "Source Test")
	git("add", ".")
	git("commit", "-q", "-m", "fixture")
	revision := git("rev-parse", "HEAD")
	f.s.modules.deployChecker = deploy.NewDeploymentChecker(f.s.modules.deployRuns, f.s.modules.deployPlanning,
		deploy.NewHostSourceAnalyzer([]string{checkout}, []string{checkout}, filepath.Join(f.s.Cfg.DataDir, "source-test-cache"), nil, f.s.modules.deployPlanning), nil)
	mugs, mugsEnvironment := seedCommittedDeployment(t, f.s, "mugs",
		deploy.DraftSourceConfig{Kind: deploy.SourceLocal, Mode: deploy.SourceModeLocalCheckout, LocalPath: checkout},
		deploy.SourceIdentity{Kind: deploy.SourceLocal, LocalPath: checkout, Revision: revision})
	variable := fmt.Sprintf("/deploy/%d/environments/%d/variables/SHOP_TOKEN", mugs, mugsEnvironment)
	if rec := f.do(http.MethodPut, variable, fmt.Sprintf(`{"revision":1,"value":%q,"sensitivity":"secret","scopes":["runtime"]}`, secret)); rec.Code != http.StatusOK {
		t.Fatalf("variable = %d %s", rec.Code, rec.Body.String())
	}

	f.provider.queue(
		claudeCalls("", [2]any{assistant.ToolReadSource, map[string]any{"deployment": mugs}}),
		claudeAnswer("It is a shop for hand-made mugs."),
	)
	events := f.ask(t, `{"question":"What is the mugs project about?","mode":"changes"}`)
	call := ofType(events, "call")
	called := ofType(events, "called")
	if len(call) != 1 || len(called) != 1 || called[0]["ok"] != true || called[0]["said"] != "README.md, 1 folder" {
		t.Fatalf("events = %v", events)
	}
	if view := call[0]["call"].(map[string]any); view["kind"] != "read" || view["label"] != "Read the repository's README and top folder" {
		t.Fatalf("call = %v", view)
	}
	sent := f.provider.raw[1]
	if !strings.Contains(sent, "hand-made mugs") || !strings.Contains(sent, revision) || strings.Contains(sent, secret) {
		t.Fatalf("the source reached the model as %s", sent)
	}
}
