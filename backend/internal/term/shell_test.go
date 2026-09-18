package term

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
