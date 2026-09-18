package deploy

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestPHPDetectionAndRecipe(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct{ constraint, want string }{
		{"", "8.3"}, {"^8.2", "8.4"}, {">=8.1", "8.4"}, {"~8.3.0", "8.3"}, {"8.2.*", "8.2"}, {">=8.2 <8.4", "8.3"},
		{"^8.1 || ^8.2", "8.4"}, {"^7.4", ""}, {"8.1.*", ""},
	} {
		got, err := choosePHPRecipeVersion(fixture.constraint)
		if (fixture.want == "") != (err != nil) || got != fixture.want {
			t.Fatalf("php %q: version %q, %v", fixture.constraint, got, err)
		}
	}
	laravel := `{"require":{"php":"^8.2","laravel/framework":"^12.0","ext-intl":"*","ext-mbstring":"*","ext-redis":"*"}}`
	for _, fixture := range []struct {
		name       string
		files      map[string]string
		framework  string
		start      string
		confidence DetectionConfidence
		issue      string
		unpinned   bool
		candidates int
	}{
		{"laravel with vite assets", map[string]string{
			"composer.json": laravel, "composer.lock": "{}", "artisan": "", "public/index.php": "<?php",
			"package.json": `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"6","laravel-vite-plugin":"1"}}`, "package-lock.json": "{}",
		}, "laravel", "php artisan migrate --force && frankenphp php-server --listen :80 --root /app/public", ConfidenceHigh, "", false, 1},
		{"symfony with doctrine migrations", map[string]string{
			"composer.json": `{"require":{"php":">=8.2","symfony/framework-bundle":"7.2.*","doctrine/doctrine-migrations-bundle":"^3"}}`, "bin/console": "", "public/index.php": "<?php",
		}, "symfony", "php bin/console doctrine:migrations:migrate --no-interaction && frankenphp php-server --listen :80 --root /app/public", ConfidenceHigh, "", true, 1},
		{"plain public root", map[string]string{"composer.json": `{"require":{"slim/slim":"4.*"}}`, "public/index.php": "<?php"},
			"slim", "frankenphp php-server --listen :80 --root /app/public", ConfidenceHigh, "", true, 1},
		{"plain root index", map[string]string{"index.php": "<?php echo 'hi';"},
			"php", "frankenphp php-server --listen :80 --root /app", ConfidenceMedium, "", false, 1},
		{"unsupported php", map[string]string{"composer.json": `{"require":{"php":"^7.4"}}`, "index.php": "<?php"},
			"php", "frankenphp php-server --listen :80 --root /app", ConfidenceMedium, "8.2 to 8.4", true, 1},
		{"composer without an entry point", map[string]string{"composer.json": `{"require":{"monolog/monolog":"^3"}}`},
			"php", "frankenphp php-server --listen :80 --root /app", ConfidenceLow, "", true, 1},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for path, content := range fixture.files {
				writeBuildFixture(t, root, path, content)
			}
			result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
			if err != nil || len(result.Candidates) != fixture.candidates {
				t.Fatalf("detect: %+v, %v", result, err)
			}
			candidate := result.Candidates[0]
			if candidate.Recipe != "php" || candidate.Framework != fixture.framework || candidate.StartCommand != fixture.start ||
				candidate.Port != 80 || candidate.Profile != ProfileWeb || candidate.Confidence != fixture.confidence ||
				candidate.UnpinnedDependencies != fixture.unpinned || !strings.Contains(candidate.RecipeIssue, fixture.issue) {
				t.Fatalf("candidate = %+v", candidate)
			}
		})
	}
	// A WordPress-shaped tree has index.php files everywhere; only the root
	// and public/ ones name an application.
	root := t.TempDir()
	writeBuildFixture(t, root, "index.php", "<?php")
	writeBuildFixture(t, root, "wp-admin/index.php", "<?php")
	writeBuildFixture(t, root, "wp-content/themes/x/index.php", "<?php")
	result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
	if err != nil || len(result.Candidates) != 1 || result.Candidates[0].Root != "" {
		t.Fatalf("nested index.php files made roots: %+v, %v", result, err)
	}

	root = t.TempDir()
	writeBuildFixture(t, root, "composer.json", laravel)
	writeBuildFixture(t, root, "composer.lock", "{}")
	writeBuildFixture(t, root, "artisan", "")
	writeBuildFixture(t, root, "public/index.php", "<?php")
	writeBuildFixture(t, root, "package.json", `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"6","laravel-vite-plugin":"1"}}`)
	writeBuildFixture(t, root, "bun.lock", "")
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root,
		BuildPlanConfig{Method: BuildRecipe, Recipe: "php", StartCommand: "php artisan migrate --force && frankenphp php-server --listen :80 --root /app/public"}, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"FROM oven/bun:1-alpine@sha256:", "AS assets", "bun install --frozen-lockfile", "RUN bun run build",
		"FROM composer:2@sha256:", "AS composer", "FROM dunglas/frankenphp:1-php8.4-alpine@sha256:",
		"ENV COMPOSER_ALLOW_SUPERUSER=1 LOG_CHANNEL=stderr",
		"RUN install-php-extensions pdo_mysql pdo_pgsql opcache intl redis",
		"COPY --from=composer /usr/bin/composer /usr/bin/composer",
		"composer install --no-dev --optimize-autoloader --no-interaction --prefer-dist",
		"COPY --from=assets /app/public/build /app/public/build",
		// json.Marshal writes && as \u0026\u0026, which Docker's JSON CMD reads back as &&.
		"mkdir -p storage/framework/cache", `CMD ["/bin/sh","-c","php artisan migrate --force \u0026\u0026 frankenphp php-server --listen :80 --root /app/public"]`,
	} {
		if !strings.Contains(prepared.DockerfilePreview, want) {
			t.Fatalf("Dockerfile missing %q:\n%s", want, prepared.DockerfilePreview)
		}
	}
	if prepared.Toolchain != "php 8.4" || len(prepared.BaseImages) != 3 {
		t.Fatalf("prepared = %+v", prepared)
	}
	if strings.Contains(prepared.DockerfilePreview, "mbstring") {
		t.Fatal("a built-in extension was installed again")
	}

	root = t.TempDir()
	writeBuildFixture(t, root, "index.php", "<?php echo 'x';")
	prepared, err = NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root,
		BuildPlanConfig{Method: BuildRecipe, Recipe: "php", StartCommand: "frankenphp php-server --listen :80 --root /app"}, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prepared.DockerfilePreview, "composer install") || strings.Contains(prepared.DockerfilePreview, "AS assets") || !strings.Contains(prepared.DockerfilePreview, "1-php8.3-alpine@") {
		t.Fatalf("plain PHP Dockerfile:\n%s", prepared.DockerfilePreview)
	}
	if _, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root, BuildPlanConfig{Method: BuildRecipe, Recipe: "php"}, false, "t:1"); !errors.Is(err, ErrUnsupportedBuilder) {
		t.Fatalf("missing start accepted: %v", err)
	}
}

