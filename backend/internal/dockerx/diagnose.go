package dockerx

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
)

// Diagnosis is the dashboard's opinion of what Docker on this server is doing
// wrong.
//
// Every tool in this class shows the same facts: a state, an exit code, a
// restart count, a port list. None of them says what those facts mean. An
// operator who has run containers for years reads "Exited (137)" as "the
// kernel killed it for using too much memory"; everybody else reads it as a
// number, searches for it, and finds six answers. The facts are already on
// screen — this turns them into sentences, and where there is something the
// dashboard can do about it, into a button.
//
// The rules are deliberately conservative. A dashboard that cries wolf gets
// ignored wholesale, so nothing here fires on a container that is merely
// unusual: every finding is either something already broken, something that
// will break on the next reboot, or something that is quietly costing disk or
// exposure right now.
type Diagnosis struct {
	// Status is the worst level present, or "ok". It is what a badge reads.
	//
	// Kept as the worst of *everything* for compatibility with clients that
	// predate the split below, but nothing new should render it as "health":
	// it is the reason the overview could say "all good" over a page of
	// warnings. Runtime and Attention are the two questions it was conflating.
	Status    string          `json:"status"`
	Findings  []DockerFinding `json:"findings"`
	CheckedAt time.Time       `json:"checkedAt"`
	// Checked is how many containers were examined, so "no findings" can be
	// distinguished from "nothing to examine".
	Checked int `json:"checked"`

	// Runtime is what Docker itself reports about what is running. It clears
	// itself when the thing it describes recovers.
	Runtime RuntimeHealth `json:"runtime"`
	// Attention is everything else: posture, storage, configuration,
	// exposure. It does not clear itself, and it is never "health".
	Attention AttentionSummary `json:"attention"`
}

// DockerFinding is one thing worth telling the operator, with the reasoning
// attached so it can be argued with rather than merely obeyed.
type DockerFinding struct {
	// ID is stable for the same condition on the same object, so the UI can
	// keep a dismissal or an expanded row attached across polls.
	ID string `json:"id"`
	// Level is the three-word severity this API shipped with. It is derived
	// from Severity rather than set, and exists so a cached older bundle keeps
	// rendering while the fields below are what everything new reads.
	Level string `json:"level"`
	// Severity is how much this costs, on four levels: a recommendation is
	// not a warning, and a panel that paints both red is a panel nobody reads.
	Severity Severity `json:"severity"`
	// Class decides which of the two summaries this belongs to. A runtime
	// finding is a fact about what Docker is doing; everything else is the
	// dashboard's opinion, and mixing the two is what let "all good" sit above
	// a list of security warnings.
	Class Class  `json:"class"`
	Title string `json:"title"`
	// Detail is what was measured. Advice is what to do about it. They are
	// separate because the first is a fact and the second is an opinion.
	Detail string `json:"detail"`
	Advice string `json:"advice,omitempty"`

	// Scope and Target say what this is about, so a finding can be rendered
	// next to the row it concerns as well as in a list of its own.
	Scope    string `json:"scope"`
	Target   string `json:"target,omitempty"`
	TargetID string `json:"targetId,omitempty"`

	// Action names a remedy the dashboard can carry out itself. The UI turns
	// it into a button; an empty one means the fix is outside the panel. This
	// is the whole difference between a warning and a fix.
	Action      string `json:"action,omitempty"`
	ActionLabel string `json:"actionLabel,omitempty"`
}

// Thresholds. Each is a claim about what is bad and deserves to be read and
// argued with in one place rather than buried in an if.
const (
	// A container that has restarted this many times in the last hour is not
	// recovering, it is looping. Docker's own backoff tops out at 60s, so
	// five in an hour is well past "it crashed once".
	restartLoopCount = 5
	// The writable layer is not storage. Anything above this is data that
	// will vanish the next time the container is recreated, which for
	// anything on a moving tag is the next update.
	writableLayerWarnBytes = 512 * 1024 * 1024
	// A json-file log with no rotation configured, at this size, is a disk
	// filling up in slow motion.
	logFileWarnBytes = 512 * 1024 * 1024
	// Reclaimable space worth mentioning. Below this, pruning is noise.
	reclaimableNoticeBytes = 2 * 1024 * 1024 * 1024
)

