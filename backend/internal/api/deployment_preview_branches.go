package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/docker/docker/errdefs"
)

// The Databases owner's side of a preview's database branches
// (deploy/preview_branches.go).
//
// A branch is restored through the migration rehearsal's own path
// (prepareCopy): the production server's container configuration on a fresh
// volume, archiving off, none of production's ports, labels or writable
// mounts, from the newest point-in-time archive or native dump. Unlike a
// rehearsal's copy it joins the preview's own database network — never
// production's or any other environment's — under a name of its own,
// db-<connection>-branch.jd.internal, and the application's account is
// given a password made for it, so the address the preview's
// ${{database.N…}} resolves to carries no password production accepts.
// Chosen columns are masked before anything but the restore has read it.
// It carries the rehearsal label with "branch-<id>", so the rehearsal's
// sweep at start finds it like any other copy and keeps it while it is
// ready, and the preview's labels, so its removal takes it with the rest.

// previewBranchLabel marks what a branch makes with the branch's id.
const previewBranchLabel = "io.just-dashboard.preview-branch"

func previewBranchContainer(environmentID, branchID int64) string {
	return fmt.Sprintf("jd-preview-e%d-branch%d-db", environmentID, branchID)
}

func previewBranchRehearsal(branchID int64) string {
	return "branch-" + strconv.FormatInt(branchID, 10)
}

func previewBranchLabels(environmentID, branchID int64) map[string]string {
	return map[string]string{
		"io.just-dashboard.managed":        "true",
		"io.just-dashboard.preview":        "true",
		"io.just-dashboard.environment-id": strconv.FormatInt(environmentID, 10),
		rehearsalLabel:                     previewBranchRehearsal(branchID),
		previewBranchLabel:                 strconv.FormatInt(branchID, 10),
	}
}

// previewBranchOwner restores the branches previews were given, one at a
// time, and removes each with its preview.
type previewBranchOwner struct {
	server    *Server
	rehearser *migrationRehearser
	poke      chan struct{}
	busy      chan struct{}
	cancel    context.CancelFunc
	done      chan struct{}
	every     time.Duration
	restores  sync.WaitGroup
}

func newPreviewBranchOwner(s *Server, rehearser *migrationRehearser) *previewBranchOwner {
	return &previewBranchOwner{server: s, rehearser: rehearser, poke: make(chan struct{}, 1), busy: make(chan struct{}, 1), every: 10 * time.Second}
}

// Poke asks for a pass now: a branch just asked for starts without waiting.
func (o *previewBranchOwner) Poke() {
	if o == nil {
		return
	}
	select {
	case o.poke <- struct{}{}:
	default:
	}
}

func (o *previewBranchOwner) Start(ctx context.Context) {
	if o == nil || o.cancel != nil {
		return
	}
	ctx, o.cancel = context.WithCancel(ctx)
	o.done = make(chan struct{})
	go func() {
		defer close(o.done)
		ticker := time.NewTicker(o.every)
		defer ticker.Stop()
		for {
			o.pass(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-o.poke:
			}
		}
	}()
}

func (o *previewBranchOwner) Stop() {
	if o == nil || o.cancel == nil {
		return
	}
	o.cancel()
	<-o.done
	o.restores.Wait()
	o.cancel = nil
}

// SettleInterrupted ends every branch a previous process was restoring; what
// it made is the rehearsal sweep's to remove, which runs next. The preview's
// next run asks for the branch again.
func (o *previewBranchOwner) SettleInterrupted(ctx context.Context) {
	store := o.server.modules.deployRuns
	branches, err := store.PreviewBranchesInState(ctx, deploy.PreviewBranchRestoring)
	if err != nil {
		return
	}
	for _, branch := range branches {
		if ended, err := store.EndPreviewBranch(ctx, branch.ID, deploy.PreviewBranchFailed, "the dashboard restarted while the branch was being restored; refresh it from the preview"); err == nil {
			o.record(ctx, "deploy.preview_branch.finish", *ended, false, "interrupted by a restart")
		}
	}
}

