package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/Wayy01/Just-Dashboard/backend/internal/assistant"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/githubrepos"
	"github.com/Wayy01/Just-Dashboard/backend/internal/gitx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/mcp"
	"github.com/Wayy01/Just-Dashboard/backend/internal/term"
)

// The assistant working on code (assistant/workspace.go): a deployment's
// repository cloned into a workspace of its own on the server, its files read and
// changed, and what changed committed and pushed.
//
// The checkout is made by Local Git's service under its roots, as the folder's
// owner: the terminal's account, whose home holds the workspaces, so run_command
// builds and tests in it as that account and nothing in it is left owned by
// root. The files are read and written through the Files routes as the person
// (s.assistantDispatch), so their role, the Files page's roots and kept versions,
// and the audit log apply. A credential of the dashboard's reaches git only in
// its environment (deploy.GitHeaderEnvironment), never its arguments or a file,
// and only a git of the dashboard's own, in its relay for that remote
// (gitx.Relay), never one of the checkout's account or in the checkout, whose
// configuration that account writes: for a push, a token of the person's own
// GitHub connection that can write that one repository, minted for the push and
// revoked after it; or the deployment's own saved token when it can push. With
// neither, the account's own Git credentials push, as the account. What git
// says, and every file, is masked before the model or the page is sent it.

// workspaceFolder is the folder in the terminal account's home that holds the
// assistant's workspaces.
const workspaceFolder = "jd-workspaces"

// workspaceMarker, inside a workspace's .git, names the deployment and the
// environment it was opened for, so a push in a later press knows whose it is.
const workspaceMarker = "jd-deployment"

// workspaceDiffLines bounds a change's diff.
const workspaceDiffLines = 160

// agentWorkspace is a workspace as the tools find it.
type agentWorkspace struct {
	folder  string
	source  *deploy.WorkspaceSource
	summary *deploy.DeploymentSummary
}

// openWorkspace makes, or fetches again, a deployment's workspace.
func (pr *agentPress) openWorkspace(ctx context.Context, p *httpx.Principal, in assistant.WorkspaceInput) agentResult {
	s := pr.s
	refuse := func(why string) agentResult { return agentResult{text: why, said: clipText(why, 300)} }
	if !p.Can(auth.CapServiceControl) {
		return refuse(fmt.Sprintf("Working on a deployment's code takes %s, which the person's role does not have.", auth.CapServiceControl))
	}
	if s.modules.git == nil || s.modules.deployChecker == nil {
		return refuse("Local Git is not available on this install, so no workspace can be made.")
	}
	summary := pr.scope.deployment
	if in.Deployment > 0 && (summary == nil || summary.ID != in.Deployment) {
		other, err := s.agentDeployment(ctx, p, in.Deployment)
		if err != nil {
			return refuse(err.Error())
		}
		summary = other
	}
	if summary == nil {
		return refuse("open_workspace works on one deployment's code: name it with deployment, its id as your list gives it.")
	}
	source, err := s.modules.deployChecker.WorkspaceSource(ctx, summary.ID, summary.EnvironmentID)
	if err != nil {
		return refuse(deploy.SourceReadFailure(err))
	}
	masker, err := pr.masker(ctx)
	if err != nil {
		return refuse("The secret values to mask could not be read, so nothing was done.")
	}
	ws := &agentWorkspace{source: source, summary: summary}
	var opened string
	if source.LocalPath != "" {
		folder, err := s.workspaceGit().Resolve(source.LocalPath)
		if err != nil {
			return refuse(fmt.Sprintf("The deployment builds from %s, which Local Git cannot open: %s", source.LocalPath, agentMaskText(err.Error(), masker)))
		}
		ws.folder, opened = folder, "This deployment builds from this folder on the server, so you work in it directly; it builds what is committed."
	} else {
		ws.folder, opened, err = pr.cloneWorkspace(ctx, p, ws, in.Branch, masker)
		if err != nil {
			return refuse(agentMaskText(err.Error(), masker))
		}
	}
	pr.git = append(pr.git, "open "+ws.folder)
	s.markWorkspace(ws)
	text := s.describeWorkspace(ctx, p, ws, opened, masker)
	return agentResult{text: text, ok: true, said: "Ready in " + ws.folder, output: clipText(text, agentOutputBytes),
		change: &agentChange{Label: filepath.Base(ws.folder), Href: "/git?repo=" + url.QueryEscape(ws.folder)}}
}

