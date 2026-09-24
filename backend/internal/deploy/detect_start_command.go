package deploy

import (
	"encoding/json"
	"path"
	"regexp"
	"strings"
)

// A container lives exactly as long as its main process. Repositories
// written for a VPS start their server the way a VPS wants it — `pm2 start`,
// `forever start`, `gunicorn --daemon`, a trailing `&` — and every one of
// those hands the server to a background process and returns, so the
// container exits, the restart policy starts it again, and the release fails
// as a readiness timeout with nothing saying why. Detection rewrites the
// forms it can into their foreground equivalents and records the rest, which
// preflight refuses before a build is spent on them.

// DetectedStartDetach is a start command that detection found backgrounding
// the application and could not rewrite.
type DetectedStartDetach struct {
	// Command is the offending command as written; Script is the package
	// script that holds it, when the start command runs one.
	Command string `json:"command"`
	Script  string `json:"script,omitempty"`
	Source  string `json:"source"`
	// Effect is "exits" when the command returns and the container stops, or
	// "backgrounds" when one process stays in the foreground and another
	// runs beside it unsupervised.
	Effect string `json:"effect"`
	Reason string `json:"reason"`
	Action string `json:"action"`
}

const (
	startDetachExits       = "exits"
	startDetachBackgrounds = "backgrounds"
)

type startDetachIssue struct {
	effect, reason, action string
}

var (
	pm2StartRE       = regexp.MustCompile(`(?:^|[\s;&|(])(?:(?:npx|bunx)\s+|pnpm\s+(?:exec\s+)?|yarn\s+)?pm2\s+(?:start|restart|reload)\b`)
	pm2ForegroundRE  = regexp.MustCompile(`--no-daemon\b|\bpm2\s+(?:logs|monit|attach)\b|\btail\s+-f\b`)
	foreverStartRE   = regexp.MustCompile(`(?:^|[\s;&|(])(?:(?:npx|bunx)\s+)?forever\s+start\b`)
	gunicornDaemonRE = regexp.MustCompile(`\bgunicorn\b[^;&|]*\s(?:-D|--daemon)(?:\s|$)`)
	gunicornFlagRE   = regexp.MustCompile(`(\s)(?:-D|--daemon)(\s|$)`)
	uwsgiDaemonRE    = regexp.MustCompile(`\buwsgi\b[^;&|]*\s(?:--daemonize2?|-d)\s`)
	celeryMultiRE    = regexp.MustCompile(`\bcelery\b[^;&|]*\bmulti\s+(?:start|restart)\b`)
	detachedTermRE   = regexp.MustCompile(`\bscreen\s+-\w*d|\btmux\s+new(?:-session)?\b[^;&|]*\s-d\b`)
	quotedRE         = regexp.MustCompile(`'[^']*'|"(?:[^"\\]|\\.)*"`)
	redirectAmpRE    = regexp.MustCompile(`&&|\|&|[0-9]*>&[0-9-]*|&>>?`)
	trailingWaitRE   = regexp.MustCompile(`&\s*wait\s*$`)
	envPrefix        = `((?:[A-Za-z_][A-Za-z0-9_]*=\S*\s+)*)`
	simpleArguments  = "([^;&|<>`()]+)"
	pm2SimpleRE      = regexp.MustCompile(`^` + envPrefix + `(?:(?:npx|bunx)\s+|pnpm\s+exec\s+|yarn\s+)?pm2\s+start\s+` + simpleArguments + `$`)
	foreverSimpleRE  = regexp.MustCompile(`^` + envPrefix + `(?:(?:npx|bunx)\s+)?forever\s+start\s+` + simpleArguments + `$`)
	backgroundOnlyRE = regexp.MustCompile(`^(?:nohup\s+)?` + simpleArguments + `\s*&$`)
	npmVariableRE    = regexp.MustCompile(`\$\{?npm_\w+`)
	scriptSegmentRE  = regexp.MustCompile(`^(?:bun|npm|pnpm|yarn)\s+run\s+(\S+)$|^(?:npm|pnpm|yarn|bun)\s+(start)$`)
)

