package deploy

import (
	"path"
	"sort"
	"strings"
)

// A chat bot or a queue consumer connects out and never listens. Planned as a
// web service it was given port 3000 (or 8000) and an HTTP readiness check,
// logged in normally, and was rolled back as a readiness timeout; planned as
// an unknown Python service it had no start command at all. Detection now
// recognises the libraries these processes are built on and plans them as
// workers: no port, no route, no readiness gate, stopped before the next one
// starts.

// DetectedBackgroundWorker is the evidence that a candidate is a worker.
type DetectedBackgroundWorker struct {
	Library  string `json:"library"`
	Kind     string `json:"kind"`
	Evidence string `json:"evidence"`
}

// nodeWorkerLibraries are packages a Node process is a bot or a queue
// consumer with. @slack/bolt is one only in socket mode; over HTTP it
// listens like any server.
var nodeWorkerLibraries = []struct{ dependency, kind string }{
	{"discord.js", "Discord bot"}, {"@discordjs/core", "Discord bot"}, {"eris", "Discord bot"}, {"oceanic.js", "Discord bot"},
	{"telegraf", "Telegram bot"}, {"grammy", "Telegram bot"}, {"node-telegram-bot-api", "Telegram bot"},
	{"@slack/bolt", "Slack bot in socket mode"}, {"whatsapp-web.js", "WhatsApp bot"},
	{"@whiskeysockets/baileys", "WhatsApp bot"}, {"baileys", "WhatsApp bot"}, {"mineflayer", "Minecraft bot"},
	{"tmi.js", "Twitch bot"}, {"bullmq", "BullMQ queue worker"}, {"bull", "Bull queue worker"},
	{"bee-queue", "Bee-Queue worker"}, {"agenda", "Agenda job worker"}, {"pg-boss", "pg-boss job worker"},
	{"graphile-worker", "Graphile Worker"}, {"node-cron", "scheduled job runner"}, {"cron", "scheduled job runner"},
	{"node-schedule", "scheduled job runner"}, {"toad-scheduler", "scheduled job runner"},
}

// pythonWorkerDistributions are the distributions that provide each bot
// library's import name; the import alone could be a vendored module.
var pythonWorkerDistributions = map[string][]string{
	"discord": {"discord-py", "py-cord"}, "nextcord": {"nextcord"}, "disnake": {"disnake"}, "hikari": {"hikari"},
	"interactions": {"discord-py-interactions"}, "telegram": {"python-telegram-bot"}, "telebot": {"pytelegrambotapi"},
	"aiogram": {"aiogram"}, "pyrogram": {"pyrogram", "pyrofork", "kurigram"}, "telethon": {"telethon"},
	"slack_bolt": {"slack-bolt"}, "twitchio": {"twitchio"},
}

func classifyBackgroundWorker(candidate *DetectedCandidate, marker *detectedMarkers, facts rootFacts) {
	switch candidate.Recipe {
	case "node":
		classifyNodeWorker(candidate, marker, facts)
	case "python":
		classifyPythonWorker(candidate, marker, facts)
	}
}

func classifyNodeWorker(candidate *DetectedCandidate, marker *detectedMarkers, facts rootFacts) {
	// A recognised framework or HTTP library is a server, and a Procfile web
	// process is the repository saying it is one.
	if candidate.Framework != "" || candidate.OutputDirectory != "" || candidate.StartCommand == "" ||
		procfileProcess(marker.procfile, "web") != "" {
		return
	}
	var manifest nodeManifest
	if !parseNodeManifest(marker.packageJSON, &manifest) {
		return
	}
	for _, library := range nodeWorkerLibraries {
		if !manifest.has(library.dependency) || (library.dependency == "@slack/bolt" && !facts.has(factSocketMode)) {
			continue
		}
		// A bot that also answers HTTP (the keep-alive server free hosts
		// ping) is a web service after all.
		if facts.has(factListen) {
			return
		}
		markWorker(candidate, &DetectedBackgroundWorker{
			Library: library.dependency, Kind: library.kind,
			Evidence: library.kind + " (" + library.dependency + "); nothing in the package listens for HTTP",
		}, marker.packagePath, ConfidenceMedium)
		return
	}
}