// sensitivePorts are the services that are meant to be reached by the
// application in front of them rather than by the network. A web server bound
// to every interface is a web server doing its job; a database bound to every
// interface is the finding this list exists to raise the severity of.
var sensitivePorts = map[int]string{
	1433:  "SQL Server",
	2375:  "the Docker API, unencrypted",
	2376:  "the Docker API",
	2379:  "etcd",
	3306:  "MySQL",
	5432:  "PostgreSQL",
	5984:  "CouchDB",
	6379:  "Redis",
	7000:  "Cassandra",
	9042:  "Cassandra",
	9200:  "Elasticsearch",
	11211: "Memcached",
	27017: "MongoDB",
	5672:  "RabbitMQ",
	9092:  "Kafka",
	8086:  "InfluxDB",
	4444:  "Selenium",
	6443:  "the Kubernetes API",
}

// Diagnose examines every container once and reports what it finds.
//
// One inspect per container, not one per check: a host with sixty containers
// would otherwise answer a page load with six hundred round trips to the
// socket.
func (c *Client) Diagnose(ctx context.Context) (*Diagnosis, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	list, err := c.ListContainers(ctx, true)
	if err != nil {
		return nil, err
	}
	d := &Diagnosis{Status: "ok", Findings: []DockerFinding{}, CheckedAt: time.Now().UTC()}

	// The writable layer is read from the disk-usage snapshot, not from the
	// container listing, and that is the difference between this check working
	// and not existing.
	//
	// Docker's container list omits SizeRw entirely unless it is asked for
	// (`size=1`), and asking makes the daemon walk every container's layer —
	// far too expensive for a listing the containers page polls twice a
	// minute. So the field was always zero, and the rule below it, which is
	// one of the three findings this panel exists for, had never fired on any
	// host. The disk-usage walk already computes exactly this figure and is
	// already cached and already being read a few lines further down for the
	// daemon findings, so taking it from there costs nothing.
	writable := map[string]int64{}
	if du := c.diskUsage(ctx); du != nil {
		for _, ct := range du.Containers {
			writable[ct.ID] = ct.SizeRw
		}
	}

	// Stack membership is needed to spot a half-up stack, which is only
	// visible across containers rather than in any one of them.
	stackTotal := map[string]int{}
	stackDown := map[string][]string{}

	// Health facts are collected alongside the findings so the runtime summary
	// can count what is *fine* as well as what is not. A list of problems can
	// never say "eight healthy, four with no check at all", and that sentence
	// is the whole reason the summary exists.
	health := map[string]healthFacts{}

	for _, ct := range list {
		insp, err := cli.ContainerInspect(ctx, ct.ID)
		if err != nil {
			continue
		}
		d.Checked++
		if size, ok := writable[ct.ID]; ok {
			ct.SizeRw = size
		}
		if ct.ComposeStack != "" {
			stackTotal[ct.ComposeStack]++
			if ct.State != "running" {
				stackDown[ct.ComposeStack] = append(stackDown[ct.ComposeStack], ct.Name)
			}
		}
		health[ct.ID] = healthFactsOf(insp)
		d.Findings = append(d.Findings, diagnoseContainer(ct, insp)...)
	}

	for stack, down := range stackDown {
		if len(down) == 0 || len(down) == stackTotal[stack] {
			// All down is a stack somebody took down on purpose. Some down is
			// the interesting case.
			continue
		}
		sort.Strings(down)
		d.Findings = append(d.Findings, DockerFinding{
			ID:       "stack.partial." + stack,
			Severity: SeverityWarning, Class: ClassRuntime,
			Title: fmt.Sprintf("%s is only partly up", stack),
			Detail: fmt.Sprintf("%d of %d services in this stack are not running: %s.",
				len(down), stackTotal[stack], strings.Join(down, ", ")),
			Advice:      "A stack with a service missing usually still answers on its main port while quietly failing whatever that service did. Bringing the stack up again starts only what is missing.",
			Scope:       "stack",
			Target:      stack,
			Action:      "stack.up",
			ActionLabel: "Start the stack",
		})
	}

	d.Findings = append(d.Findings, c.diagnoseDaemon(ctx)...)

	for i := range d.Findings {
		d.Findings[i] = normalizeFinding(d.Findings[i])
	}

	sortBySeverity(d.Findings)
	d.Runtime = summarizeRuntime(list, health)
	d.Attention = summarizeAttention(d.Findings)
	d.Status = worstLevel(d.Findings)
	return d, nil
}