// livePreviewBranches are the rehearsal-label values of branches a preview
// holds, which the sweep at start leaves.
func (s *Server) livePreviewBranches(ctx context.Context) map[string]bool {
	keep := map[string]bool{}
	if s.modules.deployRuns == nil {
		return keep
	}
	branches, err := s.modules.deployRuns.PreviewBranchesInState(ctx, deploy.PreviewBranchReady, deploy.PreviewBranchWaiting)
	if err != nil {
		return keep
	}
	for _, branch := range branches {
		keep[previewBranchRehearsal(branch.ID)] = true
	}
	return keep
}

func (o *previewBranchOwner) pass(ctx context.Context) {
	store := o.server.modules.deployRuns
	if waiting, err := store.PreviewBranchesInState(ctx, deploy.PreviewBranchWaiting); err == nil {
		for _, branch := range waiting {
			if ctx.Err() != nil {
				return
			}
			select {
			case o.busy <- struct{}{}:
			default:
				continue
			}
			claimed, err := store.ClaimPreviewBranch(ctx, branch.ID)
			if err != nil || !claimed {
				<-o.busy
				continue
			}
			o.restores.Add(1)
			go func(branch deploy.PreviewBranch) {
				defer o.restores.Done()
				defer func() { <-o.busy }()
				o.restore(ctx, branch)
				o.Poke()
			}(branch)
		}
	}
	o.sweepOrphans(ctx)
}

// sweepOrphans removes what a branch made whose row no longer says it is
// waiting, restoring or ready: one ended while its restore was finishing, or
// one whose preview went with its project.
func (o *previewBranchOwner) sweepOrphans(ctx context.Context) {
	docker := o.server.modules.docker
	if docker == nil {
		return
	}
	containers, err := docker.ListContainersWithLabels(ctx, map[string]string{"io.just-dashboard.managed": "true"})
	if err != nil {
		return
	}
	volumes, _ := docker.ListVolumes(ctx)
	ids := map[int64]bool{}
	for _, item := range containers {
		if id, err := strconv.ParseInt(item.Labels[previewBranchLabel], 10, 64); err == nil && id > 0 {
			ids[id] = true
		}
	}
	for _, volume := range volumes {
		if id, err := strconv.ParseInt(volume.Labels[previewBranchLabel], 10, 64); err == nil && id > 0 {
			ids[id] = true
		}
	}
	for id := range ids {
		branch, err := o.server.modules.deployRuns.PreviewBranch(ctx, id)
		if err == nil && deploy.PreviewBranchActive(branch.State) {
			continue
		}
		if err != nil && !errors.Is(err, deploy.ErrPreviewBranchNotFound) {
			continue
		}
		if err := o.rehearser.removeRehearsals(ctx, map[string]string{rehearsalLabel: previewBranchRehearsal(id)}, nil); err != nil {
			o.server.Log.Warn("a database branch's leftovers could not be removed yet", "branch", id, "err", err)
		}
	}
}

// previewBranchSecret is a branch's sign-in, sealed in its row: the account
// the application's URL names, with the password made for the branch.
type previewBranchSecret = dataCopySecret