func classifyPythonWorker(candidate *DetectedCandidate, marker *detectedMarkers, facts rootFacts) {
	// Only a root no web framework or Procfile web process claimed: those are
	// servers, whatever else they run.
	if candidate.Framework != "python" || candidate.Profile == ProfileWeb || facts.has(factPythonServes) {
		return
	}
	deps := readPythonDependencies(marker.pythonFiles)
	manifest := path.Join(marker.root, deps.source)
	if worker := procfileProcess(marker.procfile, "worker"); worker != "" && rejectPlanSecretLiteral("Procfile worker process", worker) == nil {
		candidate.StartCommand = worker
		markWorker(candidate, &DetectedBackgroundWorker{
			Library: "Procfile", Kind: "worker process",
			Evidence: "Procfile worker process: " + boundedEvidence(worker),
		}, path.Join(marker.root, "Procfile"), ConfidenceHigh)
		return
	}
	for _, fact := range rootLevelFirst(facts.of(factPythonWorker)) {
		if !pythonHasAny(deps, pythonWorkerDistributions[fact.value]) {
			continue
		}
		// The entry is a script at the root, or a package's __main__; a
		// module deeper in the tree is imported by one of those.
		start := "python " + fact.file
		switch {
		case !strings.Contains(fact.file, "/"):
		case path.Base(fact.file) == "__main__.py" && strings.Count(fact.file, "/") == 1:
			start = "python -m " + path.Dir(fact.file)
		default:
			continue
		}
		kind := pythonWorkerModules[fact.value]
		candidate.StartCommand = start
		markWorker(candidate, &DetectedBackgroundWorker{
			Library: fact.value, Kind: kind,
			Evidence: kind + " in " + fact.file + "; it connects out and never listens for HTTP",
		}, path.Join(marker.root, fact.file), ConfidenceHigh)
		return
	}
	type queue struct {
		distribution, kind, fact string
		command                  func(module string) string
	}
	for _, worker := range []queue{
		{"celery", "Celery worker", factCeleryApp, func(module string) string { return "celery -A " + module + " worker --loglevel=info" }},
		{"rq", "RQ worker", "", func(string) string { return `rq worker --url "$REDIS_URL"` }},
		{"dramatiq", "Dramatiq worker", factDramatiqActor, func(module string) string { return "dramatiq " + module }},
		{"arq", "arq worker", factArqSettings, func(module string) string { return "arq " + module + ".WorkerSettings" }},
	} {
		if !deps.has(worker.distribution) {
			continue
		}
		module, evidence := "", manifest
		if worker.fact != "" {
			found := rootLevelFirst(facts.of(worker.fact))
			if len(found) == 0 {
				continue
			}
			module, evidence = pythonModule(found[0].file), path.Join(marker.root, found[0].file)
		}
		candidate.StartCommand = worker.command(module)
		markWorker(candidate, &DetectedBackgroundWorker{
			Library: worker.distribution, Kind: worker.kind,
			Evidence: worker.kind + " from " + evidence,
		}, evidence, ConfidenceHigh)
		return
	}
	for _, fact := range rootLevelFirst(facts.of(factSchedulerLoop)) {
		if strings.Contains(fact.file, "/") {
			continue
		}
		candidate.StartCommand = "python " + fact.file
		markWorker(candidate, &DetectedBackgroundWorker{
			Library: "scheduler", Kind: "scheduled job runner",
			Evidence: "scheduled job runner in " + fact.file + "; it never listens for HTTP",
		}, path.Join(marker.root, fact.file), ConfidenceHigh)
		return
	}
}

// markWorker plans a candidate as a worker and drops the question detection
// had asked about it, which the evidence now answers.
func markWorker(candidate *DetectedCandidate, worker *DetectedBackgroundWorker, evidencePath string, confidence DetectionConfidence) {
	candidate.BackgroundWorker = worker
	candidate.Profile, candidate.Port = ProfileWorker, 0
	candidate.Readiness = nil
	decisions := candidate.NeedsDecision[:0]
	answered := false
	for _, decision := range candidate.NeedsDecision {
		if strings.Contains(decision, "serves HTTP") || strings.Contains(decision, "ASGI/WSGI start command") {
			answered = true
			continue
		}
		decisions = append(decisions, decision)
	}
	candidate.NeedsDecision = decisions
	if confidenceRank(confidence) > confidenceRank(candidate.Confidence) && (answered || len(decisions) == 0) && candidate.RecipeIssue == "" {
		candidate.Confidence = confidence
	}
	candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: evidencePath, Reason: boundedEvidence(worker.Evidence)})
}

// rootLevelFirst orders facts shallowest first, then by the conventional
// entry names, so bot.py at the root wins over a helper in a package.
func rootLevelFirst(facts []readinessFact) []readinessFact {
	weight := func(file string) int {
		switch path.Base(file) {
		case "main.py", "bot.py", "__main__.py":
			return 0
		case "app.py", "run.py", "worker.py", "tasks.py", "celery.py":
			return 1
		}
		return 2
	}
	sorted := append([]readinessFact(nil), facts...)
	sort.SliceStable(sorted, func(i, j int) bool {
		di, dj := strings.Count(sorted[i].file, "/"), strings.Count(sorted[j].file, "/")
		if di != dj {
			return di < dj
		}
		if wi, wj := weight(sorted[i].file), weight(sorted[j].file); wi != wj {
			return wi < wj
		}
		return sorted[i].file < sorted[j].file
	})
	return sorted
}

func pythonHasAny(deps pythonDependencies, names []string) bool {
	for _, name := range names {
		if deps.has(name) {
			return true
		}
	}
	return false
}