// containerInspect is the Engine's inspect payload, aliased so the rules below
// can be exercised against a hand-built one in a test with no daemon present.
type containerInspect = container.InspectResponse

// diagnoseContainer holds the per-container rules. Split out from Diagnose so
// each rule is readable next to the reasoning for it, and so it can be tested
// against a synthetic inspect without a daemon.
func diagnoseContainer(ct Container, insp containerInspect) []DockerFinding {
	out := []DockerFinding{}
	name := ct.Name
	add := func(f DockerFinding) {
		f.Scope = "container"
		f.Target = name
		f.TargetID = ct.ID
		out = append(out, normalizeFinding(f))
	}

	state := insp.State
	hostCfg := insp.HostConfig
	cfg := insp.Config

	// --- Why is it not running? ------------------------------------------
	if state != nil && !state.Running && !state.Restarting && !state.Paused {
		code := state.ExitCode
		switch {
		case state.OOMKilled:
			limit := "no limit was set, so it was competing with everything else on the server"
			if hostCfg != nil && hostCfg.Memory > 0 {
				limit = fmt.Sprintf("its limit was %s", humanBytes(hostCfg.Memory))
			}
			add(DockerFinding{
				ID:       "container.oom." + ct.ID,
				Severity: SeverityCritical, Class: ClassRuntime,
				Title:  name + " was killed for using too much memory",
				Detail: fmt.Sprintf("The kernel stopped this container because it asked for more memory than it was allowed — %s.", limit),
				Advice: "Raise the memory limit if the workload genuinely needs it, or find out what is growing: the usage chart on this container shows whether it climbed steadily (a leak) or spiked (one expensive request).",
				Action: "usage", ActionLabel: "Show its memory history",
			})
		case code == 0:
			// A clean exit is only worth mentioning when the container was
			// clearly meant to stay up. Without a restart policy it is
			// indistinguishable from a job that ran and finished, and saying
			// anything at all about those would fill the list with noise on
			// any host that uses one-shot containers.
			if hostCfg == nil || hostCfg.RestartPolicy.IsNone() {
				break
			}
			add(DockerFinding{
				ID:       "container.exited." + ct.ID,
				Severity: SeverityInfo, Class: ClassRuntime,
				Title:  name + " finished and stopped",
				Detail: "It exited cleanly, with status 0. For a one-off job that is success; for a service it means the process it runs decided its work was done.",
			})
		case code == 137:
			add(DockerFinding{
				ID:       "container.sigkill." + ct.ID,
				Severity: SeverityWarning, Class: ClassRuntime,
				Title:  name + " was killed outright",
				Detail: "Status 137 means the process was sent SIGKILL. That is usually the kernel running out of memory, or a stop that took longer than the timeout and was forced.",
				Advice: "If it happens repeatedly, set a memory limit so the kernel kills this container rather than picking a victim at random, and check that it shuts down when asked.",
			})
		case code == 143:
			add(DockerFinding{
				ID:       "container.sigterm." + ct.ID,
				Severity: SeverityInfo, Class: ClassRuntime,
				Title:  name + " was stopped",
				Detail: "Status 143 is a clean shutdown on SIGTERM — something asked it to stop and it did.",
			})
		case code == 127:
			add(DockerFinding{
				ID:       "container.nocommand." + ct.ID,
				Severity: SeverityCritical, Class: ClassRuntime,
				Title:  name + " could not find the command it was told to run",
				Detail: "Status 127 is \"command not found\". The image does not contain the program in this container's command, or it is not on its PATH.",
				Advice: "Check the command on the Overview tab against what the image actually ships. A shell that exists as /bin/sh but not /bin/bash is the usual culprit.",
			})
		case code == 126:
			add(DockerFinding{
				ID:       "container.notexecutable." + ct.ID,
				Severity: SeverityCritical, Class: ClassRuntime,
				Title:  name + " could not run its command",
				Detail: "Status 126 means the file was found but could not be executed — usually a missing execute bit, or a script whose interpreter line points at something not in the image.",
			})
		case code == 125:
			add(DockerFinding{
				ID:       "container.dockererror." + ct.ID,
				Severity: SeverityCritical, Class: ClassRuntime,
				Title:  name + " never started",
				Detail: "Status 125 comes from Docker itself rather than from the program: the container could not be created with the settings it was given.",
				Advice: "The error on the Overview tab says which setting. A port already in use and a missing bind-mount path are the two common ones.",
			})
		case code == 139:
			add(DockerFinding{
				ID:       "container.segfault." + ct.ID,
				Severity: SeverityCritical, Class: ClassRuntime,
				Title:  name + " crashed",
				Detail: "Status 139 is a segmentation fault — the program inside the container died in a way it could not handle.",
				Advice: "Its own logs immediately before the exit are the only place the reason will be. An image built for a different CPU architecture produces this too.",
			})
		default:
			detail := fmt.Sprintf("It exited with status %d, which comes from the program inside rather than from Docker.", code)
			if state.Error != "" {
				detail += " Docker also reported: " + state.Error
			}
			add(DockerFinding{
				ID:       "container.failed." + ct.ID,
				Severity: SeverityWarning, Class: ClassRuntime,
				Title:  name + " stopped with an error",
				Detail: detail,
				Advice: "Its last log lines are where the reason is. Anything above 128 is a signal: subtract 128 to get the signal number.",
				Action: "logs", ActionLabel: "Show its logs",
			})
		}
	}

	// --- Is it looping? ---------------------------------------------------
	if insp.RestartCount >= restartLoopCount && state != nil {
		started := parseDockerTime(state.StartedAt)
		if !started.IsZero() && time.Since(started) < time.Hour {
			add(DockerFinding{
				ID:       "container.restartloop." + ct.ID,
				Severity: SeverityCritical, Class: ClassRuntime,
				Title: name + " is restarting over and over",
				Detail: fmt.Sprintf("It has restarted %d times, most recently %s ago. Docker keeps restarting it because its restart policy says to, so it will keep failing quietly rather than staying down where you would notice.",
					insp.RestartCount, humanDuration(time.Since(started))),
				Advice: "The logs from the failing start are what explain it, and they are the same every time round the loop — read the first fifty lines rather than the tail.",
				Action: "logs", ActionLabel: "Show its logs",
			})
		}
	}

	// --- Is it actually working? ------------------------------------------
	if state != nil && state.Health != nil {
		switch state.Health.Status {
		case "unhealthy":
			detail := "This container is running, but the health check the image defines is failing, so whatever it serves is probably not answering."
			if n := len(state.Health.Log); n > 0 {
				last := strings.TrimSpace(state.Health.Log[n-1].Output)
				if last != "" {
					detail += " The check last said: " + truncate(last, 300)
				}
			}
			add(DockerFinding{
				ID:       "container.unhealthy." + ct.ID,
				Severity: SeverityCritical, Class: ClassRuntime,
				Title:  name + " says it is not healthy",
				Detail: detail,
				Advice: "A container can be up and useless; this is the check that tells them apart. Its own logs will say why the check fails.",
				Action: "logs", ActionLabel: "Show its logs",
			})
		case "starting":
			if started := parseDockerTime(state.StartedAt); !started.IsZero() && time.Since(started) > 10*time.Minute {
				add(DockerFinding{
					ID:       "container.starting." + ct.ID,
					Severity: SeverityWarning, Class: ClassRuntime,
					Title:  name + " has been starting for " + humanDuration(time.Since(started)),
					Detail: "Its health check has not passed once since it started. Docker treats a container in this state as up, so nothing else will report a problem.",
					Advice: "Either the start-up period configured on the health check is longer than it needs to be, or the service never came up at all.",
				})
			}
		}
	}
	if state != nil && state.Paused {
		add(DockerFinding{
			ID:       "container.paused." + ct.ID,
			Severity: SeverityWarning, Class: ClassRuntime,
			Title:  name + " is paused",
			Detail: "Every process inside it is frozen. It holds its memory and its ports, and answers nothing.",
			Action: "unpause", ActionLabel: "Resume it",
		})
	}

	// --- Will it survive a reboot? ---------------------------------------
	if state != nil && state.Running && hostCfg != nil {
		policy := string(hostCfg.RestartPolicy.Name)
		if policy == "" || policy == "no" {
			if ct.ComposeStack == "" && !hostCfg.AutoRemove {
				add(DockerFinding{
					ID:       "container.norestart." + ct.ID,
					Severity: SeverityRecommendation, Class: ClassLifecycle,
					Title:  name + " will not come back after a reboot",
					Detail: "It has no restart policy, so Docker will not start it again when this server restarts or when the daemon does.",
					Advice: "\"Unless stopped\" is what most services want: it comes back on boot, and stays down if you deliberately stopped it.",
					Action: "set-restart", ActionLabel: "Set a restart policy",
				})
			}
		}
	}

	// --- Is it quietly filling the disk? ---------------------------------
	if hostCfg != nil {
		driver := hostCfg.LogConfig.Type
		_, hasMaxSize := hostCfg.LogConfig.Config["max-size"]
		if (driver == "json-file" || driver == "") && !hasMaxSize {
			size := logFileSize(insp.LogPath)
			if size > logFileWarnBytes {
				add(DockerFinding{
					ID:       "container.logs." + ct.ID,
					Severity: SeverityWarning, Class: ClassStorage,
					Title:  name + " has written " + humanBytes(size) + " of logs",
					Detail: "Docker's default log driver keeps every line this container has ever printed, in one file that is never rotated. It is deleted only when the container is.",
					Advice: "Setting a maximum log size on the container caps it — this is the single most common way a server runs out of disk without anything appearing to be wrong. Capping it here rebuilds the container with a 10 MB limit over three files; the existing log file is discarded with the old container.",
					// The advice described a fix the dashboard could carry out
					// and did not offer, which is the same gap the reclaim
					// button had. Docker cannot change a log driver on a live
					// container, so the remedy is a recreate — the same bargain
					// the restart-policy finding makes, and the UI says so.
					Action:      "cap-logs",
					ActionLabel: "Cap the log size",
				})
			}
		}
	}
	if ct.SizeRw > writableLayerWarnBytes {
		add(DockerFinding{
			ID:       "container.writablelayer." + ct.ID,
			Severity: SeverityWarning, Class: ClassStorage,
			Title:       name + " has " + humanBytes(ct.SizeRw) + " written inside the container",
			Detail:      "That is data sitting in the container's own writable layer rather than in a volume. It is not backed up, it is invisible to the file manager, and it is destroyed the moment the container is recreated — which includes every image update.",
			Advice:      "If it matters, mount a volume at whatever path it is being written to. If it does not, it is still costing that much disk — and no prune can reclaim it while the container exists.",
			Action:      "changes",
			ActionLabel: "Show what it has written",
		})
	}

	// --- Is it more exposed than it needs to be? -------------------------
	if state != nil && state.Running && hostCfg != nil {
		public := []string{}
		sensitive := []string{}
		for portSpec, bindings := range hostCfg.PortBindings {
			for _, b := range bindings {
				exposure := DescribePort(b.HostIP, atoiSafe(b.HostPort), portSpec.Int(), portSpec.Proto())
				if exposure.Scope != ScopeAll {
					continue
				}
				public = append(public, b.HostPort+" → "+string(portSpec))
				if service, ok := sensitivePorts[portSpec.Int()]; ok {
					sensitive = append(sensitive, service+" on "+b.HostPort)
				}
			}
		}
		if len(public) > 0 {
			sort.Strings(public)
			sort.Strings(sensitive)
			// A web server on every interface is what a web server is for, and
			// firing a warning at every one of them is how a panel becomes
			// wallpaper. A database on every interface is a different sentence,
			// so the severity follows what is behind the port rather than the
			// binding alone.
			severity := SeverityRecommendation
			title := name + " is published on every network interface"
			detail := fmt.Sprintf("%s. Docker publishes ports by writing NAT rules that are consulted before the firewall's own, so a port published this way is reachable from anywhere that can route to this server even when the firewall appears to deny it.",
				strings.Join(public, ", "))
			if len(sensitive) > 0 {
				severity = SeverityWarning
				title = name + " publishes " + strings.Join(sensitive, ", ") + " on every interface"
				detail = fmt.Sprintf("%s Ports of this kind are meant to be reached by the application in front of them, not by the network.", detail)
			}
			add(DockerFinding{
				ID:       "container.exposed." + ct.ID,
				Severity: severity, Class: ClassExposure,
				Title:  title,
				Detail: detail,
				Advice: "If only this server needs to reach it — a database behind an application, say — bind it to 127.0.0.1 instead. If the internet needs to reach it, it should be behind the reverse proxy rather than published directly.",
				Action: "routes", ActionLabel: "Show where it is reachable",
			})
		}
	}

	// --- Is it a way onto the host? --------------------------------------
	if hostCfg != nil && hostCfg.Privileged {
		// Privileged on its own is a container holding more power than it
		// probably needs. Privileged *and* holding a piece of the host — a
		// bind mount reaching out of the container — is a path to root on the
		// server, and the two deserve different words.
		severity, detail := SeverityWarning,
			"A privileged container can reach every device on the server and drop the restrictions that separate it from the host. Anything that breaks into it has the server."
		if reach := hostReach(insp); len(reach) > 0 {
			severity = SeverityCritical
			detail += " This one also mounts " + strings.Join(reach, ", ") + " from the host, so it is not sandboxed in any meaningful sense."
		}
		add(DockerFinding{
			ID:       "container.privileged." + ct.ID,
			Severity: severity, Class: ClassSecurity,
			Title:  name + " runs privileged",
			Detail: detail,
			Advice: "Most images that ask for this need one or two capabilities rather than all of them. It is worth checking which.",
		})
	}
	for _, m := range insp.Mounts {
		src := strings.TrimSuffix(m.Source, "/")
		if src == "/var/run/docker.sock" || src == "/run/docker.sock" {
			add(DockerFinding{
				ID:       "container.dockersock." + ct.ID,
				Severity: SeverityCritical, Class: ClassSecurity,
				Title:  name + " can control Docker itself",
				Detail: "The Docker socket is mounted into this container. Anything inside it can start another container with the whole server mounted, which makes it equivalent to root on the host.",
				Advice: "Expected for things that manage containers. For anything else it is far more access than the job needs.",
			})
			break
		}
	}

	// --- Is anything watching it? ----------------------------------------
	//
	// Only for a long-running service: a one-shot job has nothing to check,
	// and a container that is already down has a louder problem. Both of these
	// are recommendations rather than warnings, because neither is costing
	// anything today and painting them the same colour as a restart loop is
	// how a panel stops being read.
	if state != nil && state.Running && hostCfg != nil && !hostCfg.AutoRemove {
		if !healthFactsOf(insp).hasCheck {
			add(DockerFinding{
				ID:       "container.nohealthcheck." + ct.ID,
				Severity: SeverityRecommendation, Class: ClassConfiguration,
				Title:  name + " has no health check",
				Detail: "Docker reports this container as up whenever its main process is alive. A process that is alive and not answering — a web server that lost its database, a worker stuck on a lock — is indistinguishable from a working one.",
				Advice: "A health check is one command the container runs against itself. With one, this dashboard, compose's dependency ordering and the restart policy can all tell working from merely running.",
			})
		}
		if hostCfg.Memory == 0 {
			add(DockerFinding{
				ID:       "container.nomemorylimit." + ct.ID,
				Severity: SeverityRecommendation, Class: ClassConfiguration,
				Title:  name + " has no memory limit",
				Detail: "It can use as much of this server's memory as it asks for. When memory runs out the kernel picks a victim by its own arithmetic, and the process it kills is often not the one that caused the problem.",
				Advice: "A limit turns \"the server became unresponsive\" into \"this container was restarted\", which is a much better night. Set it above the container's normal peak, which the usage chart on this container shows.",
				Action: "usage", ActionLabel: "Show its memory history",
			})
		}
	}

	// --- Can you tell what is running? -----------------------------------
	if cfg != nil {
		image := cfg.Image
		if strings.HasSuffix(image, ":latest") || (!strings.Contains(image, ":") && !strings.Contains(image, "@")) {
			add(DockerFinding{
				ID:       "container.latest." + ct.ID,
				Severity: SeverityRecommendation, Class: ClassConfiguration,
				Title:  name + " runs a moving tag",
				Detail: "`" + image + "` means whatever that tag pointed at the last time it was pulled. Two servers running \"the same\" tag can be running different software, and there is no version to roll back to.",
				Advice: "Pinning the tag to a version makes an update something you choose rather than something that happens when a container restarts.",
			})
		}
	}

	return out
}

