package term

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestAgentStartupArgvKeepsLaunchValuesSeparate(t *testing.T) {
	for _, shell := range []string{"/bin/bash", "/bin/zsh"} {
		m := &Manager{shell: shell, shellDir: "/shared/startup", account: Account{UID: 0}}
		cwd := "/srv/a 'quoted' path; $(touch never)"
		args := m.startupArgv(true, "codex", cwd)
		if !slices.Equal(args[len(args)-4:], []string{shell, m.shellDir, "codex", cwd}) {
			t.Fatalf("launch values were not positional: %q", args)
		}
		if strings.Contains(args[3], cwd) || strings.Contains(args[3], "codex") {
			t.Fatalf("request value reached bootstrap source: %q", args[3])
		}
	}
}

func TestAgentStartupRejectsUnknownAgentsAndUnsupportedShells(t *testing.T) {
	m := &Manager{enabled: true, shell: "/bin/bash", shellDir: "/shared/startup"}
	for _, agent := range []string{"CODEX", "codex --yolo", "claude; id", "other"} {
		if _, err := m.Create(context.Background(), CreateOptions{Agent: agent}); !errors.Is(err, ErrUnknownAgent) {
			t.Fatalf("agent %q = %v, want unknown agent", agent, err)
		}
	}
	for _, shell := range []string{"/bin/sh", "/bin/fish"} {
		m.shell = shell
		if _, err := m.Create(context.Background(), CreateOptions{Agent: "codex"}); !errors.Is(err, ErrAgentShellUnavailable) {
			t.Fatalf("shell %q = %v, want unavailable startup", shell, err)
		}
	}
	m.shell, m.shellDir = "/bin/bash", ""
	if _, err := m.Create(context.Background(), CreateOptions{Agent: "claude"}); !errors.Is(err, ErrAgentShellUnavailable) {
		t.Fatalf("missing startup = %v", err)
	}
	if len(m.List()) != 0 || m.pending != 0 {
		t.Fatal("an invalid launch consumed a terminal slot")
	}
}

func TestAgentStartupLoadsNativePATHAndKeepsShell(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		for _, agent := range []string{"codex", "claude"} {
			t.Run(shell+"/"+agent, func(t *testing.T) {
				binary, err := exec.LookPath(shell)
				if err != nil {
					t.Skip(err)
				}
				home := t.TempDir()
				bin := filepath.Join(home, "native-bin")
				cwd := filepath.Join(home, "work 'quotes' $ dollars; folder")
				for _, dir := range []string{bin, cwd} {
					if err := os.Mkdir(dir, 0700); err != nil {
						t.Fatal(err)
					}
				}
				stub := `#!/bin/sh
printf 'agent-cwd:%s\nargc:%s flag:%s env:%s/%s\n' "$PWD" "$#" "$1" "${JD_TERMINAL_START_AGENT-unset}" "${JD_TERMINAL_START_DIR-unset}"
exit 17
`
				if err := os.WriteFile(filepath.Join(bin, agent), []byte(stub), 0700); err != nil {
					t.Fatal(err)
				}
				// Only the native interactive rc adds the binary. It also changes
				// directory, which an explicit focused-directory launch must undo.
				rc := fmt.Sprintf("export PATH=%q:$PATH\ncd /\n", bin)
				if err := os.WriteFile(filepath.Join(home, "."+shell+"rc"), []byte(rc), 0600); err != nil {
					t.Fatal(err)
				}
				if shell == "zsh" {
					// .zshenv can itself spawn a shell: consume the launch before
					// any native configuration, rather than waiting for .zshrc.
					zshenv := `[[ -z ${JD_TERMINAL_START_AGENT:-} && -z ${JD_TERMINAL_START_DIR:-} ]] || printf 'leaked-launch-env\n'` + "\n"
					if err := os.WriteFile(filepath.Join(home, ".zshenv"), []byte(zshenv), 0600); err != nil {
						t.Fatal(err)
					}
				}
				m := &Manager{shell: binary, clipboard: newClipboardStore(filepath.Join(home, "terminal")), account: Account{UID: os.Geteuid()}}
				if err := m.SetupShell(); err != nil {
					t.Fatal(err)
				}
				out, write := startAgentShell(t, m, agent, cwd, home)
				flag := "--yolo"
				if agent == "claude" {
					flag = "--dangerously-skip-permissions"
				}
				seen := await(t, out, "", "argc:1 flag:"+flag+" env:unset/unset")
				if !strings.Contains(seen, "agent-cwd:"+cwd) {
					t.Fatalf("native startup lost the focused directory: %q", seen)
				}
				if strings.Contains(seen, "leaked-launch-env") {
					t.Fatalf("native config inherited the launch: %q", seen)
				}
				write("printf 'shell-alive:%s flags:%s/%s\\n' yes \"${JD_TERMINAL_START_AGENT-unset}\" \"${JD_TERMINAL_START_DIR-unset}\"\n")
				await(t, out, seen, "shell-alive:yes flags:unset/unset")
			})
		}
	}
}

