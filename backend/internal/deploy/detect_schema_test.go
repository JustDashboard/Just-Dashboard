package deploy

import (
	"strings"
	"testing"
)

func TestPythonAndDotnetSchemaToolsApplyTheSchemaBeforeServing(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name                 string
		files                map[string]string
		recipe               string
		tool, command, start string
		inStart              bool
		evidence             string
	}{
		{
			name: "alembic with revisions is chained before uvicorn",
			files: map[string]string{
				"requirements.txt": "fastapi[standard]==0.115.0\nalembic==1.14.0\n", "main.py": "from fastapi import FastAPI\napp = FastAPI()\n",
				"alembic.ini":                   "[alembic]\nscript_location = %(here)s/alembic\nsqlalchemy.url = driver://user:pass@localhost/dbname\n",
				"alembic/versions/0001_init.py": "revision = '0001'\n",
				"alembic/env.py":                "import os\nconfig = context.config\nconfig.set_main_option(\"sqlalchemy.url\", os.environ[\"DATABASE_URL\"])\n",
			},
			recipe: "python", tool: "alembic", command: "alembic upgrade head",
			start:    "alembic upgrade head && uvicorn main:app --host 0.0.0.0 --port ${PORT:-8000}",
			evidence: "Alembic migrations with committed revisions",
		},
		{
			name: "alembic connecting to the URL its ini commits is not chained",
			files: map[string]string{
				"requirements.txt": "fastapi==0.115.0\nuvicorn==0.32.0\nalembic==1.14.0\n", "main.py": "from fastapi import FastAPI\napp = FastAPI()\n",
				"alembic.ini":    "[alembic]\nscript_location = alembic\nsqlalchemy.url = postgresql://me@localhost/devdb\n",
				"alembic/env.py": "from sqlalchemy import engine_from_config, pool\nconfig = context.config\nconnectable = engine_from_config(config.get_section(config.config_ini_section), prefix=\"sqlalchemy.\")\n",
			},
			recipe: "python", tool: "alembic",
			start:    "uvicorn main:app --host 0.0.0.0 --port ${PORT:-8000}",
			evidence: "env.py connects to the URL alembic.ini commits",
		},
		{
			name: "alembic whose env.py imports the application's settings is chained",
			files: map[string]string{
				"requirements.txt": "fastapi==0.115.0\nuvicorn==0.32.0\nalembic==1.14.0\n", "main.py": "from fastapi import FastAPI\napp = FastAPI()\n",
				"alembic.ini":    "[alembic]\nscript_location = alembic\n",
				"alembic/env.py": "from app.core.config import settings\ndef get_url():\n    return str(settings.SQLALCHEMY_DATABASE_URI)\n",
			},
			recipe: "python", tool: "alembic", command: "alembic upgrade head",
			start: "alembic upgrade head && uvicorn main:app --host 0.0.0.0 --port ${PORT:-8000}",
		},
		{
			name: "alembic configured one directory down names its ini",
			files: map[string]string{
				"requirements.txt": "fastapi==0.115.0\nuvicorn==0.32.0\nalembic==1.14.0\n", "main.py": "from fastapi import FastAPI\napp = FastAPI()\n",
				"db/alembic.ini":       "[alembic]\nscript_location = migrations\n",
				"db/migrations/env.py": "from myapp.db import engine\nconnectable = engine\n",
			},
			recipe: "python", tool: "alembic", command: "alembic -c db/alembic.ini upgrade head",
			start: "alembic -c db/alembic.ini upgrade head && uvicorn main:app --host 0.0.0.0 --port ${PORT:-8000}",
		},
		{
			name: "flask-migrate upgrades through the flask command",
			files: map[string]string{
				"requirements.txt": "flask==3.1.0\nflask-migrate==4.0.7\n", "app.py": "from flask import Flask\napp = Flask(__name__)\n",
				"migrations/alembic.ini": "[alembic]\n", "migrations/versions/abc_init.py": "revision = 'abc'\n",
			},
			recipe: "python", tool: "flask-migrate", command: "flask --app app db upgrade",
			start: "flask --app app db upgrade && gunicorn --bind 0.0.0.0:${PORT:-8000} app:app",
		},
		{
			name: "aerich from pyproject",
			files: map[string]string{
				"pyproject.toml": "[project]\nname = \"x\"\ndependencies = [\"fastapi==0.115.0\", \"uvicorn==0.32.0\", \"aerich==0.8.0\"]\n\n[tool.aerich]\ntortoise_orm = \"app.config.TORTOISE_ORM\"\n",
				"main.py":        "from fastapi import FastAPI\napp = FastAPI()\n",
			},
			recipe: "python", tool: "aerich", command: "aerich upgrade",
			start: "aerich upgrade && uvicorn main:app --host 0.0.0.0 --port ${PORT:-8000}",
		},
		{
			name: "django's default start already migrates",
			files: map[string]string{
				"requirements.txt": "Django==5.1.4\n", "manage.py": "import django\n", "mysite/wsgi.py": "application = None\n",
			},
			recipe: "python", tool: "django", command: "python manage.py migrate --noinput",
			start: "python manage.py migrate --noinput && gunicorn mysite.wsgi:application --bind 0.0.0.0:${PORT:-8000}",
		},
		{
			name: "a Procfile web process is not rewritten",
			files: map[string]string{
				"requirements.txt": "Django==5.1.4\ngunicorn==23.0.0\n", "manage.py": "import django\n", "mysite/wsgi.py": "application = None\n",
				"Procfile": "release: python manage.py migrate\nweb: gunicorn mysite.wsgi\n",
			},
			recipe: "python", tool: "django", command: "python manage.py migrate --noinput",
			start: "gunicorn mysite.wsgi", evidence: "the Procfile's web process does not apply them",
		},
		{
			name: "a prestart script that runs alembic counts as the step",
			files: map[string]string{
				"requirements.txt": "fastapi==0.115.0\nuvicorn==0.32.0\nalembic==1.14.0\n", "main.py": "from fastapi import FastAPI\napp = FastAPI()\n",
				"alembic.ini": "[alembic]\n", "scripts/prestart.sh": "#!/bin/sh\nset -e\nalembic upgrade head\n",
				"Procfile": "web: sh scripts/prestart.sh && uvicorn main:app --host 0.0.0.0 --port $PORT\n",
			},
			recipe: "python", tool: "alembic", command: "alembic upgrade head",
			start: "sh scripts/prestart.sh && uvicorn main:app --host 0.0.0.0 --port $PORT", inStart: true,
		},
		{
			name: "EF Core migrations applied by the application",
			files: map[string]string{
				"App.csproj": `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>net9.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="Microsoft.EntityFrameworkCore.Design" Version="9.0.0" /></ItemGroup></Project>`,
				"Migrations/AppDbContextModelSnapshot.cs": "partial class AppDbContextModelSnapshot {}",
				"Program.cs": "using var scope = app.Services.CreateScope();\nscope.ServiceProvider.GetRequiredService<AppDb>().Database.Migrate();\n",
			},
			recipe: "dotnet", tool: "ef-core", inStart: true, evidence: "applies them when the application starts",
		},
		{
			name: "EF Core migrations nothing applies",
			files: map[string]string{
				"App.csproj": `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>net9.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="Microsoft.EntityFrameworkCore.Tools" Version="9.0.0" /></ItemGroup></Project>`,
				"Data/Migrations/AppDbContextModelSnapshot.cs": "partial class AppDbContextModelSnapshot {}",
				"Program.cs": "var app = builder.Build();\napp.Run();\n",
			},
			recipe: "dotnet", tool: "ef-core",
		},
		{
			name: "EF Core without committed migrations is no schema tool",
			files: map[string]string{
				"App.csproj": `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>net9.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="Microsoft.EntityFrameworkCore.Design" Version="9.0.0" /></ItemGroup></Project>`,
			},
			recipe: "dotnet",
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			candidate := candidateFor(t, detectFixture(t, fixture.files), BuildRecipe, fixture.recipe)
			if candidate.SchemaTool != fixture.tool || candidate.SchemaCommand != fixture.command || candidate.SchemaInStart != fixture.inStart {
				t.Fatalf("schema: tool %q command %q inStart %v", candidate.SchemaTool, candidate.SchemaCommand, candidate.SchemaInStart)
			}
			if fixture.start != "" && candidate.StartCommand != fixture.start {
				t.Fatalf("start = %q, want %q", candidate.StartCommand, fixture.start)
			}
			if fixture.evidence != "" {
				found := false
				for _, evidence := range candidate.Evidence {
					found = found || strings.Contains(evidence.Reason, fixture.evidence)
				}
				if !found {
					t.Fatalf("evidence lacks %q: %+v", fixture.evidence, candidate.Evidence)
				}
			}
		})
	}
}

