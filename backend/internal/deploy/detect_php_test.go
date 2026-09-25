package deploy

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// phpPlan detects a PHP tree, prepares the plan detection proposes and runs
// preflight over it, the way a draft is reviewed before Deploy.
func phpPlan(t *testing.T, files map[string]string, adjust ...func(*BuildPlanConfig)) (DetectedCandidate, PreparedBuild, []PreflightFinding) {
	t.Helper()
	result, candidate := detectNodeTree(t, files)
	build := BuildPlanConfig{Method: candidate.BuildMethod, Recipe: candidate.Recipe, BuildCommand: candidate.BuildCommand, StartCommand: candidate.StartCommand}
	for _, change := range adjust {
		change(&build)
	}
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), writeNodeTree(t, files), build, false, "t:1")
	if err != nil {
		t.Fatalf("prepare %+v: %v", build, err)
	}
	return candidate, prepared, preflightFindings(nodeDraft(result), nodeTestConfiguration(build), dockerHost, false)
}

// composerLockJSON writes a composer.lock the way Composer does, from
// "name version" and the package's requirements.
func composerLockJSON(t *testing.T, packages map[string]map[string]string, dev ...string) string {
	t.Helper()
	entry := func(nameVersion string, require map[string]string) map[string]any {
		name, version, _ := strings.Cut(nameVersion, " ")
		return map[string]any{"name": name, "version": version, "require": require,
			"dist": map[string]string{"type": "zip", "url": "https://example.test/" + name + ".zip"}}
	}
	lock := map[string]any{"packages": []any{}, "packages-dev": []any{}}
	names := []string{}
	for name := range packages {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		lock["packages"] = append(lock["packages"].([]any), entry(name, packages[name]))
	}
	for _, name := range dev {
		lock["packages-dev"] = append(lock["packages-dev"].([]any), entry(name, nil))
	}
	encoded, err := json.Marshal(lock)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

const laravelSkeleton = `{"require":{"php":"^8.2","laravel/framework":"^12.0"}}`

func laravelTree(extra map[string]string) map[string]string {
	files := map[string]string{"composer.json": laravelSkeleton, "artisan": "", "public/index.php": "<?php"}
	for name, content := range extra {
		files[name] = content
	}
	return files
}

// The extensions a locked package requires are installed before Composer
// runs, so `composer install` does not stop on "it is missing from your
// system"; a require-dev package's are not, since --no-dev never checks them.
func TestPHPExtensionsFromTheLockAndTheCode(t *testing.T) {
	t.Parallel()
	lock := composerLockJSON(t, map[string]map[string]string{
		"laravel/framework v12.1.0":            {"php": "^8.2", "ext-mbstring": "*"},
		"filament/support v3.3.0":              {"ext-intl": "*"},
		"phpoffice/phpspreadsheet 4.1.0":       {"ext-gd": "*", "ext-zip": "*", "ext-zend-opcache": "*"},
		"acme/exotic 1.0.0":                    {"ext-imaginary": "*"},
		"spatie/laravel-medialibrary v11.12.0": {"ext-exif": "*"},
	}, "barryvdh/laravel-debugbar v3.14.0")
	candidate, prepared, findings := phpPlan(t, laravelTree(map[string]string{
		"composer.json":         `{"require":{"php":"^8.2","laravel/framework":"^12.0","filament/support":"^3.3","phpoffice/phpspreadsheet":"^4.1","acme/exotic":"^1.0","spatie/laravel-medialibrary":"^11.12"},"require-dev":{"barryvdh/laravel-debugbar":"^3.14"}}`,
		"composer.lock":         lock,
		"app/Support/Money.php": "<?php $f = new \\NumberFormatter('en', 1); echo bcadd('1', '2');",
	}))
	assertDockerfile(t, prepared.DockerfilePreview, []string{
		"RUN install-php-extensions pdo_mysql pdo_pgsql mysqli opcache intl gd zip exif bcmath\n",
		"RUN composer install --no-dev --optimize-autoloader --no-interaction --prefer-dist\n",
	}, []string{"imaginary", "mbstring"})
	facts := candidate.PHP
	if facts == nil || facts.Lock != "read" || len(facts.Unsupported) != 1 || facts.Unsupported[0].Name != "imaginary" ||
		!slices.ContainsFunc(facts.Extensions, func(extension DetectedPHPExtension) bool {
			return extension.Name == "intl" && extension.Reason == "ext-intl required by filament/support (composer.lock)"
		}) || !slices.ContainsFunc(candidate.Evidence, func(evidence DetectionEvidence) bool {
		return evidence.Reason == "PHP extension bcmath: a bc* function in app/Support/Money.php → bcmath"
	}) {
		t.Fatalf("facts = %+v\nevidence = %+v", facts, candidate.Evidence)
	}
	if pass := findingByCode(findings, "php_extensions"); pass == nil || pass.Severity != PreflightPass ||
		pass.Measured != "pdo_mysql, pdo_pgsql, mysqli, opcache, intl, gd, zip, exif, bcmath" {
		t.Fatalf("php_extensions = %+v", pass)
	}
	if unsupported := findingByCode(findings, "php_extension_unsupported"); unsupported == nil || !strings.Contains(unsupported.Measured, "imaginary (ext-imaginary required by acme/exotic (composer.lock))") {
		t.Fatalf("php_extension_unsupported = %+v", unsupported)
	}

	// Without a lock the packages' own requirements are not in the tree; the
	// table stands in for the common ones, and a plain PHP application's
	// mysqli is a default.
	candidate, prepared, findings = phpPlan(t, laravelTree(map[string]string{
		"composer.json": `{"require":{"php":"^8.2","laravel/framework":"^12.0","filament/filament":"^3.3","maatwebsite/excel":"^3.1"}}`,
		".env.example":  "APP_KEY=\nREDIS_CLIENT=phpredis\nREDIS_HOST=127.0.0.1\n",
	}))
	assertDockerfile(t, prepared.DockerfilePreview, []string{"RUN install-php-extensions pdo_mysql pdo_pgsql mysqli opcache intl gd zip redis\n"}, nil)
	if candidate.PHP.Lock != "absent" || findingByCode(findings, "dependencies_unpinned") == nil ||
		!strings.Contains(findingByCode(findings, "dependencies_unpinned").Action, "commit composer.lock") {
		t.Fatalf("unlocked = %+v / %+v", candidate.PHP, findingByCode(findings, "dependencies_unpinned"))
	}
	// predis/predis speaks Redis in PHP, so phpredis is not needed.
	_, prepared, _ = phpPlan(t, laravelTree(map[string]string{
		"composer.json": `{"require":{"php":"^8.2","laravel/framework":"^12.0","predis/predis":"^2.0"}}`,
		".env.example":  "REDIS_CLIENT=predis\nREDIS_HOST=127.0.0.1\n",
	}))
	assertDockerfile(t, prepared.DockerfilePreview, nil, []string{" redis\n"})

	_, prepared, _ = phpPlan(t, map[string]string{
		"index.php":       "<?php require 'includes/db.php';",
		"includes/db.php": "<?php $db = mysqli_connect(getenv('DB_HOST'), 'u', 'p'); $im = imagecreatetruecolor(1, 1); $z = new ZipArchive();",
	})
	assertDockerfile(t, prepared.DockerfilePreview, []string{"RUN install-php-extensions pdo_mysql pdo_pgsql mysqli opcache gd zip\n"}, nil)

	// A lock too large to read, or not one Composer wrote, is said.
	_, _, findings = phpPlan(t, laravelTree(map[string]string{"composer.lock": "{}"}))
	if unverified := findingByCode(findings, "php_extensions_unverified"); unverified == nil || unverified.Severity != PreflightWarning {
		t.Fatalf("php_extensions_unverified = %+v", unverified)
	}
}

// A stale composer.lock is the npm incident's Composer twin: detected before
// Deploy, and built by updating just the packages the lock no longer
// matches rather than stopping on "is not present in the lock file".
func TestComposerLockStaleOrMissing(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"composer.json": `{"require":{"php":"^8.2","psr/log":"^3.0","psr/container":"^2.0","monolog/monolog":"^3.0","symfony/polyfill-php80":"^1.0","illuminate/contracts":"^11.0"},"require-dev":{"phpunit/phpunit":"^11.0"}}`,
		"composer.lock": composerLockJSON(t, map[string]map[string]string{
			"psr/log 3.0.2": nil, "monolog/monolog 2.9.3": nil, "illuminate/support v11.4.0": nil,
		}),
		"public/index.php": "<?php",
	}
	var lock map[string]any
	_ = json.Unmarshal([]byte(files["composer.lock"]), &lock)
	// A package can be satisfied by another that replaces it.
	for _, pkg := range lock["packages"].([]any) {
		if entry := pkg.(map[string]any); entry["name"] == "illuminate/support" {
			entry["replace"] = map[string]string{"illuminate/contracts": "self.version", "symfony/polyfill-php80": "*"}
		}
	}
	encoded, _ := json.Marshal(lock)
	files["composer.lock"] = string(encoded)
	candidate, prepared, findings := phpPlan(t, files)
	if facts := candidate.PHP; !slices.Equal(facts.LockMissing, []string{"psr/container"}) ||
		!slices.Equal(facts.LockOutdated, []string{"monolog/monolog is locked at 2.9.3, composer.json requires ^3.0"}) ||
		!slices.Equal(facts.DevLockOutdated, []string{"phpunit/phpunit is not in composer.lock"}) {
		t.Fatalf("lock facts = %+v", facts)
	}
	assertDockerfile(t, prepared.DockerfilePreview, []string{
		"RUN composer update --no-dev --optimize-autoloader --no-interaction --prefer-dist --with-all-dependencies monolog/monolog psr/container\n",
	}, []string{"composer install"})
	for _, code := range []string{"composer_lock_missing_packages", "composer_lock_outdated", "composer_lock_dev_outdated"} {
		if item := findingByCode(findings, code); item == nil || item.Severity != PreflightWarning {
			t.Fatalf("%s = %+v", code, item)
		}
	}
	if findingByCode(findings, "composer_lock_missing_packages").Action != "Run composer update psr/container and commit composer.lock." {
		t.Fatalf("action = %q", findingByCode(findings, "composer_lock_missing_packages").Action)
	}
	// Branch and alias versions are never a verdict.
	candidate, prepared, _ = phpPlan(t, map[string]string{
		"composer.json":    `{"require":{"acme/wip":"dev-main","acme/lib":"^1.0"}}`,
		"composer.lock":    composerLockJSON(t, map[string]map[string]string{"acme/wip dev-main": nil, "acme/lib 1.x-dev": nil}),
		"public/index.php": "<?php",
	})
	if len(candidate.PHP.LockMissing) != 0 || len(candidate.PHP.LockOutdated) != 0 || !strings.Contains(prepared.DockerfilePreview, "composer install") {
		t.Fatalf("branch versions judged: %+v", candidate.PHP)
	}
}

