package gitx

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

type RebaseItem struct {
	SHA     string `json:"sha"`
	Action  string `json:"action"`
	Message string `json:"message"`
}
type RebasePlan struct {
	Head  string       `json:"head"`
	Base  string       `json:"base"`
	Items []RebaseItem `json:"items"`
}

func (s *Service) RebasePlan(ctx context.Context, path, base string) (*RebasePlan, error) {
	status, err := s.Status(ctx, path)
	if err != nil {
		return nil, err
	}
	branch, branchErr := s.CurrentBranch(ctx, path)
	if !status.Clean || status.Operation != "" || branchErr != nil || branch == "" || branch == "HEAD" {
		return nil, fmt.Errorf("%w: check out a branch, commit or stash changes, and finish the current operation", ErrInvalidRef)
	}
	head, err := s.commitID(ctx, path, "HEAD")
	if err != nil {
		return nil, err
	}
	base, err = s.commitID(ctx, path, base)
	if err != nil {
		return nil, err
	}
	if _, err := s.run(ctx, path, "merge-base", "--is-ancestor", base, head); err != nil {
		return nil, fmt.Errorf("%w: choose an ancestor of the current branch", ErrInvalidRef)
	}
	merges, err := s.run(ctx, path, "rev-list", "--merges", base+".."+head)
	if err != nil {
		return nil, err
	}
	if merges != "" {
		return nil, fmt.Errorf("%w: this editor supports linear local history; the selection contains merge commits", ErrInvalidRef)
	}
	list, err := s.run(ctx, path, "rev-list", "--reverse", base+".."+head)
	if err != nil {
		return nil, err
	}
	shas := nonEmptyLines(list)
	if len(shas) == 0 || len(shas) > 100 {
		return nil, fmt.Errorf("%w: select between 1 and 100 commits", ErrInvalidRef)
	}
	local, err := s.run(ctx, path, "rev-list", base+".."+head, "--not", "--remotes")
	if err != nil {
		return nil, err
	}
	if len(nonEmptyLines(local)) != len(shas) {
		return nil, fmt.Errorf("%w: the selection includes published commits; choose a base after them", ErrInvalidRef)
	}
	plan := &RebasePlan{Head: head, Base: base, Items: []RebaseItem{}}
	for _, sha := range shas {
		message, err := s.run(ctx, path, "show", "--no-patch", "--format=%B", sha, "--")
		if err != nil {
			return nil, err
		}
		plan.Items = append(plan.Items, RebaseItem{SHA: sha, Action: "pick", Message: strings.TrimSuffix(message, "\n")})
	}
	return plan, nil
}

var rebaseSHA = regexp.MustCompile(`^[0-9a-f]{40,64}$`)

func rebaseTodo(items []RebaseItem) (string, error) {
	if len(items) == 0 || len(items) > 100 {
		return "", fmt.Errorf("invalid rebase length")
	}
	var todo strings.Builder
	seen := map[string]bool{}
	kept := false
	for _, item := range items {
		if !rebaseSHA.MatchString(item.SHA) || seen[item.SHA] {
			return "", fmt.Errorf("invalid or duplicate commit")
		}
		seen[item.SHA] = true
		switch item.Action {
		case "drop":
		case "pick":
			kept = true
		case "reword":
			if strings.TrimSpace(item.Message) == "" || len(item.Message) > 60000 || strings.ContainsRune(item.Message, 0) {
				return "", fmt.Errorf("enter a commit message of at most 60,000 bytes")
			}
			kept = true
		case "squash":
			if !kept {
				return "", fmt.Errorf("the first retained commit cannot be squashed")
			}
		default:
			return "", fmt.Errorf("choose pick, reword, squash or drop")
		}
		fmt.Fprintf(&todo, "%s %s\n", item.Action, item.SHA)
	}
	return todo.String(), nil
}