// diagnoseDaemon covers what is true of the host's Docker rather than of any
// one container.
func (c *Client) diagnoseDaemon(ctx context.Context) []DockerFinding {
	out := []DockerFinding{}
	// The same accounting the disk panel shows, so the figure in this finding
	// and the figure on the page it sends you to are one number rather than
	// two that disagree. Both are Docker's own definition of reclaimable —
	// shared layers counted once — because the previous naive sum promised
	// more than any prune could deliver.
	du, err := c.DiskUsage(ctx)
	if err != nil {
		return out
	}
	images, buildCache := du.ImagesLine.Reclaimable, du.BuildCacheLine.Reclaimable

	if images+buildCache > reclaimableNoticeBytes {
		parts := []string{}
		if images > 0 {
			parts = append(parts, humanBytes(images)+" of images no container is using")
		}
		if buildCache > 0 {
			parts = append(parts, humanBytes(buildCache)+" of build cache")
		}
		detail := strings.Join(parts, ", and ")
		out = append(out, DockerFinding{
			ID:       "daemon.reclaimable",
			Severity: SeverityRecommendation, Class: ClassStorage,
			Title:  humanBytes(images+buildCache) + " of Docker disk can be reclaimed",
			Detail: detail + ". Old image layers are kept after every update, and every build leaves its cache behind, which is what makes this grow on its own.",
			Advice: "Both are safe to remove: anything a container still needs is never touched, and the cache costs a slower next build rather than anything you cannot get back. Unused volumes are a different matter — those hold data, and this does not include them.",
			Scope:  "daemon",
			// Handled on the page rather than by sending the operator to a
			// tab and hoping. It used to be a link to the images list, whose
			// only button prunes *dangling* images — on a host where nothing
			// is dangling that is a promise of forty gigabytes answered by a
			// button that frees nothing.
			Action:      "prune",
			ActionLabel: "Reclaim it",
		})
	}
	if du.VolumesLine.Total-du.VolumesLine.Active > 0 && du.VolumesLine.Reclaimable > reclaimableNoticeBytes {
		out = append(out, DockerFinding{
			ID:       "daemon.orphanvolumes",
			Severity: SeverityRecommendation, Class: ClassStorage,
			Title:       fmt.Sprintf("%d volumes holding %s are not attached to anything", du.VolumesLine.Total-du.VolumesLine.Active, humanBytes(du.VolumesLine.Reclaimable)),
			Detail:      "A volume outlives the container that created it. These are usually left behind by a container that was removed and recreated — but some of them are the only copy of something.",
			Advice:      "Worth looking at one by one rather than pruning: the Volumes tab shows what each one holds and when it was created.",
			Scope:       "daemon",
			Action:      "volumes",
			ActionLabel: "Show the volumes",
		})
	}
	return out
}

