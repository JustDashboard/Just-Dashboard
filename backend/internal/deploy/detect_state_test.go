package deploy

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// persistentSummary renders the fields a plan is built from, one line per
// path, so a table can say exactly what each fixture must produce.
func persistentSummary(paths []DetectedPersistentPath) []string {
	lines := []string{}
	for _, entry := range paths {
		line := entry.Kind + " " + entry.Path + " -> " + entry.Target
		if entry.Variable != "" {
			line += " " + entry.Variable + "=" + entry.Value
		}
		if entry.DatabaseVariable != "" {
			line += " db:" + entry.DatabaseVariable
		}
		lines = append(lines, line)
	}
	return lines
}

func detectFixture(t *testing.T, files map[string]string) DetectionResult {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		writeBuildFixture(t, root, name, content)
	}
	result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func candidateFor(t *testing.T, result DetectionResult, method BuildMethod, recipe string) DetectedCandidate {
	t.Helper()
	for _, candidate := range result.Candidates {
		if candidate.BuildMethod == method && (recipe == "" || candidate.Recipe == recipe) {
			return candidate
		}
	}
	t.Fatalf("no %s/%s candidate in %+v", method, recipe, result.Candidates)
	return DetectedCandidate{}
}

const (
	nextPrismaManifest = `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16","@prisma/client":"6"},"devDependencies":{"prisma":"6"}}`
	djangoSettings     = `from pathlib import Path
BASE_DIR = Path(__file__).resolve().parent.parent
INSTALLED_APPS = ["django.contrib.admin"]
DATABASES = {
    "default": {
        "ENGINE": "django.db.backends.sqlite3",
        "NAME": BASE_DIR / "db.sqlite3",
    }
}
AUTH_PASSWORD_VALIDATORS = [
    {"NAME": "django.contrib.auth.password_validation.UserAttributeSimilarityValidator"},
]
STATIC_URL = "static/"
MEDIA_ROOT = BASE_DIR / "media"
`
	rails8DatabaseYML = `default: &default
  adapter: sqlite3
  pool: <%= ENV.fetch("RAILS_MAX_THREADS") { 5 } %>
  timeout: 5000

development:
  <<: *default
  database: storage/development.sqlite3

production:
  primary:
    <<: *default
    database: storage/production.sqlite3
  cache:
    <<: *default
    database: storage/production_cache.sqlite3
    migrations_paths: db/cache_migrate
`
	rails8Dockerfile = `# syntax=docker/dockerfile:1
ARG RUBY_VERSION=3.4.1
FROM docker.io/library/ruby:$RUBY_VERSION-slim AS base
WORKDIR /rails
FROM base AS build
COPY . .
FROM base
COPY --from=build /rails /rails
RUN groupadd --system --gid 1000 rails && \
    useradd rails --uid 1000 --gid 1000 --create-home --shell /bin/bash && \
    chown -R rails:rails db log storage tmp
USER 1000:1000
EXPOSE 80
CMD ["./bin/thrust", "./bin/rails", "server"]
`
)