// Laravel's own .env.example names its engine in DB_CONNECTION and reads a
// URL from DB_URL; a sqlite default suggests nothing.
func TestLaravelDatabaseSuggestionFollowsDBConnection(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildFixture(t, root, "composer.json", `{"require":{"php":"^8.2","laravel/framework":"^12.0"}}`)
	writeBuildFixture(t, root, "artisan", "")
	writeBuildFixture(t, root, "public/index.php", "<?php")
	writeBuildFixture(t, root, ".env.example", "APP_KEY=\nDB_CONNECTION=pgsql\nDB_HOST=127.0.0.1\n")
	result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
	if err != nil || len(result.Candidates) != 1 {
		t.Fatalf("detect: %+v, %v", result, err)
	}
	databases := result.Candidates[0].Databases
	if len(databases) != 1 || databases[0].Engine != "postgres" || databases[0].Variable != "DB_URL" || databases[0].Evidence != "DB_CONNECTION=pgsql in .env.example" {
		t.Fatalf("databases = %+v", databases)
	}
	names := []string{}
	for _, variable := range result.Candidates[0].Variables {
		names = append(names, variable.Name)
	}
	if !slices.Equal(names, []string{"APP_KEY", "DB_CONNECTION", "DB_HOST"}) {
		t.Fatalf("variables = %v", names)
	}
	writeBuildFixture(t, root, ".env.example", "APP_KEY=\nDB_CONNECTION=sqlite\n")
	result, err = (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
	if err != nil || len(result.Candidates[0].Databases) != 0 {
		t.Fatalf("sqlite suggested an engine: %+v, %v", result.Candidates[0].Databases, err)
	}
}