// normalizeFinding fills in what every finding must carry, wherever it was
// built. The three-word Level is derived from Severity rather than set beside
// it, so the two cannot drift apart — which is exactly the failure the whole
// runtime/attention split exists to stop.
func normalizeFinding(f DockerFinding) DockerFinding {
	if f.Severity == "" {
		f.Severity = SeverityInfo
	}
	if f.Class == "" {
		f.Class = ClassConfiguration
	}
	f.Level = f.Severity.legacyLevel()
	return f
}

// healthFactsOf reads whether a container defines a health check at all, and
// separately what that check currently says.
//
// Docker leaves State.Health nil for a container with no check, and the
// distinction matters more than it looks: without it "healthy" silently
// includes every container that is not being checked, and the summary reports
// perfect health for a server where nothing is watched.
func healthFactsOf(insp containerInspect) healthFacts {
	f := healthFacts{}
	if insp.Config != nil && insp.Config.Healthcheck != nil &&
		len(insp.Config.Healthcheck.Test) > 0 && insp.Config.Healthcheck.Test[0] != "NONE" {
		f.hasCheck = true
	}
	if insp.State != nil && insp.State.Health != nil {
		f.hasCheck = true
		f.status = insp.State.Health.Status
	}
	return f
}

// hostReach names the bind mounts that give a container a piece of the host's
// own filesystem. A named volume is Docker's storage and is not on this list;
// a bind mount of `/`, `/etc` or the Docker socket is the host itself.
func hostReach(insp containerInspect) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, m := range insp.Mounts {
		if string(m.Type) != "bind" {
			continue
		}
		src := strings.TrimSuffix(m.Source, "/")
		if src == "" {
			src = "/"
		}
		switch {
		case src == "/", src == "/etc", src == "/root", src == "/boot",
			src == "/var/lib/docker", src == "/usr", src == "/proc", src == "/sys",
			src == "/var/run/docker.sock", src == "/run/docker.sock":
			if !seen[src] {
				seen[src] = true
				out = append(out, src)
			}
		}
	}
	sort.Strings(out)
	return out
}