// cloneWorkspace clones the deployment's repository into its workspace, or
// fetches the one already there. With a credential of the dashboard's (the
// deployment's own), the network is the relay's (gitx.Relay): git runs with it
// only as the dashboard, in a repository of its own, and the checkout's account
// takes the objects from there; without one it is the account's own fetch.
func (pr *agentPress) cloneWorkspace(ctx context.Context, p *httpx.Principal, ws *agentWorkspace, branch string, masker *deploy.DiagnosisMasker) (string, string, error) {
	s := pr.s
	parent, err := s.workspaceParent()
	if err != nil {
		return "", "", err
	}
	source := ws.source
	if branch == "" {
		branch = source.Ref
	}
	env, _ := s.fetchCredential(ctx, p, source)
	var relay *gitx.Relay
	if env != nil {
		if relay, err = s.workspaceRelay(ctx, source.Remote); err != nil {
			return "", "", fmt.Errorf("The workspace could not be made: %v", err)
		}
	}
	for _, name := range workspaceNames(ws.summary) {
		target := filepath.Join(parent, name)
		if _, err := os.Lstat(target); err == nil {
			folder, err := s.workspaceGit().Resolve(target)
			// Another repository, or this one opened for another deployment
			// whose name reads the same: each keeps its own.
			// A folder that is not the workspaces folder's account's is not
			// one of its workspaces: git there would run as someone else.
			if err != nil || !sameOwner(folder, parent) || !sameRemote(s.workspaceGit().OriginURL(ctx, folder), source.Remote) || !workspaceMarkedFor(folder, ws.summary) {
				continue
			}
			unlock := s.workspaceGit().Lock(folder)
			defer unlock()
			if relay != nil {
				_, err = s.workspaceGit().FetchThrough(ctx, relay, folder, env)
			} else {
				_, err = s.workspaceGit().FetchOrigin(ctx, folder)
			}
			if err != nil {
				return "", "", fmt.Errorf("The workspace %s is there, but fetching it failed: %v", folder, err)
			}
			said := "The workspace was already there; it was fetched again, and what was changed in it is kept."
			if branch != "" {
				if current, _ := s.workspaceGit().CurrentBranch(ctx, folder); current != branch {
					if _, err := s.workspaceGit().Checkout(ctx, folder, branch); err != nil {
						said += fmt.Sprintf(" It stays on %s: checking out %s failed (%v).", current, branch, err)
					}
				}
			}
			return folder, said, nil
		}
		if relay != nil {
			folder, err := s.workspaceGit().CloneThrough(ctx, relay, parent, name, gitx.RelayCloneOptions{Branch: branch}, env)
			if err != nil {
				return "", "", fmt.Errorf("Cloning %s failed: %v", source.Remote, err)
			}
			return folder, "The repository was cloned into a new workspace.", nil
		}
		plan, err := s.workspaceGit().PrepareClone(parent, source.Remote, name, gitx.CloneOptions{Branch: branch})
		if err != nil {
			return "", "", fmt.Errorf("The workspace could not be made: %v", err)
		}
		var last string
		if err := s.workspaceGit().CloneStreaming(ctx, plan, nil, func(stream, text string) {
			if strings.TrimSpace(text) != "" {
				last = strings.TrimSpace(text)
			}
		}); err != nil {
			why := err.Error()
			if last != "" && !strings.Contains(why, last) {
				why += " (" + last + ")"
			}
			return "", "", fmt.Errorf("Cloning %s failed: %s", source.Remote, why)
		}
		return plan.Target, "The repository was cloned into a new workspace.", nil
	}
	return "", "", fmt.Errorf("%s holds other folders of that name; there is no free name for a workspace", parent)
}

// gitRelayFolder, in the dashboard's data folder, holds the relays a
// credentialed clone, fetch or push goes through (a workspace's, and the GitHub
// page's clone of a private repository), and gitconfig, the only Git settings
// they read.
const gitRelayFolder = "workspace-git"

// workspaceGit is Local Git's service as the workspaces use it: never git as
// root in a folder an account could have pointed elsewhere (gitx.ForAccounts),
// since a workspace is in the terminal account's home, which that account can
// swap for a link at any moment.
func (s *Server) workspaceGit() *gitx.Service { return s.modules.git.ForAccounts() }

// workspaceRelay is the dashboard's relay for remote.
func (s *Server) workspaceRelay(ctx context.Context, remote string) (*gitx.Relay, error) {
	return s.workspaceGit().OpenRelay(ctx, filepath.Join(s.Cfg.DataDir, gitRelayFolder), remote)
}

// workspaceParent is the folder that holds the workspaces: in the terminal
// account's home, made and handed to that account when it is not there yet, so
// the checkouts in it are the account's and run_command works in them.
//
// When the dashboard runs as root, a workspace is never root's: git runs as the
// folder's owner, and git run as root in a checkout runs what its .git/config
// names (a filter, a file-system monitor, an SSH command, a pager), so a
// root-owned workspace would make whatever writes that file root. A
// workspace is refused rather than made then, with where to name an account.
func (s *Server) workspaceParent() (string, error) {
	home, uid, gid := s.workspaceHomeOverride, -1, -1
	if home == "" {
		account, accountErr := term.Account{}, errors.New("the terminal is not set up")
		if s.modules.term != nil {
			account, accountErr = s.modules.term.Account()
		}
		var err error
		if home, uid, gid, err = workspaceAccount(os.Geteuid(), account, accountErr); err != nil {
			return "", err
		}
	}
	if home == "" {
		roots := s.workspaceGit().Roots()
		if len(roots) == 0 {
			return "", errors.New("Local Git has no folder to work in (JD_GIT_ROOTS)")
		}
		home = roots[0]
	}
	parent := filepath.Join(home, workspaceFolder)
	if _, err := os.Stat(parent); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return "", fmt.Errorf("the folder for workspaces, %s, could not be made: %v", parent, err)
		}
		if uid >= 0 && os.Geteuid() == 0 {
			// Lchown: the home is the account's, which could put a link where
			// the folder was made before it is handed over.
			_ = os.Lchown(parent, uid, gid)
		}
	}
	if uid >= 0 && os.Geteuid() == 0 {
		info, err := os.Lstat(parent)
		if err != nil {
			return "", err
		}
		if err := workspaceParentOwned(parent, info, uid); err != nil {
			return "", err
		}
	}
	dir, err := s.workspaceGit().ResolveDir(parent)
	if err != nil {
		return "", fmt.Errorf("%s is outside the folders Local Git works in (JD_GIT_ROOTS), so no workspace can be made there", parent)
	}
	return dir, nil
}

