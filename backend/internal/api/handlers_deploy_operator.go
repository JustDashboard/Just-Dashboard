package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/assistant"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// The deployment operator's reads: what the assistant reads about one deployment
// and the server under it, each shaped and bounded for a model, through the store
// or service the page that shows the same thing reads. The conversation they are
// part of, and everything else it can do, is in handlers_assistant_turn.go.

const (
	// maxOperatorResult bounds what one read answers the model with, as a
	// query result is bounded: the newest of a read is kept.
	maxOperatorResult = 24_000
)

// operatorContext is what the conversation begins knowing: what the
// project's page already shows anybody who may read it.
func (s *Server) operatorContext(ctx context.Context, summary *deploy.DeploymentSummary) assistant.OperatorContext {
	out := assistant.OperatorContext{
		Deployment: summary.Name, Environment: summary.EnvironmentName, Kind: string(summary.EnvironmentKind),
		Source: strings.TrimSpace(string(summary.SourceKind) + " " + summary.SourceRepository + " " + summary.SourceRef),
		Build:  string(summary.BuildMethod), Address: summary.Endpoint, Health: summary.Health,
	}
	if summary.LiveReleaseID > 0 {
		if release, err := s.modules.deployRuns.Release(ctx, summary.LiveReleaseID); err == nil {
			out.LiveRelease = release.Release.Number
		}
	}
	// A run's reason is masked as a read's is; with nothing to mask by, the
	// line goes without it.
	masker, _ := s.operatorMasker(ctx, summary)
	if summary.ActiveRun != nil {
		out.ActiveRun = runLine(summary.ActiveRun, masker)
	}
	if summary.LastRun != nil {
		out.LastRun = runLine(summary.LastRun, masker)
	}
	if watch, err := s.modules.deployRuns.OpenRollbackWatch(ctx, summary.EnvironmentID); err == nil && watch.State == deploy.RollbackWatchWatching {
		out.WarmRelease = watch.WarmReleaseNum
	}
	if s.modules.sys != nil {
		if info, err := s.modules.sys.Host(ctx); err == nil && info != nil {
			out.Server = info.Hostname
		}
	}
	return out
}

func runLine(run *deploy.EngineRun, masker *deploy.DiagnosisMasker) string {
	line := fmt.Sprintf("#%d %s, %s", run.RunNumber, run.Operation, run.State)
	if run.TerminalReason != "" && masker != nil {
		reason, _ := masker.Mask(run.TerminalReason)
		line += ": " + clipText(reason, 300)
	}
	return line
}

// operatorReadLabel is a read as its card names it.
func operatorReadLabel(call assistant.ToolCall) string {
	in := call.OperatorInput()
	switch call.Name {
	case assistant.ToolRecentRuns:
		return fmt.Sprintf("Read the last %d runs", in.Limit)
	case assistant.ToolRunTranscript:
		return fmt.Sprintf("Read run #%d's transcript (last %d lines)", in.Run, in.Lines)
	case assistant.ToolContainerLogs:
		if in.Service != "" {
			return fmt.Sprintf("Read the last %d lines %s wrote", in.Lines, in.Service)
		}
		return fmt.Sprintf("Read the last %d lines the live containers wrote", in.Lines)
	case assistant.ToolReleaseMetrics:
		return "Read the live release's usage and right-sizing verdict"
	case assistant.ToolLifecycleEvents:
		return fmt.Sprintf("Read the container events of the last %d hours", in.Hours)
	case assistant.ToolHostAlerts:
		return "Read the server's firing host alerts"
	case assistant.ToolFirewallRules:
		return "Read the firewall's rules"
	case assistant.ToolSecurityUpdates:
		return "Read the server's pending security updates"
	case assistant.ToolReadSource:
		if len(in.Paths) == 0 {
			return "Read the repository's README and top folder"
		}
		return "Read " + clipText(strings.Join(in.Paths, ", "), 80) + " in the repository"
	}
	return call.Name
}