func TestMissingAgentLeavesNativeShellUsable(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			binary, err := exec.LookPath(shell)
			if err != nil {
				t.Skip(err)
			}
			home := t.TempDir()
			// A nonexistent PATH gives a deterministic missing tool even when
			// the developer has Codex installed somewhere in the system PATH.
			if err := os.WriteFile(filepath.Join(home, "."+shell+"rc"), []byte("export PATH=/jd-missing-agent-fixture\nset -e\n"), 0600); err != nil {
				t.Fatal(err)
			}
			m := &Manager{shell: binary, clipboard: newClipboardStore(filepath.Join(home, "terminal")), account: Account{UID: os.Geteuid()}}
			if err := m.SetupShell(); err != nil {
				t.Fatal(err)
			}
			out, write := startAgentShell(t, m, "codex", home, home)
			seen := await(t, out, "", "codex")
			write("printf 'missing-agent-shell:%s\\n' alive\n")
			await(t, out, seen, "missing-agent-shell:alive")
		})
	}
}

func startAgentShell(t *testing.T, m *Manager, agent, cwd, home string) (<-chan []byte, func(string)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	args := m.startupArgv(true, agent, cwd)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HOME="+home, "TERM=xterm-256color", "ZDOTDIR="+home)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 140})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	out := make(chan []byte, 64)
	go func() {
		defer close(out)
		buffer := make([]byte, 4096)
		for {
			n, err := f.Read(buffer)
			if n > 0 {
				out <- append([]byte(nil), buffer[:n]...)
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		// Let the native shell shut down its async editor helpers and finish
		// history writes. Waiting only for a killed parent can leave a helper
		// recreating files after TempDir has started removing HOME.
		f.Write([]byte("builtin exit\n"))
		timeout := time.NewTimer(5 * time.Second)
		defer timeout.Stop()
	drain:
		for {
			select {
			case _, open := <-out:
				if !open {
					break drain
				}
			case <-timeout.C:
				t.Error("agent shell fixture did not finish its PTY output after exit")
				break drain
			}
		}
		cancel()
		f.Close()
		cmd.Wait()
	})
	return out, func(text string) {
		t.Helper()
		if _, err := f.Write([]byte(text)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBundledShellPromptAndCompletion(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			binary, err := exec.LookPath(shell)
			if err != nil {
				t.Skip(err)
			}
			home := t.TempDir()
			m := &Manager{clipboard: newClipboardStore(filepath.Join(home, "terminal"))}
			if err := m.SetupShell(); err != nil {
				t.Fatal(err)
			}
			var args []string
			env := append(os.Environ(), "HOME="+home, "TERM=xterm-256color")
			if shell == "bash" {
				os.WriteFile(filepath.Join(home, ".bashrc"), []byte("export JD_RC_LOADED=yes\n"), 0600)
				args = []string{"--noprofile", "--rcfile", filepath.Join(m.shellDir, "bashrc"), "-ic", `printf 'loaded:%s\nprompt:%s\n' "$JD_RC_LOADED" "$PS1"; bind -q menu-complete`}
			} else {
				os.WriteFile(filepath.Join(home, ".zshrc"), []byte("export JD_RC_LOADED=yes\n"), 0600)
				env = append(env, "ZDOTDIR="+m.shellDir, "JD_ORIGINAL_ZDOTDIR="+home)
				args = []string{"-ic", `printf 'loaded:%s\nprompt:%s\nhistfile:%s\n' "$JD_RC_LOADED" "$PROMPT" "$HISTFILE"; bindkey '^I'; whence -w compdef; whence -w _zsh_autosuggest_start _zsh_highlight`}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, args...)
			cmd.Env = env
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("startup: %v: %s", err, out)
			}
			got := string(out)
			if !strings.Contains(got, "loaded:yes") || !strings.Contains(got, ">") {
				t.Fatalf("profile/prompt missing: %s", got)
			}
			completion := "menu-complete"
			if shell == "zsh" {
				completion = "complete-word"
			}
			if !strings.Contains(got, completion) {
				t.Fatalf("completion missing: %s", got)
			}
			if shell == "zsh" {
				// The account rc set no history file, so the bundled startup must.
				if !strings.Contains(got, "histfile:"+filepath.Join(home, ".zsh_history")) {
					t.Fatalf("history file missing: %s", got)
				}
				// Only where the host has the packages: the startup is a guarded
				// source, not a dependency, and a build machine without them is fine.
				for plugin, fn := range map[string]string{
					"zsh-autosuggestions":     "_zsh_autosuggest_start",
					"zsh-syntax-highlighting": "_zsh_highlight",
				} {
					if !zshPluginInstalled(plugin) {
						continue
					}
					if !strings.Contains(got, fn+": function") {
						t.Fatalf("%s installed but not loaded: %s", plugin, got)
					}
				}
			}
		})
	}
}