// classifyStartCommand reports whether a shell command keeps its server in
// the foreground.
func classifyStartCommand(command string) *startDetachIssue {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	switch {
	case pm2StartRE.MatchString(command) && !pm2ForegroundRE.MatchString(command):
		return &startDetachIssue{startDetachExits,
			"pm2 start hands the application to pm2's background daemon and returns, so the container's main process ends",
			"Start it with pm2-runtime (pm2-runtime start <file>), which keeps it in the foreground, or run node directly"}
	case foreverStartRE.MatchString(command):
		return &startDetachIssue{startDetachExits,
			"forever start runs the application as a background daemon and returns, so the container's main process ends",
			"Run forever <file> without start, or run node directly"}
	case gunicornDaemonRE.MatchString(command):
		return &startDetachIssue{startDetachExits,
			"gunicorn --daemon detaches from the shell, so the container's main process ends",
			"Remove --daemon (-D) from the gunicorn command"}
	case uwsgiDaemonRE.MatchString(command):
		return &startDetachIssue{startDetachExits,
			"uwsgi --daemonize detaches from the shell, so the container's main process ends",
			"Remove --daemonize from the uwsgi command"}
	case celeryMultiRE.MatchString(command):
		return &startDetachIssue{startDetachExits,
			"celery multi starts workers in the background and returns, so the container's main process ends",
			"Run celery -A <app> worker instead of celery multi"}
	case detachedTermRE.MatchString(command):
		return &startDetachIssue{startDetachExits,
			"a detached screen or tmux session returns at once, so the container's main process ends",
			"Run the server command itself, without screen or tmux"}
	}
	bare := redirectAmpRE.ReplaceAllString(quotedRE.ReplaceAllString(command, "''"), " ")
	bare = strings.TrimSpace(trailingWaitRE.ReplaceAllString(bare, ""))
	switch {
	case strings.HasSuffix(bare, "&"):
		return &startDetachIssue{startDetachExits,
			"the trailing & puts the server in the background and the shell exits, so the container's main process ends",
			"Remove the trailing & so the server stays in the foreground"}
	case strings.Contains(bare, "&"):
		return &startDetachIssue{startDetachBackgrounds,
			"& runs a second process in the background beside the server: nothing restarts it if it stops, and its crash does not fail the release",
			"Keep one process in the start command and deploy the other from the same source as its own worker project"}
	}
	return nil
}

// correctStartCommand rewrites a command that exits into the foreground form
// of the same thing, when that form is certain: pm2-runtime for pm2 and
// forever without start (when the package installs them), gunicorn without
// --daemon, and a lone command without its trailing &. runner is the
// package's exec runner, used when a command leaves the package script that
// had node_modules/.bin on its PATH; it is empty outside Node.
func correctStartCommand(command string, declared func(string) bool, runner string) (string, string) {
	command = strings.TrimSpace(command)
	if match := pm2SimpleRE.FindStringSubmatch(command); match != nil && declared("pm2") && runner != "" {
		return match[1] + runner + " pm2-runtime start " + strings.TrimSpace(match[2]),
			"pm2 start backgrounds the app in pm2's daemon; pm2-runtime keeps it in the foreground"
	}
	if match := foreverSimpleRE.FindStringSubmatch(command); match != nil && declared("forever") && runner != "" {
		return match[1] + runner + " forever " + strings.TrimSpace(match[2]),
			"forever start daemonizes the app; forever without start keeps it in the foreground"
	}
	if gunicornDaemonRE.MatchString(command) {
		fixed := strings.Join(strings.Fields(gunicornFlagRE.ReplaceAllString(command, "$1$2")), " ")
		if classifyStartCommand(fixed) == nil {
			return fixed, "gunicorn --daemon detaches; without it gunicorn stays in the foreground"
		}
	}
	if match := backgroundOnlyRE.FindStringSubmatch(command); match != nil {
		fixed := strings.TrimSpace(match[1])
		if fixed != "" && classifyStartCommand(fixed) == nil {
			return fixed, "a trailing & backgrounded the server; without it the server stays in the foreground"
		}
	}
	return "", ""
}