// operatorRead runs one allowed read and answers the model with its JSON,
// bounded, and the page with a few words of what was read. A read the caller's
// role could not make through the dashboard's own route answers that instead.
// summary is nil in a conversation about the whole server, which reads only the
// host's own: its alerts, its firewall and its updates.
func (s *Server) operatorRead(ctx context.Context, r *http.Request, p *httpx.Principal, summary *deploy.DeploymentSummary, call assistant.ToolCall) (string, string, bool) {
	in := call.OperatorInput()
	refuse := func(why string) (string, string, bool) { return why, why, false }
	answer := func(v any, said string) (string, string, bool) {
		raw, err := json.Marshal(v)
		if err != nil {
			return refuse("the read could not be written out")
		}
		text := string(raw)
		if len(text) > maxOperatorResult {
			text = clipText(text, maxOperatorResult) + " (cut at the read's bound)"
		}
		return text, said, true
	}
	switch call.Name {
	case assistant.ToolRecentRuns:
		runs, _, err := s.modules.deployRuns.ProjectRunsFiltered(ctx, summary.ID, deploy.RunListFilter{Environment: summary.EnvironmentID, Limit: in.Limit})
		if err != nil {
			return refuse("the runs could not be read")
		}
		masker, err := s.operatorMasker(ctx, summary)
		if err != nil {
			return refuse("the environment's secret values could not be read to mask, so nothing was read")
		}
		out := make([]*mcpRun, 0, len(runs))
		for i := range runs {
			run := mcpRunOf(&runs[i])
			run.Reason, _ = masker.Mask(run.Reason)
			out = append(out, run)
		}
		return answer(map[string]any{"runs": out}, operatorCount(len(out), "run"))
	case assistant.ToolRunTranscript:
		return s.operatorTranscript(ctx, summary, in, answer, refuse)
	case assistant.ToolContainerLogs:
		return s.operatorContainerLogs(ctx, summary, in, answer, refuse)
	case assistant.ToolReadSource:
		return s.operatorSource(ctx, summary, in, answer, refuse)
	case assistant.ToolReleaseMetrics:
		targets, err := s.modules.deployPlanning.SizingTargets(ctx, summary.ID, summary.EnvironmentID)
		if err != nil || len(targets) == 0 {
			return refuse("the environment's sizing could not be read")
		}
		sizing, err := s.sizeEnvironment(ctx, targets[0], time.Now(), s.hostCPUs(ctx))
		if err != nil {
			return refuse("the environment's sizing could not be read")
		}
		containers := []map[string]any{}
		if s.modules.docker != nil {
			list, err := s.modules.docker.ListContainersWithLastRun(ctx, map[string]string{
				"io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": strconv.FormatInt(summary.EnvironmentID, 10)})
			if err == nil {
				for _, container := range list {
					containers = append(containers, map[string]any{
						"name": container.Name, "state": container.State, "status": container.Status, "health": container.Health,
						"release": container.Labels["io.just-dashboard.release-number"], "startedAt": container.StartedAt,
						"memoryLimitBytes": container.MemoryLimit,
					})
				}
			}
		}
		return answer(map[string]any{"sizing": sizing, "containers": containers}, "usage and the sizing verdict")
	case assistant.ToolLifecycleEvents:
		if s.modules.dockerEvents == nil {
			return refuse("Docker is unavailable on this host, so container lifecycle cannot be read")
		}
		events := s.deploymentLifecycleEvents(r, summary.EnvironmentID, 100)
		since := time.Now().Add(-time.Duration(in.Hours) * time.Hour)
		kept := []dockerx.Event{}
		for _, event := range events {
			if event.Time.After(since) {
				kept = append(kept, event)
			}
		}
		s.correlateDeploymentEvents(r, kept, summary.Name)
		return answer(map[string]any{"hours": in.Hours, "events": kept}, operatorCount(len(kept), "event"))
	case assistant.ToolHostAlerts:
		// The host's Alerts page is an administrator's, so its readings are.
		if !p.Can(auth.CapSystemAdmin) {
			return refuse("your role does not read the host's alerts (" + string(auth.CapSystemAdmin) + ")")
		}
		if s.modules.hostAlerts == nil {
			return refuse("host alerts are unavailable")
		}
		subjects, err := s.modules.hostAlerts.Subjects(ctx)
		if err != nil {
			return refuse("the host alerts could not be read")
		}
		return answer(map[string]any{"alerts": subjects}, operatorCount(len(subjects), "alert"))
	case assistant.ToolFirewallRules:
		if s.modules.netsec == nil {
			return refuse("the firewall is unavailable on this host")
		}
		status, err := s.modules.netsec.Status(ctx)
		if err != nil || status == nil {
			return refuse("the firewall could not be read")
		}
		// A conversation about the server reads every rule; one about a deployment,
		// those of the ports it is reached on.
		if summary == nil {
			return answer(map[string]any{
				"backend": status.Backend, "available": status.Available, "enabled": status.Enabled, "policy": status.Policy,
				"rules": status.Rules,
			}, operatorCount(len(status.Rules), "rule"))
		}
		ports := operatorPorts(summary)
		rules := []any{}
		for _, rule := range status.Rules {
			raw, _ := json.Marshal(rule)
			if mentionsPort(string(raw), ports) {
				rules = append(rules, rule)
			}
		}
		return answer(map[string]any{
			"backend": status.Backend, "available": status.Available, "enabled": status.Enabled, "policy": status.Policy,
			"ports": ports, "rules": rules, "otherRules": len(status.Rules) - len(rules),
		}, fmt.Sprintf("%s for ports %v", operatorCount(len(rules), "rule"), ports))
	case assistant.ToolSecurityUpdates:
		if s.modules.updates == nil {
			return refuse("package updates are unavailable on this host")
		}
		report, err := s.modules.updates.Check(ctx)
		if err != nil || report == nil {
			return refuse("the pending updates could not be read")
		}
		security := []any{}
		for _, pkg := range report.Packages {
			if pkg.Security {
				security = append(security, pkg)
			}
		}
		return answer(map[string]any{
			"manager": report.Manager, "securityFiltering": report.SecurityFiltering, "security": security,
			"securityCount": report.SecurityCount, "allPending": len(report.Packages), "rebootRequired": report.RebootRequired,
		}, operatorCount(len(security), "security update"))
	}
	return refuse("no such read")
}

