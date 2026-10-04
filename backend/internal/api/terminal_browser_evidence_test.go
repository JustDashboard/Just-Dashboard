package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/config"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
	"github.com/Wayy01/Just-Dashboard/backend/internal/term"
	"github.com/Wayy01/Just-Dashboard/backend/internal/wsx"
)

// This opt-in harness lets a browser exercise native PTYs and kernel resizes
// with isolated tools. The normal gate skips it; no production auth is changed.
func TestTerminalBrowserEvidenceServer(t *testing.T) {
	evidenceDir := os.Getenv("JD_TERMINAL_BROWSER_EVIDENCE_DIR")
	if evidenceDir == "" {
		t.Skip("set JD_TERMINAL_BROWSER_EVIDENCE_DIR to serve the isolated live terminal fixture")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(evidenceDir, 0700); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	cwd := filepath.Join(home, "project directory")
	for _, path := range []string{bin, cwd} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	tui := filepath.Join(home, "resize-tui.py")
	if err := os.WriteFile(tui, []byte(terminalEvidenceTUI), 0600); err != nil {
		t.Fatal(err)
	}
	shell := filepath.Join(home, "bash")
	files := map[string]string{
		shell:                          "#!/bin/sh\nexport HOME=" + evidenceShellQuote(home) + "\nexec /bin/bash \"$@\"\n",
		filepath.Join(home, ".bashrc"): "export PATH=" + evidenceShellQuote(bin) + ":$PATH\n",
	}
	realAgents := os.Getenv("JD_TERMINAL_BROWSER_REAL_AGENTS") == "1"
	for _, agent := range []string{"codex", "claude", "jd-resize-tui"} {
		command := evidenceShellQuote(python) + " " + evidenceShellQuote(tui) + " " + agent
		if realAgents && agent != "jd-resize-tui" {
			binary, err := exec.LookPath(agent)
			if err != nil {
				t.Fatal(err)
			}
			command = evidenceShellQuote(binary)
		}
		files[filepath.Join(bin, agent)] = "#!/bin/sh\nexec " + command + " \"$@\"\n"
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0700); err != nil {
			t.Fatal(err)
		}
	}
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	manager := term.NewManager(true, shell, me.Username)
	manager.SetClipboardRootForTest(filepath.Join(home, "terminal"))
	if err := manager.SetupShell(); err != nil {
		t.Fatal(err)
	}
	holders, err := os.MkdirTemp("", "jd-browser-pty-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(holders) })
	manager.SetHolderLauncherForTest(holders, terminalHolderLauncherForTest)
	t.Cleanup(func() {
		for _, sess := range manager.List() {
			manager.Kill(context.Background(), sess.ID)
		}
		manager.Shutdown()
	})
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	origins := []string{}
	for port := 43117; port <= 43131; port++ {
		origins = append(origins, "http://127.0.0.1:"+strconv.Itoa(port), "http://localhost:"+strconv.Itoa(port))
	}
	s := &Server{
		Cfg: &config.Config{}, Store: st, Log: logger, Audit: audit.New(st, logger),
		WS: wsx.NewUpgrader(origins, false), destrLim: httpx.NewLimiter(30, 10),
		modules: moduleSet{term: manager},
	}
	handler := terminalHandlerForTest(s)
	mux := http.NewServeMux()
	mux.Handle("/api/v1/", http.StripPrefix("/api/v1", handler))
	mux.Handle("/", handler)
	listener, err := net.Listen("tcp", "127.0.0.1:43128")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	t.Cleanup(func() { server.Close() })
	go server.Serve(listener)
	sess, err := manager.Create(context.Background(), term.CreateOptions{
		Owner: "tester", CWD: cwd, Rows: 30, Cols: 110,
	})
	if err != nil {
		t.Fatal(err)
	}
	stop := filepath.Join(evidenceDir, "stop")
	ready := map[string]any{
		"url": "http://" + listener.Addr().String(), "cwd": cwd, "home": home,
		"workspaceId": sess.WorkspaceID, "windowId": sess.ID, "stopFile": stop,
		"tuiCommand": "jd-resize-tui", "pid": os.Getpid(),
		"realAgents": realAgents,
	}
	data, err := json.MarshalIndent(ready, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evidenceDir, "ready.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	fmt.Printf("Terminal browser evidence ready at %s; create %s to stop\n", ready["url"], stop)
	timer := time.NewTimer(15 * time.Minute)
	defer timer.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if _, err := os.Stat(stop); err == nil {
				return
			}
		case <-timer.C:
			t.Fatal("browser evidence fixture exceeded its 15 minute limit")
		}
	}
}

func evidenceShellQuote(value string) string {
	// The fixture's generated shell source carries only trusted temporary
	// paths, but quote them exactly so spaces or quotes remain one argument.
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

const terminalEvidenceTUI = `import os, select, signal, sys, termios, tty

fd = sys.stdin.fileno()
original = termios.tcgetattr(fd)
running = True
keys = ""
tool = sys.argv[1]
command = " ".join(sys.argv[1:])

def draw(*_):
    cols, rows = os.get_terminal_size(sys.stdout.fileno())
    text = [tool + " | native PTY", "", "Command: " + command,
            "Directory: " + os.getcwd(), "", f"PTY grid: {cols} columns x {rows} rows",
            "Resize any split: this frame follows kernel SIGWINCH.",
            "Last input: " + (keys or "(click this pane and type)"),
            "Ctrl+C returns to the shell."]
    # Ignored OSC metadata keeps exact evidence available even when the
    # visible pane is too narrow to print the whole grid, argv or directory.
    frame = f"\x1b]777;PTY grid: {cols} columns x {rows} rows\x07"
    frame += "\x1b]777;Command: " + command + ";Directory: " + os.getcwd() + "\x07"
    frame += "\x1b[?7l\x1b[?25l\x1b[2J\x1b[H\x1b[38;2;110;170;205m"
    frame += "┌" + "─" * max(0, cols - 2) + "┐"
    for row in range(2, rows):
        content = text[row - 2] if row - 2 < len(text) else ""
        content = content[:max(0, cols - 4)]
        frame += f"\x1b[{row};1H│ " + content.ljust(max(0, cols - 4)) + " │"
    frame += f"\x1b[{rows};1H└" + "─" * max(0, cols - 2) + "┘\x1b[0m"
    sys.stdout.write(frame)
    sys.stdout.flush()

def stop(*_):
    global running
    running = False

signal.signal(signal.SIGWINCH, draw)
signal.signal(signal.SIGTERM, stop)
signal.signal(signal.SIGHUP, stop)
sys.stdout.write("\x1b[?1049h")
tty.setraw(fd)
try:
    draw()
    while running:
        ready, _, _ = select.select([fd], [], [], 0.15)
        if ready:
            raw = os.read(fd, 1024)
            if not raw or b"\x03" in raw:
                break
            keys = (keys + raw.decode("utf-8", errors="replace"))[-60:]
            keys = "".join(char for char in keys if char.isprintable())
            draw()
finally:
    termios.tcsetattr(fd, termios.TCSADRAIN, original)
    sys.stdout.write("\x1b[?25h\x1b[?7h\x1b[?1049l")
    sys.stdout.flush()
`
