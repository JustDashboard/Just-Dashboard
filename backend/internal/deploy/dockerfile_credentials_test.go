package deploy

import (
	"errors"
	"strings"
	"testing"
)

// railsDockerfile is the Dockerfile `rails new` writes (railties 8.0/8.1
// Dockerfile.tt, rendered), verbatim down to its comments.
const railsDockerfile = `# syntax=docker/dockerfile:1
# check=error=true

# This Dockerfile is designed for production, not development. Use with Kamal or build'n'run by hand:
# docker build -t app .
# docker run -d -p 80:80 -e RAILS_MASTER_KEY=<value from config/master.key> --name app app

# Make sure RUBY_VERSION matches the Ruby version in .ruby-version
ARG RUBY_VERSION=3.4.1
FROM docker.io/library/ruby:$RUBY_VERSION-slim AS base

# Rails app lives here
WORKDIR /rails

# Install base packages
RUN apt-get update -qq && \
    apt-get install --no-install-recommends -y curl libjemalloc2 libvips sqlite3 && \
    rm -rf /var/lib/apt/lists /var/cache/apt/archives

# Set production environment
ENV RAILS_ENV="production" \
    BUNDLE_DEPLOYMENT="1" \
    BUNDLE_PATH="/usr/local/bundle" \
    BUNDLE_WITHOUT="development"

# Throw-away build stage to reduce size of final image
FROM base AS build

# Install packages needed to build gems
RUN apt-get update -qq && \
    apt-get install --no-install-recommends -y build-essential git pkg-config && \
    rm -rf /var/lib/apt/lists /var/cache/apt/archives

# Install application gems
COPY Gemfile Gemfile.lock ./
RUN bundle install && \
    rm -rf ~/.bundle/ "${BUNDLE_PATH}"/ruby/*/cache "${BUNDLE_PATH}"/ruby/*/bundler/gems/*/.git && \
    bundle exec bootsnap precompile --gemfile

# Copy application code
COPY . .

# Precompile bootsnap code for faster boot times
RUN bundle exec bootsnap precompile app/ lib/

# Precompiling assets for production without requiring secret RAILS_MASTER_KEY
RUN SECRET_KEY_BASE_DUMMY=1 ./bin/rails assets:precompile

# Final stage for app image
FROM base

# Copy built artifacts: gems, application
COPY --from=build "${BUNDLE_PATH}" "${BUNDLE_PATH}"
COPY --from=build /rails /rails

# Run and own only the runtime files as a non-root user for security
RUN groupadd --system --gid 1000 rails && \
    useradd rails --uid 1000 --gid 1000 --create-home --shell /bin/bash && \
    chown -R rails:rails db log storage tmp
USER 1000:1000

# Entrypoint prepares the database.
ENTRYPOINT ["/rails/bin/docker-entrypoint"]

# Start server via Thruster by default, this can be overwritten at runtime
EXPOSE 80
CMD ["./bin/thrust", "./bin/rails", "server"]
`