// The schema step state detection chains into a start command is settled
// before readiness and network read that command, so the migration gets its
// slower readiness budget and the listener is still read from the server
// behind it.
func TestChainedSchemaStepReachesReadinessAndNetwork(t *testing.T) {
	t.Parallel()
	candidate := candidateFor(t, detectFixture(t, map[string]string{
		"requirements.txt": "flask==3.1.0\nflask-migrate==4.0.7\n", "app.py": "from flask import Flask\napp = Flask(__name__)\n",
		"migrations/alembic.ini": "[alembic]\n", "migrations/versions/abc_init.py": "revision = 'abc'\n",
	}), BuildRecipe, "python")
	if !strings.HasPrefix(candidate.StartCommand, "flask --app app db upgrade && ") {
		t.Fatalf("start = %q", candidate.StartCommand)
	}
	if candidate.Readiness == nil || candidate.Readiness.Attempts != readinessSlowAttempts || candidate.Readiness.SlowStart == "" {
		t.Fatalf("readiness = %+v, want the migration budget", candidate.Readiness)
	}
	if candidate.Listen != nil && candidate.Listen.Loopback != "" {
		t.Fatalf("listen = %+v, want the gunicorn bind read past the schema step", candidate.Listen)
	}
}

// A Procfile's web process keeps its own command, so the schema step state
// found is not in the start and readiness has no migration to wait for.
func TestProcfileStartWithoutTheSchemaStepGetsNoMigrationBudget(t *testing.T) {
	t.Parallel()
	candidate := candidateFor(t, detectFixture(t, map[string]string{
		"requirements.txt": "flask==3.1.0\nflask-migrate==4.0.7\ngunicorn==23.0.0\n", "app.py": "from flask import Flask\napp = Flask(__name__)\n",
		"migrations/alembic.ini": "[alembic]\n", "migrations/versions/abc_init.py": "revision = 'abc'\n",
		"Procfile": "web: gunicorn app:app --bind 0.0.0.0:$PORT\nrelease: flask db upgrade\n",
	}), BuildRecipe, "python")
	if candidate.StartCommand != "gunicorn app:app --bind 0.0.0.0:$PORT" || candidate.SchemaCommand == "" {
		t.Fatalf("start = %q, schema command = %q", candidate.StartCommand, candidate.SchemaCommand)
	}
	if candidate.Readiness != nil && candidate.Readiness.SlowStart != "" {
		t.Fatalf("readiness = %+v, want no migration budget for a start that does not migrate", candidate.Readiness)
	}
}