// The release: composer.json and every locked package agree on it, a
// narrower lock wins over a wide root constraint, config.platform.php is
// preferred, 8.5 is built only when required, and a build setting overrides.
func TestPHPVersionFromComposerAndTheLock(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name, composer string
		locked         map[string]map[string]string
		version        string
		issue          string
	}{
		{"root only", `{"require":{"php":"^8.1"}}`, nil, "8.4", ""},
		{"lock narrows", `{"require":{"php":"^8.1","laminas/laminas-diactoros":"^3.3"}}`,
			map[string]map[string]string{"laminas/laminas-diactoros 3.3.1": {"php": "~8.1.0 || ~8.2.0 || ~8.3.0"}}, "8.3", ""},
		{"platform", `{"require":{"php":"^8.1"},"config":{"platform":{"php":"8.2.10"}}}`, nil, "8.2", ""},
		{"platform false values", `{"require":{"php":"^8.2"},"config":{"platform":{"ext-mongodb":false}}}`, nil, "8.4", ""},
		{"required 8.5", `{"require":{"php":">=8.5"}}`, nil, "8.5", ""},
		{"lock conflicts", `{"require":{"php":"^8.2","acme/old":"^1.0"}}`,
			map[string]map[string]string{"acme/old 1.0.0": {"php": "~7.4.0"}}, "", "acme/old requires ~7.4.0"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			files := map[string]string{"composer.json": fixture.composer, "public/index.php": "<?php"}
			if fixture.locked != nil {
				files["composer.lock"] = composerLockJSON(t, fixture.locked)
			}
			_, candidate := detectNodeTree(t, files)
			if fixture.issue != "" {
				if !strings.Contains(candidate.RecipeIssue, fixture.issue) || !strings.Contains(candidate.RecipeIssue, "PHP 8.2 to 8.5") {
					t.Fatalf("issue = %q", candidate.RecipeIssue)
				}
				return
			}
			if candidate.PHP == nil || candidate.PHP.Version != fixture.version {
				t.Fatalf("php = %+v, issue %q", candidate.PHP, candidate.RecipeIssue)
			}
			prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), writeNodeTree(t, files),
				BuildPlanConfig{Method: BuildRecipe, Recipe: "php", StartCommand: candidate.StartCommand}, false, "t:1")
			if err != nil || !strings.Contains(prepared.DockerfilePreview, "FROM dunglas/frankenphp:1-php"+fixture.version+"-alpine@sha256:") ||
				prepared.Toolchain != "php "+fixture.version {
				t.Fatalf("prepared = %+v, %v", prepared, err)
			}
		})
	}
	files := map[string]string{
		"composer.json":    `{"require":{"php":"^8.1","laminas/laminas-diactoros":"^3.3"}}`,
		"composer.lock":    composerLockJSON(t, map[string]map[string]string{"laminas/laminas-diactoros 3.3.1": {"php": "~8.1.0 || ~8.2.0 || ~8.3.0"}}),
		"public/index.php": "<?php",
	}
	candidate, prepared, findings := phpPlan(t, files, func(build *BuildPlanConfig) { build.PHPVersion = "8.4" })
	if !slices.ContainsFunc(candidate.Evidence, func(evidence DetectionEvidence) bool {
		return evidence.Reason == "php ^8.1; laminas/laminas-diactoros requires ~8.1.0 || ~8.2.0 || ~8.3.0 (composer.lock) → PHP 8.3"
	}) {
		t.Fatalf("evidence = %+v", candidate.Evidence)
	}
	if !strings.Contains(prepared.DockerfilePreview, "1-php8.4-alpine") {
		t.Fatalf("the build setting did not choose the release:\n%s", prepared.DockerfilePreview)
	}
	if refused := findingByCode(findings, "php_version_unsupported"); refused == nil || refused.Severity != PreflightBlocked ||
		refused.FieldID != "configuration.build.phpVersion" || !strings.Contains(refused.Measured, "laminas/laminas-diactoros requires") {
		t.Fatalf("php_version_unsupported = %+v", refused)
	}
	configuration := nodeTestConfiguration(BuildPlanConfig{Method: BuildRecipe, Recipe: "php", PHPVersion: "8.6", StartCommand: "x"})
	if configuration.Validate() == nil {
		t.Fatal("a release outside the catalogue was accepted")
	}
	configuration = nodeTestConfiguration(BuildPlanConfig{Method: BuildRecipe, Recipe: "node", PHPVersion: "8.4", StartCommand: "x"})
	if configuration.Validate() == nil {
		t.Fatal("a PHP release was accepted for another recipe")
	}
}

