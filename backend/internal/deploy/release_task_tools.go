package deploy

import "strings"

// releaseTaskInstalledTools live only where the application's dependencies
// are installed — node_modules/.bin, a virtualenv, a bundle — which the
// unbuilt checkout a host task runs over never is.
var releaseTaskInstalledTools = map[string]bool{
	"prisma": true, "drizzle-kit": true, "knex": true, "sequelize": true, "sequelize-cli": true,
	"typeorm": true, "mikro-orm": true, "tsx": true, "ts-node": true, "alembic": true, "flask": true,
	"django-admin": true, "celery": true, "rails": true, "rake": true,
}

// releaseTaskAppCommands run the application's own code: package-manager
// runners and language interpreters, which on the host shell meet a checkout
// with none of its dependencies installed.
var releaseTaskAppCommands = map[string]bool{
	"npx": true, "bunx": true, "pnpm": true, "yarn": true, "npm": true, "bun": true, "node": true, "deno": true,
	"python": true, "python3": true, "pip": true, "uv": true, "poetry": true, "pipenv": true, "php": true,
	"composer": true, "ruby": true, "bundle": true, "mix": true, "elixir": true, "java": true, "mvn": true,
	"gradle": true, "dotnet": true, "cargo": true, "go": true, "artisan": true, "manage.py": true,
}

// releaseTaskInstalledTool names a program in a host task that can only
// exist once the application's dependencies are installed.
func releaseTaskInstalledTool(command string) (string, bool) {
	for _, token := range releaseTaskCommandTokens(command) {
		base := token[strings.LastIndex(token, "/")+1:]
		if releaseTaskInstalledTools[base] || strings.Contains(token, "node_modules/") || strings.Contains(token, ".venv/") ||
			strings.Contains(token, "vendor/bin/") {
			return token, true
		}
	}
	return "", false
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

// releaseTaskNeedsApplication names the program in a host task that runs the
// application's own code — an installed tool, a runner, an interpreter, a
// repository script — which the unbuilt checkout the host runner uses cannot
// serve, so such a task is never counted as the schema step.
func releaseTaskNeedsApplication(command string) (string, bool) {
	for _, token := range releaseTaskCommandTokens(command) {
		base := token[strings.LastIndex(token, "/")+1:]
		if _, installed := releaseTaskInstalledTool(token); installed || releaseTaskAppCommands[base] ||
			strings.HasPrefix(token, "./") || strings.HasPrefix(token, "bin/") {
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
			if !strings.ContainsAny(token, "/$`(){}!") && !shellBuiltins[token] {
				tools = append(tools, token)
			}
		}
	}
	return uniqueSorted(tools)
}

// shellBuiltins are words /bin/sh answers itself, which no PATH lookup finds.
var shellBuiltins = map[string]bool{
	".": true, ":": true, "[": true, "alias": true, "break": true, "case": true, "command": true, "continue": true,
	"do": true, "done": true, "echo": true, "elif": true, "else": true, "esac": true, "eval": true, "exit": true,
	"export": true, "false": true, "fi": true, "for": true, "if": true, "printf": true, "pwd": true, "read": true,
	"readonly": true, "return": true, "set": true, "shift": true, "source": true, "test": true, "then": true,
	"trap": true, "true": true, "type": true, "ulimit": true, "umask": true, "unset": true, "until": true,
	"wait": true, "while": true,
}
