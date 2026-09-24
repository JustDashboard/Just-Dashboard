package deploy

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// A project runs one process. Most applications need more: a queue worker
// that sends the mail the web process enqueues, a scheduler, a WebSocket
// server, a release command that migrates. Only the web process used to be
// planned, so a Django app's Celery worker or a Laravel app's queue simply
// never ran — the deploy was green and the jobs piled up. Detection now reads
// every process the source declares or its framework implies, and preflight
// names each one that this project will not run, with the command a second
// project from the same source would start.

var (
	processNameRE    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	celeryAppRE      = regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*Celery\(`)
	celeryBeatRE     = regexp.MustCompile(`beat_schedule|CELERY_BEAT_SCHEDULE|CELERYBEAT_SCHEDULE`)
	laravelHealthyQs = map[string]bool{"sync": true, "null": true}
)

func validProcessKind(kind string) bool {
	switch kind {
	case "worker", "scheduler", "release", "web":
		return true
	}
	return false
}

func validProcessName(name string) bool { return processNameRE.MatchString(name) }

// procfileProcesses reads every `name: command` line of a Procfile, in order.
func procfileProcesses(content []byte) [][2]string {
	var result [][2]string
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, command, found := strings.Cut(line, ":")
		name, command = strings.TrimSpace(name), strings.TrimSpace(command)
		if !found || !validProcessName(name) || command == "" {
			continue
		}
		result = append(result, [2]string{name, command})
	}
	return result
}

// processKind reads what a process does from its name and command.
func processKind(name, command string) string {
	lower := strings.ToLower(name + " " + command)
	switch {
	case name == "release" || strings.Contains(lower, "release_command"):
		return "release"
	case strings.Contains(lower, "beat") || strings.Contains(lower, "clock") || strings.Contains(lower, "schedule") ||
		strings.Contains(lower, "cron"):
		return "scheduler"
	case strings.Contains(lower, "reverb:start") || strings.Contains(lower, "websocket") || strings.HasPrefix(name, "web"):
		return "web"
	}
	return "worker"
}

func (s *repoShapeScan) applyProcesses(result *DetectionResult, context shapeContext) {
	for index := range result.Candidates {
		candidate := &result.Candidates[index]
		if candidate.NotDeployable != "" || candidate.Profile == ProfileStatic || candidate.BuildMethod == BuildStatic ||
			candidate.BuildMethod == BuildCompose {
			continue
		}
		marker := context.markers[candidate.Root]
		if marker != nil && len(marker.procfile) > 0 {
			for _, process := range procfileProcesses(marker.procfile) {
				if process[0] == "web" {
					continue
				}
				candidate.Processes = appendProcesses(candidate.Processes, DetectedProcess{
					Name: process[0], Kind: processKind(process[0], process[1]), Command: process[1],
					Source: joinRoot(candidate.Root, "Procfile"), Reason: "Procfile declares the " + process[0] + " process",
				})
			}
		}
		if marker == nil {
			continue
		}
		if marker.hasPythonManifest() {
			candidate.Processes = appendProcesses(candidate.Processes, s.pythonProcesses(*candidate, marker, context)...)
		}
		if len(marker.composerJSON) > 0 {
			candidate.Processes = appendProcesses(candidate.Processes, s.phpProcesses(candidate, marker)...)
		}
		if len(marker.packageJSON) > 0 && candidate.Recipe == "node" {
			candidate.Processes = appendProcesses(candidate.Processes, s.nodeProcesses(*candidate, marker)...)
		}
	}
}

func hasProcessKind(processes []DetectedProcess, kind string) bool {
	for _, process := range processes {
		if process.Kind == kind {
			return true
		}
	}
	return false
}

// pythonProcesses proposes the Celery, RQ or Dramatiq processes a Python
// application's dependencies imply, unless its Procfile already declares a
// process of that kind.
func (s *repoShapeScan) pythonProcesses(candidate DetectedCandidate, marker *detectedMarkers, context shapeContext) []DetectedProcess {
	deps := readPythonDependencies(marker.pythonFiles)
	source := joinRoot(candidate.Root, deps.source)
	var result []DetectedProcess
	worker := hasProcessKind(candidate.Processes, "worker")
	scheduler := hasProcessKind(candidate.Processes, "scheduler")
	switch {
	case deps.has("celery"):
		module, beat := s.celeryApp(candidate.Root, context)
		if !worker {
			process := DetectedProcess{Name: "worker", Kind: "worker", Source: source,
				Reason: "celery is a dependency; its worker runs tasks the web process queues"}
			if module != "" {
				process.Command = "celery -A " + module + " worker --loglevel=info"
			} else {
				process.Reason += "; name the Celery app module (celery -A <module> worker)"
			}
			result = append(result, process)
		}
		if !scheduler && (beat || deps.has("django-celery-beat")) {
			process := DetectedProcess{Name: "beat", Kind: "scheduler", Source: source,
				Reason: "a Celery beat schedule is configured; beat enqueues the periodic tasks"}
			if module != "" {
				process.Command = "celery -A " + module + " beat --loglevel=info"
				if deps.has("django-celery-beat") {
					process.Command += " --scheduler django_celery_beat.schedulers:DatabaseScheduler"
				}
			}
			result = append(result, process)
		}
	case deps.has("rq") && !worker:
		command := "rq worker"
		for _, variable := range candidate.Variables {
			if variable.Name == "REDIS_URL" {
				command = `rq worker --url "$REDIS_URL"`
				break
			}
		}
		result = append(result, DetectedProcess{Name: "worker", Kind: "worker", Command: command, Source: source,
			Reason: "rq is a dependency; its worker runs the jobs the web process enqueues"})
	case deps.has("dramatiq") && !worker:
		result = append(result, DetectedProcess{Name: "worker", Kind: "worker", Source: source,
			Reason: "dramatiq is a dependency; start its workers with dramatiq <the module that defines the actors>"})
	case deps.has("arq") && !worker:
		result = append(result, DetectedProcess{Name: "worker", Kind: "worker", Source: source,
			Reason: "arq is a dependency; start its worker with arq <module>.WorkerSettings"})
	}
	return result
}

// celeryApp finds the module that defines the Celery application, relative
// to the candidate root, and whether a beat schedule is configured anywhere
// detection read.
func (s *repoShapeScan) celeryApp(root string, context shapeContext) (string, bool) {
	module := ""
	beat := false
	files := append(append([]pythonEntry(nil), s.pythonProcess...), context.pythonEntries...)
	for _, file := range sortedPythonEntries(files) {
		if !underRoot(file.path, root) {
			continue
		}
		if celeryBeatRE.Match(file.content) {
			beat = true
		}
		if module != "" {
			continue
		}
		match := celeryAppRE.FindSubmatch(file.content)
		if match == nil {
			continue
		}
		relative := strings.TrimPrefix(strings.TrimPrefix(file.path, root), "/")
		module = pythonModule(relative)
		if name := string(match[1]); name != "app" && name != "celery" {
			module += ":" + name
		}
	}
	return module, beat
}

// phpProcesses proposes a Laravel application's queue worker, scheduler and
// WebSocket server, and a Symfony application's Messenger consumer.
func (s *repoShapeScan) phpProcesses(candidate *DetectedCandidate, marker *detectedMarkers) []DetectedProcess {
	manifest, ok := parseComposerManifest(marker.composerJSON)
	if !ok {
		return nil
	}
	source := joinRoot(candidate.Root, "composer.json")
	var result []DetectedProcess
	worker := hasProcessKind(candidate.Processes, "worker")
	scheduler := hasProcessKind(candidate.Processes, "scheduler")
	switch {
	case manifest.has("laravel/framework"):
		queued := s.laravelQueuedJob(candidate.Root)
		connection := "database"
		for _, variable := range candidate.Variables {
			if variable.Name == "QUEUE_CONNECTION" && variable.Example != "" {
				connection = strings.ToLower(variable.Example)
			}
		}
		switch {
		case worker:
		case manifest.has("laravel/horizon"):
			result = append(result, DetectedProcess{Name: "horizon", Kind: "worker", Command: "php artisan horizon", Source: source,
				Reason: "laravel/horizon runs the queue workers"})
		case queued != "" && !laravelHealthyQs[connection]:
			result = append(result, DetectedProcess{Name: "queue", Kind: "worker", Command: "php artisan queue:work --tries=3 --max-time=3600", Source: queued,
				Reason: "queued jobs (ShouldQueue) on the " + connection + " connection need a queue worker"})
		}
		if schedule := s.laravelSchedule(candidate.Root); schedule != "" && !scheduler {
			result = append(result, DetectedProcess{Name: "scheduler", Kind: "scheduler", Command: "php artisan schedule:work", Source: schedule,
				Reason: "scheduled tasks run only while php artisan schedule:work (or a cron calling schedule:run) does"})
		}
		if manifest.has("laravel/reverb") {
			result = append(result, DetectedProcess{Name: "reverb", Kind: "web", Command: "php artisan reverb:start --host=0.0.0.0 --port=8080", Source: source,
				Reason: "laravel/reverb serves WebSockets from its own process on port 8080"})
		}
		if manifest.has("laravel/octane") {
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: source,
				Reason: "laravel/octane is installed; php artisan octane:frankenphp --host=0.0.0.0 --port=80 is an alternative start command"})
		}
	case manifest.has("symfony/messenger") && !worker && s.files[joinRoot(candidate.Root, "config/packages/messenger.yaml")]:
		result = append(result, DetectedProcess{Name: "messenger", Kind: "worker", Command: "php bin/console messenger:consume async --time-limit=3600",
			Source: joinRoot(candidate.Root, "config/packages/messenger.yaml"),
			Reason: "Symfony Messenger routes messages to a transport that a consumer process drains"})
	}
	return result
}

// nodeProcesses proposes a BullMQ worker when the application constructs one
// in a file of its own.
func (s *repoShapeScan) nodeProcesses(candidate DetectedCandidate, marker *detectedMarkers) []DetectedProcess {
	var manifest nodeManifest
	if !parseNodeManifest(marker.packageJSON, &manifest) || !manifest.has("bullmq") || hasProcessKind(candidate.Processes, "worker") {
		return nil
	}
	file := s.sources.firstUnder(s.sources.queueWorkers, candidate.Root)
	if file == "" {
		return nil
	}
	relative := strings.TrimPrefix(strings.TrimPrefix(file, candidate.Root), "/")
	for _, script := range []string{"start", "main"} {
		if strings.Contains(manifest.Scripts[script], path.Base(relative)) || strings.TrimPrefix(manifest.Main, "./") == relative {
			return nil
		}
	}
	runner := candidate.PackageManager
	if runner == "" {
		runner = "npm"
	}
	process := DetectedProcess{Name: "worker", Kind: "worker", Source: file,
		Reason: "bullmq Worker constructed in " + relative + ", which the start command does not run"}
	switch path.Ext(relative) {
	case ".js", ".mjs", ".cjs", ".ts", ".mts":
		process.Command = nodeEntryCommand(runner, relative)
	}
	return []DetectedProcess{process}
}

// laravelQueuedJob finds a class under the directories Laravel keeps jobs,
// mailables, notifications and listeners in that implements ShouldQueue. The
// read is bounded to 200 files and 1 MiB, apart from detection's own limits.
func (s *repoShapeScan) laravelQueuedJob(root string) string {
	files := []string{}
	for file := range s.files {
		for _, directory := range []string{"app/Jobs/", "app/Mail/", "app/Notifications/", "app/Listeners/"} {
			if strings.HasPrefix(file, rootPrefix(root)+directory) && strings.HasSuffix(file, ".php") {
				files = append(files, file)
				break
			}
		}
	}
	sort.Strings(files)
	read := int64(0)
	for index, file := range files {
		if index >= 200 || read >= 1<<20 {
			break
		}
		content, err := readContainedRegular(s.root, file, 256<<10)
		if err != nil {
			continue
		}
		read += int64(len(content))
		if strings.Contains(string(content), "ShouldQueue") {
			return file
		}
	}
	return ""
}

// laravelSchedule finds the scheduled tasks of Laravel 11's routes/console.php
// or the older console kernel.
func (s *repoShapeScan) laravelSchedule(root string) string {
	for _, candidate := range []struct{ file, marker string }{
		{"routes/console.php", "Schedule::"}, {"app/Console/Kernel.php", "$schedule->"},
	} {
		file := joinRoot(root, candidate.file)
		if !s.files[file] {
			continue
		}
		if content, err := readContainedRegular(s.root, file, 256<<10); err == nil && strings.Contains(string(content), candidate.marker) {
			return file
		}
	}
	return ""
}
