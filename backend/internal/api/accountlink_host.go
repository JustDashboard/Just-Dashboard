package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/metrics"
	"github.com/Wayy01/Just-Dashboard/backend/internal/selfupdate"
	"github.com/Wayy01/Just-Dashboard/backend/internal/sysinfo"
)

// accountHost gives the account-link connector this dashboard's own services.
// A command from a linked site goes through the same methods the pages call:
// one code path per action, so a site cannot do anything a button could not,
// and a change to what a button does changes it for the site too.
//
// The connector has already verified the command, enforced its scope and will
// audit it. What is added here is the budget the browser's destructive routes
// are under, which a site is under too.
type accountHost struct {
	s *Server

	// The snapshot's rates (network, CPU) are differences between two reads,
	// and the shared collector's reads belong to whoever has a page open, so
	// the connector reads with a collector of its own.
	metrics *sysinfo.Collector
	mu      sync.Mutex
	primed  bool
}

func newAccountHost(s *Server) *accountHost {
	return &accountHost{s: s, metrics: sysinfo.NewCollector()}
}

// allowDestructive spends one of the destructive budget for the actor, the way
// the routes behind s.destructive do for a person.
func (s *Server) allowDestructive(actor string) error {
	if !s.destrLim.Allow(actor) {
		return httpx.Err(http.StatusTooManyRequests, "rate_limited", "too many destructive actions; try again shortly")
	}
	return nil
}

// ---------------------------------------------------------------- the actions --

func (h *accountHost) Container(ctx context.Context, actor, name, action string) (string, error) {
	var act dockerx.LifecycleAction
	var done string
	switch action {
	case accountlink.ContainerStart:
		act, done = dockerx.ActionStart, "started"
	case accountlink.ContainerStop:
		act, done = dockerx.ActionStop, "stopped"
	case accountlink.ContainerRestart:
		act, done = dockerx.ActionRestart, "restarted"
	default:
		return "", fmt.Errorf("container action %q is not one a linked account may take", action)
	}
	if err := h.refuseOwnContainer(ctx, name); err != nil {
		return "", err
	}
	// Stop and restart interrupt a service and are behind the destructive
	// budget on the container page; start is not.
	if act != dockerx.ActionStart {
		if err := h.s.allowDestructive(actor); err != nil {
			return "", err
		}
	}
	if _, err := h.s.runContainerAction(ctx, name, act, nil); err != nil {
		return "", err
	}
	return name + " " + done, nil
}

// RestartStack restarts a compose stack. Restarting does not take the typed
// stack name that taking a stack down does (composeNeedsPhrase is true for
// `down` alone), and the site, which can only ever restart, is held to the same
// rule as the stack page rather than a stricter or a looser one.
func (h *accountHost) RestartStack(ctx context.Context, actor, name string) (string, error) {
	if err := h.refuseOwnStack(ctx, name); err != nil {
		return "", err
	}
	if err := h.s.allowDestructive(actor); err != nil {
		return "", err
	}
	_, res, err := h.s.runStackAction(ctx, actor, name, dockerx.ComposeRestart, "", nil)
	if err != nil {
		return "", err
	}
	// Compose's own output can carry values from the stack's environment, so
	// it stays on this machine.
	if res.ExitCode != 0 {
		return "", fmt.Errorf("docker compose restart of %s exited with code %d", name, res.ExitCode)
	}
	return "restarted " + name, nil
}

func (h *accountHost) RunBackup(ctx context.Context, actor string, jobID int64) (string, error) {
	job, err := h.s.startBackupRun(ctx, actor, jobID)
	if err != nil {
		return "", err
	}
	return "started backup job " + job.Name, nil
}

