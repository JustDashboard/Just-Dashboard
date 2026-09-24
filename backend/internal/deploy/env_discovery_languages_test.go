package deploy

import (
	"reflect"
	"sort"
	"testing"
)

// Every ecosystem with a recipe, and the ones only a Dockerfile builds, opens
// the form with the names its code and configuration read. `required` lists
// the names read with no default where the application starts.
func TestEnvironmentDiscoveryAcrossLanguages(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name     string
		files    map[string]string
		names    []string
		required []string
		examples map[string]string
	}{
		{
			name: "Spring properties, YAML and Java reads",
			files: map[string]string{
				"pom.xml": "<project><artifactId>api</artifactId><dependencies><dependency><artifactId>spring-boot-starter-web</artifactId></dependency></dependencies></project>",
				"src/main/resources/application.properties": "spring.datasource.url=${DATABASE_URL}\nserver.port=${PORT:8080}\napp.cache=${CACHE_TTL:60}\n",
				"src/main/resources/application-prod.yml":   "app:\n  jwt: ${JWT_SIGNING_KEY}\n",
				// A developer's profile never loads here; a staging one may,
				// so it is listed without deciding requiredness.
				"src/main/resources/application-dev.yml":     "app:\n  token: ${DEV_ONLY_TOKEN}\n",
				"src/main/resources/application-staging.yml": "app:\n  token: ${STAGING_TOKEN}\n",
				"src/main/java/app/Config.java":              "class Config { String a = System.getenv(\"API_KEY\"); String b = System.getenv().get(\"REGION\"); @Value(\"${MAIL_FROM:noreply}\") String c; @Value(\"${WEBHOOK_TOKEN}\") String d; }",
			},
			names:    []string{"API_KEY", "CACHE_TTL", "DATABASE_URL", "JWT_SIGNING_KEY", "MAIL_FROM", "REGION", "STAGING_TOKEN", "WEBHOOK_TOKEN"},
			required: []string{"DATABASE_URL", "JWT_SIGNING_KEY", "WEBHOOK_TOKEN"},
			examples: map[string]string{"CACHE_TTL": "60", "MAIL_FROM": "noreply"},
		},
		{
			name: "Kotlin, Scala and Clojure",
			files: map[string]string{
				"build.gradle.kts":                    "plugins { kotlin(\"jvm\") }\n",
				"src/main/kotlin/App.kt":              "val token = System.getenv(\"BOT_TOKEN\")\n",
				"src/main/scala/Main.scala":           "val region = sys.env.getOrElse(\"AWS_REGION\", \"eu\")\n",
				"src/main/clojure/app/core.clj":       "(System/getenv \"CLOJURE_KEY\")\n",
				"src/main/resources/application.conf": "db.url = ${?JDBC_URL}\nplay.http.secret.key = ${APPLICATION_SECRET}\n",
			},
			names:    []string{"APPLICATION_SECRET", "AWS_REGION", "BOT_TOKEN", "CLOJURE_KEY", "JDBC_URL"},
			required: []string{"APPLICATION_SECRET"},
		},
		{
			name: "Rust std::env, dotenvy, compile-time macros and clap",
			files: map[string]string{
				"Cargo.toml":  "[package]\nname = \"api\"\nversion = \"0.1.0\"\n\n[dependencies]\naxum = \"0.8\"\n",
				"src/main.rs": "fn main() {\n let db = std::env::var(\"DATABASE_URL\").expect(\"DATABASE_URL\");\n let jwt = env::var(\"JWT_SECRET\").unwrap_or_default();\n let k = dotenvy::var(\"STRIPE_KEY\");\n let v = env!(\"BUILD_SHA\");\n let o = option_env!(\"SENTRY_RELEASE\");\n}\n#[derive(Parser)]\nstruct Args { #[arg(long, env = \"LISTEN_PORT\")] port: u16 }\n",
			},
			names:    []string{"BUILD_SHA", "DATABASE_URL", "JWT_SECRET", "LISTEN_PORT", "SENTRY_RELEASE", "STRIPE_KEY"},
			required: []string{"BUILD_SHA"},
		},
		{
			name: ".NET environment, configuration and connection strings",
			files: map[string]string{
				"Api.csproj":                   "<Project Sdk=\"Microsoft.NET.Sdk.Web\"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>",
				"Program.cs":                   "var key = Environment.GetEnvironmentVariable(\"STRIPE_KEY\");\nvar jwt = builder.Configuration[\"Jwt:Key\"];\nvar db = builder.Configuration.GetConnectionString(\"Shop\");\n",
				"appsettings.json":             "{\n  // comments are allowed here\n  \"Logging\": { \"LogLevel\": { \"Default\": \"\" } },\n  \"ConnectionStrings\": { \"Reports\": \"Host=localhost;Database=reports\" },\n  \"Smtp\": { \"Password\": \"\", \"Host\": \"smtp.example.com\" }\n}\n",
				"appsettings.Development.json": "{ \"Feature\": { \"Flag\": \"\" } }",
			},
			names: []string{"CONNECTIONSTRINGS__REPORTS", "CONNECTIONSTRINGS__SHOP", "FEATURE__FLAG", "JWT__KEY", "SMTP__PASSWORD", "STRIPE_KEY"},
		},
		{
			name: "Elixir runtime configuration",
			files: map[string]string{
				"Dockerfile":         "FROM elixir:1.18\nEXPOSE 4000\n",
				"mix.exs":            "defmodule App.MixProject do\n  defp deps, do: [{:phoenix, \"~> 1.7\"}]\nend\n",
				"config/runtime.exs": "System.fetch_env!(\"SECRET_KEY_BASE\")\nSystem.get_env(\"DATABASE_URL\") ||\n  raise \"missing\"\nSystem.get_env(\"POOL_SIZE\", \"10\")\nSystem.fetch_env(\"OPTIONAL_KEY\")\n",
				"lib/app/mailer.ex":  "System.fetch_env!(\"MAILGUN_KEY\")\n",
				// A release never loads Mix's dev and test configuration.
				"config/test.exs": "database: \"app_test#{System.get_env(\"MIX_TEST_PARTITION\")}\"\n",
				"config/dev.exs":  "System.get_env(\"DEV_SECRET\") || raise \"missing\"\n",
			},
			// Phoenix reads PHX_HOST through its endpoint config, so it is implied.
			names:    []string{"DATABASE_URL", "MAILGUN_KEY", "OPTIONAL_KEY", "PHX_HOST", "POOL_SIZE", "SECRET_KEY_BASE"},
			required: []string{"DATABASE_URL", "SECRET_KEY_BASE"},
			examples: map[string]string{"POOL_SIZE": "10"},
		},
		{
			name: "Dart, Swift, Haskell and Gleam",
			files: map[string]string{
				"Dockerfile":                  "FROM scratch\n",
				"bin/server.dart":             "Platform.environment['DART_KEY']\n",
				"Sources/App/configure.swift": "Environment.get(\"SWIFT_KEY\")\nProcessInfo.processInfo.environment[\"SWIFT_OTHER\"]\n",
				"app/Main.hs":                 "lookupEnv \"HASKELL_KEY\"\n",
				"src/app.gleam":               "envoy.get(\"GLEAM_KEY\")\n",
			},
			names: []string{"DART_KEY", "GLEAM_KEY", "HASKELL_KEY", "SWIFT_KEY", "SWIFT_OTHER"},
		},
		{
			name: "Go struct tags",
			files: map[string]string{
				"go.mod":           "module example.com/app\n\ngo 1.25\n",
				"main.go":          "package main\nfunc main() {}\n",
				"config/config.go": "package config\ntype Config struct {\n\tDB string `env:\"DB_URL,required\"`\n\tLevel string `env:\"LOG_FORMAT\" envDefault:\"json\"`\n\tToken string `envconfig:\"API_TOKEN\" required:\"true\"`\n\tName string `json:\"name\"`\n}\n",
			},
			names:    []string{"API_TOKEN", "DB_URL", "LOG_FORMAT"},
			required: []string{"API_TOKEN", "DB_URL"},
			examples: map[string]string{"LOG_FORMAT": "json"},
		},
		{
			name: "pydantic-settings, django-environ, dj-database-url and the bare environ",
			files: map[string]string{
				"requirements.txt": "fastapi\npydantic-settings\n",
				"app/config.py":    "from pydantic_settings import BaseSettings, SettingsConfigDict\nfrom pydantic import Field\n\nclass Settings(BaseSettings):\n    model_config = SettingsConfigDict(env_prefix=\"APP_\")\n    database_url: str\n    openai_api_key: str = Field(..., alias=\"OPENAI_API_KEY\")\n    debug_sql: bool = False\n    region: str = \"eu-west-1\"\n\nsettings = Settings()\n",
				"app/settings.py":  "import environ\nimport dj_database_url\nenv = environ.Env()\nDATABASES = {\"default\": env.db()}\nCACHES = {\"default\": env.cache()}\nOTHER = dj_database_url.config(env=\"REPLICA_URL\", default=\"sqlite://\")\n",
				"app/main.py":      "from os import environ\napp = FastAPI()\nTOKEN = environ[\"BOT_TOKEN\"]\ndef handler():\n    return environ.get(\"HANDLER_ONLY\")\n",
				"app/wsgi.py":      "def application(environ, start):\n    return environ['PATH_INFO']\n",
			},
			names:    []string{"APP_DATABASE_URL", "APP_DEBUG_SQL", "APP_REGION", "BOT_TOKEN", "CACHE_URL", "DATABASE_URL", "HANDLER_ONLY", "OPENAI_API_KEY", "REPLICA_URL"},
			required: []string{"APP_DATABASE_URL", "BOT_TOKEN", "CACHE_URL", "DATABASE_URL", "OPENAI_API_KEY"},
			examples: map[string]string{"APP_REGION": "eu-west-1"},
		},
		{
			// A default passed positionally is still a default: django-environ's
			// typed readers and decouple's config() take it second, while a bare
			// env() or env.list() takes a cast there.
			name: "Python positional defaults and casts",
			files: map[string]string{
				"requirements.txt":   "Django==5.2\ndjango-environ\npython-decouple\n",
				"manage.py":          "import os\n",
				"mysite/settings.py": "DEBUG = env.bool('DJANGO_DEBUG', False)\nWORKERS = env.int('WEB_CONCURRENCY', 4)\nTIMEOUT = config('TIMEOUT', 30, cast=int)\nCAST = env('CAST_ONLY', str)\nKEYWORD = env('KEYWORD', default=1)\nLISTED = env.list('LISTED', str)\nHOSTS = env.list('HOSTS_DEFAULT', default=['a'])\nFLAG = config('STARLETTE_FLAG', cast=bool, default=False)\nPLAIN = config('REQUIRED_PLAIN')\nTYPED = env('TYPED_DEFAULT', int, 5)\n",
				"mysite/wsgi.py":     "application = get_wsgi_application()\n",
			},
			names:    []string{"CAST_ONLY", "DJANGO_DEBUG", "HOSTS_DEFAULT", "KEYWORD", "LISTED", "REQUIRED_PLAIN", "STARLETTE_FLAG", "TIMEOUT", "TYPED_DEFAULT", "WEB_CONCURRENCY"},
			required: []string{"CAST_ONLY", "LISTED", "REQUIRED_PLAIN"},
		},
		{
			name: "Python required reads only count at start-up",
			files: map[string]string{
				"requirements.txt":    "flask\n",
				"app.py":              "import os\napp = Flask(__name__)\nSECRET = os.environ['FLASK_SIGNING']\nCONFIG = {\n    'db': os.environ['NESTED_DB'],\n}\n\ndef late():\n    return os.environ['ONLY_WHEN_CALLED']\n",
				"scripts/backfill.py": "import os\nos.environ['BACKFILL_TOKEN']\nos.environ['SET_HERE'] = '1'\n",
			},
			names:    []string{"BACKFILL_TOKEN", "FLASK_SIGNING", "NESTED_DB", "ONLY_WHEN_CALLED", "SET_HERE"},
			required: []string{"FLASK_SIGNING", "NESTED_DB"},
		},
		{
			name: "Rails ERB configuration",
			files: map[string]string{
				"Dockerfile":                    "FROM ruby:3.4\n",
				"Gemfile.lock":                  "GEM\n  specs:\n    railties (8.0.3)\n",
				"config/database.yml":           "production:\n  primary:\n    password: <%= ENV[\"APP_DATABASE_PASSWORD\"] %>\n    host: <%= ENV.fetch(\"DB_HOST\") %>\n",
				"config/storage.yml":            "amazon:\n  service: S3\n",
				"config/initializers/stripe.rb": "Stripe.api_key = ENV.fetch(\"STRIPE_API_KEY\")\nENV.fetch(\"OPTIONAL_FLAG\") { \"off\" }\n",
			},
			names:    []string{"APP_DATABASE_PASSWORD", "DB_HOST", "OPTIONAL_FLAG", "SECRET_KEY_BASE", "STRIPE_API_KEY"},
			required: []string{"DB_HOST", "SECRET_KEY_BASE", "STRIPE_API_KEY"},
		},
		{
			name: "t3 createEnv, Astro envField, AdonisJS and Nuxt runtimeConfig",
			files: map[string]string{
				"package.json":      `{"scripts":{"build":"nuxt build"},"dependencies":{"nuxt":"4"}}`,
				"package-lock.json": `{}`,
				"src/env.js":        "export const env = createEnv({\n  server: {\n    DATABASE_URL: z.string().url(),\n    SENTRY_DSN: z.string().optional(),\n  },\n  client: {\n    NEXT_PUBLIC_POSTHOG: z.string(),\n  },\n})\n",
				"start/env.ts":      "export default await Env.create(new URL('../', import.meta.url), {\n  APP_KEY: Env.schema.string(),\n  SMTP_HOST: Env.schema.string.optional(),\n})\n",
				"astro.config.mjs":  "export default defineConfig({ env: { schema: { API_SECRET: envField.string({ context: \"server\", access: \"secret\" }), PUBLIC_SITE: envField.string({ context: \"client\", access: \"public\", optional: true }) } } })\n",
				"nuxt.config.ts":    "export default defineNuxtConfig({ runtimeConfig: { apiSecret: '', stripe: { key: '' }, public: { apiBase: '/api' } } })\n",
			},
			names:    []string{"API_SECRET", "APP_KEY", "DATABASE_URL", "NEXT_PUBLIC_POSTHOG", "NUXT_API_SECRET", "NUXT_PUBLIC_API_BASE", "PUBLIC_SITE", "SENTRY_DSN", "SMTP_HOST"},
			required: []string{"API_SECRET", "APP_KEY", "DATABASE_URL", "NEXT_PUBLIC_POSTHOG"},
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			result := detectFixture(t, fixture.files)
			if len(result.Candidates) == 0 {
				t.Fatalf("no candidates")
			}
			variables := result.Candidates[0].Variables
			names, required := []string{}, []string{}
			for _, variable := range variables {
				names = append(names, variable.Name)
				if variable.Required {
					required = append(required, variable.Name)
				}
				if want, ok := fixture.examples[variable.Name]; ok && variable.Example != want {
					t.Fatalf("%s example = %q, want %q", variable.Name, variable.Example, want)
				}
			}
			sort.Strings(names)
			sort.Strings(required)
			if fixture.required == nil {
				fixture.required = []string{}
			}
			if !reflect.DeepEqual(names, fixture.names) {
				t.Fatalf("names = %v\n want %v", names, fixture.names)
			}
			if !reflect.DeepEqual(required, fixture.required) {
				t.Fatalf("required = %v\n want %v", required, fixture.required)
			}
		})
	}
}