func TestDetectionFindsTheStateAnApplicationKeeps(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name   string
		files  map[string]string
		method BuildMethod
		recipe string
		want   []string
	}{
		{
			name: "prisma sqlite read from DATABASE_URL moves onto a data volume",
			files: map[string]string{
				"package.json": nextPrismaManifest, "bun.lock": "",
				"prisma/schema.prisma": "datasource db {\n  provider = \"sqlite\"\n  url      = env(\"DATABASE_URL\")\n}\n",
				".env.example":         "DATABASE_URL=\"file:./dev.db\"\n",
			},
			method: BuildRecipe, recipe: "node",
			want: []string{"sqlite /app/prisma/dev.db -> /data DATABASE_URL=file:/data/dev.db"},
		},
		{
			name: "prisma 7 reads its url from prisma.config",
			files: map[string]string{
				"package.json": nextPrismaManifest, "package-lock.json": "",
				"prisma/schema.prisma": "datasource db {\n  provider = \"sqlite\"\n}\n",
				"prisma.config.ts":     "export default defineConfig({ schema: 'prisma/schema.prisma', datasource: { url: env('DATABASE_URL') } })\n",
			},
			method: BuildRecipe, recipe: "node",
			want: []string{"sqlite /app/prisma/dev.db -> /data DATABASE_URL=file:/data/app.db"},
		},
		{
			name: "a hardcoded prisma file beside the schema cannot be mounted",
			files: map[string]string{
				"package.json": nextPrismaManifest, "package-lock.json": "",
				"prisma/schema.prisma":                     "datasource db {\n  provider = \"sqlite\"\n  url = \"file:./dev.db\"\n}\n",
				"prisma/migrations/0001/migration.sql":     "create table x();",
				"prisma/migrations/migration_lock.toml":    "provider = \"sqlite\"",
				"prisma/data-that-is-not-a-directory.txt":  "x",
				"prisma/../README.md":                      "x",
				"src/app/page.tsx":                         "export default function Page() {}",
				"src/lib/unrelated.ts":                     "export const x = 1",
				"src/lib/also-unrelated.ts":                "export const y = 2",
				"prisma/seed-data/placeholder/.gitkeep":    "",
				"prisma/seed-data/placeholder/readme.json": "{}",
			},
			method: BuildRecipe, recipe: "node",
			want: []string{"sqlite /app/prisma/dev.db -> "},
		},
		{
			name: "a hardcoded prisma file in its own data directory is mounted there",
			files: map[string]string{
				"package.json": nextPrismaManifest, "package-lock.json": "",
				"prisma/schema.prisma": "datasource db {\n  provider = \"sqlite\"\n  url = \"file:./data/app.db\"\n}\n",
				"prisma/data/.gitkeep": "",
			},
			method: BuildRecipe, recipe: "node",
			want: []string{"sqlite /app/prisma/data/app.db -> /app/prisma/data"},
		},
		{
			name: "better-sqlite3 path from a variable keeps its plain-path shape",
			files: map[string]string{
				"package.json": `{"scripts":{"start":"node server.js"},"dependencies":{"express":"5","better-sqlite3":"11"}}`, "package-lock.json": "",
				".env.example": "DB_FILE_NAME=local.db\n",
				"server.js":    "const db = new Database(process.env.DB_FILE_NAME)\n",
			},
			method: BuildRecipe, recipe: "node",
			want: []string{"sqlite /app/local.db -> /data DB_FILE_NAME=/data/local.db"},
		},
		{
			name: "libsql wants a file: URL",
			files: map[string]string{
				"package.json": `{"scripts":{"start":"node index.js"},"dependencies":{"hono":"4","@libsql/client":"0.14","drizzle-orm":"0.40"},"devDependencies":{"drizzle-kit":"0.30"}}`, "pnpm-lock.yaml": "",
				".env.example":      "DB_FILE_NAME=file:local.db\n",
				"drizzle.config.ts": "export default defineConfig({ dialect: 'sqlite', dbCredentials: { url: process.env.DB_FILE_NAME! } })\n",
			},
			method: BuildRecipe, recipe: "node",
			want: []string{"sqlite /app/local.db -> /data DB_FILE_NAME=file:/data/local.db"},
		},
		{
			name: "a sqlite literal in its own directory, and one at the root",
			files: map[string]string{
				"package.json": `{"scripts":{"start":"node src/index.js"},"dependencies":{"express":"5","better-sqlite3":"11"}}`, "package-lock.json": "",
				"src/db.js":     "import Database from 'better-sqlite3'\nexport const db = new Database('data/app.db')\nexport const cache = new Database('cache.db')\n",
				"data/.gitkeep": "",
			},
			method: BuildRecipe, recipe: "node",
			want: []string{"sqlite /app/data/app.db -> /app/data", "sqlite /app/cache.db -> "},
		},
		{
			name: "strapi keeps its database in .tmp and uploads in public/uploads",
			files: map[string]string{
				"package.json": `{"scripts":{"build":"strapi build","start":"strapi start"},"dependencies":{"@strapi/strapi":"5"}}`, "package-lock.json": "",
				".env.example":            "DATABASE_CLIENT=sqlite\nDATABASE_FILENAME=.tmp/data.db\n",
				"public/uploads/.gitkeep": "",
			},
			method: BuildRecipe, recipe: "node",
			want: []string{"sqlite /app/.tmp/data.db -> /app/.tmp", "uploads /app/public/uploads -> /app/public/uploads"},
		},
		{
			name: "multer writes to an uploads directory",
			files: map[string]string{
				"package.json": `{"scripts":{"start":"node app.js"},"dependencies":{"express":"5","multer":"2"}}`, "package-lock.json": "",
				"app.js": "const multer = require('multer')\nconst upload = multer({ dest: 'uploads/' })\n",
			},
			method: BuildRecipe, recipe: "node",
			want: []string{"uploads /app/uploads -> /app/uploads"},
		},
		{
			name: "an upload directory read from a variable gets its own volume",
			files: map[string]string{
				"package.json": `{"scripts":{"start":"node app.js"},"dependencies":{"express":"5"}}`, "package-lock.json": "",
				".env.example": "UPLOAD_DIR=./uploads\n",
				"app.js":       "fs.writeFileSync(path.join(process.env.UPLOAD_DIR, name), body)\n",
			},
			method: BuildRecipe, recipe: "node",
			want: []string{"uploads /app/uploads -> /data/uploads UPLOAD_DIR=/data/uploads"},
		},
		{
			name: "a static site keeps nothing",
			files: map[string]string{
				"package.json": `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"8","better-sqlite3":"11"}}`, "package-lock.json": "",
				".env.example": "DATABASE_URL=file:./dev.db\n",
			},
			method: BuildRecipe, recipe: "node",
			want: []string{},
		},
		{
			name: "django's default sqlite file sits beside the code; media gets a volume",
			files: map[string]string{
				"requirements.txt": "Django==5.1.4\n", "manage.py": "import django\n",
				"mysite/settings.py": djangoSettings, "mysite/wsgi.py": "application = get_wsgi_application()\n",
			},
			method: BuildRecipe, recipe: "python",
			want: []string{"sqlite /app/db.sqlite3 -> ", "uploads /app/media -> /app/media"},
		},
		{
			name: "django reading DATABASE_URL falls back to sqlite and is offered postgres",
			files: map[string]string{
				"requirements.txt": "Django==5.1.4\ndj-database-url==2.3.0\n", "manage.py": "import django\n",
				"mysite/settings.py": strings.Replace(djangoSettings, "DATABASES = {", "import dj_database_url\nDATABASES = {'default': dj_database_url.config(default='sqlite:///db.sqlite3')}\nLEGACY = {", 1),
				"mysite/wsgi.py":     "application = get_wsgi_application()\n",
			},
			method: BuildRecipe, recipe: "python",
			want: []string{"sqlite /app/db.sqlite3 ->  db:DATABASE_URL", "uploads /app/media -> /app/media"},
		},
		{
			name: "django's sqlite file named by a variable moves onto a data volume",
			files: map[string]string{
				"requirements.txt": "Django==5.1.4\n", "manage.py": "import django\n",
				"mysite/settings.py": strings.Replace(djangoSettings, `"NAME": BASE_DIR / "db.sqlite3"`, `"NAME": os.environ.get("SQLITE_PATH", BASE_DIR / "db.sqlite3")`, 1),
				"mysite/wsgi.py":     "application = get_wsgi_application()\n",
			},
			method: BuildRecipe, recipe: "python",
			want: []string{"sqlite /app/db.sqlite3 -> /data SQLITE_PATH=/data/db.sqlite3", "uploads /app/media -> /app/media"},
		},
		{
			name: "the FastAPI tutorial's database lives beside the code",
			files: map[string]string{
				"requirements.txt": "fastapi==0.115.0\nsqlalchemy==2.0.36\n",
				"main.py":          "from fastapi import FastAPI\napp = FastAPI()\n",
				"database.py":      "from sqlalchemy import create_engine\nSQLALCHEMY_DATABASE_URL = \"sqlite:///./sql_app.db\"\nengine = create_engine(SQLALCHEMY_DATABASE_URL)\n",
			},
			method: BuildRecipe, recipe: "python",
			want: []string{"sqlite /app/sql_app.db -> "},
		},
		{
			name: "flask-sqlalchemy 3 keeps a relative database in the instance folder",
			files: map[string]string{
				"requirements.txt": "flask==3.1.0\nflask-sqlalchemy==3.1.1\n",
				"app.py":           "from flask import Flask\napp = Flask(__name__)\napp.config['SQLALCHEMY_DATABASE_URI'] = 'sqlite:///site.db'\n",
			},
			method: BuildRecipe, recipe: "python",
			want: []string{"sqlite /app/instance/site.db -> /app/instance"},
		},
		{
			name: "a pydantic setting falls back to sqlite",
			files: map[string]string{
				"requirements.txt": "fastapi==0.115.0\npydantic-settings==2.6.0\n",
				"main.py":          "from fastapi import FastAPI\napp = FastAPI()\n",
				"app/config.py":    "from pydantic_settings import BaseSettings\nclass Settings(BaseSettings):\n    database_url: str = \"sqlite:///./data/app.db\"\n",
			},
			method: BuildRecipe, recipe: "python",
			want: []string{"sqlite /app/data/app.db -> /app/data db:DATABASE_URL"},
		},
		{
			name: "the mega-tutorial's joined path with a DATABASE_URL override",
			files: map[string]string{
				"requirements.txt": "flask==3.1.0\nflask-sqlalchemy==3.1.1\n",
				"app.py":           "from flask import Flask\napp = Flask(__name__)\n",
				"config.py":        "import os\nbasedir = os.path.abspath(os.path.dirname(__file__))\nclass Config:\n    SQLALCHEMY_DATABASE_URI = os.environ.get('DATABASE_URL') or 'sqlite:///' + os.path.join(basedir, 'app.db')\n",
			},
			method: BuildRecipe, recipe: "python",
			want: []string{"sqlite /app/app.db ->  db:DATABASE_URL"},
		},
		{
			name: "laravel 11 defaults to sqlite, moved into storage",
			files: map[string]string{
				"composer.json": `{"require":{"php":"^8.2","laravel/framework":"^11.31"}}`, "composer.lock": "{}", "artisan": "",
				"public/index.php":           "<?php",
				"storage/app/.gitignore":     "*",
				"storage/framework/.gitkeep": "",
			},
			method: BuildRecipe, recipe: "php",
			want: []string{"sqlite /app/database/database.sqlite -> /app/storage DB_DATABASE=/app/storage/database.sqlite db:DB_URL"},
		},
		{
			name: "laravel on mysql keeps no sqlite, but filament uploads get storage",
			files: map[string]string{
				"composer.json": `{"require":{"php":"^8.2","laravel/framework":"^11.31","filament/filament":"^3.2"}}`, "composer.lock": "{}", "artisan": "",
				".env.example": "DB_CONNECTION=mysql\nDB_DATABASE=laravel\n", "public/index.php": "<?php",
			},
			method: BuildRecipe, recipe: "php",
			want: []string{"uploads /app/storage/app -> /app/storage"},
		},
		{
			name: "laravel 10 without a documented connection is not sqlite",
			files: map[string]string{
				"composer.json": `{"require":{"php":"^8.1","laravel/framework":"^10.10"}}`, "composer.lock": "{}", "artisan": "",
				"public/index.php": "<?php",
			},
			method: BuildRecipe, recipe: "php",
			want: []string{},
		},
		{
			name: "a laravel config/database.php with a literal sqlite path cannot move",
			files: map[string]string{
				"composer.json": `{"require":{"php":"^8.2","laravel/framework":"^12.0"}}`, "composer.lock": "{}", "artisan": "",
				".env.example": "DB_CONNECTION=sqlite\n", "public/index.php": "<?php",
				"config/database.php": "<?php return ['connections' => ['sqlite' => ['driver' => 'sqlite', 'database' => database_path('database.sqlite')]]];",
			},
			method: BuildRecipe, recipe: "php",
			want: []string{"sqlite /app/database/database.sqlite ->  db:DB_URL"},
		},
		{
			name: "rails 8 from its own Dockerfile keeps storage",
			files: map[string]string{
				"Dockerfile": rails8Dockerfile, "Gemfile": "gem \"rails\", \"~> 8.0.1\"\n", "Gemfile.lock": "    rails (8.0.1)\n    sqlite3 (2.5.0)\n",
				"config/database.yml":                 rails8DatabaseYML,
				"config/storage.yml":                  "local:\n  service: Disk\n  root: <%= Rails.root.join(\"storage\") %>\n",
				"config/environments/production.rb":   "Rails.application.configure do\n  config.active_storage.service = :local\nend\n",
				"config/deploy.yml":                   "service: app\nvolumes:\n  - \"app_storage:/rails/storage\"\n",
				"storage/.keep":                       "",
				".github/workflows/deploy.yml":        "volumes:\n  - \"nope:/nope\"\n",
				"db/migrate/20240101_create_users.rb": "class CreateUsers; end",
			},
			method: BuildDockerfile,
			want: []string{
				"sqlite /rails/storage/production.sqlite3 -> /rails/storage db:DATABASE_URL",
				"sqlite /rails/storage/production_cache.sqlite3 -> /rails/storage db:DATABASE_URL",
				"uploads /rails/storage -> /rails/storage",
				"storage /rails/storage -> /rails/storage",
			},
		},
		{
			name: "a Dockerfile's declared volumes",
			files: map[string]string{
				"Dockerfile": "FROM alpine:3.22\nWORKDIR /app\nVOLUME [\"/app/data\", \"/var/log/app\"]\nVOLUME /cache \\\n  /tmp/work\nCMD [\"./run\"]\n",
			},
			method: BuildDockerfile,
			want: []string{
				"volume /app/data -> /app/data", "volume /cache -> /cache", "volume /tmp/work -> /tmp/work", "volume /var/log/app -> /var/log/app",
			},
		},
		{
			name: "an unprivileged Dockerfile cannot be given a directory it lacks",
			files: map[string]string{
				"Dockerfile":   "FROM node:22-alpine\nWORKDIR /app\nCOPY . .\nUSER node\nCMD [\"npm\",\"start\"]\n",
				"package.json": nextPrismaManifest, "package-lock.json": "",
				"prisma/schema.prisma": "datasource db {\n  provider = \"sqlite\"\n  url      = env(\"DATABASE_URL\")\n}\n",
			},
			method: BuildDockerfile,
			want:   []string{"sqlite /app/prisma/dev.db ->  DATABASE_URL="},
		},
		{
			name: "a go service's sqlite file in data/ lands on the prepared data directory",
			files: map[string]string{
				"go.mod":  "module example.com/app\n\ngo 1.25\n\nrequire modernc.org/sqlite v1.34.0\n",
				"main.go": "package main\nimport (\"database/sql\"; _ \"modernc.org/sqlite\")\nfunc main() { sql.Open(\"sqlite\", \"data/app.db\"); sql.Open(\"sqlite\", \"file:cache.db?cache=shared\") }\n",
			},
			method: BuildRecipe, recipe: "go",
			want: []string{"sqlite /home/app/data/app.db -> /home/app/data", "sqlite /home/app/cache.db -> "},
		},
		{
			name: "pocketbase keeps pb_data beside its binary",
			files: map[string]string{
				"go.mod":  "module example.com/app\n\ngo 1.25\n\nrequire github.com/pocketbase/pocketbase v0.25.0\n",
				"main.go": "package main\nfunc main() { app := pocketbase.New(); app.Start() }\n",
			},
			method: BuildRecipe, recipe: "go",
			want: []string{"sqlite /pb_data -> "},
		},
		{
			name: "a rust service's sqlx URL keeps its scheme and query",
			files: map[string]string{
				"Cargo.toml":   "[package]\nname = \"app\"\nversion = \"0.1.0\"\n\n[dependencies]\naxum = \"0.8\"\nsqlx = { version = \"0.8\", features = [\"runtime-tokio\", \"sqlite\"] }\n",
				".env.example": "DATABASE_URL=sqlite://data.db?mode=rwc\n",
				"src/main.rs":  "fn main() { let pool = SqlitePool::connect(&std::env::var(\"DATABASE_URL\").unwrap()); }\n",
			},
			method: BuildRecipe, recipe: "rust",
			want: []string{"sqlite /home/app/data.db -> /home/app/data DATABASE_URL=sqlite:///home/app/data/data.db?mode=rwc"},
		},
		{
			name: "asp.net identity on sqlite: the connection string moves, the key ring is kept",
			files: map[string]string{
				"App.csproj":       `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="Microsoft.EntityFrameworkCore.Sqlite" Version="8.0.0" /></ItemGroup></Project>`,
				"appsettings.json": `{"ConnectionStrings":{"DefaultConnection":"DataSource=app.db;Cache=Shared;Password=nope"},"Logging":{}}`,
				"Program.cs":       "var builder = WebApplication.CreateBuilder(args);\nbuilder.Services.AddDefaultIdentity<IdentityUser>().AddEntityFrameworkStores<ApplicationDbContext>();\nbuilder.Services.AddRazorPages();\n",
			},
			method: BuildRecipe, recipe: "dotnet",
			want: []string{
				"sqlite /app/app.db -> /app/data ConnectionStrings__DefaultConnection=Data Source=/app/data/app.db;Cache=Shared",
				"keys /home/app/.aspnet/DataProtection-Keys -> /home/app/.aspnet/DataProtection-Keys",
			},
		},
		{
			name: "persisted data protection keys need no volume",
			files: map[string]string{
				"App.csproj": `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`,
				"Program.cs": "builder.Services.AddControllersWithViews();\nbuilder.Services.AddDataProtection().PersistKeysToDbContext<Keys>();\n",
			},
			method: BuildRecipe, recipe: "dotnet",
			want: []string{},
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			result := detectFixture(t, fixture.files)
			candidate := candidateFor(t, result, fixture.method, fixture.recipe)
			if got := persistentSummary(candidate.PersistentPaths); !reflect.DeepEqual(got, fixture.want) {
				t.Fatalf("persistent paths\n got: %q\nwant: %q", got, fixture.want)
			}
			if err := validateDetectionResult(&DraftSourceConfig{Kind: SourceGit}, withGitSource(result)); err != nil {
				t.Fatalf("detection with persistent paths does not validate: %v", err)
			}
			for _, entry := range candidate.PersistentPaths {
				if strings.Contains(entry.Value, "Password") {
					t.Fatalf("a credential option was carried into a planned value: %+v", entry)
				}
			}
		})
	}
}