// InstallUpdate does what the Update now button does, with the same checks: an
// install that cannot update itself, nothing newer, and a version that is no
// longer the newest, which the site is told as version_moved.
func (h *accountHost) InstallUpdate(ctx context.Context, actor, want string) (string, error) {
	rep := h.s.modules.selfUpdate.Report(ctx, selfupdate.Cached)
	if !rep.Install.Supported {
		return "", errors.New("this install cannot update itself in place: " + rep.Install.Reason)
	}
	if !rep.Available {
		if rep.Check.Error != "" {
			return "", errors.New("the newest version could not be looked up: " + rep.Check.Error)
		}
		return "", fmt.Errorf("this dashboard is already on %s, which is the newest published version", rep.Version)
	}
	target := rep.Latest
	if want != "" && selfupdate.Compare(want, target) != 0 {
		return "", fmt.Errorf("%w: the newest version is now %s, not %s", accountlink.ErrVersionMoved, target, want)
	}
	if err := h.s.allowDestructive(actor); err != nil {
		return "", err
	}
	if _, err := h.s.modules.selfUpdate.Install(ctx, target, actor); err != nil {
		switch {
		case errors.Is(err, selfupdate.ErrInProgress):
			return "", errors.New("an update is already running")
		case errors.Is(err, selfupdate.ErrNotNewer):
			return "", fmt.Errorf("%s is not newer than the installed %s", target, rep.Version)
		case errors.Is(err, selfupdate.ErrNoLocation):
			return "", errors.New("this install cannot update itself in place")
		}
		return "", err
	}
	return fmt.Sprintf("updating %s to %s", rep.Version, target), nil
}

func (h *accountHost) RestartDashboard(ctx context.Context, actor string, rebuild bool) (string, error) {
	if err := h.s.allowDestructive(actor); err != nil {
		return "", err
	}
	if _, err := h.s.modules.selfConfig.Restart(ctx, rebuild, actor); err != nil {
		return "", mapSelfConfigError(err)
	}
	if rebuild {
		return "rebuilding and restarting the dashboard", nil
	}
	return "restarting the dashboard", nil
}

func (h *accountHost) UpdateStatus(ctx context.Context) (accountlink.UpdateStatus, error) {
	rep := h.s.modules.selfUpdate.Report(ctx, selfupdate.Cached)
	return accountlink.UpdateStatus{
		Version: rep.Version, Latest: rep.Latest, Available: rep.Available, Breaking: rep.Breaking,
		Supported: rep.Install.Supported, Busy: rep.Run != nil && rep.Run.Status.Live(),
	}, nil
}

// ------------------------------------------------------------ the snapshot --

// Collect reads what a heartbeat reports. Each part is read on its own and a
// part that cannot be read is left out, so a host with no Docker still reports
// its CPU. Names are included here and removed by the connector for a scope
// that does not share them.
func (h *accountHost) Collect(ctx context.Context) (accountlink.Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	s := h.s
	var snap accountlink.Snapshot

	host, hostErr := s.modules.sys.Host(ctx)
	if hostErr == nil {
		snap.Server.Name = host.Hostname
		snap.Server.UptimeSeconds = host.UptimeSeconds
		snap.Server.Kernel = host.KernelVersion
		if host.Platform != "" {
			snap.Server.OS = strings.ToUpper(host.Platform[:1]) + host.Platform[1:]
			if host.PlatformVersion != "" {
				snap.Server.OS += " " + host.PlatformVersion
			}
		}
	}
	snap.Server.Arch = runtime.GOARCH

	rep := s.modules.selfUpdate.Report(ctx, selfupdate.Cached)
	snap.Server.Version = rep.Version
	snap.Server.Latest = rep.Latest
	snap.Server.UpdateAvailable = rep.Available
	snap.Server.UpdateBreaking = rep.Breaking
	snap.Server.UpdateRun = updateRunInfo(rep.Run, time.Now())

	snap.Access = h.access(ctx)
	snap.Metrics = h.metricsInfo(ctx)

	// The lists leave out the dashboard's own containers and stack: a site is
	// refused any command on them (it restarts the dashboard with
	// dashboard.restart, at maintain), so it is not shown a row it cannot act
	// on. The counts are of everything on the host.
	own, ownDir := "", ""
	if list, err := s.modules.docker.ListContainers(ctx, true); err == nil {
		own = s.selfProject(list)
		snap.Containers = containersInfo(list, own)
	}
	if stacks, err := s.modules.docker.ListStacks(ctx, s.Cfg.ComposeRoots); err == nil {
		ownDir = ownStackDir(stacks, own)
		snap.Stacks = stacksInfo(stacks, own, ownDir)
	}
	if subjects, err := s.modules.hostAlerts.Subjects(ctx); err == nil {
		snap.Alerts = alertsInfo(subjects)
	}
	if jobs, err := s.modules.backupStore.List(ctx); err == nil {
		snap.Backups = backupsInfo(jobs)
	}
	if projects, err := s.modules.deployStore.List(ctx); err == nil {
		snap.Deployments = deploymentsInfo(projects)
	}
	if hostErr != nil {
		return snap, hostErr
	}
	return snap, nil
}