func zshPluginInstalled(name string) bool {
	for _, dir := range []string{"/usr/share/" + name, "/usr/share/zsh/plugins/" + name} {
		if _, err := os.Stat(filepath.Join(dir, name+".zsh")); err == nil {
			return true
		}
	}
	return false
}

func TestShellSetupRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(root, ".shell")); err != nil {
		t.Fatal(err)
	}
	m := &Manager{clipboard: newClipboardStore(root)}
	if err := m.SetupShell(); err == nil {
		t.Fatal("accepted symlink startup directory")
	}
}

func TestScrollToUsesTmuxHistory(t *testing.T) {
	m := newTmuxManager(t)
	sess := newSession(t, m, CreateOptions{})
	waitForTmuxSession(t, m, sess.TmuxName)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "tmux", "send-keys", "-t", sess.TmuxName, "for i in {1..200}; do echo line-$i; done", "Enter").CombinedOutput(); err != nil {
		t.Fatalf("seed: %v %s", err, out)
	}
	for {
		state, err := m.ScrollState(ctx, sess.TmuxName)
		if err != nil {
			t.Fatal(err)
		}
		if state.History >= 100 {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("history did not fill")
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, offset := range []int{40, 80, 15, 0} {
		if err := m.ScrollTo(ctx, sess.TmuxName, offset); err != nil {
			t.Fatal(err)
		}
		state, err := m.ScrollState(ctx, sess.TmuxName)
		if err != nil {
			t.Fatal(err)
		}
		if state.Offset != offset || state.Active != (offset > 0) {
			t.Fatalf("seek %d returned %+v", offset, state)
		}
	}
}

// Exercise the actual login argv, PTY editor and Tab key, not just its bindings.
func TestNativePromptCompletesInTmux(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			binary, err := exec.LookPath(shell)
			if err != nil {
				t.Skip(err)
			}
			m := newTmuxManager(t)
			m.shell = binary
			dir := t.TempDir()
			m.clipboard = newClipboardStore(filepath.Join(dir, "terminal"))
			if err := m.SetupShell(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "autocomplete-fixture.txt"), []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			sess := newSession(t, m, CreateOptions{CWD: dir, Cols: 140, Rows: 24})
			waitForTmuxSession(t, m, sess.TmuxName)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			waitText := func(want string) {
				t.Helper()
				for {
					out, err := exec.CommandContext(ctx, "tmux", "capture-pane", "-p", "-t", sess.TmuxName).CombinedOutput()
					if err != nil {
						t.Fatalf("capture: %v %s", err, out)
					}
					if strings.Contains(string(out), want) {
						return
					}
					if ctx.Err() != nil {
						t.Fatalf("missing %q: %s", want, out)
					}
					time.Sleep(25 * time.Millisecond)
				}
			}
			waitText(">")
			if out, err := exec.CommandContext(ctx, "tmux", "send-keys", "-t", sess.TmuxName, "cat autocomplete-fi", "Tab").CombinedOutput(); err != nil {
				t.Fatalf("Tab: %v %s", err, out)
			}
			waitText("cat autocomplete-fixture.txt")
		})
	}
}