// sameOwner is whether two folders belong to one account.
func sameOwner(a, b string) bool {
	ia, errA := os.Lstat(a)
	ib, errB := os.Lstat(b)
	if errA != nil || errB != nil {
		return false
	}
	sa, okA := ia.Sys().(*syscall.Stat_t)
	sb, okB := ib.Sys().(*syscall.Stat_t)
	return okA && okB && sa.Uid == sb.Uid
}

// workspaceAccount is whose home holds the workspaces, for a dashboard running
// as euid: the terminal's account. A dashboard that is not root makes them as
// itself, under the first Git root when there is no account ("" home). A root
// dashboard needs an account that is not root: its workspace would be root's.
func workspaceAccount(euid int, account term.Account, accountErr error) (string, int, int, error) {
	usable := accountErr == nil && account.Home != ""
	switch {
	case usable && (euid != 0 || !account.IsRoot()):
		return account.Home, account.UID, account.GID, nil
	case euid != 0:
		return "", -1, -1, nil
	}
	why := "there is no terminal account"
	if accountErr != nil {
		why = "the terminal account could not be read (" + accountErr.Error() + ")"
	} else if usable {
		why = "the terminal account is root"
	}
	return "", -1, -1, fmt.Errorf("Working on code needs a terminal account that is not root, and %s: a workspace of root's would run git as root on whatever its .git/config names. Set JD_TERMINAL_USER in the dashboard's .env to the server account to work as, and restart the dashboard (the Terminal page says which account it uses)", why)
}

// workspaceParentOwned refuses a workspaces folder that is not the account's:
// one made before, as root or by another account, is never handed over (a tree
// is not chowned in place), so its checkouts would not be the account's.
func workspaceParentOwned(parent string, info os.FileInfo, uid int) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if info.Mode()&os.ModeSymlink == 0 && info.IsDir() && ok && int(st.Uid) == uid {
		return nil
	}
	owner := "another account"
	if ok {
		owner = "uid " + strconv.FormatUint(uint64(st.Uid), 10)
	}
	return fmt.Errorf("%s belongs to %s, not to the terminal account (uid %d), so a workspace in it would not be that account's: hand it to the account (chown -R) or remove it, then try again", parent, owner, uid)
}

var workspaceUnsafe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// workspaceNames are the folders a deployment's workspace may be in, in the
// order they are tried: its name; with its environment's id, when a folder of
// that name holds another repository or was opened for another deployment whose
// name reads the same; and with both ids, which only it has.
func workspaceNames(summary *deploy.DeploymentSummary) []string {
	name := workspaceName(summary)
	env := strconv.FormatInt(summary.EnvironmentID, 10)
	return []string{name, name + "-" + env, name + "-" + strconv.FormatInt(summary.ID, 10) + "-" + env}
}

// workspaceMarkedFor is whether a workspace may be this deployment's: its marker
// names it, or it has none.
func workspaceMarkedFor(folder string, summary *deploy.DeploymentSummary) bool {
	fields := readWorkspaceMarker(folder)
	if len(fields) != 2 {
		return true
	}
	return fields[0] == strconv.FormatInt(summary.ID, 10) && fields[1] == strconv.FormatInt(summary.EnvironmentID, 10)
}

// workspaceName is a deployment's workspace's folder: its name, and its
// environment's when that is not production.
func workspaceName(summary *deploy.DeploymentSummary) string {
	name := summary.Name
	if summary.EnvironmentKind != deploy.EnvironmentProduction && summary.EnvironmentName != "" {
		name += "-" + summary.EnvironmentName
	}
	name = strings.Trim(workspaceUnsafe.ReplaceAllString(strings.ToLower(name), "-"), "-.")
	if name == "" {
		name = "deployment-" + strconv.FormatInt(summary.ID, 10)
	}
	return name
}

// sameRemote compares two addresses of a repository as git would reach it.
func sameRemote(a, b string) bool {
	norm := func(v string) string {
		return strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(v)), "/"), ".git")
	}
	return a != "" && norm(a) == norm(b)
}

// markWorkspace writes which deployment a workspace was opened for into its .git.
// The dashboard writes it as root into a folder another account owns, so no link
// in it is followed: a .git that is a link, or a marker that is one, is left as
// it is (os.Root and O_NOFOLLOW), and the marker is handed to the folder's owner
// through its own descriptor rather than by name.
func (s *Server) markWorkspace(ws *agentWorkspace) {
	root, err := os.OpenRoot(ws.folder)
	if err != nil {
		return
	}
	defer root.Close()
	if info, err := root.Lstat(".git"); err != nil || !info.IsDir() {
		return
	}
	file, err := root.OpenFile(filepath.Join(".git", workspaceMarker), os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() {
		return
	}
	if _, err := fmt.Fprintf(file, "%d %d\n", ws.summary.ID, ws.summary.EnvironmentID); err != nil {
		return
	}
	if info, err := root.Stat("."); err == nil && os.Geteuid() == 0 {
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			_ = file.Chown(int(st.Uid), int(st.Gid))
		}
	}
}

// readWorkspaceMarker reads a workspace's marker without following a link, and
// no more of it than a marker holds.
func readWorkspaceMarker(folder string) []string {
	root, err := os.OpenRoot(folder)
	if err != nil {
		return nil
	}
	defer root.Close()
	file, err := root.OpenFile(filepath.Join(".git", workspaceMarker), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil
	}
	defer file.Close()
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil
	}
	raw := make([]byte, 64)
	n, _ := io.ReadFull(file, raw)
	return strings.Fields(string(raw[:n]))
}

