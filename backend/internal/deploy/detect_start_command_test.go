package deploy

import (
	"strings"
	"testing"
)

func TestClassifyStartCommandFindsDetachingForms(t *testing.T) {
	t.Parallel()
	for command, effect := range map[string]string{
		"pm2 start ecosystem.config.js --env production":        startDetachExits,
		"npx pm2 start server.js -i max":                        startDetachExits,
		"pnpm exec pm2 reload all":                              startDetachExits,
		"forever start app.js":                                  startDetachExits,
		"gunicorn app:app --daemon --bind 0.0.0.0:8000":         startDetachExits,
		"gunicorn -D app:app":                                   startDetachExits,
		"uwsgi --ini uwsgi.ini --daemonize /var/log/uwsgi.log":  startDetachExits,
		"celery -A proj multi start w1":                         startDetachExits,
		"screen -dmS app node server.js":                        startDetachExits,
		"node server.js &":                                      startDetachExits,
		"nohup node server.js > app.log 2>&1 &":                 startDetachExits,
		"node worker.js & node server.js":                       startDetachBackgrounds,
		"node a.js & node b.js & wait":                          startDetachBackgrounds,
		"pm2 start app.js --no-daemon":                          "",
		"pm2 start app.js && pm2 logs":                          "",
		"pm2-runtime start ecosystem.config.js":                 "",
		"node server.js 2>&1":                                   "",
		"node server.js &> /dev/stdout":                         "",
		"npx prisma migrate deploy && npm run start":            "",
		"sh -c 'echo \"a & b\" && node server.js'":              "",
		"gunicorn app:app --bind 0.0.0.0:8000":                  "",
		"node server.js & wait":                                 "",
		"python manage.py migrate --noinput && gunicorn x.wsgi": "",
		"uvicorn main:app --host 0.0.0.0 --port 8000 --reload":  "",
	} {
		issue := classifyStartCommand(command)
		got := ""
		if issue != nil {
			got = issue.effect
		}
		if got != effect {
			t.Fatalf("%q classified %q, want %q", command, got, effect)
		}
	}
}

// Start commands written for a VPS are rewritten into their foreground form
// when that form is certain; the rest is recorded for preflight to refuse.
func TestStartCommandsThatDetachAreRewrittenOrRecorded(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name, start, script, effect string
		files                       map[string]string
	}{
		{name: "pm2 start with pm2 installed",
			files: map[string]string{"package.json": `{"scripts":{"start":"pm2 start ecosystem.config.js --env production"},"dependencies":{"express":"^4.21.0","pm2":"^5.4.0"}}`, "package-lock.json": nodeLock},
			start: "npx pm2-runtime start ecosystem.config.js --env production"},
		{name: "pm2 start with pm2 under bun",
			files: map[string]string{"package.json": `{"scripts":{"start":"NODE_ENV=production pm2 start server.js --name api"},"dependencies":{"express":"^4.21.0","pm2":"^5.4.0"}}`, "bun.lock": ""},
			start: "NODE_ENV=production bunx pm2-runtime start server.js --name api"},
		{name: "pm2 start through another script",
			files: map[string]string{"package.json": `{"scripts":{"start":"npm run serve","serve":"pm2 start app.js"},"dependencies":{"fastify":"^5.0.0","pm2":"^5.4.0"}}`, "package-lock.json": nodeLock},
			start: "npx pm2-runtime start app.js"},
		{name: "pm2 start after a build in the same script",
			files: map[string]string{"package.json": `{"scripts":{"start":"npm run build && pm2 start dist/main.js","build":"tsc"},"dependencies":{"express":"^4.21.0","pm2":"^5.4.0","typescript":"^5.6.0"}}`, "package-lock.json": nodeLock},
			start: "npm run build && npx pm2-runtime start dist/main.js"},
		{name: "a package binary leaves its script with the runner",
			files: map[string]string{"package.json": `{"scripts":{"start":"cross-env NODE_ENV=production node server.js &"},"dependencies":{"express":"^4.21.0","cross-env":"^7.0.0"}}`, "package-lock.json": nodeLock},
			start: "npx cross-env NODE_ENV=production node server.js"},
		{name: "pm2 start keeps the prestart hook npm ran first",
			files: map[string]string{"package.json": `{"scripts":{"prestart":"prisma migrate deploy","start":"pm2 start dist/main.js"},"dependencies":{"express":"^4.21.0","pm2":"^5.4.0","prisma":"^6.0.0"}}`, "package-lock.json": nodeLock},
			start: "npm run prestart && npx pm2-runtime start dist/main.js"},
		{name: "pm2 start keeps the prestart hook under bun",
			files: map[string]string{"package.json": `{"scripts":{"prestart":"bun run migrate","start":"pm2 start server.js"},"dependencies":{"hono":"^4.0.0","pm2":"^5.4.0"}}`, "bun.lock": ""},
			start: "bun run prestart && bunx pm2-runtime start server.js"},
		{name: "pm2 start keeps the hook of the script it runs through",
			files: map[string]string{"package.json": `{"scripts":{"start":"npm run serve","preserve":"node scripts/migrate.js","serve":"pm2 start app.js"},"dependencies":{"fastify":"^5.0.0","pm2":"^5.4.0"}}`, "package-lock.json": nodeLock},
			start: "npm run preserve && npx pm2-runtime start app.js"},
		{name: "yarn 4 runs no prestart hook to keep",
			files: map[string]string{"package.json": `{"packageManager":"yarn@4.5.0","scripts":{"prestart":"node migrate.js","start":"pm2 start server.js"},"dependencies":{"express":"^4.21.0","pm2":"^5.4.0"}}`, "yarn.lock": ""},
			start: "yarn pm2-runtime start server.js"},
		{name: "a script reading npm variables keeps its package manager",
			files: map[string]string{"package.json": `{"scripts":{"start":"pm2 start server.js --name $npm_package_name"},"dependencies":{"express":"^4.21.0","pm2":"^5.4.0"}}`, "package-lock.json": nodeLock},
			start: "npm run start", script: "start", effect: startDetachExits},
		{name: "pm2 start without pm2 installed",
			files: map[string]string{"package.json": `{"scripts":{"start":"pm2 start app.js"},"dependencies":{"express":"^4.21.0"}}`, "package-lock.json": nodeLock},
			start: "npm run start", script: "start", effect: startDetachExits},
		{name: "forever start with forever installed",
			files: map[string]string{"package.json": `{"scripts":{"start":"forever start server.js"},"dependencies":{"koa":"^2.0.0","forever":"^4.0.0"}}`, "package-lock.json": nodeLock},
			start: "npx forever server.js"},
		{name: "trailing ampersand in a script",
			files: map[string]string{"package.json": `{"scripts":{"start":"node server.js &"},"dependencies":{"express":"^4.21.0"}}`, "package-lock.json": nodeLock},
			start: "node server.js"},
		{name: "trailing ampersand on a package binary",
			files: map[string]string{"package.json": `{"scripts":{"build":"next build","start":"next start -p 3000 &"},"dependencies":{"next":"16.0.0"}}`, "package-lock.json": nodeLock},
			start: "npx next start -p 3000"},
		{name: "two processes in one script",
			files: map[string]string{"package.json": `{"scripts":{"start":"node worker.js & node server.js"},"dependencies":{"express":"^4.21.0"}}`, "package-lock.json": nodeLock},
			start: "npm run start", script: "start", effect: startDetachBackgrounds},
		{name: "gunicorn daemon in a Procfile",
			files: map[string]string{"requirements.txt": "flask==3.0.0\ngunicorn==23.0.0\n", "Procfile": "web: gunicorn app:app --daemon --bind 0.0.0.0:8000\n",
				"app.py": "from flask import Flask\napp = Flask(__name__)\n"},
			start: "gunicorn app:app --bind 0.0.0.0:8000"},
		{name: "nohup with a log file stays recorded",
			files: map[string]string{"requirements.txt": "flask==3.0.0\n", "Procfile": "web: nohup python app.py > app.log 2>&1 &\n"},
			start: "nohup python app.py > app.log 2>&1 &", effect: startDetachExits},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			candidate := fixtureCandidate(t, detectFixture(t, fixture.files), BuildRecipe)
			if candidate.StartCommand != fixture.start {
				t.Fatalf("start = %q", candidate.StartCommand)
			}
			detach := candidate.StartDetaches
			if fixture.effect == "" {
				if detach != nil {
					t.Fatalf("recorded %+v", detach)
				}
				if !strings.Contains(evidenceText(candidate), "foreground") {
					t.Fatalf("rewrite left no evidence: %+v", candidate.Evidence)
				}
				return
			}
			if detach == nil || detach.Effect != fixture.effect || detach.Script != fixture.script || detach.Reason == "" || detach.Action == "" {
				t.Fatalf("detach = %+v", detach)
			}
		})
	}
}