// operatorMasker masks the deployment's secret values out of what is sent to a model:
// a line the dashboard prints to a page is not therefore a line to send to somebody
// else's model unmasked. It covers all of the deployment, because the model may name
// another environment or an older run, and what a log holds may have been rotated.
func (s *Server) operatorMasker(ctx context.Context, summary *deploy.DeploymentSummary) (*deploy.DiagnosisMasker, error) {
	values, err := s.modules.deployPlanning.DeploymentSecrets(ctx, summary.ID)
	if err != nil {
		return nil, err
	}
	return deploy.NewDiagnosisMasker(values), nil
}

type operatorAnswer func(any, string) (string, string, bool)
type operatorRefusal func(string) (string, string, bool)

func (s *Server) operatorTranscript(ctx context.Context, summary *deploy.DeploymentSummary, in assistant.OperatorInput, answer operatorAnswer, refuse operatorRefusal) (string, string, bool) {
	run, err := s.modules.deployRuns.ProjectRunByNumber(ctx, summary.ID, in.Run)
	if err != nil {
		return refuse(fmt.Sprintf("%s has no run #%d", summary.Name, in.Run))
	}
	snapshot, err := s.modules.deployRuns.Snapshot(ctx, run.ID)
	if err != nil {
		return refuse("the run could not be read")
	}
	masker, err := s.operatorMasker(ctx, summary)
	if err != nil {
		return refuse("the run's secret values could not be read to mask, so nothing was read")
	}
	tail, earlier, err := s.modules.deployRuns.RunTranscriptTail(ctx, run.ID, in.Lines)
	if err != nil {
		return refuse("the run's transcript could not be read")
	}
	names := map[int64]string{}
	steps := make([]mcpStep, 0, len(snapshot.Steps))
	for _, step := range snapshot.Steps {
		label := deploy.StepLabel(step.Key, snapshot.Run.Operation)
		if label == "" {
			label = string(step.Key)
		}
		names[step.ID] = label
		message, _ := masker.Mask(step.ErrorMessage)
		steps = append(steps, mcpStep{Step: label, State: string(step.State), Attempt: step.Attempt, Code: step.ErrorCode, Error: clipText(message, 2000)})
	}
	lines := make([]mcpLine, 0, len(tail))
	for _, line := range tail {
		text, _ := masker.Mask(line.Text)
		lines = append(lines, mcpLine{Step: names[line.StepID], Stream: line.Stream, At: line.At, Text: clipText(text, 2000)})
	}
	view := mcpRunOf(&snapshot.Run)
	view.Reason, _ = masker.Mask(view.Reason)
	return answer(map[string]any{"run": view, "steps": steps, "lines": lines, "earlierLines": earlier},
		fmt.Sprintf("run #%d, %s", in.Run, operatorCount(len(lines), "line")))
}

