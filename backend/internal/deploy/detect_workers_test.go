package deploy

import (
	"slices"
	"strings"
	"testing"
)

// Bots and queue consumers connect out and never listen. They are planned
// as workers — no port, no readiness gate — unless something in the package
// does listen, which makes it a web service after all.
func TestBackgroundWorkersArePlannedWithoutAPort(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name       string
		files      map[string]string
		profile    WorkloadProfile
		confidence DetectionConfidence
		start      string
		library    string
	}{
		{name: "discord.js bot with a start script",
			files: map[string]string{"package.json": `{"scripts":{"start":"node index.js"},"dependencies":{"discord.js":"^14.16.0"}}`, "package-lock.json": nodeLock,
				"index.js": "const { Client } = require('discord.js')\nnew Client({ intents: [] }).login(process.env.DISCORD_TOKEN)\n"},
			profile: ProfileWorker, confidence: ConfidenceMedium, start: "npm run start", library: "discord.js"},
		{name: "bullmq consumer",
			files: map[string]string{"package.json": `{"scripts":{"start":"node worker.js"},"dependencies":{"bullmq":"^5.0.0","ioredis":"^5.0.0"}}`, "package-lock.json": nodeLock,
				"worker.js": "new Worker('mail', async job => {}, { connection })\n"},
			profile: ProfileWorker, confidence: ConfidenceMedium, start: "npm run start", library: "bullmq"},
		{name: "bot with an express keep-alive stays web",
			files:   map[string]string{"package.json": `{"scripts":{"start":"node index.js"},"dependencies":{"discord.js":"^14.16.0","express":"^4.21.0"}}`, "package-lock.json": nodeLock},
			profile: ProfileWeb, confidence: ConfidenceMedium, start: "npm run start"},
		{name: "bot with a node:http keep-alive stays web",
			files: map[string]string{"package.json": `{"scripts":{"start":"node index.js"},"dependencies":{"telegraf":"^4.16.0"}}`, "package-lock.json": nodeLock,
				"keep_alive.js": "require('http').createServer((req, res) => res.end('ok')).listen(8080)\n"},
			profile: ProfileWeb, confidence: ConfidenceMedium, start: "npm run start"},
		{name: "slack bolt over HTTP stays web",
			files: map[string]string{"package.json": `{"scripts":{"start":"node app.js"},"dependencies":{"@slack/bolt":"^4.0.0"}}`, "package-lock.json": nodeLock,
				"app.js": "const app = new App({ token, signingSecret })\nawait app.start(process.env.PORT || 3000)\n"},
			profile: ProfileWeb, confidence: ConfidenceMedium, start: "npm run start"},
		{name: "slack bolt in socket mode",
			files: map[string]string{"package.json": `{"scripts":{"start":"node app.js"},"dependencies":{"@slack/bolt":"^4.0.0"}}`, "package-lock.json": nodeLock,
				"app.js": "const app = new App({ token, socketMode: true, appToken })\nawait app.start()\n"},
			profile: ProfileWorker, confidence: ConfidenceMedium, start: "npm run start", library: "@slack/bolt"},
		{name: "discord.py bot in bot.py",
			files: map[string]string{"requirements.txt": "discord.py==2.4.0\n",
				"bot.py": "import discord\nclient = discord.Client(intents=discord.Intents.default())\nclient.run(os.environ['DISCORD_TOKEN'])\n"},
			profile: ProfileWorker, confidence: ConfidenceMedium, start: "python bot.py", library: "discord"},
		{name: "telegram bot in main.py",
			files: map[string]string{"requirements.txt": "python-telegram-bot==21.6\n",
				"main.py": "from telegram.ext import Application\napp = Application.builder().token(TOKEN).build()\napp.run_polling()\n"},
			profile: ProfileWorker, confidence: ConfidenceMedium, start: "python main.py", library: "telegram"},
		{name: "aiogram bot as a package",
			files: map[string]string{"pyproject.toml": "[project]\nname = \"bot\"\ndependencies = [\"aiogram>=3.13\"]\n",
				"bot/__main__.py": "import asyncio\nfrom aiogram import Bot, Dispatcher\nasyncio.run(dp.start_polling(bot))\n"},
			profile: ProfileWorker, confidence: ConfidenceMedium, start: "python -m bot", library: "aiogram"},
		{name: "aiogram webhook serves HTTP",
			files: map[string]string{"requirements.txt": "aiogram==3.13.0\naiohttp==3.10.0\n",
				"main.py": "from aiogram import Bot\nfrom aiohttp import web\nweb.run_app(app, host='0.0.0.0', port=8080)\n"},
			profile: ProfileWorker, confidence: ConfidenceLow, start: "python main.py"},
		{name: "celery-only repository",
			files:   map[string]string{"requirements.txt": "celery==5.4.0\nredis==5.0.0\n", "tasks.py": "from celery import Celery\napp = Celery('tasks', broker=BROKER)\n"},
			profile: ProfileWorker, confidence: ConfidenceMedium, start: "celery -A tasks worker --loglevel=info", library: "celery"},
		{name: "celery app in a package",
			files:   map[string]string{"requirements.txt": "celery==5.4.0\n", "proj/__init__.py": "", "proj/celery.py": "from celery import Celery\napp = Celery('proj')\n"},
			profile: ProfileWorker, confidence: ConfidenceMedium, start: "celery -A proj.celery worker --loglevel=info", library: "celery"},
		{name: "rq worker",
			files:   map[string]string{"requirements.txt": "rq==1.16.0\n", "jobs.py": "def send(): pass\n"},
			profile: ProfileWorker, confidence: ConfidenceMedium, start: `rq worker --url "$REDIS_URL"`, library: "rq"},
		{name: "arq worker",
			files:   map[string]string{"requirements.txt": "arq==0.26.0\n", "worker.py": "class WorkerSettings:\n    functions = [download]\n"},
			profile: ProfileWorker, confidence: ConfidenceMedium, start: "arq worker.WorkerSettings", library: "arq"},
		{name: "procfile worker",
			files:   map[string]string{"requirements.txt": "requests==2.32.0\n", "Procfile": "worker: python poller.py\n"},
			profile: ProfileWorker, confidence: ConfidenceMedium, start: "python poller.py", library: "Procfile"},
		{name: "a bot whose source was not all read stays web and asks",
			files: map[string]string{"package.json": `{"scripts":{"start":"node index.js"},"dependencies":{"discord.js":"^14.16.0"}}`, "package-lock.json": nodeLock,
				"index.js": "require('./keep_alive')\n", "keep_alive.js": "// " + strings.Repeat("x", readinessMaxFileBytes) + "\n"},
			profile: ProfileWeb, confidence: ConfidenceMedium, start: "npm run start"},
		{name: "a django project with celery stays web",
			files:   map[string]string{"requirements.txt": "django==5.2\ncelery==5.4.0\n", "manage.py": "", "mysite/wsgi.py": "", "mysite/celery.py": "app = Celery('mysite')\n"},
			profile: ProfileWeb, confidence: ConfidenceHigh, start: "python manage.py migrate --noinput && gunicorn --bind 0.0.0.0:${PORT:-8000} --access-logfile - mysite.wsgi:application"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			candidate := fixtureCandidate(t, detectFixture(t, fixture.files), BuildRecipe)
			if candidate.Profile != fixture.profile || candidate.Confidence != fixture.confidence || candidate.StartCommand != fixture.start {
				t.Fatalf("candidate = %+v", candidate)
			}
			if fixture.library == "" {
				if candidate.BackgroundWorker != nil {
					t.Fatalf("worker evidence on a server: %+v", candidate.BackgroundWorker)
				}
				if strings.Contains(evidenceText(candidate), "not every source file was read") &&
					!slices.ContainsFunc(candidate.NeedsDecision, func(decision string) bool { return strings.Contains(decision, "serves HTTP") }) {
					t.Fatalf("an unverified bot is not asked about: %q", candidate.NeedsDecision)
				}
				return
			}
			if candidate.BackgroundWorker == nil || candidate.BackgroundWorker.Library != fixture.library || candidate.Port != 0 || candidate.Readiness != nil {
				t.Fatalf("worker = %+v", candidate)
			}
			if slices.ContainsFunc(candidate.NeedsDecision, func(decision string) bool {
				return strings.Contains(decision, "serves HTTP") || strings.Contains(decision, "ASGI/WSGI")
			}) {
				t.Fatalf("answered decision kept: %q", candidate.NeedsDecision)
			}
		})
	}
}

