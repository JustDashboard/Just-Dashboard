package deploy

import "strings"

// releaseTaskAppCommands run only where the application's dependencies are
// installed: package-manager runners, language interpreters, and the tools
// that live in node_modules/.bin, a virtualenv or a bundle.
var releaseTaskAppCommands = map[string]bool{
	"npx": true, "bunx": true, "pnpm": true, "yarn": true, "npm": true, "bun": true, "node": true, "deno": true,
	"tsx": true, "ts-node": true, "python": true, "python3": true, "pip": true, "uv": true, "poetry": true,
	"pipenv": true, "php": true, "composer": true, "ruby": true, "bundle": true, "rails": true, "rake": true,
	"mix": true, "elixir": true, "java": true, "mvn": true, "gradle": true, "dotnet": true, "cargo": true,
	"go": true, "prisma": true, "drizzle-kit": true, "knex": true, "sequelize": true, "sequelize-cli": true,
	"typeorm": true, "mikro-orm": true, "alembic": true, "flask": true, "django-admin": true, "celery": true,
	"artisan": true, "manage.py": true,
}

// releaseTaskCommandTokens are the program each segment of a shell command
// line runs: `cd api && npx prisma migrate deploy; ./bin/seed` names npx and
// ./bin/seed.
func releaseTaskCommandTokens(command string) []string {
	tokens := []string{}
	replacer := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n")
	for _, segment := range strings.Split(replacer.Replace(command), "\n") {
		words := shellWords(strings.TrimSpace(segment), '\\')
		for len(words) > 0 {
			switch {
			case words[0] == "env" || words[0] == "exec" || words[0] == "time" || words[0] == "nohup":
				words = words[1:]
				continue
			case strings.Contains(words[0], "=") && !strings.HasPrefix(words[0], "-"):
				words = words[1:]
				continue
			}
			break
		}
		if len(words) == 0 || words[0] == "cd" {
			continue
		}
		tokens = append(tokens, words[0])
	}
	return tokens
}

// releaseTaskNeedsApplication names the program in a host task that cannot
// work over the unbuilt checkout the host runner uses.
func releaseTaskNeedsApplication(command string) (string, bool) {
	for _, token := range releaseTaskCommandTokens(command) {
		base := token[strings.LastIndex(token, "/")+1:]
		if releaseTaskAppCommands[base] || strings.Contains(token, "node_modules/") || strings.Contains(token, ".venv/") ||
			strings.HasPrefix(token, "./") || strings.HasPrefix(token, "bin/") || strings.HasPrefix(token, "vendor/") {
			return token, true
		}
	}
	return "", false
}

// releaseTaskHostTools are the programs host tasks run that the host has to
// provide itself, for preflight to look up without executing anything.
func releaseTaskHostTools(tasks []ReleaseTaskConfig) []string {
	tools := []string{}
	for _, task := range tasks {
		if task.Runner == ReleaseTaskRunnerImage {
			continue
		}
		for _, token := range releaseTaskCommandTokens(task.Command) {
			if !strings.ContainsAny(token, "/$`") {
				tools = append(tools, token)
			}
		}
	}
	return uniqueSorted(tools)
}