func (s *Server) operatorContainerLogs(ctx context.Context, summary *deploy.DeploymentSummary, in assistant.OperatorInput, answer operatorAnswer, refuse operatorRefusal) (string, string, bool) {
	if s.modules.docker == nil {
		return refuse("Docker is unavailable on this host")
	}
	if summary.LiveReleaseID <= 0 {
		return refuse("this environment has no live release, so no container to read")
	}
	list, err := s.modules.docker.ListContainersWithLabels(ctx, map[string]string{
		"io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": strconv.FormatInt(summary.EnvironmentID, 10),
		"io.just-dashboard.release-id": strconv.FormatInt(summary.LiveReleaseID, 10)})
	if err != nil {
		return refuse("the live release's containers could not be listed")
	}
	masker, err := s.operatorMasker(ctx, summary)
	if err != nil {
		return refuse("the environment's secret values could not be read to mask, so nothing was read")
	}
	type containerLines struct {
		Name  string   `json:"name"`
		State string   `json:"state"`
		Lines []string `json:"lines"`
	}
	out := []containerLines{}
	for _, container := range list {
		if in.Service != "" && container.ComposeSvc != in.Service && container.Name != in.Service {
			continue
		}
		lines, closer, err := s.modules.docker.Logs(ctx, container.ID, dockerx.LogOptions{
			Tail: strconv.Itoa(in.Lines), Timestamps: true, Stdout: true, Stderr: true})
		read := containerLines{Name: container.Name, State: container.State, Lines: []string{}}
		if err == nil {
			for line := range lines {
				text, _ := masker.Mask(line.Text)
				read.Lines = append(read.Lines, clipText(line.Stream+" "+text, 2000))
			}
			_ = closer.Close()
		}
		out = append(out, read)
	}
	if len(out) == 0 {
		return refuse("no live container matched")
	}
	total := 0
	for _, c := range out {
		total += len(c.Lines)
	}
	return answer(map[string]any{"containers": out}, operatorCount(total, "line")+" from "+operatorCount(len(out), "container"))
}

// operatorCount is a count as a read's few words say it: "1 run", "12 runs".
// operatorSource reads the deployment's code from the commit a deployment would
// build now, masked as a log is: a repository can hold a value the deployment
// keeps as a secret.
func (s *Server) operatorSource(ctx context.Context, summary *deploy.DeploymentSummary, in assistant.OperatorInput, answer operatorAnswer, refuse operatorRefusal) (string, string, bool) {
	if s.modules.deployChecker == nil {
		return refuse("the deployment's source cannot be read on this install")
	}
	read, err := s.modules.deployChecker.ReadSource(ctx, summary.ID, summary.EnvironmentID, in.Paths)
	if err != nil {
		return refuse(deploy.SourceReadFailure(err))
	}
	masker, err := s.operatorMasker(ctx, summary)
	if err != nil {
		return refuse("the environment's secret values could not be read to mask, so nothing was read")
	}
	read.Repository = summary.SourceRepository
	for i := range read.Files {
		read.Files[i].Content, _ = masker.Mask(read.Files[i].Content)
	}
	var said []string
	for _, file := range read.Files {
		said = append(said, file.Path)
	}
	if len(said) > 3 {
		said = append(said[:2], operatorCount(len(read.Files)-2, "more file"))
	}
	if len(read.Folders) > 0 {
		said = append(said, operatorCount(len(read.Folders), "folder"))
	}
	if len(said) == 0 {
		said = append(said, "nothing it asked for")
	}
	return answer(read, strings.Join(said, ", "))
}

func operatorCount(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// operatorPorts are the ports a visitor reaches the deployment on: the
// ingress's two when it has an address, and the one it publishes itself.
func operatorPorts(summary *deploy.DeploymentSummary) []int {
	ports := []int{}
	if summary.Endpoint != "" {
		ports = append(ports, 80, 443)
	}
	if summary.HostPort > 0 && !slices.Contains(ports, summary.HostPort) {
		ports = append(ports, summary.HostPort)
	}
	return ports
}

// mentionsPort reads whether a rule, as its tool prints it, names one of the
// ports: a bare number bounded by anything that is not a digit.
func mentionsPort(rule string, ports []int) bool {
	for _, port := range ports {
		needle := strconv.Itoa(port)
		for index := strings.Index(rule, needle); index >= 0; {
			before := index == 0 || rule[index-1] < '0' || rule[index-1] > '9'
			end := index + len(needle)
			after := end >= len(rule) || rule[end] < '0' || rule[end] > '9'
			if before && after {
				return true
			}
			next := strings.Index(rule[end:], needle)
			if next < 0 {
				break
			}
			index = end + next
		}
	}
	return false
}