// A worker beside a web application in another root must not tie with it:
// the web candidate is still the one an import selects.
func TestWorkerRootDoesNotOutrankItsWebSibling(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name  string
		files map[string]string
		web   string
	}{
		{name: "django web root and a celery worker root",
			files: map[string]string{
				"web/requirements.txt": "django==5.2\ngunicorn==23.0\n", "web/manage.py": "", "web/mysite/wsgi.py": "", "web/mysite/settings.py": "",
				"worker/requirements.txt": "celery==5.4.0\nredis==5.0.0\n", "worker/tasks.py": "from celery import Celery\napp = Celery('tasks')\n",
			},
			web: "web"},
		{name: "a Next.js root and a discord.py bot",
			files: map[string]string{
				"package.json": `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16.0.0"}}`, "package-lock.json": nodeLock,
				"bot/requirements.txt": "discord.py==2.4.0\n", "bot/bot.py": "import discord\nclient = discord.Client(intents=None)\nclient.run(TOKEN)\n",
			},
			web: ""},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			result := detectFixture(t, fixture.files)
			var worker bool
			for _, candidate := range result.Candidates {
				if candidate.BackgroundWorker != nil {
					worker = true
				}
				if candidate.ID == result.SelectedID && (candidate.Root != fixture.web || candidate.Profile != ProfileWeb) {
					t.Fatalf("selected %+v", candidate)
				}
			}
			if !worker || result.SelectedID == "" {
				t.Fatalf("selected %q among %+v", result.SelectedID, result.Candidates)
			}
		})
	}
}