func TestPythonReadHasDefault(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		callee, arguments string
		want              bool
	}{
		{"env.bool", "False)", true},
		{"env.int", "4)", true},
		{"config", "30, cast=int)", true},
		{"config", "cast=int)", false},
		{"config", "cast=bool, default=False)", true},
		{"env", "str)", false},
		{"env", "default=1)", true},
		{"env", "str, 'fallback')", true},
		{"env", "'fallback')", true},
		{"env", "None)", true},
		{"env.list", "str)", false},
		{"env.list", "default=['a, b'])", true},
		{"env.str", "multiline=True)", false},
		{"env", "cast=dict(value=int), parse_default=True)", false},
	} {
		content := []byte(test.arguments)
		if got := pythonReadHasDefault(test.callee, callArguments(content, 0)); got != test.want {
			t.Fatalf("%s(X, %s = %v, want %v (arguments %q)", test.callee, test.arguments, got, test.want, callArguments(content, 0))
		}
	}
}

func TestSpringDatasourceVariablesReadOnlyTheDatasourceKey(t *testing.T) {
	t.Parallel()
	yaml := "app:\n  webhook:\n    url: ${WEBHOOK_URL}\nspring:\n  mail:\n    url: ${MAIL_URL}\n  datasource:\n    # the pool reads it\n    url: \"${JDBC_DATABASE_URL}\"\n    username: ${DB_USER}\n---\nurl: ${TOP_URL}\n"
	if got := springDatasourceVariables(false, []byte(yaml)); !reflect.DeepEqual(got, []string{"JDBC_DATABASE_URL"}) {
		t.Fatalf("yaml = %v", got)
	}
	properties := "app.webhook.url=${WEBHOOK_URL}\nspring.datasource.hikari.jdbc-url=${POOL_URL}\n%dev.quarkus.datasource.jdbc.url=${DEV_URL}\n%prod.quarkus.datasource.jdbc.url=${PROD_URL}\n"
	if got := springDatasourceVariables(true, []byte(properties)); !reflect.DeepEqual(got, []string{"POOL_URL", "PROD_URL"}) {
		t.Fatalf("properties = %v", got)
	}
}