// withExecRunner makes a command taken out of a package script find the
// package's own binary, which `npm run` had put on its PATH. Only a binary
// the package installs is prefixed: the runner would try to download any
// other name from the registry.
func withExecRunner(command, runner string, declared func(string) bool) string {
	fields := strings.Fields(command)
	index := 0
	for index < len(fields) && strings.Contains(fields[index], "=") && !strings.HasPrefix(fields[index], "-") {
		index++
	}
	if runner == "" || index >= len(fields) || !declared(fields[index]) {
		return command
	}
	return strings.Join(append(append(append([]string{}, fields[:index]...), runner), fields[index:]...), " ")
}

// startSettler rewrites one candidate's start command, following the
// package scripts it runs.
type startSettler struct {
	manifest            nodeManifest
	node                bool
	runner              string
	yarnBerry           bool
	packagePath, source string
	evidence            []DetectionEvidence
	detach              *DetectedStartDetach
	// rewritten is the last command settle rewrote, kept to be recorded as
	// the detach when the script holding it cannot be replaced after all.
	rewritten *DetectedStartDetach
}

func (s *startSettler) declared(name string) bool { return s.node && s.manifest.has(name) }

// settle returns command with each segment that detaches rewritten, and
// whether anything changed. script is the package script command is the
// body of, and top the script the start command itself runs — the one a
// configured start command is matched against in preflight. A script whose
// body changed is replaced by that body, run outside the package script.
func (s *startSettler) settle(command, script, top string, depth int) (string, bool) {
	segments := strings.Split(command, " && ")
	changed := false
	for index, segment := range segments {
		segment = strings.TrimSpace(segment)
		if name := scriptSegmentName(segment); s.node && name != "" {
			body, ok := s.manifest.Scripts[name]
			if !ok || depth >= 3 {
				continue
			}
			owner := firstNonEmpty(top, name)
			mark := len(s.evidence)
			if fixed, bodyChanged := s.settle(body, name, owner, depth+1); bodyChanged {
				// Outside the package manager the body would lose the npm_*
				// variables it sets, so a body that reads them keeps its
				// script and the command is recorded as one detection could
				// not fix.
				if npmVariableRE.MatchString(body) {
					s.evidence = s.evidence[:mark]
					if s.detach == nil {
						s.detach = s.rewritten
					}
					continue
				}
				parts := strings.Split(fixed, " && ")
				for part := range parts {
					parts[part] = withExecRunner(strings.TrimSpace(parts[part]), s.runner, s.declared)
				}
				// The package manager ran the pre<name> hook before the
				// script (a prestart that migrates the database, say); the
				// replacement runs it the same way.
				if hook := s.preHook(segment, name); hook != "" {
					parts = append([]string{hook}, parts...)
				}
				segments[index], changed = strings.Join(parts, " && "), true
			}
			continue
		}
		issue := classifyStartCommand(segment)
		if issue == nil {
			continue
		}
		from := s.source
		if script != "" {
			from = s.packagePath
		}
		if issue.effect == startDetachExits {
			if fixed, why := correctStartCommand(segment, s.declared, s.runner); fixed != "" {
				segments[index], changed = fixed, true
				if script != "" {
					why = script + " script: " + why
				}
				s.evidence = append(s.evidence, DetectionEvidence{Path: from, Reason: boundedEvidence(why)})
				s.rewritten = &DetectedStartDetach{
					Command: boundedEvidence(segment), Script: top, Source: from,
					Effect: issue.effect, Reason: issue.reason, Action: issue.action,
				}
				continue
			}
		}
		s.detach = &DetectedStartDetach{
			Command: boundedEvidence(segment), Script: top, Source: from,
			Effect: issue.effect, Reason: issue.reason, Action: issue.action,
		}
	}
	return strings.Join(segments, " && "), changed
}

// preHook is the command that runs a script's pre<name> hook with the
// package manager segment named it with. Every manager the recipes install
// runs the hook before the script except Yarn 2 and later.
func (s *startSettler) preHook(segment, name string) string {
	hook := "pre" + name
	if strings.TrimSpace(s.manifest.Scripts[hook]) == "" {
		return ""
	}
	manager := strings.Fields(segment)[0]
	if manager == "yarn" && s.yarnBerry {
		return ""
	}
	return manager + " run " + hook
}