// access is how a person reaches this dashboard: a tailnet address, or an SSH
// tunnel to a loopback port. It is read from the settings the dashboard answers
// by, as the Configuration page reads them.
func (h *accountHost) access(ctx context.Context) *accountlink.AccessInfo {
	s := h.s
	rep := s.modules.selfConfig.Report(ctx)
	site, tls := s.Cfg.Site, s.Cfg.TLSMode
	info := &accountlink.AccessInfo{}
	if rep.Supported {
		site, tls = rep.Settings.Site, rep.Settings.TLS
		info.Endpoint, info.Port = rep.Endpoint, rep.Settings.Port
	}
	info.TLS = tls
	// The installer's own rule: a loopback JD_SITE means the SSH tunnel. An
	// empty one is read as localhost, as the Configuration page reads it.
	info.Kind = "tailscale"
	if site == "" || site == "localhost" || site == "127.0.0.1" {
		info.Kind = "tunnel"
	}
	// The tailnet identity is part of the way in only when the way in is the
	// tailnet; a tunnel install does not tell the site about a tailnet it
	// happens to be on.
	if info.Kind == "tailscale" && rep.Tailscale.Available {
		info.Tailscale = &accountlink.TailscaleInfo{Hostname: rep.Tailscale.Hostname, IP4: rep.Tailscale.IP4, Running: rep.Tailscale.Running}
	}
	return info
}

func (h *accountHost) metricsInfo(ctx context.Context) *accountlink.MetricsInfo {
	h.mu.Lock()
	first := !h.primed
	h.primed = true
	h.mu.Unlock()
	if first {
		// Rates are differences, so the first read only sets the baseline.
		_, _ = h.metrics.Collect(ctx)
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			return nil
		}
	}
	snap, err := h.metrics.Collect(ctx)
	if err != nil {
		return nil
	}
	return metricsInfo(snap)
}

func round1(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }

func metricsInfo(snap *sysinfo.Snapshot) *accountlink.MetricsInfo {
	sample := metrics.Reduce(snap)
	info := &accountlink.MetricsInfo{
		CPUPercent: round1(sample.CPUPercent), Load1: sample.Load1, MemPercent: round1(sample.MemPercent),
		MemUsed: sample.MemUsed, MemTotal: sample.MemTotal, SwapPercent: round1(sample.SwapPercent),
		DiskPercent: round1(sample.DiskPercent), NetRx: round1(sample.NetRx), NetTx: round1(sample.NetTx),
		CPUSteal: round1(sample.CPUSteal), PSICPU: round1(sample.PSICPU),
	}
	// The mount behind the diskPercent reading: the fullest one.
	var fullest float64
	for _, m := range snap.Mounts {
		if m.UsedPercent >= fullest && m.Mountpoint != "" {
			fullest, info.DiskMount = m.UsedPercent, m.Mountpoint
		}
	}
	return info
}