// withGitSource makes a fixture's detection shaped like one a saved Git
// source produced, for the validator.
func withGitSource(result DetectionResult) DetectionResult {
	result.Source.Kind = SourceGit
	if result.SelectedID == "" && len(result.Candidates) > 0 {
		result.SelectedID = result.Candidates[0].ID
	}
	return result
}

func TestDetectionOffersAServerDatabaseForASQLiteFallback(t *testing.T) {
	t.Parallel()
	result := detectFixture(t, map[string]string{
		"requirements.txt": "Django==5.1.4\ndj-database-url==2.3.0\n", "manage.py": "import django\n",
		"mysite/settings.py": "import dj_database_url\nDATABASES = {'default': dj_database_url.config(default='sqlite:///db.sqlite3')}\nFALLBACK = {'ENGINE': 'django.db.backends.sqlite3', 'NAME': BASE_DIR / 'db.sqlite3'}\n",
		"mysite/wsgi.py":     "application = get_wsgi_application()\n",
	})
	candidate := candidateFor(t, result, BuildRecipe, "python")
	found := false
	for _, database := range candidate.Databases {
		found = found || (database.Engine == "postgres" && database.Variable == "DATABASE_URL")
	}
	if !found {
		t.Fatalf("no postgres suggestion on DATABASE_URL: %+v", candidate.Databases)
	}
}