// Frameworks whose front controller is not public/ are served from their own
// document root, and their migrations run only where there are some.
func TestPHPDocumentRootConventions(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name      string
		files     map[string]string
		framework string
		start     string
	}{
		{"cakephp", map[string]string{"composer.json": `{"require":{"cakephp/cakephp":"^5.1","cakephp/migrations":"^4.0"}}`, "index.php": "<?php require 'webroot/index.php';",
			"webroot/index.php": "<?php", "config/Migrations/20240101000000_Initial.php": "<?php"},
			"cakephp", "php bin/cake.php migrations migrate && frankenphp php-server --listen :80 --root /app/webroot"},
		{"cakephp without migrations", map[string]string{"composer.json": `{"require":{"cakephp/cakephp":"^5.1"}}`, "index.php": "<?php", "webroot/index.php": "<?php"},
			"cakephp", "frankenphp php-server --listen :80 --root /app/webroot"},
		{"codeigniter", map[string]string{"composer.json": `{"require":{"codeigniter4/framework":"^4.6"}}`, "spark": "", "public/index.php": "<?php",
			"app/Database/Migrations/2024-01-01-000000_Users.php": "<?php"},
			"codeigniter", "php spark migrate --all && frankenphp php-server --listen :80 --root /app/public"},
		{"codeigniter without migrations", map[string]string{"composer.json": `{"require":{"codeigniter4/framework":"^4.6"}}`, "public/index.php": "<?php"},
			"codeigniter", "frankenphp php-server --listen :80 --root /app/public"},
		{"yii", map[string]string{"composer.json": `{"require":{"yiisoft/yii2":"~2.0.51"}}`, "web/index.php": "<?php", "yii": ""},
			"yii", "frankenphp php-server --listen :80 --root /app/web"},
		{"drupal", map[string]string{"composer.json": `{"require":{"drupal/core-recommended":"^11.1"},"extra":{"drupal-scaffold":{"locations":{"web-root":"docroot/"}}}}`},
			"drupal", "frankenphp php-server --listen :80 --root /app/docroot"},
		{"mezzio", map[string]string{"composer.json": `{"require":{"mezzio/mezzio":"^3.20"}}`, "public/index.php": "<?php"},
			"mezzio", "frankenphp php-server --listen :80 --root /app/public"},
		{"plain htdocs", map[string]string{"htdocs/index.php": "<?php"}, "php", "frankenphp php-server --listen :80 --root /app/htdocs"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			_, candidate := detectNodeTree(t, fixture.files)
			if candidate.Recipe != "php" || candidate.Framework != fixture.framework || candidate.StartCommand != fixture.start {
				t.Fatalf("candidate = %s %s %q", candidate.Recipe, candidate.Framework, candidate.StartCommand)
			}
		})
	}
	// Yii's front controllers define YII_ENV only when it is unset; the
	// recipe's environment and prepend define the production values first.
	_, prepared, _ := phpPlan(t, map[string]string{"composer.json": `{"require":{"yiisoft/yii2":"~2.0.51"}}`, "web/index.php": "<?php"})
	assertDockerfile(t, prepared.DockerfilePreview, []string{"ENV COMPOSER_ALLOW_SUPERUSER=1 LOG_CHANNEL=stderr YII_ENV=prod YII_DEBUG=0\n", `define("YII_ENV", getenv("YII_ENV"));`}, nil)
	// A www/ index.php is a page of whatever else owns the directory.
	result, err := (Detector{}).DetectPath(context.Background(), writeNodeTree(t, map[string]string{
		"package.json": `{"name":"api","scripts":{"start":"node server.js"},"dependencies":{"express":"^5"}}`, "www/index.php": "<?php",
	}), SourceIdentity{})
	if err != nil || slices.ContainsFunc(result.Candidates, func(candidate DetectedCandidate) bool { return candidate.Recipe == "php" }) {
		t.Fatalf("a www/ index.php beside a Node application became a PHP candidate: %+v, %v", result.Candidates, err)
	}
}