func updateRunInfo(run *selfupdate.Run, now time.Time) *accountlink.UpdateRunInfo {
	if run == nil {
		return nil
	}
	// A finished run is worth saying for a few minutes, so the site shows how
	// it ended; the record itself lingers until somebody dismisses it.
	if !run.Status.Live() && (run.FinishedAt == nil || now.Sub(*run.FinishedAt) > 10*time.Minute) {
		return nil
	}
	return &accountlink.UpdateRunInfo{Status: string(run.Status), Phase: string(run.Phase), ToVersion: run.ToVersion}
}

func containersInfo(list []dockerx.Container, own string) *accountlink.ContainersInfo {
	info := &accountlink.ContainersInfo{List: make([]accountlink.ContainerInfo, 0, len(list))}
	for _, c := range list {
		if c.State == "running" {
			info.Running++
			if c.Health == "unhealthy" {
				info.Unhealthy++
			}
		} else {
			info.Stopped++
		}
		if own != "" && c.ComposeStack == own {
			continue
		}
		info.List = append(info.List, accountlink.ContainerInfo{ID: dockerx.ShortID(c.ID), Name: c.Name, Image: c.Image, State: c.State, Health: c.Health})
	}
	return info
}

func stacksInfo(stacks []dockerx.ComposeStack, own, ownDir string) []accountlink.StackInfo {
	out := make([]accountlink.StackInfo, 0, len(stacks))
	for _, st := range stacks {
		if isOwnStack(st, own, ownDir) {
			continue
		}
		out = append(out, accountlink.StackInfo{Name: st.Name, Services: st.Total, Running: st.Running, Managed: st.Managed})
	}
	return out
}

// alertsInfo is the alerts firing now. One that is still holding before it is
// told, and one an operator muted, is not firing as far as a person reading the
// site is concerned.
func alertsInfo(subjects []hostAlertSubject) []accountlink.AlertInfo {
	out := make([]accountlink.AlertInfo, 0, len(subjects))
	for _, sub := range subjects {
		if sub.State != "firing" || sub.Muted || sub.Since == nil {
			continue
		}
		out = append(out, accountlink.AlertInfo{Kind: sub.Kind, Subject: sub.Subject, Reading: alertReading(sub.Kind, sub.Detail), Since: sub.Since.Unix()})
	}
	return out
}

func backupsInfo(all []*backups.Job) *accountlink.BackupsInfo {
	// An imported job is the record of another server's archives: it never
	// runs here, so it is not one of this server's backups to report.
	jobs := make([]*backups.Job, 0, len(all))
	for _, j := range all {
		if !j.Imported {
			jobs = append(jobs, j)
		}
	}
	info := &accountlink.BackupsInfo{Jobs: len(jobs), List: make([]accountlink.BackupInfo, 0, len(jobs))}
	var newest int64
	for _, j := range jobs {
		item := accountlink.BackupInfo{ID: j.ID, Name: j.Name}
		if run := j.LastRun; run != nil {
			at := run.StartedAt
			if run.EndedAt != nil {
				at = *run.EndedAt
			}
			item.LastStatus, item.LastAt = string(run.Status), at.Unix()
			if run.Status == backups.StatusFailed {
				info.Failed++
			}
			if at.Unix() > newest {
				newest = at.Unix()
				info.LastRun = &accountlink.RunInfo{Status: string(run.Status), At: newest}
			}
		}
		info.List = append(info.List, item)
	}
	return info
}

func deploymentsInfo(projects []*deploy.Project) *accountlink.DeploymentsInfo {
	info := &accountlink.DeploymentsInfo{}
	var newest int64
	for _, p := range projects {
		if p.ArchivedAt != nil {
			continue
		}
		info.Projects++
		if run := p.LastRun; run != nil {
			at := run.StartedAt
			if run.EndedAt != nil {
				at = *run.EndedAt
			}
			if at.Unix() > newest {
				newest = at.Unix()
				info.LastRun = &accountlink.DeployRunInfo{Project: p.Name, Status: string(run.Status), At: newest}
			}
		}
	}
	return info
}

// ---- what a refused command is, and what an alert says -----------------------