func TestDetectionEvidenceNamesTheStateAndItsVolume(t *testing.T) {
	t.Parallel()
	result := detectFixture(t, map[string]string{
		"package.json": nextPrismaManifest, "bun.lock": "",
		"prisma/schema.prisma": "datasource db {\n  provider = \"sqlite\"\n  url      = env(\"DATABASE_URL\")\n}\n",
	})
	candidate := candidateFor(t, result, BuildRecipe, "node")
	for _, evidence := range candidate.Evidence {
		if evidence.Path == "prisma/schema.prisma" && strings.Contains(evidence.Reason, "a volume at /data keeps it, with DATABASE_URL pointing there") {
			return
		}
	}
	t.Fatalf("evidence does not explain the planned volume: %+v", candidate.Evidence)
}

func TestPersistentPathValidationRefusesUnsafeShapes(t *testing.T) {
	t.Parallel()
	good := DetectedPersistentPath{Kind: PersistentSQLite, Path: "/app/app.db", Target: "/data", Variable: "DATABASE_URL", Value: "file:/data/app.db", Source: "x", Reason: "r"}
	if !validPersistentPaths([]DetectedPersistentPath{good}) {
		t.Fatal("a well-formed path was refused")
	}
	for name, mutate := range map[string]func(*DetectedPersistentPath){
		"unknown kind":       func(p *DetectedPersistentPath) { p.Kind = "cache" },
		"relative path":      func(p *DetectedPersistentPath) { p.Path = "app.db" },
		"root target":        func(p *DetectedPersistentPath) { p.Target = "/" },
		"unclean target":     func(p *DetectedPersistentPath) { p.Target = "/data/../etc" },
		"bad variable":       func(p *DetectedPersistentPath) { p.Variable = "1BAD" },
		"value without name": func(p *DetectedPersistentPath) { p.Variable = "" },
		"credential value":   func(p *DetectedPersistentPath) { p.Value = "postgres://user:secret@db/app" },
		"secret assignment":  func(p *DetectedPersistentPath) { p.Value = "Data Source=/data/app.db;Password=hunter2" },
		"newline in reason":  func(p *DetectedPersistentPath) { p.Reason = "a\nb" },
		"empty reason":       func(p *DetectedPersistentPath) { p.Reason = "" },
	} {
		entry := good
		mutate(&entry)
		if validPersistentPaths([]DetectedPersistentPath{entry}) {
			t.Errorf("%s: accepted %+v", name, entry)
		}
	}
	many := make([]DetectedPersistentPath, 17)
	for index := range many {
		many[index] = good
	}
	if validPersistentPaths(many) {
		t.Fatal("more than 16 paths accepted")
	}
}