// A Heroku PHP buildpack process is translated to the document root it
// names; the server configuration it points at does not apply.
func TestPHPHerokuProcfile(t *testing.T) {
	t.Parallel()
	candidate, _, findings := phpPlan(t, laravelTree(map[string]string{"Procfile": "web: vendor/bin/heroku-php-apache2 public/\nrelease: php artisan migrate --force\n"}))
	if candidate.StartCommand != "php artisan migrate --force && frankenphp php-server --listen :80 --root /app/public" ||
		!slices.ContainsFunc(candidate.Evidence, func(evidence DetectionEvidence) bool {
			return evidence.Reason == "Procfile vendor/bin/heroku-php-apache2 public/ → FrankenPHP serving public/"
		}) || candidate.ReleaseCommand != "php artisan migrate --force" || findingByCode(findings, "php_procfile_server_config_ignored") != nil {
		t.Fatalf("candidate = %q %q %+v", candidate.StartCommand, candidate.ReleaseCommand, candidate.Evidence)
	}
	candidate, _, findings = phpPlan(t, map[string]string{"composer.json": `{"require":{"slim/slim":"^4"}}`, "web/index.php": "<?php",
		"Procfile": "web: heroku-php-nginx -C nginx_app.conf web/\n"})
	if candidate.StartCommand != "frankenphp php-server --listen :80 --root /app/web" {
		t.Fatalf("start = %q", candidate.StartCommand)
	}
	if ignored := findingByCode(findings, "php_procfile_server_config_ignored"); ignored == nil || ignored.Measured != "-C nginx_app.conf" {
		t.Fatalf("php_procfile_server_config_ignored = %+v", ignored)
	}
	// Any other web process is the start command, as it always was.
	_, candidate = detectNodeTree(t, map[string]string{"index.php": "<?php", "Procfile": "web: php -S 0.0.0.0:$PORT\n"})
	if candidate.StartCommand != "php -S 0.0.0.0:$PORT" {
		t.Fatalf("start = %q", candidate.StartCommand)
	}
}

// Every PHP image runs PHP's production php.ini and a prepend that believes
// the proxy's X-Forwarded-Proto only from a private peer and only while the
// runtime has not withdrawn PHP_FORWARDED_TRUST.
func TestPHPProductionIniAndForwardedHTTPS(t *testing.T) {
	t.Parallel()
	_, prepared, findings := phpPlan(t, map[string]string{"index.php": "<?php echo 1;"})
	assertDockerfile(t, prepared.DockerfilePreview, []string{
		`RUN cp "$PHP_INI_DIR/php.ini-production" "$PHP_INI_DIR/php.ini" && printf '%s\n' 'upload_max_filesize=64M' 'post_max_size=64M' 'memory_limit=256M' 'expose_php=Off' 'auto_prepend_file=/usr/local/lib/just-dashboard/prepend.php' > "$PHP_INI_DIR/conf.d/zz-just-dashboard.ini" && mkdir -p /usr/local/lib/just-dashboard && printf '%s\n' '<?php'`,
		`FILTER_FLAG_NO_PRIV_RANGE | FILTER_FLAG_NO_RES_RANGE`, "\nENV PHP_FORWARDED_TRUST=private\n",
	}, nil)
	// Written with printf between single quotes, the platform's own PHP
	// cannot contain one.
	for _, line := range append(append([]string{}, phpPrependSource...), phpWordPressConfigSource...) {
		if strings.Contains(line, "'") {
			t.Fatalf("constant PHP line holds a quote: %s", line)
		}
	}
	if trust := imageProxyTrust(prepared); !slices.Equal(trust, []string{"PHP_FORWARDED_TRUST"}) {
		t.Fatalf("image proxy trust = %v", trust)
	}
	withdrawn := withdrawnProxyTrust(runtimeReleaseSnapshot{ProxyTrust: []string{"PHP_FORWARDED_TRUST"},
		Domains: []PlannedDomain{{Hostname: "shop.example.com", HTTPS: true}}, Plan: RuntimePlanConfig{BindAddress: "0.0.0.0"}}, nil)
	if len(withdrawn) != 1 || withdrawn[0].Name != "PHP_FORWARDED_TRUST" || withdrawn[0].Value != "none" {
		t.Fatalf("withdrawn = %+v", withdrawn)
	}
	if docroot := findingByCode(findings, "php_docroot_is_repository_root"); docroot == nil {
		t.Fatal("a plain PHP root served whole was not warned about")
	}
}

// Symfony builds and runs in prod whatever its committed .env says, compiles
// AssetMapper's assets, and gets its APP_SECRET minted.
func TestSymfonyProductionEnvironment(t *testing.T) {
	t.Parallel()
	candidate, prepared, _ := phpPlan(t, map[string]string{
		"composer.json":    `{"require":{"php":">=8.4","symfony/framework-bundle":"8.1.*","symfony/asset-mapper":"8.1.*","symfonycasts/tailwind-bundle":"^0.10"},"require-dev":{"symfony/debug-bundle":"8.1.*"}}`,
		".env":             "APP_ENV=dev\nAPP_SECRET=\nDEFAULT_URI=http://localhost\n",
		"importmap.php":    "<?php return [];",
		"bin/console":      "",
		"public/index.php": "<?php",
	})
	assertDockerfile(t, prepared.DockerfilePreview, []string{
		"ENV COMPOSER_ALLOW_SUPERUSER=1 LOG_CHANNEL=stderr APP_ENV=prod APP_DEBUG=0\n",
		"RUN composer install --no-dev --optimize-autoloader --no-interaction --prefer-dist\nRUN php bin/console tailwind:build --minify\nRUN php bin/console importmap:install\nRUN php bin/console asset-map:compile\n",
	}, nil)
	secret := slices.IndexFunc(candidate.Variables, func(variable DetectedVariable) bool { return variable.Name == "APP_SECRET" })
	uri := slices.IndexFunc(candidate.Variables, func(variable DetectedVariable) bool { return variable.Name == "DEFAULT_URI" })
	if secret < 0 || candidate.Variables[secret].Setup != "generate" || candidate.Variables[secret].GenerateFormat != "hex" ||
		uri < 0 || candidate.Variables[uri].Setup != "domain" || candidate.Variables[uri].DomainTemplate != "{{scheme}}://{{hostname}}" {
		t.Fatalf("variables = %+v", candidate.Variables)
	}
}