func TestEFCoreSchemaStepMissingNamesTheStartupCall(t *testing.T) {
	t.Parallel()
	item := schemaStepFinding(&DetectedCandidate{SchemaTool: "ef-core"}, BuildPlanConfig{StartCommand: "dotnet /app/App.dll"})
	if item.Code != "schema_step_missing" || !strings.Contains(item.Action, "Database.Migrate()") || item.Title == "" {
		t.Fatalf("finding = %+v", item)
	}
	if !schemaStepConfigured(&DetectedCandidate{SchemaTool: "ef-core", SchemaInStart: true}, BuildPlanConfig{}) {
		t.Fatal("an application that migrates itself counted as unconfigured")
	}
	if !schemaStepConfigured(&DetectedCandidate{SchemaTool: "alembic"}, BuildPlanConfig{StartCommand: "alembic -c db/alembic.ini upgrade head && uvicorn main:app"}) {
		t.Fatal("alembic with its ini not recognised")
	}
	// Alembic left unchained names the env.py change, not a command that
	// would connect to the ini's database.
	item = schemaStepFinding(&DetectedCandidate{SchemaTool: "alembic"}, BuildPlanConfig{StartCommand: "uvicorn main:app"})
	if item.Code != "schema_step_missing" || !strings.Contains(item.Action, "env.py read the database URL from the environment") {
		t.Fatalf("alembic finding = %+v", item)
	}
}

func TestNodeSchemaPushIsRecorded(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name     string
		manifest string
		files    []string
		push     bool
	}{
		{"prisma without migrations pushes", nextPrismaManifest, []string{"prisma/schema.prisma"}, true},
		{"prisma with migrations deploys", nextPrismaManifest, []string{"prisma/schema.prisma", "prisma/migrations/0001_init/migration.sql"}, false},
		{"a start script that pushes", `{"scripts":{"start":"prisma db push && next start"},"dependencies":{"next":"16","prisma":"6"}}`, []string{"prisma/schema.prisma"}, true},
		{"a start script that migrates", `{"scripts":{"start":"prisma migrate deploy && next start"},"dependencies":{"next":"16","prisma":"6"}}`, []string{"prisma/schema.prisma"}, false},
		{"knex has no separate push", `{"scripts":{"start":"node index.js"},"dependencies":{"knex":"3"}}`, []string{"knexfile.js"}, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			files := map[string]string{"package.json": fixture.manifest, "package-lock.json": ""}
			for _, name := range fixture.files {
				files[name] = "datasource db {\n  provider = \"postgresql\"\n}\n"
			}
			candidate := candidateFor(t, detectFixture(t, files), BuildRecipe, "node")
			if candidate.SchemaPush != fixture.push {
				t.Fatalf("schemaPush = %v (%+v)", candidate.SchemaPush, candidate)
			}
		})
	}
}

