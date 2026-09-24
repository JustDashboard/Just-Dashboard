package deploy

import (
	"reflect"
	"strings"
	"testing"
)

// Every manifest the detector reads names its engine, the suggestion carries
// the connection shape its consumer parses, and a hosted-only driver is said
// to be one.
func TestDatabaseDetectionAcrossManifests(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name  string
		files map[string]string
		want  []DetectedDatabase
	}{
		{name: "Rails Gemfile.lock", files: map[string]string{
			"Dockerfile":   "FROM ruby:3.4\nEXPOSE 80\n",
			"Gemfile.lock": "GEM\n  specs:\n    mysql2 (0.5.6)\n    rails (7.1.3)\n    railties (7.1.3)\n    sidekiq (7.3.0)\n",
		}, want: []DetectedDatabase{
			{Engine: "mysql", Variable: "DATABASE_URL", Evidence: "mysql2 in Gemfile.lock", Format: "mysql2"},
			{Engine: "redis", Variable: "REDIS_URL", Evidence: "sidekiq in Gemfile.lock"},
		}},
		{name: "Phoenix mix.exs", files: map[string]string{
			"Dockerfile": "FROM elixir:1.18\nEXPOSE 4000\n",
			"mix.exs":    "defp deps do\n  [{:postgrex, \">= 0.0.0\"}, {:redix, \"~> 1.1\"}]\nend\n",
		}, want: []DetectedDatabase{
			{Engine: "postgres", Variable: "DATABASE_URL", Evidence: "postgrex in mix.exs"},
			{Engine: "redis", Variable: "REDIS_URL", Evidence: "redix in mix.exs"},
		}},
		{name: "Rust sqlx features", files: map[string]string{
			"Cargo.toml":  "[package]\nname = \"api\"\nversion = \"0.1.0\"\n\n[dependencies]\naxum = \"0.8\"\nsqlx = { version = \"0.8\", features = [\"runtime-tokio\", \"postgres\"] }\n\n[dependencies.redis]\nversion = \"0.27\"\n",
			"src/main.rs": "fn main() {}\n",
		}, want: []DetectedDatabase{
			{Engine: "postgres", Variable: "DATABASE_URL", Evidence: "sqlx postgres in Cargo.toml"},
			{Engine: "redis", Variable: "REDIS_URL", Evidence: "redis in Cargo.toml"},
		}},
		{name: "Spring reads a JDBC URL through its own placeholder", files: map[string]string{
			"pom.xml": "<project><dependencies><dependency><groupId>org.springframework.boot</groupId><artifactId>spring-boot-starter-data-jpa</artifactId></dependency><dependency><groupId>org.postgresql</groupId><artifactId>postgresql</artifactId></dependency></dependencies></project>",
			"src/main/resources/application.properties": "spring.datasource.url=${JDBC_DATABASE_URL}\n",
		}, want: []DetectedDatabase{
			{Engine: "postgres", Variable: "JDBC_DATABASE_URL", Evidence: "org.postgresql in the build manifest", Format: "jdbc"},
		}},
		{name: "Spring without a placeholder", files: map[string]string{
			"build.gradle": "plugins { id 'org.springframework.boot' version '3.4.0' }\ndependencies { runtimeOnly 'com.mysql:mysql-connector-j' }\n",
		}, want: []DetectedDatabase{
			{Engine: "mysql", Variable: "SPRING_DATASOURCE_URL", Evidence: "mysql-connector-j in the build manifest", Format: "jdbc"},
		}},
		{name: ".NET Npgsql with a named connection string", files: map[string]string{
			"Shop.csproj": "<Project Sdk=\"Microsoft.NET.Sdk.Web\"><ItemGroup><PackageReference Include=\"Npgsql.EntityFrameworkCore.PostgreSQL\" Version=\"8.0.0\" /><PackageReference Include=\"StackExchange.Redis\" Version=\"2.8.0\" /></ItemGroup></Project>",
			"Program.cs":  "builder.Configuration.GetConnectionString(\"ShopDb\");\n",
		}, want: []DetectedDatabase{
			{Engine: "postgres", Variable: "CONNECTIONSTRINGS__SHOPDB", Evidence: "npgsql.entityframeworkcore.postgresql package reference", Format: "adonet"},
			{Engine: "redis", Variable: "REDIS_URL", Evidence: "stackexchange.redis package reference"},
		}},
		{name: "Vapor and Dart", files: map[string]string{
			"Dockerfile":    "FROM swift:6\nEXPOSE 8080\n",
			"Package.swift": ".package(url: \"https://github.com/vapor/fluent-postgres-driver.git\", from: \"2.0.0\")\n",
			"pubspec.yaml":  "name: api\ndependencies:\n  mongo_dart: ^0.10.0\n",
		}, want: []DetectedDatabase{
			{Engine: "postgres", Variable: "DATABASE_URL", Evidence: "fluent-postgres-driver in Package.swift"},
			{Engine: "mongodb", Variable: "MONGODB_URI", Evidence: "mongo_dart in pubspec.yaml"},
		}},
		{name: "Laravel 10 reads DATABASE_URL", files: map[string]string{
			"composer.json":       `{"require":{"php":"^8.1","laravel/framework":"^10.10","predis/predis":"^2.0"}}`,
			"artisan":             "",
			"public/index.php":    "<?php\n",
			".env.example":        "DB_CONNECTION=mysql\n",
			"config/database.php": "<?php return ['url' => env('DATABASE_URL')];\n",
		}, want: []DetectedDatabase{
			{Engine: "redis", Variable: "REDIS_URL", Evidence: "predis/predis in composer.json"},
			{Engine: "mysql", Variable: "DATABASE_URL", Evidence: "DB_CONNECTION=mysql in .env.example"},
		}},
		{name: "Laravel 11 reads DB_URL", files: map[string]string{
			"composer.json":    `{"require":{"php":"^8.2","laravel/framework":"^11.0"}}`,
			"artisan":          "",
			"public/index.php": "<?php\n",
			".env.example":     "DB_CONNECTION=pgsql\n",
		}, want: []DetectedDatabase{
			{Engine: "postgres", Variable: "DB_URL", Evidence: "DB_CONNECTION=pgsql in .env.example"},
		}},
		{name: "Symfony's committed .env names its engine by scheme", files: map[string]string{
			"composer.json":    `{"require":{"php":"^8.2","symfony/framework-bundle":"^7.1","doctrine/doctrine-bundle":"^2.13"}}`,
			"bin/console":      "",
			"public/index.php": "<?php\n",
			".env":             "DATABASE_URL=\"postgresql://app:!ChangeMe!@127.0.0.1:5432/app?serverVersion=16\"\n",
		}, want: []DetectedDatabase{
			{Engine: "postgres", Variable: "DATABASE_URL", Evidence: "DATABASE_URL scheme in .env"},
		}},
		{name: "Neon over HTTP is hosted-only", files: map[string]string{
			"package.json":      `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16","drizzle-orm":"0.44","@neondatabase/serverless":"1"}}`,
			"package-lock.json": `{}`,
			"src/db/index.ts":   "import { drizzle } from 'drizzle-orm/neon-http'\n",
			".env.example":      "DATABASE_URL=postgresql://user@ep-cool.eu-central-1.aws.neon.tech/neondb\n",
		}, want: []DetectedDatabase{
			{Engine: "postgres", Variable: "DATABASE_URL", Evidence: "@neondatabase/serverless in package.json", Hosted: "neon-http"},
		}},
		{name: "@vercel/postgres alone is hosted-only", files: map[string]string{
			"package.json":      `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16","@vercel/postgres":"0.10"}}`,
			"package-lock.json": `{}`,
		}, want: []DetectedDatabase{
			{Engine: "postgres", Variable: "DATABASE_URL", Evidence: "@vercel/postgres in package.json", Hosted: "vercel-postgres"},
		}},
		{name: "a TCP driver beside a hosted dependency stays local", files: map[string]string{
			"package.json":      `{"scripts":{"start":"node index.js"},"dependencies":{"express":"5","pg":"8","@neondatabase/serverless":"1"}}`,
			"package-lock.json": `{}`,
		}, want: []DetectedDatabase{
			{Engine: "postgres", Variable: "DATABASE_URL", Evidence: "pg in package.json"},
		}},
		{name: "Upstash REST", files: map[string]string{
			"package.json":      `{"scripts":{"start":"node index.js"},"dependencies":{"express":"5","@upstash/redis":"1"}}`,
			"package-lock.json": `{}`,
			".env.example":      "UPSTASH_REDIS_REST_URL=\nUPSTASH_REDIS_REST_TOKEN=\n",
		}, want: []DetectedDatabase{
			{Engine: "redis", Variable: "UPSTASH_REDIS_REST_URL", Evidence: "@upstash/redis in package.json", Hosted: "upstash-rest"},
		}},
		{name: "pgvector from the Prisma schema, PostGIS from a migration", files: map[string]string{
			"package.json":                              `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16","@prisma/client":"6"},"devDependencies":{"prisma":"6"}}`,
			"package-lock.json":                         `{}`,
			"prisma/schema.prisma":                      "generator client {\n  provider = \"prisma-client-js\"\n  previewFeatures = [\"postgresqlExtensions\"]\n}\ndatasource db {\n  provider   = \"postgresql\"\n  url        = env(\"DATABASE_URL\")\n  extensions = [vector]\n}\n",
			"prisma/migrations/0001_init/migration.sql": "CREATE EXTENSION IF NOT EXISTS \"postgis\";\n",
		}, want: []DetectedDatabase{
			{Engine: "postgres", Variable: "DATABASE_URL", Evidence: "Prisma datasource in prisma/schema.prisma", Extensions: []string{"postgis", "vector"}},
		}},
		{name: "a vector library names Postgres on its own", files: map[string]string{
			"requirements.txt": "fastapi\nlangchain-postgres\n",
			"main.py":          "app = FastAPI()\n",
		}, want: []DetectedDatabase{
			{Engine: "postgres", Variable: "DATABASE_URL", Evidence: "the schema uses the vector extension", Extensions: []string{"vector"}},
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			result := detectFixture(t, fixture.files)
			if len(result.Candidates) == 0 {
				t.Fatalf("no candidates")
			}
			got := result.Candidates[0].Databases
			if !reflect.DeepEqual(got, fixture.want) {
				t.Fatalf("databases:\n got %+v\nwant %+v", got, fixture.want)
			}
			for _, database := range got {
				if err := validateDetectedDatabaseDetails(database); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestConnectionStringForFormat(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		raw, format, database, want string
	}{
		{"postgres://jd:p%40ss@db-4.jd.internal:5432/app?sslmode=disable", "jdbc", "",
			"jdbc:postgresql://db-4.jd.internal:5432/app?password=p%40ss&sslmode=disable&user=jd"},
		{"mysql://jd:pw@db-5.jd.internal:3306/shop", "jdbc", "", "jdbc:mysql://db-5.jd.internal:3306/shop?password=pw&user=jd"},
		{"postgres://jd:p;w@db-4.jd.internal/app", "adonet", "", `Host=db-4.jd.internal;Port=5432;Database=app;Username=jd;Password="p;w"`},
		{"mysql://jd:pw@db-5.jd.internal:3306/shop", "adonet", "", "Server=db-5.jd.internal;Port=3306;Database=shop;User ID=jd;Password=pw"},
		{"mysql://jd:pw@db-5.jd.internal:3306/shop", "mysql2", "", "mysql2://jd:pw@db-5.jd.internal:3306/shop"},
		{"postgres://jd:pw@db-4.jd.internal:5432/app", "url", "app_cache", "postgres://jd:pw@db-4.jd.internal:5432/app_cache"},
		{"postgres://jd:pw@db-4.jd.internal:5432/app", "", "", "postgres://jd:pw@db-4.jd.internal:5432/app"},
	} {
		got, err := ConnectionStringForFormat(test.raw, test.format, test.database)
		if err != nil || got != test.want {
			t.Fatalf("ConnectionStringForFormat(%s, %s) = %q, %v\n want %q", test.raw, test.format, got, err, test.want)
		}
	}
	for _, refused := range []struct{ raw, format string }{
		{"redis://:pw@db-6.jd.internal:6379", "jdbc"},
		{"postgres://jd:pw@db-4.jd.internal/app", "mysql2"},
		{"jd:pw@tcp(127.0.0.1:3306)/app", "jdbc"},
		{"postgres://jd:pw@db-4.jd.internal/app", "odbc"},
	} {
		if got, err := ConnectionStringForFormat(refused.raw, refused.format, ""); err == nil {
			t.Fatalf("%s as %s = %q, want a refusal", refused.raw, refused.format, got)
		}
	}
}

func TestGeneratedSecretFormats(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		format string
		length int
		check  func(string) bool
	}{
		{"", 50, func(v string) bool {
			return len(v) == 50 && strings.Trim(v, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789") == ""
		}},
		{"hex", 128, func(v string) bool { return len(v) == 128 && strings.Trim(v, "0123456789abcdef") == "" }},
		{"base64", 32, func(v string) bool { return len(v) == 44 }},
		{"laravel", 32, func(v string) bool { return strings.HasPrefix(v, "base64:") && len(v) == 51 }},
		{"keylist", 16, func(v string) bool { return len(strings.Split(v, ",")) == 4 && len(strings.Split(v, ",")[0]) == 24 }},
	} {
		first, err := generatedSecretValue(test.length, test.format)
		second, _ := generatedSecretValue(test.length, test.format)
		if err != nil || !test.check(first) || first == second {
			t.Fatalf("format %q: %q, %q, %v", test.format, first, second, err)
		}
	}
	if validGeneratedSecretFormat("shell") || !validGeneratedSecretFormat("") {
		t.Fatal("format validation")
	}
	base := PlanConfiguration{
		Build: BuildPlanConfig{Method: BuildNone}, Runtime: RuntimePlanConfig{Image: "alpine:3", Strategy: StrategyStopFirst},
		Dependencies: []PlannedDependency{}, Checks: []PlannedCheck{},
	}
	for name, variable := range map[string]PlannedVariable{
		"format without generation": {Name: "APP_KEY", Sensitivity: "secret", Scopes: []string{"runtime"}, GenerateFormat: "laravel"},
		"unknown format":            {Name: "APP_KEY", Sensitivity: "secret", Scopes: []string{"runtime"}, Generate: 32, GenerateFormat: "shell"},
	} {
		configuration := base
		configuration.Variables = []PlannedVariable{variable}
		if configuration.Validate() == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	configuration := base
	configuration.Variables = []PlannedVariable{{Name: "APP_KEY", Sensitivity: "secret", Scopes: []string{"runtime"}, Generate: 32, GenerateFormat: "laravel"}}
	if err := configuration.Validate(); err != nil {
		t.Fatal(err)
	}
}