// Laravel's official starter kits build their assets with PHP (Wayfinder
// runs artisan) and vendor/ (Flux's and Ziggy's imports) present.
func TestLaravelAssetStageHasPHPAndVendor(t *testing.T) {
	t.Parallel()
	files := laravelTree(map[string]string{
		"composer.json":     `{"require":{"php":"^8.2","laravel/framework":"^12.0","laravel/wayfinder":"^0.1"},"require-dev":{"acme/dev-icons":"^1.0"}}`,
		"package.json":      `{"private":true,"scripts":{"build":"vite build"},"devDependencies":{"vite":"^7","laravel-vite-plugin":"^2","@laravel/vite-plugin-wayfinder":"^0.1"}}`,
		"package-lock.json": `{"lockfileVersion":3,"packages":{"":{"devDependencies":{"vite":"^7","laravel-vite-plugin":"^2","@laravel/vite-plugin-wayfinder":"^0.1"}}}}`,
		"vite.config.ts":    "import icons from './vendor/acme/dev-icons/vite.js'\nexport default defineConfig({ plugins: [laravel({ input: ['resources/js/app.ts'] }), wayfinder(), icons()] })",
	})
	candidate, prepared, _ := phpPlan(t, files)
	assertDockerfile(t, prepared.DockerfilePreview, []string{
		"FROM php-base AS vendor\nCOPY . .\nRUN mkdir -p storage/framework/cache",
		"FROM node:22-alpine@sha256:", " AS assets-toolchain\nFROM vendor AS assets\nCOPY --from=assets-toolchain /usr/local /opt/node\nCOPY --from=assets-toolchain /opt /opt\nENV PATH=/opt/node/bin:$PATH\n",
		"RUN composer install --no-interaction --prefer-dist --no-scripts\nRUN npm ci\n", nodeBuildRun("npm run build\n"),
		"FROM php-base\nCOPY --from=vendor /app /app\nCOPY --from=assets /app/public/build /app/public/build\n",
	}, nil)
	if !slices.ContainsFunc(candidate.Evidence, func(evidence DetectionEvidence) bool {
		return evidence.Reason == "assets are built with PHP and vendor/ present (Wayfinder runs php artisan)"
	}) || candidate.PHP.AssetOutput != "public/build" || candidate.PHP.AssetsOutsideRoot {
		t.Fatalf("candidate = %+v / %+v", candidate.Evidence, candidate.PHP)
	}
}

// Laravel Mix builds with its production script into public/; without one,
// and without committed assets, mix() fails and preflight says so.
func TestLaravelMixAssets(t *testing.T) {
	t.Parallel()
	mix := laravelTree(map[string]string{
		"package.json":      `{"private":true,"scripts":{"dev":"mix","production":"mix --production"},"devDependencies":{"laravel-mix":"^6.0"}}`,
		"package-lock.json": `{"lockfileVersion":3,"packages":{"":{"devDependencies":{"laravel-mix":"^6.0"}}}}`,
		"webpack.mix.js":    "mix.js('resources/js/app.js', 'public/js').postCss('resources/css/app.css', 'public/css')",
	})
	_, prepared, findings := phpPlan(t, mix)
	assertDockerfile(t, prepared.DockerfilePreview, []string{nodeBuildRun("npm run production\n"), "COPY --from=assets /app/public /app/public\n"}, nil)
	if findingByCode(findings, "laravel_mix_unbuilt") != nil {
		t.Fatal("a buildable Mix project was reported unbuilt")
	}
	mix["public/mix-manifest.json"] = "{}"
	_, prepared, _ = phpPlan(t, mix)
	assertDockerfile(t, prepared.DockerfilePreview, nil, []string{"AS assets"})
	delete(mix, "public/mix-manifest.json")
	mix["package.json"] = `{"private":true,"scripts":{"dev":"mix"},"devDependencies":{"laravel-mix":"^6.0"}}`
	candidate, prepared, findings := phpPlan(t, mix)
	if unbuilt := findingByCode(findings, "laravel_mix_unbuilt"); unbuilt == nil || !candidate.PHP.MixUnbuilt || strings.Contains(prepared.DockerfilePreview, "AS assets") {
		t.Fatalf("laravel_mix_unbuilt = %+v", unbuilt)
	}
}

// Any PHP application's Vite build is built by the PHP recipe into the
// directory Vite writes, rather than deployed as a static site of its own.
func TestPHPViteAssetsWithoutKnownFramework(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"composer.json":     `{"require":{"php":"^8.2","vlucas/phpdotenv":"^5"}}`,
		"public/index.php":  "<?php",
		"package.json":      `{"private":true,"scripts":{"build":"vite build"},"devDependencies":{"vite":"^7"}}`,
		"package-lock.json": `{"lockfileVersion":3,"packages":{"":{"devDependencies":{"vite":"^7"}}}}`,
		"vite.config.js":    "export default { build: { outDir: 'public/dist', manifest: true } }",
	}
	result, err := (Detector{}).DetectPath(context.Background(), writeNodeTree(t, files), SourceIdentity{})
	if err != nil || len(result.Candidates) != 1 || result.Candidates[0].Recipe != "php" {
		t.Fatalf("candidates = %+v, %v", result.Candidates, err)
	}
	_, prepared, findings := phpPlan(t, files)
	assertDockerfile(t, prepared.DockerfilePreview, []string{"COPY --from=assets /app/public/dist /app/public/dist\n"}, nil)
	if findingByCode(findings, "php_assets_outside_docroot") != nil {
		t.Fatal("assets under public/ were called outside it")
	}
	files["vite.config.js"] = "export default {}"
	candidate, prepared, findings := phpPlan(t, files)
	assertDockerfile(t, prepared.DockerfilePreview, []string{"COPY --from=assets /app/dist /app/dist\n"}, nil)
	if outside := findingByCode(findings, "php_assets_outside_docroot"); outside == nil || !candidate.PHP.AssetsOutsideRoot || outside.Measured != "dist/" {
		t.Fatalf("php_assets_outside_docroot = %+v", outside)
	}
}

