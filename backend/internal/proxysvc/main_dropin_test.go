package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// includeHost is an nginx directory holding files, the first being
// nginx.conf ($ROOT spelled out), behind an nginx shim: -V prints version
// (nginx.org's build, stream compiled in, unless $root/version says
// otherwise), -T prints $root/dump, -t passes unless $root/fail-test exists and
// a reload fails while $root/fail-reload does. Every run is logged to
// $root/runs.
func includeHost(t *testing.T, files map[string]string) (*Service, string) {
	t.Helper()
	root, _ := nginxLayout(t, files)
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "version"), []byte(alpineNginxV), 0o644); err != nil {
		t.Fatal(err)
	}
	shim := fmt.Sprintf(`#!/bin/sh
echo "$*" >> '%[1]s/runs'
case "$1" in
-V) cat '%[1]s/version' >&2 ;;
-T) cat '%[1]s/dump' 2>/dev/null ;;
-t) if [ -e '%[1]s/fail-test' ]; then echo "nginx: [emerg] test refused" >&2; exit 1; fi ;;
-s) if [ -e '%[1]s/fail-reload' ]; then echo "nginx: [alert] kill(1234, 1) failed (3: No such process)" >&2; exit 1; fi ;;
esac
exit 0
`, root)
	if err := os.WriteFile(filepath.Join(root, "bin", "nginx"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	return New(root, filepath.Join(root, "Caddyfile")), root
}

// debianConf is Ubuntu's nginx.conf in shape: the modules directory included
// at the top level, before events and http.
const debianConf = "user www-data;\npid /run/nginx.pid;\ninclude $ROOT/modules-enabled/*.conf;\n\nevents {\n\tworker_connections 768;\n}\n\nhttp {\n\tinclude $ROOT/conf.d/*.conf;\n}\n"

// nginxOrgConf is nginx.org's: nothing included at the top level.
const nginxOrgConf = "user nginx;\nevents {\n    worker_connections 1024;\n}\nhttp {\n    include $ROOT/conf.d/*.conf;\n}\n"

func includeError(t *testing.T, err error) string {
	t.Helper()
	var refused *StreamIncludeError
	if !errors.As(err, &refused) {
		t.Fatalf("got %v, want a refusal", err)
	}
	return refused.Code
}

// On Debian and Ubuntu the include is a file of its own in modules-enabled:
// main context, after the modules' own files, and nginx.conf untouched.
func TestPlanStreamIncludeUsesTheModulesDirectory(t *testing.T) {
	svc, root := includeHost(t, map[string]string{
		"nginx.conf":                        debianConf,
		"modules-enabled/50-mod-x.conf":     "",
		"conf.d/app.conf":                   "server { listen 80; }\n",
		"stream.d/replica.conf":             "server { listen 16432; proxy_pass 10.0.0.5:5432; }\n",
		"stream.d/replica.conf.bak":         "server { listen 16432; proxy_pass 10.0.0.5:5432; }\n",
		"modules-available/ignored.conf":    "",
		"stream.d/.hidden-editor-swap.conf": "",
	})
	plan, err := svc.PlanStreamInclude(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "modules-enabled", streamDropIn)
	if plan.Mode != IncludeDropIn || plan.Path != want || plan.Exists || plan.Before != "" {
		t.Fatalf("plan = %+v", plan)
	}
	if !strings.Contains(plan.After, "stream {\n    include "+root+"/stream.d/*.conf;\n}\n") || plan.Added != plan.After || plan.Line != 1 {
		t.Fatalf("drop-in:\n%s", plan.After)
	}
	if strings.Join(plan.Streams, ",") != "replica" || len(plan.Conflicts) != 0 {
		t.Fatalf("streams %v conflicts %v", plan.Streams, plan.Conflicts)
	}
	before := mustRead(t, filepath.Join(root, "nginx.conf"))

	res, err := svc.ApplyStreamInclude(context.Background(), plan.Mode, plan.Path, true)
	if err != nil {
		t.Fatalf("%v %+v", err, res)
	}
	if !res.Validation.Valid || !res.Reloaded || res.Backup != "" || res.Streams != 1 {
		t.Fatalf("result = %+v", res)
	}
	if mustRead(t, want) != plan.After {
		t.Fatal("the drop-in is not what the plan showed")
	}
	if mustRead(t, filepath.Join(root, "nginx.conf")) != before {
		t.Fatal("nginx.conf was edited although a drop-in was enough")
	}
	if runs := nginxRuns(t, root); !strings.Contains(runs, "-t\n-s reload") {
		t.Fatalf("runs = %q", runs)
	}

	// Connected once, it is not connected twice, and the listing says whose
	// include it is.
	if _, err := svc.PlanStreamInclude(context.Background()); includeError(t, err) != "already_included" {
		t.Fatalf("second plan: %v", err)
	}
	status, err := svc.Streams(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Included || status.Connection == nil || status.Connection.Mode != IncludeDropIn || status.Connection.Path != want {
		t.Fatalf("status = %+v %+v", status, status.Connection)
	}

	// Disconnecting takes exactly that file out.
	removed, err := svc.RemoveStreamInclude(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if removed.Mode != IncludeDropIn || !removed.Reloaded || removed.Streams != 1 {
		t.Fatalf("removed = %+v", removed)
	}
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Fatalf("the drop-in is still there: %v", err)
	}
	if status, _ := svc.Streams(context.Background()); status.Included || status.Connection != nil {
		t.Fatalf("after disconnect: %+v", status)
	}
}

// nginx.org's nginx.conf includes nothing at the top level, so the block is
// appended — after every load_module — with a copy of the file beside it.
func TestPlanStreamIncludeAppendsToAMainFileWithNoDirectory(t *testing.T) {
	svc, root := includeHost(t, map[string]string{"nginx.conf": nginxOrgConf})
	main := filepath.Join(root, "nginx.conf")
	before := mustRead(t, main)
	plan, err := svc.PlanStreamInclude(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != IncludeMainFile || plan.Path != main || !plan.Exists || plan.Before != before {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.Reason != AppendNoDirectory || plan.DropIn != "" || !plan.KeepsCopy || len(plan.Warnings) != 0 {
		t.Fatalf("reason %q, drop-in %q, copy %v, warnings %q", plan.Reason, plan.DropIn, plan.KeepsCopy, plan.Warnings)
	}
	if !strings.HasPrefix(plan.After, before+"\n# Just Dashboard: streams begin.") ||
		!strings.HasSuffix(plan.After, "stream {\n    include "+root+"/stream.d/*.conf;\n}\n# Just Dashboard: streams end.\n") {
		t.Fatalf("after:\n%s", plan.After)
	}
	if lines := strings.Split(plan.After, "\n"); !strings.HasPrefix(lines[plan.Line-1], "# Just Dashboard: streams begin.") {
		t.Fatalf("line %d is %q", plan.Line, lines[plan.Line-1])
	}

	res, err := svc.ApplyStreamInclude(context.Background(), plan.Mode, plan.Path, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Reloaded || res.Backup == "" || mustRead(t, res.Backup) != before {
		t.Fatalf("result = %+v", res)
	}
	if strings.Contains(nginxRuns(t, root), "reload") {
		t.Fatal("reloaded although not asked to")
	}
	if !streamIncludeFound(root, svc.streamDir()) {
		t.Fatal("not included after the append")
	}
	if _, err := svc.RemoveStreamInclude(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, main); got != before {
		t.Fatalf("disconnect left:\n%q\nwant\n%q", got, before)
	}
}

// A test that fails puts nginx.conf back as it was, byte for byte: the same
// CRLF line endings and the same mode, and no copy left behind.
func TestApplyStreamIncludeRestoresTheMainFileWhenTheTestFails(t *testing.T) {
	crlf := strings.ReplaceAll(nginxOrgConf, "\n", "\r\n") + "# no newline at the end"
	svc, root := includeHost(t, map[string]string{"nginx.conf": crlf})
	main := filepath.Join(root, "nginx.conf")
	if err := os.Chmod(main, 0o640); err != nil {
		t.Fatal(err)
	}
	before := mustRead(t, main)
	plan, err := svc.PlanStreamInclude(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.After, "# no newline at the end\r\n\r\n# Just Dashboard: streams begin.") ||
		strings.Contains(strings.ReplaceAll(plan.After, "\r\n", ""), "\n") {
		t.Fatalf("the block does not keep the file's CRLF:\n%q", plan.After)
	}
	if err := os.WriteFile(filepath.Join(root, "fail-test"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := svc.ApplyStreamInclude(context.Background(), plan.Mode, plan.Path, true)
	if !errors.Is(err, ErrInvalidConf) || res == nil || res.Validation.Valid || res.Backup != "" {
		t.Fatalf("err = %v, res = %+v", err, res)
	}
	if got := mustRead(t, main); got != before {
		t.Fatalf("nginx.conf is not as it was:\n%q", got)
	}
	if st, _ := os.Stat(main); st.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v", st.Mode().Perm())
	}
	if backups, _ := filepath.Glob(main + ".jd-stream-*.bak"); len(backups) != 0 {
		t.Fatalf("copies left behind: %v", backups)
	}
	if strings.Contains(nginxRuns(t, root), "reload") {
		t.Fatal("reloaded after a failed test")
	}
}

// A drop-in whose test fails is taken away again.
func TestApplyStreamIncludeRemovesADropInWhoseTestFails(t *testing.T) {
	svc, root := includeHost(t, map[string]string{"nginx.conf": debianConf, "modules-enabled/.keep": ""})
	plan, err := svc.PlanStreamInclude(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "fail-test"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApplyStreamInclude(context.Background(), plan.Mode, plan.Path, true); !errors.Is(err, ErrInvalidConf) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(plan.Path); !os.IsNotExist(err) {
		t.Fatalf("the drop-in stayed: %v", err)
	}
}

// A second stream block is "duplicate" to nginx, so where one is there the
// include goes into it, as a line indented as the file indents.
func TestPlanStreamIncludeGoesIntoTheStreamBlockThatIsThere(t *testing.T) {
	for _, tc := range []struct {
		name, conf, want string
	}{
		{
			"tabs",
			"events {}\nstream {\n\tserver {\n\t\tlisten 2222;\n\t\tproxy_pass 10.0.0.9:22;\n\t}\n}\nhttp {}\n",
			"stream {\n\tserver {\n\t\tlisten 2222;\n\t\tproxy_pass 10.0.0.9:22;\n\t}\n\tinclude $ROOT/stream.d/*.conf; # added by Just Dashboard\n}\nhttp {}\n",
		},
		{
			"one line, with a brace in a comment and a string",
			"events {}\n# stream { }\nstream { server { listen 2222; proxy_pass \"10.0.0.9:22\"; } }\nhttp {}\n",
			"stream { server { listen 2222; proxy_pass \"10.0.0.9:22\"; } \n    include $ROOT/stream.d/*.conf; # added by Just Dashboard\n}\nhttp {}\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, root := includeHost(t, map[string]string{"nginx.conf": tc.conf})
			main := filepath.Join(root, "nginx.conf")
			before := mustRead(t, main)
			status, err := svc.Streams(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if status.StreamBlock != main || status.Snippet != "include "+root+"/stream.d/*.conf;" {
				t.Fatalf("snippet for a host with a stream block: %q in %q", status.Snippet, status.StreamBlock)
			}
			plan, err := svc.PlanStreamInclude(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if plan.Mode != IncludeStreamBlock || plan.Path != main {
				t.Fatalf("plan = %+v", plan)
			}
			if want := strings.ReplaceAll(tc.want, "$ROOT", root); !strings.HasSuffix(plan.After, want) {
				t.Fatalf("after:\n%s\nwant it to end:\n%s", plan.After, want)
			}
			if lines := strings.Split(plan.After, "\n"); !strings.Contains(lines[plan.Line-1], "include "+root) {
				t.Fatalf("line %d is %q", plan.Line, lines[plan.Line-1])
			}
			if _, err := svc.ApplyStreamInclude(context.Background(), plan.Mode, plan.Path, true); err != nil {
				t.Fatal(err)
			}
			if !streamIncludeFound(root, svc.streamDir()) {
				t.Fatal("not included")
			}
			if _, err := svc.RemoveStreamInclude(context.Background(), true); err != nil {
				t.Fatal(err)
			}
			after := mustRead(t, main)
			if tc.name == "tabs" && after != before {
				t.Fatalf("disconnect left:\n%s", after)
			}
			if streamIncludeFound(root, svc.streamDir()) || strings.Contains(after, "Just Dashboard") {
				t.Fatalf("still connected:\n%s", after)
			}
		})
	}
}

// A load_module nginx reads after the drop-in would be "specified too late",
// so the block goes at the end of nginx.conf instead.
func TestPlanStreamIncludeAppendsWhenAModuleLoadsAfterTheDirectory(t *testing.T) {
	svc, root := includeHost(t, map[string]string{
		"nginx.conf":                  "include $ROOT/modules-enabled/*.conf;\nload_module modules/ngx_stream_js_module.so;\nevents {}\n",
		"modules-enabled/50-mod.conf": "load_module modules/ngx_http_js_module.so;\n",
	})
	plan, err := svc.PlanStreamInclude(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != IncludeMainFile || plan.Path != filepath.Join(root, "nginx.conf") {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.Reason != AppendModuleLoadsAfter || plan.DropIn != filepath.Join(root, "modules-enabled", streamDropIn) {
		t.Fatalf("reason %q, drop-in %q", plan.Reason, plan.DropIn)
	}
}

// Every way a drop-in can fail is its own reason for editing nginx.conf, so
// the page never gives the reason of another: the directory there but a file
// of the drop-in's name in it, or the directory leading outside the nginx
// directory. The first such directory's reason is the one given.
func TestPlanStreamIncludeSaysWhyItEditsNginxConf(t *testing.T) {
	outside := t.TempDir()
	for _, tc := range []struct {
		name   string
		files  map[string]string
		reason string
		dropIn string
	}{
		{
			"a file of that name is there",
			map[string]string{"nginx.conf": debianConf, "modules-enabled/" + streamDropIn: "# the operator's own\n"},
			AppendNameTaken, "$ROOT/modules-enabled/" + streamDropIn,
		},
		{
			"the directory is outside",
			map[string]string{"nginx.conf": "include " + outside + "/*.conf;\nevents {}\n"},
			AppendDirectoryElsewhere, outside + "/" + streamDropIn,
		},
		{
			"the first directory decides",
			map[string]string{
				"nginx.conf":                      "include $ROOT/modules-enabled/*.conf;\ninclude " + outside + "/*.conf;\nevents {}\n",
				"modules-enabled/" + streamDropIn: "# the operator's own\n",
			},
			AppendNameTaken, "$ROOT/modules-enabled/" + streamDropIn,
		},
		{
			"no glob the name matches",
			map[string]string{"nginx.conf": "include $ROOT/modules-enabled/*.load;\nevents {}\n", "modules-enabled/.keep": ""},
			AppendNoDirectory, "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, root := includeHost(t, tc.files)
			plan, err := svc.PlanStreamInclude(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if plan.Mode != IncludeMainFile || plan.Reason != tc.reason || plan.DropIn != strings.ReplaceAll(tc.dropIn, "$ROOT", root) {
				t.Fatalf("mode %q, reason %q, drop-in %q", plan.Mode, plan.Reason, plan.DropIn)
			}
		})
	}
}

// Where connecting cannot work, nothing is planned, and each case has its
// own code for the page to act on.
func TestPlanStreamIncludeRefuses(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "streams.conf"), []byte("stream { server { listen 1; proxy_pass 10.0.0.1:1; } }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, conf, code string
		missing          bool
	}{
		{"already", "events {}\nstream { include stream.d/*.conf; }\n", "already_included", false},
		{"misplaced", "events {}\nhttp { include $ROOT/stream.d/*.conf; }\n", "include_misplaced", false},
		{"module missing", nginxOrgConf, "module_missing", true},
		{"stream block outside", "events {}\ninclude " + outside + "/streams.conf;\n", "stream_block_elsewhere", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, root := includeHost(t, map[string]string{"nginx.conf": tc.conf})
			if tc.missing {
				version := withModulesPath(hostNginxV, t.TempDir())
				if err := os.WriteFile(filepath.Join(root, "version"), []byte(version), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "dump"), []byte("# configuration file "+root+"/nginx.conf:\nevents {}\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			before := mustRead(t, filepath.Join(root, "nginx.conf"))
			_, err := svc.PlanStreamInclude(context.Background())
			if code := includeError(t, err); code != tc.code {
				t.Fatalf("code = %s (%v)", code, err)
			}
			if _, err := svc.ApplyStreamInclude(context.Background(), IncludeMainFile, filepath.Join(root, "nginx.conf"), true); includeError(t, err) != tc.code {
				t.Fatalf("apply: %v", err)
			}
			if mustRead(t, filepath.Join(root, "nginx.conf")) != before {
				t.Fatal("a refused connect changed nginx.conf")
			}
		})
	}
}

// A connect made from a plan that no longer holds is refused rather than
// made some other way.
func TestApplyStreamIncludeRefusesAChangedPlan(t *testing.T) {
	svc, root := includeHost(t, map[string]string{"nginx.conf": nginxOrgConf})
	_, err := svc.ApplyStreamInclude(context.Background(), IncludeDropIn, filepath.Join(root, "modules-enabled", streamDropIn), true)
	if includeError(t, err) != "plan_changed" {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(mustRead(t, filepath.Join(root, "nginx.conf")), "stream") {
		t.Fatal("changed anyway")
	}
}

// The streams already in the directory start at the reload. One whose port
// another program holds would fail that reload in the master while the
// command exits 0 — and every reload after it — so the plan names it and the
// connect is refused.
func TestStreamIncludeChecksThePortsOfTheStreamsItStarts(t *testing.T) {
	svc, root := includeHost(t, map[string]string{
		"nginx.conf":            nginxOrgConf,
		"stream.d/ssh.conf":     "server { listen 2222; proxy_pass 10.0.0.9:22; }\n",
		"stream.d/db.conf":      "server { listen 127.0.0.1:6432; proxy_pass 10.0.0.5:5432; }\n",
		"stream.d/db-copy.conf": "server { listen 127.0.0.1:6432; proxy_pass 10.0.0.6:5432; }\n",
	})
	withListeners(t, func(context.Context) ([]Listener, error) {
		return []Listener{{Protocol: "tcp", Address: "::", Port: 2222, PID: 900, Process: "sshd"}}, nil
	})
	plan, err := svc.PlanStreamInclude(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"db-copy and db both listen on port 6432/tcp", "ssh: port 2222/tcp is already in use by sshd (pid 900)"}
	if strings.Join(plan.Conflicts, "|") != strings.Join(want, "|") {
		t.Fatalf("conflicts = %q", plan.Conflicts)
	}
	_, err = svc.ApplyStreamInclude(context.Background(), plan.Mode, plan.Path, true)
	if includeError(t, err) != "port_in_use" || !strings.Contains(err.Error(), "sshd") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(mustRead(t, filepath.Join(root, "nginx.conf")), "stream") {
		t.Fatal("connected over a port in use")
	}
}

// An include written by hand is the operator's to take out; one the
// dashboard wrote and the operator then edited is too.
func TestRemoveStreamIncludeLeavesAHandWrittenInclude(t *testing.T) {
	for _, conf := range []string{
		"events {}\nstream {\n    include $ROOT/stream.d/*.conf;\n}\n",
		"events {}\n# Just Dashboard: streams begin. Reads the streams in $ROOT/stream.d;\nstream {\n    include $ROOT/stream.d/*.conf; # tuned by hand\n}\n# Just Dashboard: streams end.\n",
	} {
		svc, root := includeHost(t, map[string]string{"nginx.conf": conf})
		before := mustRead(t, filepath.Join(root, "nginx.conf"))
		if _, err := svc.RemoveStreamInclude(context.Background(), true); includeError(t, err) != "not_connected" {
			t.Fatalf("err = %v", err)
		}
		if mustRead(t, filepath.Join(root, "nginx.conf")) != before {
			t.Fatal("changed a hand-written include")
		}
	}
}

// The drop-in is taken out only as the dashboard wrote it: one the operator
// has edited since is theirs, so a disconnect refuses and leaves it, and the
// listing does not call it the dashboard's.
func TestRemoveStreamIncludeLeavesAHandEditedDropIn(t *testing.T) {
	svc, root := includeHost(t, map[string]string{"nginx.conf": debianConf, "modules-enabled/.keep": ""})
	dropIn := filepath.Join(root, "modules-enabled", streamDropIn)
	edited := strings.Replace(renderDropIn(svc.streamDir()), "stream {\n", "stream {\n    proxy_timeout 1h;\n", 1)
	if edited == renderDropIn(svc.streamDir()) {
		t.Fatal("the edit changed nothing")
	}
	if err := os.WriteFile(dropIn, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RemoveStreamInclude(context.Background(), true); includeError(t, err) != "not_connected" {
		t.Fatalf("err = %v", err)
	}
	if got, err := os.ReadFile(dropIn); err != nil || string(got) != edited {
		t.Fatalf("the edited drop-in was changed: %v %q", err, got)
	}
	if strings.Contains(nginxRuns(t, root), "reload") {
		t.Fatal("reloaded although nothing was taken out")
	}
	status, err := svc.Streams(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Included || status.Connection != nil {
		t.Fatalf("included %v, connection %+v", status.Included, status.Connection)
	}
}

// A copy of the file a connect edits is kept only where nginx would not read
// it: beside a stream block in a directory included whole, the copy would be a
// second stream block. The plan says so before the change, and the change
// keeps none; the same block in a directory included by *.conf keeps its copy.
func TestApplyStreamIncludeKeepsNoCopyNginxWouldRead(t *testing.T) {
	block := "stream {\n    server { listen 2222; proxy_pass 10.0.0.9:22; }\n}\n"
	for _, tc := range []struct {
		name, include, file string
		kept                bool
	}{
		{"included whole", "$ROOT/main.d/*", "main.d/streams", false},
		{"included by suffix", "$ROOT/main.d/*.conf", "main.d/streams.conf", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, root := includeHost(t, map[string]string{
				"nginx.conf": "events {}\ninclude " + tc.include + ";\nhttp {}\n",
				tc.file:      block,
			})
			path := filepath.Join(root, tc.file)
			plan, err := svc.PlanStreamInclude(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			warned := strings.Contains(strings.Join(plan.Warnings, " "), "No copy of "+path)
			if plan.Mode != IncludeStreamBlock || plan.Path != path || plan.KeepsCopy != tc.kept || warned == tc.kept {
				t.Fatalf("mode %q, path %q, copy %v, warnings %q", plan.Mode, plan.Path, plan.KeepsCopy, plan.Warnings)
			}
			res, err := svc.ApplyStreamInclude(context.Background(), plan.Mode, plan.Path, true)
			if err != nil {
				t.Fatalf("%v %+v", err, res)
			}
			copies, _ := filepath.Glob(filepath.Join(root, "main.d", "*.jd-stream-*.bak"))
			if tc.kept != (res.Backup != "") || len(copies) != map[bool]int{true: 1, false: 0}[tc.kept] {
				t.Fatalf("backup %q, copies %v", res.Backup, copies)
			}
			if tc.kept && mustRead(t, res.Backup) != block {
				t.Fatal("the copy is not the file as it was")
			}
			if !tc.kept && !strings.Contains(strings.Join(res.Warnings, " "), "No copy of "+path) {
				t.Fatalf("warnings = %q", res.Warnings)
			}
			if mustRead(t, path) != plan.After {
				t.Fatal("the file is not what the plan showed")
			}
		})
	}
}

// A disconnect whose test fails puts the drop-in back.
func TestRemoveStreamIncludePutsItBackWhenTheTestFails(t *testing.T) {
	svc, root := includeHost(t, map[string]string{"nginx.conf": debianConf, "modules-enabled/.keep": ""})
	plan, err := svc.PlanStreamInclude(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApplyStreamInclude(context.Background(), plan.Mode, plan.Path, true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "fail-test"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RemoveStreamInclude(context.Background(), true); !errors.Is(err, ErrInvalidConf) {
		t.Fatalf("err = %v", err)
	}
	if mustRead(t, plan.Path) != plan.After {
		t.Fatal("the drop-in was not put back")
	}
}

// A copy of an edited file is kept only where no include would read it: in a
// directory included whole, the copy would be configuration.
func TestReadByNginx(t *testing.T) {
	root := t.TempDir()
	files := []ConfigFile{{Path: filepath.Join(root, "nginx.conf"), Content: "events {}\ninclude extra/*;\nhttp { include conf.d/*.conf; }\n"}}
	for path, want := range map[string]bool{
		filepath.Join(root, "extra", "nginx.conf.jd-stream-1.bak"): true,
		filepath.Join(root, "conf.d", "app.conf.jd-stream-1.bak"):  false,
		filepath.Join(root, "nginx.conf.jd-stream-1.bak"):          false,
		filepath.Join(root, "conf.d", "app.conf"):                  true,
	} {
		if got := readByNginx(files, root, path); got != want {
			t.Errorf("%s: %v, want %v", path, got, want)
		}
	}
}

func TestTopLevelBlock(t *testing.T) {
	for _, tc := range []struct {
		content    string
		open, stop int
		ok         bool
	}{
		{"stream { }", 7, 9, true},
		{"# stream { }\nstream {}", 20, 21, true},
		{`http { map $x ${y}z { default "}"; } } stream { a '{'; }`, 46, 55, true},
		{"http { stream { } }", 0, 0, false},
		{"upstream x { }", 0, 0, false},
	} {
		open, stop, ok := topLevelBlock(tc.content, "stream")
		if ok != tc.ok || ok && (open != tc.open || stop != tc.stop) {
			t.Errorf("%q: %d %d %v", tc.content, open, stop, ok)
		}
	}
}