// restore makes one branch. However it ends, the branch is either ready or
// ended with its reason and everything it made removed.
func (o *previewBranchOwner) restore(ctx context.Context, branch deploy.PreviewBranch) {
	s := o.server
	store := s.modules.deployRuns
	started := time.Now()
	fail := func(reason string) {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		_ = o.rehearser.removeRehearsals(cleanup, map[string]string{rehearsalLabel: previewBranchRehearsal(branch.ID)}, nil)
		if ended, err := store.EndPreviewBranch(cleanup, branch.ID, deploy.PreviewBranchFailed, reason); err == nil {
			o.record(cleanup, "deploy.preview_branch.finish", *ended, false, reason)
		}
	}
	// The restore limits are the previewed environment's rehearsal terms:
	// the operator's word on how long a copy of this database may take.
	policy, err := store.RehearsalPolicy(ctx, branch.SourceEnvironmentID)
	if err != nil {
		fail("the environment's restore limits could not be read")
		return
	}
	network, err := s.modules.deployDatabases.network(ctx, branch.EnvironmentID, true)
	if err != nil {
		fail("the preview's database network could not be made: " + err.Error())
		return
	}
	container, host := previewBranchContainer(branch.EnvironmentID, branch.ID), deploy.PreviewBranchHost(branch.ConnectionID)
	restoreCtx, cancel := context.WithTimeout(ctx, time.Duration(policy.RestoreTimeoutSeconds)*time.Second)
	defer cancel()
	last := ""
	made, err := o.rehearser.prepareCopy(restoreCtx, copyPlan{
		connectionID: branch.ConnectionID, container: container, network: network.Name, aliases: []string{host},
		labels: previewBranchLabels(branch.EnvironmentID, branch.ID), maxBytes: int64(policy.MaxBackupMB) << 20, latest: true,
	}, func(line deploy.BuildLog) error {
		if strings.TrimSpace(line.Text) != "" {
			last = line.Text
		}
		return nil
	})
	if err != nil {
		reason := strings.TrimPrefix(err.Error(), deploy.ErrRehearsalUnavailable.Error()+": ")
		if ctx.Err() != nil {
			reason = "the dashboard stopped while the branch was being restored; refresh it from the preview"
		} else if restoreCtx.Err() != nil {
			reason = fmt.Sprintf("the branch was not restored within %d seconds (the environment's restore limit)", policy.RestoreTimeoutSeconds)
		} else if last != "" && !strings.Contains(reason, last) && len(last) < 300 {
			reason += " (" + last + ")"
		}
		fail(reason)
		return
	}
	// Masked before any account the preview knows opens the copy: until the
	// application's account is given its password below, only production's
	// passwords sign in to it, and the preview holds neither. A refreshed
	// branch's preview already holds its address and password, so the
	// order is what keeps it from reading a column before it is masked.
	var maskedRows int64
	if len(branch.MaskedColumns) > 0 {
		port, err := applicationPort(ctx, s, made)
		if err == nil {
			maskedRows, err = o.mask(restoreCtx, branch, made, network.Name, port)
		}
		if err != nil {
			fail("the branch's columns could not be masked, so it was not kept: " + err.Error())
			return
		}
	}
	// A refresh carries the sign-in over, so the preview's address — its
	// host and its password — is what it was before.
	password := ""
	if sealed, err := store.PreviewBranchSecret(ctx, branch.ID); err == nil && sealed != "" {
		if plain, err := s.Sealer.Open(sealed); err == nil {
			var previous previewBranchSecret
			if json.Unmarshal([]byte(plain), &previous) == nil && previous.User == made.info.User {
				password = previous.Password
			}
		}
	}
	if password == "" {
		if password, err = dataCopyPassword(); err != nil {
			fail("a password for the branch could not be made")
			return
		}
	}
	if err := o.rehearser.setCopyPassword(restoreCtx, made, password); err != nil {
		fail("the branch could not be given a sign-in of its own: " + err.Error())
		return
	}
	appURL, port, err := dataCopyURL(ctx, s, made, host, password)
	if err != nil {
		fail("the branch's address could not be written: " + err.Error())
		return
	}
	// The copy runs production's server configuration, so its superuser
	// signs in with production's password over the network unless that is
	// changed too; the dashboard reaches the copy through the server's own
	// socket, which the change leaves alone.
	if made.engine.user != made.info.User {
		if err := o.rehearser.lockSuperuser(restoreCtx, made); err != nil {
			fail("the branch's superuser could not be given a password of its own: " + err.Error())
			return
		}
	}
	raw, _ := json.Marshal(previewBranchSecret{User: made.info.User, Password: password, URL: appURL})
	sealed, err := s.Sealer.Seal(string(raw))
	if err != nil {
		fail("the branch's sign-in could not be sealed")
		return
	}
	ready, err := store.PreviewBranchReady(context.WithoutCancel(ctx), branch.ID, deploy.PreviewBranchReadiness{
		Container: container, Network: network.Name, Host: host, Port: port, Database: made.info.Database,
		Engine: made.engine.name, SecretSealed: sealed, Source: made.copy.Source, RecoveredTo: timePointer(made.recoveredTo),
		MaskedRows: maskedRows, RestoreMS: time.Since(started).Milliseconds(),
	})
	if err != nil {
		fail("its preview was removed before its restore finished")
		return
	}
	o.record(ctx, "deploy.preview_branch.finish", *ready, true, "")
}