// WordPress repositories deploy in every shape they come in: WordPress
// itself, a wp-content tree, a theme, a plugin, and Bedrock.
func TestWordPressShapes(t *testing.T) {
	t.Parallel()
	core := map[string]string{
		"index.php": "<?php require __DIR__ . '/wp-blog-header.php';", "wp-settings.php": "<?php",
		"wp-includes/version.php": "<?php\n$wp_version = '6.8.3';\n", "wp-config-sample.php": "<?php",
		"wp-admin/index.php": "<?php",
	}
	result, candidate := detectNodeTree(t, core)
	build := BuildPlanConfig{Method: BuildRecipe, Recipe: "php", StartCommand: candidate.StartCommand}
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), writeNodeTree(t, core), build, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Framework != "wordpress" || candidate.Name != "WordPress site in ." || candidate.StartCommand != "frankenphp php-server --listen :80 --root /app" ||
		candidate.PHP.WordPress != "core" || !candidate.PHP.WordPressConfig ||
		len(candidate.Databases) != 1 || candidate.Databases[0].Engine != "mysql" || candidate.Databases[0].Variable != "DATABASE_URL" ||
		!slices.ContainsFunc(candidate.PersistentPaths, func(state DetectedPersistentPath) bool {
			return state.Kind == PersistentUploads && state.Target == "/app/wp-content/uploads"
		}) {
		t.Fatalf("candidate = %+v", candidate)
	}
	assertDockerfile(t, prepared.DockerfilePreview, []string{
		"RUN install-php-extensions pdo_mysql pdo_pgsql mysqli opcache gd exif intl zip\n",
		`RUN mkdir -p wp-content/uploads && printf '%s\n' '<?php' '$jd_database = parse_url((string) getenv("DATABASE_URL")) ?: [];'`,
		`'require_once ABSPATH . "wp-settings.php";' > wp-config.php`,
	}, []string{"composer install", "AS wordpress"})
	findings := preflightFindings(nodeDraft(result), nodeTestConfiguration(build), dockerHost, false)
	if required := findingByCode(findings, "wordpress_database_required"); required == nil || required.Severity != PreflightDecision {
		t.Fatalf("wordpress_database_required = %+v", required)
	}
	if findingByCode(findings, "php_docroot_is_repository_root") != nil {
		t.Fatal("WordPress's own root was warned about as a repository root")
	}
	configuration := nodeTestConfiguration(build)
	configuration.Dependencies = []PlannedDependency{{Kind: "database", Ownership: "attach", ResourceKind: "database_connection", ResourceID: "1"}}
	if findingByCode(preflightFindings(nodeDraft(result), configuration, dockerHost, false), "wordpress_database_required") != nil {
		t.Fatal("a linked database was still asked for")
	}
	// A committed wp-config.php is the repository's own.
	core["wp-config.php"] = "<?php"
	_, prepared, _ = phpPlan(t, core)
	assertDockerfile(t, prepared.DockerfilePreview, []string{"RUN mkdir -p wp-content/uploads\n"}, []string{"> wp-config.php"})

	for _, fixture := range []struct {
		name, shape, copy string
		files             map[string]string
	}{
		{"theme", "theme", "COPY --from=wordpress /usr/src/wordpress /app\nCOPY . wp-content/themes/acme-blocks/\n",
			map[string]string{"style.css": "/*\nTheme Name: Acme Blocks\nText Domain: acme-blocks\n*/", "index.php": "<?php get_header();", "functions.php": "<?php"}},
		{"plugin", "plugin", "COPY --from=wordpress /usr/src/wordpress /app\nCOPY . wp-content/plugins/acme-forms/\n",
			map[string]string{"acme-forms.php": "<?php\n/**\n * Plugin Name: Acme Forms\n */", "index.php": "<?php // Silence is golden."}},
		{"content", "content", "COPY --from=wordpress /usr/src/wordpress /app\nCOPY . wp-content/\n",
			map[string]string{"index.php": "<?php // Silence is golden.", "themes/acme/style.css": "/* Theme Name: Acme */", "plugins/index.php": "<?php"}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			candidate, prepared, _ := phpPlan(t, fixture.files)
			if candidate.Framework != "wordpress" || candidate.PHP.WordPress != fixture.shape || candidate.StartCommand != "frankenphp php-server --listen :80 --root /app" {
				t.Fatalf("candidate = %+v %+v", candidate, candidate.PHP)
			}
			assertDockerfile(t, prepared.DockerfilePreview, []string{"FROM wordpress:6-php8.4-fpm-alpine@sha256:", " AS wordpress\n", fixture.copy, "> wp-config.php"}, nil)
		})
	}

	candidate, prepared, _ = phpPlan(t, map[string]string{
		"composer.json": `{"name":"roots/bedrock","require":{"php":">=8.2","roots/wordpress":"^6.8","roots/wp-config":"^1.0"},"extra":{"wordpress-install-dir":"web/wp"}}`,
		"web/index.php": "<?php", "web/wp-config.php": "<?php",
		".env.example": "DB_NAME=wordpress\nWP_HOME=http://example.com\nWP_SITEURL=${WP_HOME}/wp\nAUTH_KEY='generateme'\nNONCE_SALT='generateme'\n",
	})
	home := slices.IndexFunc(candidate.Variables, func(variable DetectedVariable) bool { return variable.Name == "WP_HOME" })
	siteURL := slices.IndexFunc(candidate.Variables, func(variable DetectedVariable) bool { return variable.Name == "WP_SITEURL" })
	salt := slices.IndexFunc(candidate.Variables, func(variable DetectedVariable) bool { return variable.Name == "NONCE_SALT" })
	if candidate.PHP.WordPress != "bedrock" || candidate.StartCommand != "frankenphp php-server --listen :80 --root /app/web" ||
		home < 0 || candidate.Variables[home].DomainTemplate != "{{scheme}}://{{hostname}}" ||
		siteURL < 0 || candidate.Variables[siteURL].DomainTemplate != "{{scheme}}://{{hostname}}/wp" ||
		salt < 0 || candidate.Variables[salt].Setup != "generate" ||
		len(candidate.Databases) == 0 || candidate.Databases[0].Engine != "mysql" ||
		!slices.ContainsFunc(candidate.PersistentPaths, func(state DetectedPersistentPath) bool { return state.Target == "/app/web/app/uploads" }) {
		t.Fatalf("bedrock = %+v", candidate)
	}
	assertDockerfile(t, prepared.DockerfilePreview, []string{"RUN composer install --no-dev"}, []string{"AS wordpress", "> wp-config.php"})
}