func TestSQLiteLocationsKeepTheDriversScheme(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct{ example, prefix, want string }{
		{"file:./dev.db", "", "file:/data/dev.db"},
		{"file:dev.db?connection_limit=1", "", "file:/data/dev.db?connection_limit=1"},
		{"sqlite://data.db?mode=rwc", "", "sqlite:///data/data.db?mode=rwc"},
		{"sqlite:app.sqlite", "", "sqlite:/data/app.sqlite"},
		{"./local.db", "", "/data/local.db"},
		{"", "file:", "file:/data/app.db"},
		{"", "", "/data/app.db"},
		{"file:../../etc/passwd.db", "", "file:/data/passwd.db"},
		{"file:dev.db?x=$(id)", "", "file:/data/dev.db"},
	} {
		if got := relocatedSQLite(fixture.example, "/data", fixture.prefix); got != fixture.want {
			t.Errorf("relocatedSQLite(%q) = %q, want %q", fixture.example, got, fixture.want)
		}
	}
	for _, value := range []string{"postgres://db/app", "libsql://app.turso.io", ":memory:", "file::memory:?cache=shared", "https://x"} {
		if _, _, _, ok := sqliteLocation(value); ok {
			t.Errorf("%q named a file", value)
		}
	}
}

func TestDockerfileFinalStageAndVolumes(t *testing.T) {
	t.Parallel()
	content := []byte("FROM golang AS build\nWORKDIR /src\nVOLUME /build-only\nUSER builder\nFROM alpine\n# VOLUME /commented\nWORKDIR /srv\nWORKDIR app\nUSER 1000:1000\nVOLUME [\"/srv/app/data\"]\nVOLUME $DATA relative/path /\n")
	workdir, user := dockerfileFinalStage(content)
	if workdir != "/srv/app" || user != "1000:1000" {
		t.Fatalf("final stage = %q %q", workdir, user)
	}
	if got := detectedDockerfileVolumes(content); !reflect.DeepEqual(got, []string{"/srv/app/data"}) {
		t.Fatalf("volumes = %q", got)
	}
	inherited := []byte("FROM alpine AS base\nVOLUME /data\nFROM base\nCMD [\"x\"]\n")
	if got := detectedDockerfileVolumes(inherited); !reflect.DeepEqual(got, []string{"/data"}) {
		t.Fatalf("inherited volumes = %q", got)
	}
	if got := imagePersistentPaths("postgres:16", []string{"/var/lib/postgresql/data", "/", "relative"}); len(got) != 1 || got[0].Target != "/var/lib/postgresql/data" || got[0].Kind != PersistentVolume {
		t.Fatalf("image volumes = %+v", got)
	}
}

func TestStateScannerKeepsConfigurationAheadOfALargeSourceTree(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"package.json": nextPrismaManifest, "bun.lock": "",
		"prisma/schema.prisma": "datasource db {\n  provider = \"sqlite\"\n  url      = env(\"DATABASE_URL\")\n}\n",
	}
	// Sources sort before prisma/ and mention sqlite, so they are read and
	// kept; they must not use up the budget the schema needs.
	for index := 0; index < 400; index++ {
		files["app/components/c"+strings.Repeat("x", index%7)+string(rune('a'+index%26))+string(rune('a'+index/26))+".ts"] = "// sqlite helper\nexport const x = 1\n"
	}
	result := detectFixture(t, files)
	candidate := candidateFor(t, result, BuildRecipe, "node")
	if len(candidate.PersistentPaths) != 1 || candidate.PersistentPaths[0].Variable != "DATABASE_URL" {
		t.Fatalf("schema was crowded out: %+v", candidate.PersistentPaths)
	}
}