func timePointer(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	at = at.UTC()
	return &at
}

// Remove removes what a branch made and ends it, in that order, so a branch
// that reads removed is gone.
func (o *previewBranchOwner) Remove(ctx context.Context, branch deploy.PreviewBranch, reason, actor string) (*deploy.PreviewBranch, error) {
	if !deploy.PreviewBranchActive(branch.State) {
		return nil, deploy.ErrPreviewBranchEnded
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	removeErr := o.rehearser.removeRehearsals(cleanup, map[string]string{rehearsalLabel: previewBranchRehearsal(branch.ID)}, nil)
	ended, err := o.server.modules.deployRuns.EndPreviewBranch(cleanup, branch.ID, deploy.PreviewBranchRemoved, reason)
	if err != nil {
		return nil, err
	}
	if actor == "system" {
		o.record(cleanup, "deploy.preview_branch.remove", *ended, removeErr == nil, reason)
	}
	return ended, removeErr
}

// removeEnvironmentBranches removes every branch a preview holds, before its
// database network is removed: a branch is an endpoint on that network the
// removal would otherwise refuse as somebody's application.
func (o *previewBranchOwner) removeEnvironmentBranches(ctx context.Context, environmentID int64) error {
	if o == nil {
		return nil
	}
	branches, err := o.server.modules.deployRuns.PreviewBranchesInState(ctx, deploy.PreviewBranchWaiting, deploy.PreviewBranchRestoring, deploy.PreviewBranchReady)
	if err != nil {
		return err
	}
	failures := []error{}
	for _, branch := range branches {
		if branch.EnvironmentID != environmentID {
			continue
		}
		if _, err := o.Remove(ctx, branch, "its preview was removed", "system"); err != nil && !errors.Is(err, deploy.ErrPreviewBranchEnded) {
			failures = append(failures, err)
		}
	}
	// What a branch ended before this left is found by its label.
	if docker := o.server.modules.docker; docker != nil {
		containers, err := docker.ListContainersWithLabels(ctx, map[string]string{
			"io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": strconv.FormatInt(environmentID, 10),
		})
		if err == nil {
			for _, item := range containers {
				if id, err := strconv.ParseInt(item.Labels[previewBranchLabel], 10, 64); err == nil && id > 0 {
					failures = append(failures, o.rehearser.removeRehearsals(ctx, map[string]string{rehearsalLabel: previewBranchRehearsal(id)}, nil))
				}
			}
		}
	}
	return errors.Join(failures...)
}

func (o *previewBranchOwner) record(ctx context.Context, action string, branch deploy.PreviewBranch, success bool, reason string) {
	detail, _ := json.Marshal(map[string]any{
		"deploymentId": branch.ProjectID, "environmentId": branch.EnvironmentID, "connectionId": branch.ConnectionID,
		"state": branch.State, "source": branch.Source.Kind, "maskedColumns": branch.MaskedColumns, "reason": reason,
		"container": branch.Container,
	})
	o.server.Audit.Record(ctx, audit.Entry{Actor: "system", Action: action, Target: strconv.FormatInt(branch.ID, 10), Success: success, Detail: string(detail)})
}

// previewBranchURL is what a preview's reference to a database it holds a
// branch of resolves to: the branch's own address, once it is ready. A
// branch still being restored is waited for, within the previewed
// environment's restore limit, since the preview's run asked for it at the
// same moment it asked for the build.
func (s *Server) previewBranchURL(ctx context.Context, environmentID, connectionID int64) (string, bool, error) {
	store := s.modules.deployRuns
	branch, err := store.ActivePreviewBranch(ctx, environmentID, connectionID)
	if errors.Is(err, deploy.ErrPreviewBranchNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", true, err
	}
	if branch.State != deploy.PreviewBranchReady {
		s.modules.previewBranches.Poke()
		limit := 10 * time.Minute
		if policy, err := store.RehearsalPolicy(ctx, branch.SourceEnvironmentID); err == nil {
			limit = time.Duration(policy.RestoreTimeoutSeconds)*time.Second + time.Minute
		}
		wait, cancel := context.WithTimeout(ctx, limit)
		defer cancel()
		for branch.State != deploy.PreviewBranchReady {
			if !deploy.PreviewBranchActive(branch.State) {
				return "", true, fmt.Errorf("the database branch of %s could not be made: %s", branch.Name, branch.Reason)
			}
			select {
			case <-wait.Done():
				return "", true, fmt.Errorf("the database branch of %s was not ready in time", branch.Name)
			case <-time.After(time.Second):
			}
			if branch, err = store.PreviewBranch(wait, branch.ID); err != nil {
				return "", true, err
			}
		}
	}
	sealed, err := store.PreviewBranchSecret(ctx, branch.ID)
	if err != nil {
		return "", true, err
	}
	plain, err := s.Sealer.Open(sealed)
	if err != nil {
		return "", true, errors.New("the database branch's sign-in could not be opened")
	}
	var secret previewBranchSecret
	if err := json.Unmarshal([]byte(plain), &secret); err != nil || secret.URL == "" {
		return "", true, errors.New("the database branch's sign-in is malformed")
	}
	return secret.URL, true, nil
}

// previewBranchNetwork is the preview's database network a ready branch of
// the connection is on, for the preview's containers to join.
func (o *deploymentDatabaseNetworks) previewBranchNetwork(ctx context.Context, environmentID, connectionID int64) (string, error) {
	branch, err := o.server.modules.deployRuns.ActivePreviewBranch(ctx, environmentID, connectionID)
	if err != nil {
		return "", fmt.Errorf("the preview holds no database branch of connection %d", connectionID)
	}
	if branch.State != deploy.PreviewBranchReady {
		return "", fmt.Errorf("the database branch of %s is not ready", branch.Name)
	}
	network, err := o.network(ctx, environmentID, false)
	if err != nil || network.Name != branch.Network {
		return "", fmt.Errorf("the database branch of %s is not on the preview's database network", branch.Name)
	}
	if docker := o.server.modules.docker; docker != nil {
		if _, err := docker.Inspect(ctx, branch.Container); errdefs.IsNotFound(err) {
			return "", fmt.Errorf("the database branch of %s is gone; refresh it from the preview", branch.Name)
		}
	}
	return network.Name, nil
}

// lockSuperuser gives the copy's superuser a password nobody keeps, for
// every way in over the network: production's no longer opens the branch,
// and the socket the dashboard reaches it through is left as it was.
func (m *migrationRehearser) lockSuperuser(ctx context.Context, made *madeCopy) error {
	password, err := dataCopyPassword()
	if err != nil {
		return err
	}
	info := *made.info
	info.User = made.engine.user
	superuser := *made
	superuser.info = &info
	err = m.setCopyPassword(ctx, &superuser, password)
	// A superuser with no account the network signs in to has nothing to lock.
	if err != nil && strings.Contains(err.Error(), "has no account the network signs in to") {
		return nil
	}
	return err
}

// applicationPort is the port the database's application URL names, which
// the copy listens on as production does.
func applicationPort(ctx context.Context, s *Server, made *madeCopy) (int, error) {
	_, port, err := dataCopyURL(ctx, s, made, "localhost", "unused")
	return port, err
}