func TestDockerfileCommandThatDetachesIsRecorded(t *testing.T) {
	t.Parallel()
	for dockerfile, detaches := range map[string]bool{
		"FROM node:22\nEXPOSE 3000\nCMD [\"pm2\", \"start\", \"server.js\"]\n":                               true,
		"FROM node:22\nEXPOSE 3000\nENTRYPOINT [\"/usr/bin/tini\", \"--\"]\nCMD pm2 start server.js\n":       true,
		"FROM node:22\nEXPOSE 3000\nCMD [\"pm2-runtime\", \"start\", \"server.js\"]\n":                       false,
		"FROM node:22\nEXPOSE 3000\nCMD [\"sh\", \"-c\", \"node server.js &\"]\n":                            true,
		"FROM node:22\nEXPOSE 3000\nENTRYPOINT [\"pm2\"]\nCMD [\"start\", \"server.js\", \"--no-daemon\"]\n": false,
	} {
		candidate := fixtureCandidate(t, detectFixture(t, map[string]string{"Dockerfile": dockerfile}), BuildDockerfile)
		if (candidate.StartDetaches != nil) != detaches {
			t.Fatalf("%q: %+v", dockerfile, candidate.StartDetaches)
		}
		if detaches && candidate.StartDetaches.Source != "Dockerfile" {
			t.Fatalf("source = %q", candidate.StartDetaches.Source)
		}
	}
}

func evidenceText(candidate DetectedCandidate) string {
	var reasons []string
	for _, evidence := range candidate.Evidence {
		reasons = append(reasons, evidence.Reason)
	}
	return strings.Join(reasons, "\n")
}

func TestYarnBerryPackageManager(t *testing.T) {
	t.Parallel()
	for manifest, berry := range map[string]bool{
		`{"packageManager":"yarn@4.5.0+sha512.abc"}`: true,
		`{"packageManager":"yarn@2.4.3"}`:            true,
		`{"packageManager":"yarn@1.22.22"}`:          false,
		`{"packageManager":"pnpm@9.0.0"}`:            false,
		`{}`:                                         false,
		`not json`:                                   false,
	} {
		if got := yarnBerryPackageManager([]byte(manifest)); got != berry {
			t.Fatalf("%s: %v", manifest, got)
		}
	}
}