// Private Composer repositories need credentials the install receives as
// COMPOSER_AUTH, and a repository without archives needs git.
func TestPrivateComposerRepositories(t *testing.T) {
	t.Parallel()
	files := laravelTree(map[string]string{
		"composer.json": `{"require":{"php":"^8.2","laravel/framework":"^12.0","laravel/nova":"^5.0","acme/internal":"^1.0"},"repositories":[{"type":"composer","url":"https://nova.laravel.com"},{"type":"vcs","url":"git@gitlab.acme.test:acme/internal.git"},{"type":"path","url":"../shared"}]}`,
	})
	candidate, prepared, findings := phpPlan(t, files)
	index := slices.IndexFunc(candidate.Variables, func(variable DetectedVariable) bool { return variable.Name == "COMPOSER_AUTH" })
	if index < 0 || candidate.Variables[index].Step != "install" || !candidate.Variables[index].InstallRequired ||
		candidate.Variables[index].Sources[0] != "composer.json repositories (nova.laravel.com, gitlab.acme.test)" {
		t.Fatalf("variables = %+v", candidate.Variables)
	}
	assertDockerfile(t, prepared.DockerfilePreview, []string{"RUN apk add --no-cache git\nCOPY --from=composer"}, nil)
	if missing := findingByCode(findings, "registry_token_missing"); missing == nil || missing.Severity != PreflightBlocked || missing.Measured != "COMPOSER_AUTH" {
		t.Fatalf("registry_token_missing = %+v", missing)
	}
	files["auth.json"] = `{}`
	candidate, _, _ = phpPlan(t, files)
	if slices.ContainsFunc(candidate.Variables, func(variable DetectedVariable) bool { return variable.Name == "COMPOSER_AUTH" }) {
		t.Fatal("a committed auth.json still asked for COMPOSER_AUTH")
	}

	// The public stores the Bedrock, Drupal and Yii templates list, and a
	// URL that carries its own credentials, need nothing; a host neither
	// public nor a known paid store may, which is a warning.
	for _, fixture := range []struct {
		name     string
		files    map[string]string
		severity PreflightSeverity
	}{
		{"bedrock", map[string]string{
			"composer.json": `{"name":"roots/bedrock","require":{"php":">=8.2","roots/wordpress":"^6.8","wpackagist-plugin/akismet":"^5.3"},"repositories":[{"type":"composer","url":"https://wpackagist.org","only":["wpackagist-plugin/*","wpackagist-theme/*"]}],"extra":{"wordpress-install-dir":"web/wp"}}`,
			"web/index.php": "<?php", "web/wp-config.php": "<?php",
		}, ""},
		{"drupal recommended-project", map[string]string{
			"composer.json": `{"name":"drupal/recommended-project","require":{"drupal/core-recommended":"^11.2","drupal/core-composer-scaffold":"^11.2"},"repositories":[{"type":"composer","url":"https://packages.drupal.org/8"}],"extra":{"drupal-scaffold":{"locations":{"web-root":"web/"}}}}`,
			"web/index.php": "<?php",
		}, ""},
		{"yii2 basic", map[string]string{
			"composer.json": `{"name":"yiisoft/yii2-app-basic","require":{"php":">=7.4.0","yiisoft/yii2":"~2.0.45"},"repositories":{"asset-packagist":{"type":"composer","url":"https://asset-packagist.org"}}}`,
			"web/index.php": "<?php",
		}, ""},
		{"credentials in the URL", map[string]string{
			"composer.json":    `{"require":{"slim/slim":"^4"},"repositories":[{"type":"composer","url":"https://deploy:token@satis.acme.test"}]}`,
			"public/index.php": "<?php",
		}, ""},
		{"an unknown store", map[string]string{
			"composer.json":    `{"require":{"slim/slim":"^4"},"repositories":[{"type":"composer","url":"https://satis.acme.test"}]}`,
			"public/index.php": "<?php",
		}, PreflightWarning},
		{"a paid store", map[string]string{
			"composer.json":    `{"require":{"slim/slim":"^4","acme/pro":"^1"},"repositories":[{"type":"composer","url":"https://acme-pro.composer.sh"}]}`,
			"public/index.php": "<?php",
		}, PreflightBlocked},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			candidate, _, findings := phpPlan(t, fixture.files)
			missing := findingByCode(findings, "registry_token_missing")
			requested := slices.ContainsFunc(candidate.Variables, func(variable DetectedVariable) bool { return variable.Name == "COMPOSER_AUTH" })
			switch {
			case fixture.severity == "" && (missing != nil || requested):
				t.Fatalf("a public repository asked for credentials: %+v %+v", missing, candidate.Variables)
			case fixture.severity != "" && (missing == nil || missing.Severity != fixture.severity):
				t.Fatalf("registry_token_missing = %+v", missing)
			}
		})
	}
}