// workspaceOf finds the deployment a folder was opened for, from its marker,
// or the conversation's own deployment when its source is that folder's.
func (s *Server) workspaceOf(ctx context.Context, p *httpx.Principal, folder string, scope *agentScope) *agentWorkspace {
	ws := &agentWorkspace{folder: folder}
	if fields := readWorkspaceMarker(folder); len(fields) == 2 {
		id, _ := strconv.ParseInt(fields[0], 10, 64)
		env, _ := strconv.ParseInt(fields[1], 10, 64)
		if scope.deployment != nil && scope.deployment.ID == id && scope.deployment.EnvironmentID == env {
			ws.summary = scope.deployment
		} else if summary, err := s.modules.deployRuns.EnvironmentDeploymentSummary(ctx, id, env, deploy.QueueBudget{
			Heavy: s.Cfg.DeployHeavySlots, Light: s.Cfg.DeployLightSlots,
		}); err == nil && summary != nil && p.Can(auth.CapServiceControl) {
			ws.summary = summary
		}
	}
	if ws.summary == nil && scope.deployment != nil {
		ws.summary = scope.deployment
	}
	if ws.summary != nil && s.modules.deployChecker != nil {
		if source, err := s.modules.deployChecker.WorkspaceSource(ctx, ws.summary.ID, ws.summary.EnvironmentID); err == nil {
			origin := s.workspaceGit().OriginURL(ctx, folder)
			if (source.LocalPath != "" && filepath.Clean(source.LocalPath) == folder) || sameRemote(origin, source.Remote) {
				ws.source = source
			}
		}
	}
	if ws.source == nil {
		ws.summary = nil
	}
	return ws
}

// describeWorkspace is what open_workspace answers with.
func (s *Server) describeWorkspace(ctx context.Context, p *httpx.Principal, ws *agentWorkspace, opened string, masker *deploy.DiagnosisMasker) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Workspace: %s\n%s\n", ws.folder, opened)
	if ws.source.Remote != "" {
		fmt.Fprintf(&b, "Repository: %s\n", ws.source.Remote)
	}
	if ws.source.Ref != "" {
		fmt.Fprintf(&b, "The deployment builds: %s\n", ws.source.Ref)
	}
	if ws.source.Subdirectory != "" {
		fmt.Fprintf(&b, "It builds the folder %s inside it.\n", filepath.Join(ws.folder, ws.source.Subdirectory))
	}
	if status, err := s.workspaceGit().Status(ctx, ws.folder); err == nil && status.Repo != nil {
		repo := status.Repo
		fmt.Fprintf(&b, "Checked out: %s at %s", repo.Branch, shortCommit(repo.Head))
		if repo.Subject != "" {
			fmt.Fprintf(&b, " (%s)", repo.Subject)
		}
		b.WriteString("\n")
		if repo.Ahead > 0 || repo.Behind > 0 {
			fmt.Fprintf(&b, "Against its remote: %d ahead, %d behind.\n", repo.Ahead, repo.Behind)
		}
		if status.Clean {
			b.WriteString("Changes: none.\n")
		} else {
			lines := []string{}
			for i, change := range status.Files {
				if i == 20 {
					lines = append(lines, fmt.Sprintf("and %d more", len(status.Files)-20))
					break
				}
				lines = append(lines, change.Label+" "+change.Path)
			}
			fmt.Fprintf(&b, "Changes: %s.\n", strings.Join(lines, ", "))
		}
	}
	auth := s.pushAuthFor(ctx, p, ws.source)
	fmt.Fprintf(&b, "A push will authenticate %s.\n", auth.words)
	b.WriteString(s.deploysOnPush(ctx, ws, ""))
	b.WriteString("Read and change its files with read_file, edit_file and write_file by their absolute paths in it; build or test with run_command (cd " + ws.folder + " && …); then push_changes.")
	return agentMaskText(b.String(), masker)
}