func TestSeedCommandsAreDetected(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name    string
		files   map[string]string
		method  BuildMethod
		recipe  string
		command string
		resets  bool
	}{
		{
			name: "prisma seed from package.json that clears tables",
			files: map[string]string{
				"package.json": `{"scripts":{"build":"next build","start":"next start"},"prisma":{"seed":"tsx prisma/seed.ts"},"dependencies":{"next":"16","@prisma/client":"6"},"devDependencies":{"prisma":"6"}}`, "bun.lock": "",
				"prisma/schema.prisma": "datasource db {\n  provider = \"postgresql\"\n}\n",
				"prisma/seed.ts":       "await prisma.user.deleteMany()\nawait prisma.user.create({ data: { email: 'admin@example.com' } })\n",
			},
			method: BuildRecipe, recipe: "node", command: "bunx prisma db seed", resets: true,
		},
		{
			name: "prisma 7 seed from prisma.config",
			files: map[string]string{
				"package.json": nextPrismaManifest, "package-lock.json": "",
				"prisma/schema.prisma": "datasource db {\n  provider = \"postgresql\"\n}\n",
				"prisma.config.ts":     "export default defineConfig({ migrations: { seed: 'tsx prisma/seed.ts' } })\n",
				"prisma/seed.ts":       "await prisma.role.upsert({ where: { name: 'admin' }, update: {}, create: { name: 'admin' } })\n",
			},
			method: BuildRecipe, recipe: "node", command: "npx prisma db seed",
		},
		{
			name: "a db:seed script",
			files: map[string]string{
				"package.json": `{"scripts":{"start":"node server.js","db:seed":"node scripts/seed.js"},"dependencies":{"express":"5","drizzle-orm":"0.40"}}`, "pnpm-lock.yaml": "",
				"scripts/seed.js": "await db.execute(sql`TRUNCATE users`)\n",
			},
			method: BuildRecipe, recipe: "node", command: "pnpm run db:seed", resets: true,
		},
		{
			name: "laravel's skeleton test user is no seed",
			files: map[string]string{
				"composer.json": `{"require":{"laravel/framework":"^11.0"}}`, "public/index.php": "<?php",
				"database/seeders/DatabaseSeeder.php": "<?php\nclass DatabaseSeeder extends Seeder {\n  public function run(): void {\n    // User::factory(10)->create();\n    User::factory()->create([\n      'name' => 'Test User',\n      'email' => 'test@example.com',\n    ]);\n  }\n}\n",
			},
			method: BuildRecipe, recipe: "php",
		},
		{
			name: "a laravel seeder that calls others",
			files: map[string]string{
				"composer.json": `{"require":{"laravel/framework":"^11.0"}}`, "public/index.php": "<?php",
				"database/seeders/DatabaseSeeder.php": "<?php\nclass DatabaseSeeder extends Seeder {\n  public function run(): void {\n    $this->call([RoleSeeder::class, AdminSeeder::class]);\n  }\n}\n",
			},
			method: BuildRecipe, recipe: "php", command: "php artisan db:seed --force",
		},
		{
			name: "rails seeds that are only the generated comment",
			files: map[string]string{
				"Dockerfile": rails8Dockerfile, "Gemfile.lock": "    rails (8.0.1)\n",
				"db/seeds.rb": "# This file should ensure the existence of records required to run the application.\n#\n#   [\"Action\"].each do |genre_name|\n#   end\n",
			},
			method: BuildDockerfile,
		},
		{
			name: "rails seeds with records",
			files: map[string]string{
				"Dockerfile": rails8Dockerfile, "Gemfile.lock": "    rails (8.0.1)\n",
				"db/seeds.rb": "# Admin\nUser.find_or_create_by!(email: \"admin@example.com\")\n",
			},
			method: BuildDockerfile, command: "bin/rails db:seed",
		},
		{
			name: "django fixtures an application commits, but not the tests' own",
			files: map[string]string{
				"requirements.txt": "Django==5.1.4\n", "manage.py": "import django\n", "mysite/wsgi.py": "application = None\n",
				"shop/fixtures/categories.json": "[]", "shop/fixtures/initial-roles.yaml": "[]",
				"shop/tests/fixtures/sample_orders.json": "[]", "shop/fixtures/notes.txt": "x",
			},
			method: BuildRecipe, recipe: "python", command: "python manage.py loaddata categories initial-roles",
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			candidate := candidateFor(t, detectFixture(t, fixture.files), fixture.method, fixture.recipe)
			if candidate.SeedCommand != fixture.command || candidate.SeedResets != fixture.resets {
				t.Fatalf("seed = %q resets %v", candidate.SeedCommand, candidate.SeedResets)
			}
		})
	}
}