func (s *Service) rebasePlanPath(ctx context.Context, path string) (string, error) {
	dir, err := s.run(ctx, path, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	return filepath.Join(strings.TrimSpace(dir), "jd-rebase-plan.json"), nil
}

func (s *Service) StartRebase(ctx context.Context, path string, req RebasePlan) (*Result, error) {
	current, err := s.RebasePlan(ctx, path, req.Base)
	if err != nil {
		return nil, err
	}
	if req.Head != current.Head || len(req.Items) != len(current.Items) {
		return nil, fmt.Errorf("%w: history changed; reload the plan", ErrInvalidRef)
	}
	valid := map[string]bool{}
	for _, item := range current.Items {
		valid[item.SHA] = true
	}
	for _, item := range req.Items {
		if !valid[item.SHA] {
			return nil, fmt.Errorf("%w: the plan contains an unrelated commit", ErrInvalidRef)
		}
	}
	if _, err := rebaseTodo(req.Items); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRef, err)
	}
	planPath, err := s.rebasePlanPath(ctx, path)
	if err != nil {
		return nil, err
	}
	// O_EXCL refuses a stale plan rather than overwriting another operation.
	f, err := os.OpenFile(planPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, fmt.Errorf("cannot create rebase plan: %w", err)
	}
	if o, ok := hostexec.OwnerOf(path); ok {
		err = f.Chown(int(o.UID), int(o.GID))
	}
	if err == nil {
		err = json.NewEncoder(f).Encode(req)
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(planPath)
		return nil, err
	}
	recovery := "jd-before-rebase-" + time.Now().UTC().Format("20060102-150405.000000000")
	if _, err := s.run(ctx, path, "branch", recovery, current.Head); err != nil {
		_ = os.Remove(planPath)
		return nil, err
	}
	res, err := s.rebaseCommand(ctx, path, planPath, "--interactive", "--keep-empty", "--empty=keep", "--no-autosquash", "--no-update-refs", "--onto", current.Base, current.Base)
	if res != nil {
		res.Output = "Recovery branch: " + recovery + "\n" + res.Output
	}
	return res, err
}

func (s *Service) rebaseCommand(ctx context.Context, path, planPath string, args ...string) (*Result, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	// Git invokes its editor through a shell. Only this server-controlled
	// executable and fixed mode reach that command; all operator text is JSON.
	quoted := "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'"
	env := map[string]string{"GIT_SEQUENCE_EDITOR": quoted + " --git-editor sequence", "GIT_EDITOR": quoted + " --git-editor message", "JD_GIT_REBASE_PLAN": planPath}
	out, runErr := s.executeEnv(ctx, path, 2*time.Minute, "", env, append([]string{"rebase"}, args...)...)
	if s.operationInProgress(ctx, path) != "rebase" {
		_ = os.Remove(planPath)
	}
	res := &Result{Command: "git rebase", Output: strings.TrimSpace(string(out)), OK: runErr == nil}
	if runErr != nil {
		return res, fmt.Errorf("git rebase: %s", res.Output)
	}
	return res, nil
}

// RunEditor is the private non-server entrypoint used by Git's sequencer.
// It writes only the fixed Git editor targets under the plan's metadata dir.
func RunEditor(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("invalid Git editor invocation")
	}
	planPath := os.Getenv("JD_GIT_REBASE_PLAN")
	if !filepath.IsAbs(planPath) || filepath.Base(planPath) != "jd-rebase-plan.json" {
		return fmt.Errorf("no rebase plan")
	}
	data, err := os.ReadFile(planPath)
	if err != nil {
		return err
	}
	if len(data) > 8<<20 {
		return fmt.Errorf("rebase plan too large")
	}
	var plan RebasePlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return err
	}
	todo, err := rebaseTodo(plan.Items)
	if err != nil {
		return err
	}
	target, err := filepath.Abs(args[1])
	if err != nil {
		return err
	}
	dir := filepath.Dir(planPath)
	content := ""
	switch args[0] {
	case "sequence":
		if target != filepath.Join(dir, "rebase-merge", "git-rebase-todo") {
			return fmt.Errorf("unexpected sequence target")
		}
		content = todo
	case "message":
		if target != filepath.Join(dir, "COMMIT_EDITMSG") {
			return fmt.Errorf("unexpected message target")
		}
		done, err := os.ReadFile(filepath.Join(dir, "rebase-merge", "done"))
		if err != nil {
			return err
		}
		lines := nonEmptyLines(string(done))
		if len(lines) == 0 {
			return fmt.Errorf("no current rebase item")
		}
		fields := strings.Fields(lines[len(lines)-1])
		if len(fields) < 2 {
			return fmt.Errorf("invalid current item")
		}
		if fields[0] != "reword" {
			return nil
		}
		for _, item := range plan.Items {
			if strings.HasPrefix(item.SHA, fields[1]) && item.Action == "reword" {
				content = item.Message + "\n"
				break
			}
		}
		if content == "" {
			return fmt.Errorf("no message for current commit")
		}
	default:
		return fmt.Errorf("unknown editor mode")
	}
	info, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("editor target must be a regular file")
	}
	return os.WriteFile(target, []byte(content), 0600)
}