// A require-dev package's provider registered for every environment stops
// `composer install --no-dev`; one registered for local only does not.
func TestLaravelDevProviderUnderNoDev(t *testing.T) {
	t.Parallel()
	files := laravelTree(map[string]string{
		"composer.json":                              `{"require":{"php":"^8.2","laravel/framework":"^12.0"},"require-dev":{"laravel/telescope":"^5.0","barryvdh/laravel-ide-helper":"^3.0"}}`,
		"bootstrap/providers.php":                    "<?php\nreturn [\n    App\\Providers\\AppServiceProvider::class,\n    App\\Providers\\TelescopeServiceProvider::class,\n];\n",
		"app/Providers/TelescopeServiceProvider.php": "<?php\nnamespace App\\Providers;\nuse Laravel\\Telescope\\TelescopeApplicationServiceProvider;\nclass TelescopeServiceProvider extends TelescopeApplicationServiceProvider {}\n",
		"app/Providers/AppServiceProvider.php":       "<?php\nnamespace App\\Providers;\nclass AppServiceProvider {}\n",
	})
	candidate, _, findings := phpPlan(t, files)
	if !slices.Equal(candidate.PHP.DevProviders, []string{`App\Providers\TelescopeServiceProvider in bootstrap/providers.php (laravel/telescope is require-dev)`}) {
		t.Fatalf("dev providers = %v", candidate.PHP.DevProviders)
	}
	if blocked := findingByCode(findings, "laravel_dev_provider_registered"); blocked == nil || blocked.Severity != PreflightBlocked {
		t.Fatalf("laravel_dev_provider_registered = %+v", blocked)
	}
	files["bootstrap/providers.php"] = "<?php\nreturn [\n    App\\Providers\\AppServiceProvider::class,\n];\n"
	files["config/app.php"] = "<?php\nreturn ['providers' => [\n    // App\\Providers\\TelescopeServiceProvider::class,\n    ...(app()->environment('local') ? [Barryvdh\\LaravelIdeHelper\\IdeHelperServiceProvider::class] : []),\n]];\n"
	candidate, _, findings = phpPlan(t, files)
	if len(candidate.PHP.DevProviders) != 0 || findingByCode(findings, "laravel_dev_provider_registered") != nil {
		t.Fatalf("a local-only registration was refused: %v", candidate.PHP.DevProviders)
	}

	// Telescope's local-only installation registers the package inside
	// AppServiceProvider, which extends Laravel's own provider; Laravel 10's
	// config/app.php aliases a dev package's facade, which is resolved only
	// when called. Neither is a registration package:discover boots.
	telescope := laravelTree(map[string]string{
		"composer.json":           `{"require":{"php":"^8.2","laravel/framework":"^12.0"},"require-dev":{"laravel/telescope":"^5.0","barryvdh/laravel-debugbar":"^3.14"}}`,
		"bootstrap/providers.php": "<?php\nreturn [\n    App\\Providers\\AppServiceProvider::class,\n];\n",
		"app/Providers/AppServiceProvider.php": "<?php\nnamespace App\\Providers;\n\nuse Illuminate\\Support\\ServiceProvider;\n\nclass AppServiceProvider extends ServiceProvider\n{\n    public function register(): void\n    {\n" +
			"        if ($this->app->environment('local') && class_exists(\\Laravel\\Telescope\\TelescopeServiceProvider::class)) {\n" +
			"            $this->app->register(\\Laravel\\Telescope\\TelescopeServiceProvider::class);\n            $this->app->register(TelescopeServiceProvider::class);\n        }\n    }\n}\n",
		"app/Providers/TelescopeServiceProvider.php": "<?php\nnamespace App\\Providers;\nuse Laravel\\Telescope\\TelescopeApplicationServiceProvider;\nclass TelescopeServiceProvider extends TelescopeApplicationServiceProvider {}\n",
		"config/app.php": "<?php\nuse Illuminate\\Support\\Facades\\Facade;\nuse Illuminate\\Support\\ServiceProvider;\nreturn [\n    'name' => env('APP_NAME', 'Laravel'),\n" +
			"    'providers' => ServiceProvider::defaultProviders()->merge([\n        /* The application's own providers */\n        App\\Providers\\AppServiceProvider::class,\n    ])->toArray(),\n" +
			"    'aliases' => Facade::defaultAliases()->merge([\n        'Debugbar' => Barryvdh\\Debugbar\\Facades\\Debugbar::class,\n    ])->toArray(),\n];\n",
	})
	candidate, _, findings = phpPlan(t, telescope)
	if len(candidate.PHP.DevProviders) != 0 || findingByCode(findings, "laravel_dev_provider_registered") != nil {
		t.Fatalf("the local-only installation or a facade alias was refused: %v", candidate.PHP.DevProviders)
	}
	// The same config/app.php with the provider in its providers list, and an
	// application provider that extends the package's under an alias, are.
	telescope["config/app.php"] = strings.Replace(telescope["config/app.php"], "App\\Providers\\AppServiceProvider::class,\n    ])",
		"App\\Providers\\AppServiceProvider::class,\n        Barryvdh\\Debugbar\\ServiceProvider::class,\n        App\\Providers\\Local\\Watcher::class,\n    ])", 1)
	telescope["app/Providers/Local/Watcher.php"] = "<?php\nnamespace App\\Providers\\Local;\nuse Laravel\\Telescope\\TelescopeApplicationServiceProvider as Base;\nfinal class Watcher extends Base {}\n"
	candidate, _, _ = phpPlan(t, telescope)
	if !slices.Equal(candidate.PHP.DevProviders, []string{
		`Barryvdh\Debugbar\ServiceProvider in config/app.php (barryvdh/laravel-debugbar is require-dev)`,
		`App\Providers\Local\Watcher in config/app.php (laravel/telescope is require-dev)`,
	}) {
		t.Fatalf("dev providers = %v", candidate.PHP.DevProviders)
	}
}

// The PHP and Deno failures preflight cannot foresee are named from the
// build's and the runtime's own words.
func TestPHPAndDenoFailureSignatures(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		line, code, detail, subject string
	}{
		{"    The 'https://nova.laravel.com/packages.json' URL required authentication.", "build_registry_auth", "composer", "https://nova.laravel.com/packages.json"},
		{"Failed to clone the git@gitlab.acme.test:acme/internal.git repository, try running in interactive mode so that you can enter your credentials; git was not found in your PATH, skipping source download", "build_command_not_found", "composer", "git"},
		{"error: Unsupported lockfile version '5'. Try upgrading Deno or recreating the lockfile.", "build_lockfile_incompatible", "deno.lock", "5"},
		{`In ProviderRepository.php line 205: Class "Barryvdh\LaravelIdeHelper\IdeHelperServiceProvider" not found`, "", "", ""},
	} {
		match := classifyLines(buildSignatures, []collectedLine{{text: fixture.line}}, 1)
		switch {
		case fixture.code == "" && match != nil && match.code == "build_dev_dependency_in_production":
			t.Fatalf("a provider line without package:discover was read as a dev dependency: %+v", match)
		case fixture.code == "":
		case match == nil || match.code != fixture.code || match.detail != fixture.detail ||
			len(match.subjects) == 0 || match.subjects[0] != fixture.subject:
			t.Fatalf("%q = %+v", fixture.line, match)
		}
	}
	discover := classifyLines(buildSignatures, []collectedLine{
		{text: `In ProviderRepository.php line 205: Class "Barryvdh\LaravelIdeHelper\IdeHelperServiceProvider" not found`},
		{text: "Script @php artisan package:discover --ansi handling the post-autoload-dump event returned with error code 1"},
	}, 1)
	if discover == nil || discover.code != "build_dev_dependency_in_production" || discover.detail != "laravel" {
		t.Fatalf("package:discover = %+v", discover)
	}
	for _, fixture := range []struct{ line, code, subject, sentence string }{
		{"PHP Fatal error:  Uncaught Error: Call to undefined function mysqli_connect() in /app/includes/db.php:3", "runtime_php_extension_missing", "mysqli_connect", `"ext-mysqli": "*"`},
		{`PHP Fatal error:  Uncaught Error: Class "Redis" not found in /app/vendor/laravel/framework/src/Illuminate/Redis/Connectors/PhpRedisConnector.php:79`, "runtime_php_extension_missing", "Redis", `"ext-redis": "*"`},
		{"Your PHP installation appears to be missing the MySQL extension which is required by WordPress.", "runtime_php_extension_missing", "MySQL", "ext-mysqli"},
		{`production.ERROR: Vite manifest not found at: /app/public/build/manifest.json`, "runtime_assets_missing", "/app/public/build/manifest.json", "build script"},
		{`production.ERROR: Mix manifest does not exist.`, "runtime_assets_missing", "mix-manifest.json", "production script"},
	} {
		match := classifyLines(runtimeSignatures, []collectedLine{{text: fixture.line}}, 1)
		if match == nil || match.code != fixture.code || len(match.subjects) == 0 || match.subjects[0] != fixture.subject {
			t.Fatalf("%q = %+v", fixture.line, match)
		}
		cause := &OutputCause{Code: fixture.code, Subjects: match.subjects}
		if !strings.Contains(cause.sentence(), fixture.sentence) || outputCauseTitles[fixture.code] == "" {
			t.Fatalf("%s sentence = %q", fixture.code, cause.sentence())
		}
	}
}