// ownStackDir and the helpers around it answer "is this the dashboard's own
// stack", which a linked site may not restart: `docker compose restart` of the
// stack the backend runs in stops the backend that is running it, and an
// explicit stop is not undone by the restart policy. Both a container of the
// dashboard's compose project and a stack in the dashboard's own directory
// count, since a checkout whose directory is not named like its project lists
// twice, once as the running project and once as a file on disk, and both
// restart the same containers.
func ownStackDir(stacks []dockerx.ComposeStack, own string) string {
	if own == "" {
		return ""
	}
	for _, st := range stacks {
		if st.Name == own && st.WorkingDir != "" {
			return filepath.Clean(st.WorkingDir)
		}
	}
	return ""
}

func isOwnStack(st dockerx.ComposeStack, own, ownDir string) bool {
	if own == "" {
		return false
	}
	return st.Name == own || (ownDir != "" && st.WorkingDir != "" && filepath.Clean(st.WorkingDir) == ownDir)
}

func (h *accountHost) refuseOwnContainer(ctx context.Context, name string) error {
	list, err := h.s.modules.docker.ListContainers(ctx, true)
	if err != nil {
		return h.s.dockerErr(err)
	}
	own := h.s.selfProject(list)
	if own == "" {
		return nil
	}
	for _, c := range list {
		if c.ComposeStack != own {
			continue
		}
		if strings.TrimPrefix(c.Name, "/") == name || strings.HasPrefix(c.ID, name) {
			return accountlink.Refuse("%s is one of the dashboard's own containers; restart the dashboard itself with dashboard.restart, which needs the maintain scope", name)
		}
	}
	return nil
}

func (h *accountHost) refuseOwnStack(ctx context.Context, name string) error {
	list, err := h.s.modules.docker.ListContainers(ctx, true)
	if err != nil {
		return h.s.dockerErr(err)
	}
	own := h.s.selfProject(list)
	if own == "" {
		return nil
	}
	stacks, err := h.s.modules.docker.ListStacks(ctx, h.s.Cfg.ComposeRoots)
	if err != nil {
		return h.s.dockerErr(err)
	}
	ownDir := ownStackDir(stacks, own)
	for _, st := range stacks {
		if st.Name == name && isOwnStack(st, own, ownDir) {
			return accountlink.Refuse("%s is the dashboard's own stack; restart the dashboard itself with dashboard.restart, which needs the maintain scope", name)
		}
	}
	return nil
}

// The only things an alert's reading is made of. An alert's message is a
// sentence for a person on the dashboard, and it carries what that person
// needs: the busiest container's name, the line of a backup's log that failed,
// what Docker said about a container that died. None of it leaves the
// machine. What leaves is the number the message was about, taken out of it
// by a pattern that can match nothing else, and nothing where there is none.
var (
	cpuReading  = regexp.MustCompile(`^CPU averaged ([0-9]{1,3}(?:\.[0-9])?%)`)
	memReading  = regexp.MustCompile(`^Memory averaged ([0-9]{1,3}(?:\.[0-9])?%)`)
	diskReading = regexp.MustCompile(`^([0-9]{1,3}(?:\.[0-9])?%) of `)
	exitReading = regexp.MustCompile(`\(exit ([0-9]{1,3})\)|exited with code ([0-9]{1,3})`)
)

func alertReading(kind, detail string) string {
	pick := func(re *regexp.Regexp) string {
		if m := re.FindStringSubmatch(detail); m != nil {
			return m[1]
		}
		return ""
	}
	switch kind {
	case hostAlertCPUHigh:
		return pick(cpuReading)
	case hostAlertMemoryHigh:
		return pick(memReading)
	case hostAlertDiskFilling:
		return pick(diskReading)
	case hostAlertContainerDown:
		// The code is in the sentence's first clause; what Docker said after
		// it is free text and is not read.
		head, _, _ := strings.Cut(detail, " Docker said: ")
		if m := exitReading.FindStringSubmatch(head); m != nil {
			return "exit " + m[1] + m[2]
		}
	}
	// backup_failed, and any kind added later, has no number to send.
	return ""
}