func sortFindings(f []DockerFinding) {
	rank := map[string]int{"critical": 0, "warning": 1, "notice": 2}
	sort.SliceStable(f, func(i, j int) bool {
		if rank[f[i].Level] != rank[f[j].Level] {
			return rank[f[i].Level] < rank[f[j].Level]
		}
		return f[i].Target < f[j].Target
	})
}

func worstLevel(f []DockerFinding) string {
	worst := "ok"
	for _, item := range f {
		switch item.Level {
		case "critical":
			return "critical"
		case "warning":
			worst = "warning"
		case "notice":
			if worst == "ok" {
				worst = "notice"
			}
		}
	}
	return worst
}

func logFileSize(path string) int64 {
	if path == "" {
		return 0
	}
	st, err := os.Stat(path)
	if err != nil {
		// The log lives under the daemon's data root. On a host where that is
		// not visible from this process there is nothing to measure, and a
		// guess would be worse than silence.
		return 0
	}
	return st.Size()
}

func parseDockerTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// humanBytes matches `bytes()` in the frontend's format.ts — one decimal place
// at every scale but bytes themselves — so a figure quoted inside a finding
// reads identically to the same figure in the table beside it. Two spellings
// of the same number on one screen reads as two different numbers.
func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit && exp < 4; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %sB", float64(b)/float64(div), []string{"K", "M", "G", "T", "P"}[exp])
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d seconds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
}