func TestObjectLiteralKeysAndSnakeUpper(t *testing.T) {
	t.Parallel()
	keys, nested := objectLiteralKeys(`{ apiSecret: 'a,b', "quoted": 1, nested: { inner: { deep: 2 } }, list: [1, { x: 2 }], fn: () => ({ y: 1 }) }`)
	if !reflect.DeepEqual(keys, []string{"apiSecret", "quoted", "nested", "list", "fn"}) {
		t.Fatalf("keys = %v", keys)
	}
	inner, _ := objectLiteralKeys(nested["nested"])
	if !reflect.DeepEqual(inner, []string{"inner"}) {
		t.Fatalf("nested = %v (%q)", inner, nested["nested"])
	}
	for in, want := range map[string]string{"apiSecret": "API_SECRET", "apiBase": "API_BASE", "x": "X"} {
		if got := snakeUpper(in); got != want {
			t.Fatalf("snakeUpper(%s) = %s", in, got)
		}
	}
}

func TestJavaScriptRoutePath(t *testing.T) {
	t.Parallel()
	for rel, want := range map[string]string{
		"app/api/webhooks/stripe/route.ts":   "/api/webhooks/stripe",
		"src/app/(shop)/api/stripe/route.js": "/api/stripe",
		"pages/api/stripe-webhook.ts":        "/api/stripe-webhook",
		"src/pages/api/webhooks/index.ts":    "/api/webhooks",
		"server/routes/webhook.ts":           "",
	} {
		if got := javaScriptRoutePath(rel); got != want {
			t.Fatalf("javaScriptRoutePath(%s) = %q, want %q", rel, got, want)
		}
	}
}