// yarnBerryPackageManager reports whether package.json pins Yarn 2 or
// later, which runs no pre and post hooks; without a pin Corepack installs
// Yarn 1, which runs them.
func yarnBerryPackageManager(packageJSON []byte) bool {
	var manifest struct {
		PackageManager string `json:"packageManager"`
	}
	if json.Unmarshal(packageJSON, &manifest) != nil {
		return false
	}
	version, ok := strings.CutPrefix(strings.TrimSpace(manifest.PackageManager), "yarn@")
	major, _, _ := strings.Cut(version, ".")
	return ok && major != "" && major != "0" && major != "1"
}

// settleStartCommand checks a recipe candidate's start command, and the
// package scripts it runs, for a command that detaches; it rewrites what it
// can and records what it cannot.
func settleStartCommand(candidate *DetectedCandidate, marker *detectedMarkers) {
	if candidate.StartCommand == "" {
		return
	}
	settler := &startSettler{packagePath: marker.packagePath, source: "start command"}
	settler.node = candidate.Recipe == "node" && parseNodeManifest(marker.packageJSON, &settler.manifest)
	if settler.node {
		settler.runner = nodeExecRunner(firstNonEmpty(candidate.PackageManager, "npm"))
		settler.yarnBerry = yarnBerryPackageManager(marker.packageJSON)
	}
	if procfile := procfileProcess(marker.procfile, "web"); procfile != "" && strings.Contains(candidate.StartCommand, procfile) {
		settler.source = path.Join(marker.root, "Procfile")
	}
	if settled, changed := settler.settle(candidate.StartCommand, "", "", 0); changed {
		candidate.StartCommand = settled
	}
	candidate.Evidence = append(candidate.Evidence, settler.evidence...)
	candidate.StartDetaches = settler.detach
}

// scriptSegmentName is the package script a start command segment runs:
// `npm run start`, `pnpm run serve`, or the `npm start` shorthand.
func scriptSegmentName(segment string) string {
	match := scriptSegmentRE.FindStringSubmatch(strings.TrimSpace(segment))
	if match == nil {
		return ""
	}
	return match[1] + match[2]
}

// settleDockerfileStart records a Dockerfile whose command detaches. The
// Dockerfile is the operator's own, so nothing is rewritten.
func settleDockerfileStart(candidate *DetectedCandidate, marker *detectedMarkers) {
	command := dockerfileStartText(marker.dockerfileContent)
	issue := classifyStartCommand(command)
	if issue == nil {
		return
	}
	candidate.StartDetaches = &DetectedStartDetach{
		Command: boundedEvidence(command), Source: marker.dockerfile,
		Effect: issue.effect, Reason: issue.reason, Action: issue.action,
	}
}

// dockerfileStartText is the command the final stage runs, as one shell
// line: the CMD, behind an ENTRYPOINT unless that is only an init or an
// entrypoint script that execs its arguments.
func dockerfileStartText(content []byte) string {
	command, entrypoint := "", ""
	for _, instruction := range dockerfileFinalStage(content) {
		switch instruction[0] {
		case "CMD":
			command = dockerfileCommandLine(instruction[1])
		case "ENTRYPOINT":
			entrypoint = dockerfileCommandLine(instruction[1])
		}
	}
	if entrypoint == "" || dockerfileWrapperEntrypoint(entrypoint) {
		return command
	}
	return strings.TrimSpace(entrypoint + " " + command)
}

func dockerfileCommandLine(arguments string) string {
	arguments = strings.TrimSpace(arguments)
	if !strings.HasPrefix(arguments, "[") {
		return arguments
	}
	var argv []string
	if json.Unmarshal([]byte(arguments), &argv) != nil {
		return arguments
	}
	if len(argv) >= 3 && (argv[0] == "sh" || argv[0] == "/bin/sh" || argv[0] == "bash" || argv[0] == "/bin/bash") && argv[1] == "-c" {
		return argv[2]
	}
	return strings.Join(argv, " ")
}

func dockerfileWrapperEntrypoint(entrypoint string) bool {
	first := path.Base(strings.Fields(entrypoint)[0])
	switch first {
	case "tini", "dumb-init", "tini-static":
		return true
	}
	return strings.Contains(first, "entrypoint")
}