func shortCommit(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// pushChanges commits what changed in a workspace and pushes it.
func (pr *agentPress) pushChanges(ctx context.Context, p *httpx.Principal, in assistant.WorkspaceInput) agentResult {
	s := pr.s
	refuse := func(why string) agentResult { return agentResult{text: why, said: clipText(why, 300)} }
	if !p.Can(auth.CapServiceControl) {
		return refuse(fmt.Sprintf("Pushing a deployment's code takes %s, which the person's role does not have.", auth.CapServiceControl))
	}
	if s.modules.git == nil {
		return refuse("Local Git is not available on this install, so nothing can be pushed.")
	}
	masker, err := pr.masker(ctx)
	if err != nil {
		return refuse("The secret values to mask could not be read, so nothing was pushed.")
	}
	folder, err := s.workspaceGit().Resolve(in.Folder)
	if err != nil {
		return refuse(fmt.Sprintf("%s is not a checkout Local Git can work on: %s", in.Folder, agentMaskText(err.Error(), masker)))
	}
	// One mutation of a checkout at a time, as Local Git's own routes take it
	// (gitActionAt): the page's stage or commit cannot land between this stage,
	// commit and push.
	unlock := s.workspaceGit().Lock(folder)
	defer unlock()
	status, err := s.workspaceGit().Status(ctx, folder)
	if err != nil {
		return refuse("Its state could not be read: " + agentMaskText(err.Error(), masker))
	}
	if status.Operation != "" {
		return refuse(fmt.Sprintf("A %s stopped halfway in %s; finish or abort it (run_command with git) before pushing.", status.Operation, folder))
	}
	branch := in.Branch
	if branch == "" {
		if branch, err = s.workspaceGit().CurrentBranch(ctx, folder); err != nil {
			return refuse(fmt.Sprintf("%s is on no branch (its deployment builds a tag or a commit), so it has nowhere to push as it is: name branch to push it to, a new one if need be.", folder))
		}
	}
	ws := s.workspaceOf(ctx, p, folder, pr.scope)
	source := ws.source
	if source == nil {
		source = &deploy.WorkspaceSource{Remote: s.workspaceGit().OriginURL(ctx, folder)}
	}
	auth := s.pushAuthFor(ctx, p, source)
	var b strings.Builder
	if !status.Clean {
		if strings.TrimSpace(in.Message) == "" {
			return refuse("Something is changed in it, and a commit needs a message: say what changed and why in message.")
		}
		if _, err := s.workspaceGit().Stage(ctx, folder, nil); err != nil {
			return refuse("Staging the changes failed: " + agentMaskText(err.Error(), masker))
		}
		stat := s.workspaceGit().DiffStat(ctx, folder)
		name, email := s.commitIdentity(ctx, p, auth)
		if _, err := s.workspaceGit().CommitAs(ctx, folder, in.Message, name, email); err != nil {
			return refuse("Committing failed: " + agentMaskText(err.Error(), masker))
		}
		head, _ := s.workspaceGit().HeadCommit(ctx, folder)
		fmt.Fprintf(&b, "Committed %s", shortCommit(head))
		if stat != "" {
			fmt.Fprintf(&b, " (%s)", stat)
		}
		b.WriteString(".\n")
	} else {
		b.WriteString("Nothing was changed since the last commit, so the branch is pushed as it is.\n")
	}
	env, revoke, err := s.pushEnvironment(ctx, auth)
	if err != nil {
		return refuse("No credential to push with could be made: " + agentMaskText(err.Error(), masker))
	}
	var result *gitx.Result
	if env != nil {
		// A credential of the dashboard's is given to git only in the relay,
		// never in the checkout or as its account.
		var relay *gitx.Relay
		if relay, err = s.workspaceRelay(ctx, auth.remote); err == nil {
			result, err = s.workspaceGit().PushThrough(ctx, relay, folder, in.Branch, env)
		}
	} else {
		result, err = s.workspaceGit().PushOrigin(ctx, folder, in.Branch)
	}
	revoke()
	if err != nil {
		pr.git = append(pr.git, "push "+folder+" to "+branch+" refused")
	} else {
		pr.git = append(pr.git, "push "+folder+" to "+branch)
	}
	output := ""
	if result != nil {
		output = agentMaskText(result.Output, masker)
	}
	if err != nil {
		text := b.String() + fmt.Sprintf("The push to %s, made %s, was refused:\n%s", branch, auth.words, agentMaskText(err.Error(), masker))
		if auth.hint != "" {
			text += "\n" + auth.hint
		}
		return agentResult{text: text, said: "Push refused", output: clipText(text, agentOutputBytes)}
	}
	fmt.Fprintf(&b, "Pushed to %s, %s.\n", branch, auth.words)
	if output != "" && output != "Done." {
		fmt.Fprintf(&b, "%s\n", clipText(output, 2000))
	}
	b.WriteString(s.deploysOnPush(ctx, ws, branch))
	text := b.String()
	return agentResult{text: text, ok: true, said: "Pushed to " + branch, output: clipText(text, agentOutputBytes),
		change: &agentChange{Label: filepath.Base(folder), Href: "/git?repo=" + url.QueryEscape(folder)}}
}

// deploysOnPush says whether a push of branch deploys a workspace's deployment
// on its own.
func (s *Server) deploysOnPush(ctx context.Context, ws *agentWorkspace, branch string) string {
	if ws.summary == nil || ws.source == nil {
		return "No deployment of this dashboard is known to build this checkout, so a push deploys nothing here by itself.\n"
	}
	name := ws.summary.Name
	if ws.source.LocalPath != "" {
		return fmt.Sprintf("%s builds this folder's committed state: start a deploy to run what was committed.\n", name)
	}
	policy, err := s.modules.deployRuns.GitDeploymentPolicy(ctx, ws.summary.ID, ws.summary.EnvironmentID)
	switch {
	case err != nil:
		return fmt.Sprintf("Whether %s deploys a push on its own could not be read: start a deploy if one does not appear among its runs.\n", name)
	case !policy.Automatic || policy.PromotionOnly:
		return fmt.Sprintf("%s does not deploy a push on its own: start a deploy (and follow it) to run it.\n", name)
	case branch != "" && ws.source.Ref != "" && branch != ws.source.Ref:
		return fmt.Sprintf("%s builds %s, not %s, so this push deploys nothing.\n", name, ws.source.Ref, branch)
	case len(policy.WatchInclude) > 0 || len(policy.WatchExclude) > 0:
		return fmt.Sprintf("%s deploys a push to %s on its own, unless its path filters skip the files it changed; a run appears among its runs within its next check.\n", name, ws.source.Ref)
	}
	return fmt.Sprintf("%s deploys a push to %s on its own: a run appears among its runs within its next check of the branch.\n", name, orDefault(ws.source.Ref, "its branch"))
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// pushAuth is how a push will authenticate.
type pushAuth struct {
	words string
	hint  string
	// access is the person's GitHub connection that can push, credentialID the
	// deployment's own credential; neither is the account's own credentials.
	access       *githubrepos.RepoAccess
	credentialID int64
	remote, host string
}

// pushAuthFor chooses how a push to source's repository authenticates: the
// person's own GitHub connection when it can push there, then the deployment's
// own token when it can push, then the account's own credentials.
func (s *Server) pushAuthFor(ctx context.Context, p *httpx.Principal, source *deploy.WorkspaceSource) pushAuth {
	account := "with the server account's own Git credentials (its credential helper or SSH keys)"
	if s.modules.term != nil {
		if a, err := s.modules.term.Account(); err == nil && a.Name != "" {
			account = fmt.Sprintf("with the Git credentials of the server account %s (its credential helper or SSH keys)", a.Name)
		}
	}
	auth := pushAuth{words: account, remote: source.Remote}
	if parsed, err := url.Parse(source.Remote); err == nil && parsed.Scheme == "https" {
		auth.host = parsed.Hostname()
	}
	if owner, name, ok := githubRepository(source.Remote); ok && auth.host != "" && s.modules.githubRepos != nil {
		if access := s.githubPushAccess(ctx, p, owner, name); access != nil {
			auth.access = access
			auth.words = fmt.Sprintf("with a token of your GitHub connection @%s that can only write %s, made for this push and revoked after it", access.Login, access.Repository.FullName)
			return auth
		}
		auth.hint = "To push to GitHub from here, connect a GitHub account that can push to " + owner + "/" + name + " on the GitHub page (its App needs Contents: write), or give the server account Git credentials for it."
	}
	if source.CredentialID > 0 && auth.host != "" && s.modules.deployPlanning != nil {
		if credential, err := s.modules.deployPlanning.WorkspaceCredential(ctx, source.CredentialID, source.Remote); err == nil && !credential.ReadOnly {
			auth.credentialID = source.CredentialID
			auth.words = fmt.Sprintf("with the deployment's saved credential %s", credential.Name)
		}
	}
	return auth
}

// githubPushAccess is one of the person's GitHub connections that can push to
// owner/name through the GitHub page's App.
func (s *Server) githubPushAccess(ctx context.Context, p *httpx.Principal, owner, name string) *githubrepos.RepoAccess {
	connections, err := s.modules.githubRepos.Connections(ctx, p.UserID())
	if err != nil {
		return nil
	}
	for _, connection := range connections {
		access, err := s.modules.githubRepos.Access(ctx, p.UserID(), connection.ID, owner, name)
		if err == nil && access.Repository.Can.Push && access.Installation.Grants("contents", "write") {
			return access
		}
	}
	return nil
}

// pushEnvironment makes the environment one push runs with, and what revokes it.
func (s *Server) pushEnvironment(ctx context.Context, auth pushAuth) (map[string]string, func(), error) {
	switch {
	case auth.access != nil:
		token, revoke, err := s.modules.githubRepos.PushToken(ctx, auth.access)
		if err != nil {
			return nil, func() {}, err
		}
		return deploy.GitHeaderEnvironment(auth.remote, deploy.GitBasicAuthorization(auth.host, token)), revoke, nil
	case auth.credentialID > 0:
		credential, err := s.modules.deployPlanning.WorkspaceCredential(ctx, auth.credentialID, auth.remote)
		if err != nil {
			return nil, func() {}, err
		}
		return credential.Environment, func() {}, nil
	}
	return nil, func() {}, nil
}

// fetchCredential is what a clone or fetch of source authenticates with: the
// deployment's own credential, which is what its deploys fetch with, or else the
// server account's own Git credentials.
func (s *Server) fetchCredential(ctx context.Context, p *httpx.Principal, source *deploy.WorkspaceSource) (map[string]string, string) {
	if source.CredentialID > 0 && s.modules.deployPlanning != nil {
		if credential, err := s.modules.deployPlanning.WorkspaceCredential(ctx, source.CredentialID, source.Remote); err == nil {
			return credential.Environment, "the deployment's credential " + credential.Name
		}
	}
	return nil, "the server account's own Git credentials"
}

var githubRemote = regexp.MustCompile(`^(?:https://github\.com/|git@github\.com:)([A-Za-z0-9-]+)/([A-Za-z0-9._-]+?)(?:\.git)?/?$`)

// githubRepository reads owner/name from a github.com address.
func githubRepository(remote string) (string, string, bool) {
	match := githubRemote.FindStringSubmatch(strings.TrimSpace(remote))
	if match == nil {
		return "", "", false
	}
	return match[1], match[2], true
}

// commitIdentity is who a commit is recorded as when the checkout's account has
// no name for commits: the GitHub account a push goes through, or the person.
func (s *Server) commitIdentity(ctx context.Context, p *httpx.Principal, auth pushAuth) (string, string) {
	if auth.access != nil && auth.access.Login != "" {
		return auth.access.Login, auth.access.Login + "@users.noreply.github.com"
	}
	host := "localhost"
	if s.modules.sys != nil {
		if info, err := s.modules.sys.Host(ctx); err == nil && info != nil && info.Hostname != "" {
			host = info.Hostname
		}
	}
	return p.Username(), p.Username() + "@" + host
}

// fileRead is a file as the Files route reads it.
type fileRead struct {
	files.FileContent
	missing bool
}

// readFileRoute reads a file through the Files route, as the person.
func (pr *agentPress) readFileRoute(ctx context.Context, p *httpx.Principal, path string, masker *deploy.DiagnosisMasker) (*fileRead, *agentResult) {
	rec, err := pr.s.assistantDispatch(ctx, pr.r, p, http.MethodGet, mcp.RouteRequest{Path: "/files/read", Query: url.Values{"path": {path}}})
	if err != nil {
		result := agentResult{text: agentDispatchFailure(err), said: "Not read"}
		return nil, &result
	}
	if rec.status == http.StatusNotFound {
		return &fileRead{missing: true}, nil
	}
	if rec.status >= http.StatusBadRequest {
		refusal := agentRefusal(rec, masker)
		refusal.text = fmt.Sprintf("Reading %s was refused: %s", path, refusal.text)
		return nil, &refusal
	}
	var content files.FileContent
	if err := json.Unmarshal(rec.body.Bytes(), &content); err != nil {
		result := agentResult{text: "The file's answer could not be read.", said: "Not read"}
		return nil, &result
	}
	return &fileRead{FileContent: content}, nil
}

// readFile reads lines of a file, numbered.
func (pr *agentPress) readFile(ctx context.Context, p *httpx.Principal, in assistant.FileInput) agentResult {
	refuse := func(why string) agentResult { return agentResult{text: why, said: clipText(why, 300)} }
	masker, err := pr.masker(ctx)
	if err != nil {
		return refuse("The secret values to mask could not be read, so nothing was read.")
	}
	read, failed := pr.readFileRoute(ctx, p, in.Path, masker)
	switch {
	case failed != nil:
		return *failed
	case read.missing:
		return refuse(fmt.Sprintf("There is no file %s.", in.Path))
	case read.Binary:
		return refuse(fmt.Sprintf("%s is a binary file of %d bytes, not text.", in.Path, read.Size))
	}
	content := agentMaskText(read.Content, masker)
	lines := strings.SplitAfter(content, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	total := len(lines)
	if total == 0 {
		return agentResult{text: in.Path + " is empty.", ok: true, said: "Empty"}
	}
	start := in.Offset
	if start < 1 {
		start = 1
	}
	if start > total {
		return refuse(fmt.Sprintf("%s has %d lines, so there is no line %d.", in.Path, total, start))
	}
	limit := in.Limit
	if limit <= 0 {
		limit = assistant.FileLinesDefault
	}
	end := min(total, start+limit-1)
	var b strings.Builder
	fmt.Fprintf(&b, "%s, %d lines", in.Path, total)
	if start > 1 || end < total {
		fmt.Fprintf(&b, "; lines %d to %d", start, end)
	}
	b.WriteString(":\n")
	for i := start; i <= end; i++ {
		line := strings.TrimRight(lines[i-1], "\r\n")
		if len(line) > 2000 {
			line = clipText(line, 2000) + " (line cut)"
		}
		entry := fmt.Sprintf("%6d\t%s\n", i, line)
		if b.Len()+len(entry) > maxOperatorResult-200 {
			end = i - 1
			fmt.Fprintf(&b, "(the answer was long enough; read on from offset %d)\n", i)
			break
		}
		b.WriteString(entry)
	}
	if end < total && !strings.HasSuffix(b.String(), ")\n") {
		fmt.Fprintf(&b, "(%d more lines; read on from offset %d)\n", total-end, end+1)
	}
	return agentResult{text: b.String(), ok: true, said: fmt.Sprintf("Lines %d–%d of %d", start, end, total)}
}

// editFile replaces text in a file.
func (pr *agentPress) editFile(ctx context.Context, p *httpx.Principal, in assistant.FileInput) agentResult {
	refuse := func(why string) agentResult { return agentResult{text: why, said: clipText(why, 300)} }
	masker, err := pr.masker(ctx)
	if err != nil {
		return refuse("The secret values to mask could not be read, so nothing was changed.")
	}
	read, failed := pr.readFileRoute(ctx, p, in.Path, masker)
	switch {
	case failed != nil:
		return *failed
	case read.missing:
		return refuse(fmt.Sprintf("There is no file %s; write_file makes one.", in.Path))
	case read.Binary:
		return refuse(fmt.Sprintf("%s is a binary file, which edit_file does not change.", in.Path))
	case strings.Contains(in.OldText, maskedMark) && !strings.Contains(read.Content, maskedMark):
		return refuse("old_text holds " + masked + ", a secret masked for you, so it cannot match the file: choose text beside the secret instead.")
	case strings.Contains(in.NewText, maskedMark) && !strings.Contains(read.Content, maskedMark):
		return refuse("new_text holds " + masked + ", a secret masked for you, which would replace the real value: leave that text out of the change.")
	}
	count := strings.Count(read.Content, in.OldText)
	if count == 0 {
		// A file written with CRLF reads back as it is; the model writes LF.
		if crlf := strings.ReplaceAll(in.OldText, "\n", "\r\n"); crlf != in.OldText && strings.Count(read.Content, crlf) > 0 {
			in.OldText, in.NewText = crlf, strings.ReplaceAll(in.NewText, "\n", "\r\n")
			count = strings.Count(read.Content, in.OldText)
		}
	}
	switch {
	case count == 0:
		return refuse(fmt.Sprintf("old_text was not found in %s. Read the file again and copy the text exactly, spaces and all, without the line numbers.", in.Path))
	case count > 1 && !in.ReplaceAll:
		return refuse(fmt.Sprintf("old_text occurs %d times in %s (lines %s). Add lines around it so it occurs once, or set replace_all.", count, in.Path, occurrenceLines(read.Content, in.OldText)))
	}
	next := strings.Replace(read.Content, in.OldText, in.NewText, map[bool]int{true: -1, false: 1}[in.ReplaceAll])
	return pr.saveFile(ctx, p, in.Path, read, next, masker)
}

// masked is how a secret value reaches the model, and maskedMark what every
// mask begins with: a key block reaches it as "[REDACTED PRIVATE KEY]".
const (
	masked     = "[REDACTED]"
	maskedMark = "[REDACTED"
)

// writeFile writes a whole file.
func (pr *agentPress) writeFile(ctx context.Context, p *httpx.Principal, in assistant.FileInput) agentResult {
	refuse := func(why string) agentResult { return agentResult{text: why, said: clipText(why, 300)} }
	masker, err := pr.masker(ctx)
	if err != nil {
		return refuse("The secret values to mask could not be read, so nothing was written.")
	}
	read, failed := pr.readFileRoute(ctx, p, in.Path, masker)
	switch {
	case failed != nil:
		return *failed
	case read.Binary:
		return refuse(fmt.Sprintf("%s is a binary file, which write_file does not replace.", in.Path))
	case strings.Contains(in.Content, maskedMark) && !strings.Contains(read.Content, maskedMark):
		return refuse("content holds " + masked + ", a secret masked for you, which would replace the real value: change the file with edit_file around the secret instead.")
	}
	if read.missing {
		// The folders it needs, made as the Files page makes them, owned as the
		// folder they are made in; one that is there already answers 409.
		parent := filepath.Dir(in.Path)
		made, err := pr.s.assistantDispatch(ctx, pr.r, p, http.MethodPost, mcp.RouteRequest{Path: "/files/mkdir", Body: workspaceBody(map[string]string{"path": parent})})
		if err != nil {
			return refuse(agentDispatchFailure(err))
		}
		if made.status >= http.StatusBadRequest && made.status != http.StatusConflict {
			refusal := agentRefusal(made, masker)
			refusal.text = fmt.Sprintf("Making the folder %s was refused: %s", parent, refusal.text)
			return refusal
		}
	}
	return pr.saveFile(ctx, p, in.Path, read, in.Content, masker)
}

// saveFile writes next over what was read, refusing if the file changed since.
func (pr *agentPress) saveFile(ctx context.Context, p *httpx.Principal, path string, read *fileRead, next string, masker *deploy.DiagnosisMasker) agentResult {
	expected := read.Revision
	if read.missing {
		expected = ""
	}
	if !read.missing && read.Content == next {
		return agentResult{text: path + " already reads that way; nothing was changed.", ok: true, said: "Unchanged"}
	}
	// More masks than the file holds is a masked value about to be written over
	// a real one, even where the file also holds the mask's words as they are.
	if strings.Count(next, maskedMark) > strings.Count(read.Content, maskedMark) {
		return agentResult{text: "The change holds " + masked + ", a secret masked for you, which would replace the real value: change the file around the secret instead.", said: "Not written"}
	}
	rec, err := pr.s.assistantDispatch(ctx, pr.r, p, http.MethodPut, mcp.RouteRequest{Path: "/files/write", Body: workspaceBody(map[string]any{
		"path": path, "content": next, "expectedRevision": expected,
	})})
	if err != nil {
		return agentResult{text: agentDispatchFailure(err), said: "Not written"}
	}
	if rec.status >= http.StatusBadRequest {
		refusal := agentRefusal(rec, masker)
		refusal.text = fmt.Sprintf("Writing %s was refused: %s", path, refusal.text)
		if rec.status == http.StatusConflict {
			refusal.text += " It changed since it was read: read it again and redo the change."
		}
		return refusal
	}
	before := read.Content
	diff := agentMaskText(lineDiff(before, next, 3, workspaceDiffLines), masker)
	verb := "Changed"
	if read.missing {
		verb = "Made"
	}
	text := fmt.Sprintf("%s %s.\n%s", verb, path, diff)
	return agentResult{text: text, ok: true, said: verb + " " + filepath.Base(path), output: clipText(diff, agentOutputBytes)}
}

func workspaceBody(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}

// occurrenceLines names the lines text begins on in content, the first few.
func occurrenceLines(content, text string) string {
	var lines []string
	offset := 0
	for len(lines) < 6 {
		i := strings.Index(content[offset:], text)
		if i < 0 {
			break
		}
		at := offset + i
		lines = append(lines, strconv.Itoa(strings.Count(content[:at], "\n")+1))
		offset = at + max(1, len(text))
		if offset >= len(content) {
			break
		}
	}
	return strings.Join(lines, ", ")
}

// lineDiff is a change as a unified diff of the one region it touches: the lines
// both sides share at the start and at the end are context, and what is between
// them is removed and added. At most bound lines are written.
func lineDiff(before, after string, context, bound int) string {
	a, b := splitLines(before), splitLines(after)
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	start := max(0, prefix-context)
	tail := min(suffix, context)
	aEnd, bEnd := len(a)-suffix+tail, len(b)-suffix+tail
	var out []string
	out = append(out, fmt.Sprintf("@@ -%d,%d +%d,%d @@", start+1, aEnd-start, start+1, bEnd-start))
	for i := start; i < prefix; i++ {
		out = append(out, " "+a[i])
	}
	for i := prefix; i < len(a)-suffix; i++ {
		out = append(out, "-"+a[i])
	}
	for i := prefix; i < len(b)-suffix; i++ {
		out = append(out, "+"+b[i])
	}
	for i := len(a) - suffix; i < aEnd; i++ {
		out = append(out, " "+a[i])
	}
	if len(out) > bound {
		more := len(out) - bound
		out = append(out[:bound], fmt.Sprintf("… %d more lines of the diff", more))
	}
	return strings.Join(out, "\n")
}

func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}