func TestCustomDockerfileRefusesOnlyLiteralCredentials(t *testing.T) {
	for _, fixture := range []struct {
		name, dockerfile string
		refused          bool
		issue            string
	}{
		{"rails 8 generated", railsDockerfile, false, ""},
		{"rails 7.1 line", "FROM ruby:3.3\nRUN SECRET_KEY_BASE_DUMMY=1 ./bin/rails assets:precompile\n", false, ""},
		{"rails before 7.1", "FROM ruby:3.2\nRUN SECRET_KEY_BASE=dummy bundle exec rake assets:precompile\n", false, ""},
		{"django collectstatic", "FROM python:3.13\nRUN SECRET_KEY=dummy python manage.py collectstatic --noinput\n", false, ""},
		{"django build-only key", "FROM python:3.13\nRUN SECRET_KEY=build-only python manage.py collectstatic\n", false, ""},
		{"empty build argument", "FROM node:22\nARG GITHUB_TOKEN=\"\"\nARG NPM_TOKEN=\n", false, ""},
		{"argument without default", "FROM node:22\nARG NPM_TOKEN\n", false, ""},
		{"reference only", "FROM ruby:3.3\nENV SECRET_KEY_BASE=$SECRET_KEY_BASE\nENV API_TOKEN=${API_TOKEN}\n", false, ""},
		{"tokenizer threads", "FROM python:3.13\nENV TOKENIZERS_PARALLELISM=false\n", false, ""},
		{"token budget", "FROM python:3.13\nENV MAX_TOKENS=4096\nENV OPENAI_MAX_TOKENS=2048\n", false, ""},
		{"secret file path", "FROM postgres:16\nENV POSTGRES_PASSWORD_FILE=/run/secrets/db_password\n", false, ""},
		{"build secret mount", "FROM node:22\nRUN --mount=type=secret,id=t NPM_TOKEN=$(cat /run/secrets/t) npm ci\n", false, ""},
		{"numeric expiry", "FROM node:22\nENV JWT_TOKEN_EXPIRES_IN=3600\n", false, ""},
		{"public build argument", "FROM node:22\nARG NEXT_PUBLIC_API_URL\nENV NEXT_PUBLIC_API_URL=$NEXT_PUBLIC_API_URL\nENV NEXT_TELEMETRY_DISABLED=1\n", false, ""},
		{"comment mentions a key", "FROM node:22\n# docker run -e API_TOKEN=ghp_realvalue123 app\nCMD [\"node\", \"server.js\"]\n", false, ""},
		{"env literal token", "FROM node:22\nENV API_TOKEN=ghp_abcdef0123456789\n", true, "line 2 (ENV) sets a literal value for API_TOKEN"},
		{"legacy env form", "FROM node:22\nENV API_TOKEN ghp_abcdef0123456789\n", true, "line 2 (ENV) sets a literal value for API_TOKEN"},
		{"argument default", "FROM node:22\nARG NPM_TOKEN=npm_abcdef0123456789\n", true, "line 2 (ARG) sets a literal value for NPM_TOKEN"},
		{"continuation hides it", "FROM node:22\nRUN a && \\\n  API_KEY=sk_live_abcdef ./x\n", true, "line 2 (RUN) passes a literal value for API_KEY"},
		{"comment inside continuation", "FROM node:22\nRUN a && \\\n# note\n  API_KEY=sk_live_abcdef ./x\n", true, "line 2 (RUN) passes a literal value for API_KEY"},
		{"literal default behind a reference", "FROM node:22\nENV API_TOKEN=${API_TOKEN:-ghp_abcdef0123}\n", true, "API_TOKEN"},
		{"password flag", "FROM mysql:8\nRUN mysql --password=hunter2 -e 'select 1'\n", true, "--password"},
		{"password flag value", "FROM mysql:8\nCMD [\"mysqld\", \"--password\", \"hunter2\"]\n", true, "--password"},
		{"heredoc script", "# syntax=docker/dockerfile:1\nFROM alpine\nRUN <<EOF\nset -e\nexport API_TOKEN=ghp_abcdef0123\nEOF\n", true, "line 4 (RUN) writes a literal value for API_TOKEN"},
		{"heredoc file", "# syntax=docker/dockerfile:1\nFROM alpine\nCOPY <<-EOF /app/.env\n\tDB_PASSWORD=hunter2\n\tEOF\n", true, "DB_PASSWORD"},
		{"private key", "FROM alpine\nRUN echo '-----BEGIN RSA PRIVATE KEY-----' > /k\n", true, "embeds a private key"},
		{"url credentials", "FROM alpine\nRUN git clone https://deploy:hunter2@example.com/app.git\n", true, "embeds a URL with credentials"},
		{"backtick escape", "# escape=`\nFROM mcr.microsoft.com/windows\nRUN a && `\n  API_KEY=sk_live_abcdef x\n", true, "line 3 (RUN) passes a literal value for API_KEY"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			err := validateCustomDockerfile([]byte(fixture.dockerfile))
			if fixture.refused != (err != nil) {
				t.Fatalf("refused = %v, want %v", err, fixture.refused)
			}
			if err == nil {
				return
			}
			if !errors.Is(err, ErrUnsupportedBuilder) || !strings.Contains(err.Error(), fixture.issue) {
				t.Fatalf("error = %v, want it to name %q", err, fixture.issue)
			}
			// The issue travels through detection evidence, which refuses a
			// literal assignment outright; it must name, never show.
			issue, _ := dockerfileCredentialIssue([]byte(fixture.dockerfile))
			if rejectPlanSecretLiteral("issue", issue.String()) != nil || strings.Contains(issue.String(), "hunter2") ||
				strings.Contains(issue.String(), "ghp_") || strings.Contains(issue.String(), "sk_live") {
				t.Fatalf("issue text leaks the value: %q", issue.String())
			}
		})
	}
}

func TestParseDockerfileReadsLogicalInstructions(t *testing.T) {
	parsed := parseDockerfile([]byte(railsDockerfile))
	keywords := []string{}
	for _, instruction := range parsed.Instructions {
		keywords = append(keywords, instruction.Keyword)
	}
	want := "ARG FROM WORKDIR RUN ENV FROM RUN COPY RUN COPY RUN RUN FROM COPY COPY RUN USER ENTRYPOINT EXPOSE CMD"
	if strings.Join(keywords, " ") != want {
		t.Fatalf("instructions = %s", strings.Join(keywords, " "))
	}
	env := parsed.Instructions[4]
	if env.Line != 21 || len(dockerfileKeyValues(env, parsed.Escape)) != 4 {
		t.Fatalf("ENV continuation = %+v", env)
	}
	copyFrom := parsed.Instructions[13]
	if from, ok := copyFrom.flag("from"); !ok || from != "build" {
		t.Fatalf("COPY --from = %q, %v", from, ok)
	}
	if entry := parsed.Instructions[17]; !entry.IsJSON || entry.JSON[0] != "/rails/bin/docker-entrypoint" {
		t.Fatalf("exec form = %+v", entry)
	}

	heredoc := parseDockerfile([]byte("FROM alpine\nRUN <<EOF bash\necho one\nEOF\nRUN cat <<<'not a heredoc'\nCMD [\"sh\"]\n"))
	if len(heredoc.Instructions) != 4 || len(heredoc.Instructions[1].Heredocs) != 1 ||
		heredoc.Instructions[1].Heredocs[0].Body != "echo one" || len(heredoc.Instructions[2].Heredocs) != 0 {
		t.Fatalf("heredocs = %+v", heredoc.Instructions)
	}
	if words := shellWords(`a "b c" 'd e' f\ g`, '\\'); strings.Join(words, "|") != "a|b c|d e|f g" {
		t.Fatalf("words = %q", words)
	}
}
