import { describe, expect, test } from "bun:test"
import { existsSync } from "node:fs"
import { join } from "node:path"
import {
  ProductGlyph,
  ProductLogos,
  buildMethodProduct,
  channelProduct,
  containerProduct,
  containerProducts,
  frameworkProduct,
  gitProviderProduct,
  hasProductLogo,
  hostProduct,
  imageProduct,
  issuerProduct,
  packageManagerProduct,
  platformProduct,
  pm2Product,
  portProduct,
  processProduct,
  programProduct,
  recipeProduct,
  unitProduct,
  variableProduct,
  webhookProduct,
} from "./product-logo"

const PUBLIC = join(import.meta.dir, "../../public")

/** Every id a reader below returned, so the last test can check each has a file. */
const returned = new Set()
const seen = (id) => {
  if (id !== undefined) returned.add(id)
  return id
}

describe("hostProduct", () => {
  test("a forge is read from any shape of its address", () => {
    for (const address of [
      "github.com",
      "api.github.com",
      "https://github.com/owner/repo.git",
      "git@github.com:owner/repo.git",
      "ssh://git@github.com:22/owner/repo",
      "https://x-access-token:secret@github.com/owner/repo",
      "ghcr.io",
      "ghcr.io/owner/app:1.2",
      "raw.githubusercontent.com",
    ]) {
      expect(seen(hostProduct(address))).toBe("github")
    }
    expect(seen(hostProduct("registry.gitlab.com"))).toBe("gitlab")
    expect(seen(hostProduct("bitbucket.org"))).toBe("bitbucket")
    expect(seen(hostProduct("codeberg.org"))).toBe("codeberg")
    expect(seen(hostProduct("gitea.com"))).toBe("gitea")
  })

  test("the registries, public and cloud", () => {
    for (const host of ["docker.io", "index.docker.io", "registry-1.docker.io", "hub.docker.com"]) {
      expect(seen(hostProduct(host))).toBe("docker")
    }
    expect(seen(hostProduct("quay.io/coreos/etcd"))).toBe("quay")
    expect(seen(hostProduct("acme.azurecr.io"))).toBe("azure")
    expect(seen(hostProduct("123456789012.dkr.ecr.eu-west-1.amazonaws.com"))).toBe("aws")
    expect(seen(hostProduct("public.ecr.aws"))).toBe("aws")
    expect(seen(hostProduct("gcr.io"))).toBe("google-cloud")
    expect(seen(hostProduct("eu.gcr.io"))).toBe("google-cloud")
    expect(seen(hostProduct("europe-west1-docker.pkg.dev"))).toBe("google-cloud")
  })

  test("a self-hosted forge or registry that names itself", () => {
    expect(seen(hostProduct("gitlab.example.com"))).toBe("gitlab")
    expect(seen(hostProduct("https://my-gitea.lan/owner/repo"))).toBe("gitea")
    expect(seen(hostProduct("code.forgejo.org"))).toBe("forgejo")
    expect(seen(hostProduct("harbor.corp.internal"))).toBe("harbor")
    expect(seen(hostProduct("github.example.com"))).toBe("github")
  })

  test("a host that says nothing, or is no host, has no product", () => {
    expect(hostProduct("git.example.com")).toBeUndefined()
    expect(hostProduct("registry.example.com:5000")).toBeUndefined()
    expect(hostProduct("localhost:5000")).toBeUndefined()
    expect(hostProduct("notgithub.com")).toBeUndefined()
    expect(hostProduct("nginx")).toBeUndefined()
    expect(hostProduct("")).toBeUndefined()
    expect(hostProduct(undefined)).toBeUndefined()
  })
})

test("gitProviderProduct names the four forges and nothing else", () => {
  for (const provider of ["github", "gitlab", "bitbucket", "gitea"]) {
    expect(seen(gitProviderProduct(provider))).toBe(provider)
  }
  expect(gitProviderProduct("generic_hook")).toBeUndefined()
  expect(gitProviderProduct("api")).toBeUndefined()
  expect(gitProviderProduct("legacy_hook")).toBeUndefined()
  expect(gitProviderProduct(undefined)).toBeUndefined()
})

describe("recipeProduct", () => {
  test("every recipe is drawn as the language it builds", () => {
    expect(seen(recipeProduct("node"))).toBe("nodejs")
    for (const recipe of [
      "go",
      "python",
      "rust",
      "java",
      "dotnet",
      "deno",
      "php",
      "ruby",
      "elixir",
      "scala",
      "dart",
    ]) {
      expect(seen(recipeProduct(recipe))).toBe(recipe)
    }
    // Clojure and Gleam have no mark in the bundle, so their tile keeps its glyph.
    expect(recipeProduct("clojure")).toBeUndefined()
    expect(recipeProduct("gleam")).toBeUndefined()
  })

  test("a Node project on Bun is Bun", () => {
    expect(seen(recipeProduct("node", "bun"))).toBe("bun")
    expect(recipeProduct("node", "pnpm")).toBe("nodejs")
    expect(recipeProduct("python", "bun")).toBe("python")
  })

  test("no recipe, or one the backend does not build, is no product", () => {
    expect(recipeProduct(undefined)).toBeUndefined()
    expect(recipeProduct("cobol")).toBeUndefined()
  })
})

test("packageManagerProduct", () => {
  for (const pm of ["bun", "npm", "pnpm", "yarn"]) {
    expect(seen(packageManagerProduct(pm))).toBe(pm)
  }
  expect(packageManagerProduct("pip")).toBeUndefined()
  expect(packageManagerProduct(undefined)).toBeUndefined()
})

describe("frameworkProduct", () => {
  test("a framework with a mark is drawn as itself", () => {
    const own = {
      nextjs: "nextjs",
      sveltekit: "svelte",
      vite: "vite",
      astro: "astro",
      nuxt: "nuxt",
      remix: "remix",
      "react-router": "react-router",
      "solid-start": "solid",
      "tanstack-start": "tanstack",
      angular: "angular",
      nestjs: "nestjs",
      gatsby: "gatsby",
      docusaurus: "docusaurus",
      vitepress: "vitepress",
      eleventy: "eleventy",
      "create-react-app": "react",
      "vue-cli": "vuejs",
      ember: "ember",
      express: "express",
      fastify: "fastify",
      hono: "hono",
      koa: "koa",
      django: "django",
      fastapi: "fastapi",
      flask: "flask",
      streamlit: "streamlit",
      gradio: "gradio",
      "spring-boot": "spring-boot",
      quarkus: "quarkus",
      laravel: "laravel",
      symfony: "symfony",
      fresh: "deno",
    }
    for (const [framework, id] of Object.entries(own)) {
      expect(seen(frameworkProduct(framework))).toBe(id)
    }
  })

  test("a framework without one is drawn as its language", () => {
    for (const framework of ["axum", "actix-web", "rocket", "warp", "poem", "salvo"]) {
      expect(frameworkProduct(framework)).toBe("rust")
    }
    for (const framework of ["micronaut", "javalin", "helidon", "vertx"]) {
      expect(frameworkProduct(framework)).toBe("java")
    }
    expect(seen(frameworkProduct("ktor"))).toBe("kotlin")
    expect(seen(frameworkProduct("aspnet"))).toBe("dotnet")
  })

  test("the rest are left to the recipe", () => {
    for (const framework of ["nitro", "parcel", "elysia", "hapi", "slim", "unheard-of"]) {
      expect(frameworkProduct(framework)).toBeUndefined()
    }
    expect(frameworkProduct(undefined)).toBeUndefined()
  })
})

test("buildMethodProduct draws what each build runs on", () => {
  expect(seen(buildMethodProduct("dockerfile"))).toBe("docker")
  expect(seen(buildMethodProduct("static"))).toBe("nginx")
  expect(seen(buildMethodProduct("compose"))).toBe("docker-compose")
  expect(buildMethodProduct("legacy_compose")).toBe("docker-compose")
  expect(seen(buildMethodProduct("image", { image: "ghcr.io/acme/n8n:1.2" }))).toBe("n8n")
  expect(buildMethodProduct("image", { image: "ghcr.io/acme/api:1" })).toBe("docker")
  expect(buildMethodProduct("image")).toBe("docker")
  expect(buildMethodProduct("recipe", { recipe: "python" })).toBe("python")
  expect(buildMethodProduct("recipe", { recipe: "node", packageManager: "bun" })).toBe("bun")
  expect(buildMethodProduct("recipe")).toBeUndefined()
  expect(buildMethodProduct("none")).toBeUndefined()
  expect(buildMethodProduct(undefined)).toBeUndefined()
})

test("channelProduct: every channel but e-mail is a product", () => {
  for (const kind of ["discord", "slack", "telegram", "webhook"]) {
    expect(seen(channelProduct(kind))).toBe(kind)
  }
  expect(channelProduct("email")).toBeUndefined()
})

describe("webhookProduct", () => {
  test("a service's own webhook host", () => {
    expect(seen(webhookProduct("https://discord.com/api/webhooks/1/abc"))).toBe("discord")
    expect(seen(webhookProduct("https://hooks.slack.com/services/T0/B0/x"))).toBe("slack")
    expect(seen(webhookProduct("https://api.telegram.org/bot123:abc/sendMessage"))).toBe("telegram")
    expect(seen(webhookProduct("https://hc-ping.com/5f1c"))).toBe("healthchecks")
  })

  test("a self-hosted receiver its host is named after", () => {
    expect(seen(webhookProduct("https://n8n.example.com/webhook/deploys"))).toBe("n8n")
    expect(seen(webhookProduct("https://ntfy.sh/my-topic"))).toBe("ntfy")
    expect(seen(webhookProduct("https://gotify.lan/message?token=x"))).toBe("gotify")
    expect(seen(webhookProduct("http://hass.local:8123/api/webhook/deploy"))).toBe("home-assistant")
    expect(webhookProduct("https://home-assistant.lan/api/webhook/x")).toBe("home-assistant")
    expect(seen(webhookProduct("https://uptime-kuma.example.com/api/push/x"))).toBe("uptime-kuma")
    expect(webhookProduct("https://kuma.example.com/api/push/x")).toBe("uptime-kuma")
  })

  test("a host that does not say is left to the webhook's own mark", () => {
    expect(webhookProduct("https://example.com/hooks/deploy")).toBeUndefined()
    expect(webhookProduct("")).toBeUndefined()
    expect(webhookProduct(undefined)).toBeUndefined()
  })
})

test("issuerProduct knows Let's Encrypt by its intermediates' names", () => {
  for (const issuer of ["R3", "R10", "R11", "E5", "E6", "YR1", "YE2", " R10 "]) {
    expect(seen(issuerProduct(issuer))).toBe("lets-encrypt")
  }
  expect(issuerProduct("Let's Encrypt Authority X3")).toBe("lets-encrypt")
  expect(issuerProduct("ISRG Root X1")).toBe("lets-encrypt")
  for (const issuer of ["Sectigo RSA Domain Validation Secure Server CA", "GTS CA 1C3", "R", ""]) {
    expect(issuerProduct(issuer)).toBeUndefined()
  }
  expect(issuerProduct(undefined)).toBeUndefined()
})

describe("variableProduct", () => {
  test("a service named anywhere in the name", () => {
    const named = {
      STRIPE_SECRET_KEY: "stripe",
      STRIPE_WEBHOOK_SECRET: "stripe",
      SENTRY_DSN: "sentry",
      NEXT_PUBLIC_SENTRY_DSN: "sentry",
      OPENAI_API_KEY: "openai",
      ANTHROPIC_API_KEY: "claude",
      AWS_SECRET_ACCESS_KEY: "aws",
      CLOUDFLARE_API_TOKEN: "cloudflare",
      NEXT_PUBLIC_SUPABASE_URL: "supabase",
      SLACK_WEBHOOK_URL: "slack",
      DISCORD_TOKEN: "discord",
      GOOGLE_CLIENT_ID: "google",
      NEXT_PUBLIC_POSTHOG_KEY: "posthog",
      MAILGUN_DOMAIN: "mailgun",
      SENDGRID_API_KEY: "sendgrid",
      GH_TOKEN: "github",
      PG_HOST: "postgres",
      POSTGRES_PASSWORD: "postgres",
      REDIS_URL: "redis",
      MONGO_URL: "mongodb",
      MEILI_MASTER_KEY: "meilisearch",
      AMQP_URL: "rabbitmq",
      INFLUX_TOKEN: "influxdb",
    }
    for (const [name, id] of Object.entries(named)) {
      expect(seen(variableProduct(name))).toBe(id)
    }
  })

  test("a framework's prefix, only as the first word", () => {
    expect(seen(variableProduct("NEXT_PUBLIC_API_URL"))).toBe("nextjs")
    expect(variableProduct("NEXTAUTH_SECRET")).toBe("nextjs")
    expect(seen(variableProduct("VITE_API_URL"))).toBe("vite")
    expect(seen(variableProduct("NODE_ENV"))).toBe("nodejs")
    expect(variableProduct("API_NODE_URL")).toBeUndefined()
  })

  test("a name that says nothing about who holds it", () => {
    expect(variableProduct("DATABASE_URL")).toBeUndefined()
    expect(variableProduct("S3_BUCKET")).toBeUndefined()
    expect(variableProduct("PORT")).toBeUndefined()
    expect(variableProduct("")).toBeUndefined()
  })
})

test("the images the build recipes produce are drawn as their language", () => {
  const images = {
    "node:22-alpine": "nodejs",
    "golang:1.26-alpine": "go",
    "eclipse-temurin:17-jre-alpine": "java",
    "maven:3-eclipse-temurin-17": "java",
    "gradle:8-jdk21": "java",
    "dunglas/frankenphp:1-php8.4-alpine": "php",
    "composer:2": "php",
    "mcr.microsoft.com/dotnet/aspnet:8.0": "dotnet",
    "rust:1.85.0-alpine": "rust",
    "oven/bun:1-alpine": "bun",
    "denoland/deno:alpine": "deno",
    "python:3.12-slim": "python",
    "nginx:1.29-alpine": "nginx-static",
    "ghcr.io/acme/api:1": "docker",
  }
  for (const [image, id] of Object.entries(images)) {
    expect(seen(imageProduct(image))).toBe(id)
  }
})

test("a container its image reference cannot name is what its image's labels say", () => {
  const id = "sha256:c9051a2ac152cb76dba839db5eea38f3b407bed22ebd38e77aa1160d60485286"
  const title = (value) => ({ "org.opencontainers.image.title": value })
  const source = (value) => ({ "org.opencontainers.image.source": value })
  expect(seen(containerProduct({ image: id, labels: title("n8n") }))).toBe("n8n")
  expect(seen(containerProduct({ image: id, labels: title("Caddy") }))).toBe("caddy")
  expect(
    seen(
      containerProduct({ image: id, labels: source("https://github.com/filebrowser/filebrowser") }),
    ),
  ).toBe("filebrowser")
  // The reference wins when it names a product: labels are inherited from a
  // base image, and the reference is the one the operator chose.
  expect(containerProduct({ image: "postgres:16-alpine", labels: title("bun") })).toBe("postgres")
  expect(containerProduct({ image: id, labels: title("my-api") })).toBe("docker")
  expect(containerProduct({ image: "bet-bot-tracker", labels: {} })).toBe("docker")
  expect(
    containerProducts([
      { image: id, labels: title("bun") },
      { image: "postgres:16-alpine", labels: {} },
      { image: "oven/bun:1", labels: {} },
      { image: "epgjauto-app", labels: {} },
    ]),
  ).toEqual(["bun", "postgres"])
})

test("portProduct names the services the attention list names by number", () => {
  expect(seen(portProduct(5432))).toBe("postgresql")
  expect(seen(portProduct(6379))).toBe("redis")
  expect(seen(portProduct(25565))).toBe("minecraft-java")
  // 8080 is anything, and a guessed mark would be the row lying.
  expect(portProduct(8080)).toBeUndefined()
  expect(portProduct(22)).toBeUndefined()
})

test("processProduct does not draw the X server as the site", () => {
  expect(processProduct("X")).toBeUndefined()
  expect(seen(processProduct("java"))).toBe("java")
  expect(seen(processProduct("postgres"))).toBe("postgresql")
  // A node process is Node.js and a python3 one is Python; bash is nothing.
  expect(seen(processProduct("node"))).toBe("nodejs")
  expect(seen(processProduct("python3"))).toBe("python")
  expect(seen(processProduct("php-fpm"))).toBe("php")
  expect(processProduct("bash")).toBeUndefined()
  expect(processProduct("kworker/0:1")).toBeUndefined()
})

// A unit is the product it runs: the suffix and an instance name are dropped,
// the name is read as a process, then its first word is. What names nothing
// keeps a glyph, as a process does.
test("unitProduct reads a unit as the product it runs", () => {
  expect(seen(unitProduct("postgresql.service"))).toBe("postgresql")
  expect(seen(unitProduct("postgresql@16-main.service"))).toBe("postgresql")
  expect(seen(unitProduct("nginx.service"))).toBe("nginx-static")
  expect(seen(unitProduct("redis-server.service"))).toBe("redis")
  expect(seen(unitProduct("pm2-deploy.service"))).toBe("pm2")
  expect(seen(unitProduct("docker.socket"))).toBe("docker")
  expect(seen(unitProduct("containerd.service"))).toBe("docker")
  expect(seen(unitProduct("tailscaled.service"))).toBe("tailscale")
  expect(seen(unitProduct("php8.3-fpm.service"))).toBe("php")
  expect(seen(unitProduct("caddy.service"))).toBe("caddy")
  expect(seen(unitProduct("grafana-server.service"))).toBe("grafana")
  expect(seen(unitProduct("certbot.timer"))).toBe("lets-encrypt")
  expect(unitProduct("apt-daily.timer")).toBeUndefined()
  expect(unitProduct("ssh.service")).toBeUndefined()
  expect(unitProduct("cron.service")).toBeUndefined()
  expect(unitProduct("systemd-resolved.service")).toBeUndefined()
  // A Compose stack run as a unit is Compose's, the way a stack is.
  expect(seen(unitProduct("docker-compose@app.service"))).toBe("docker-compose")
})

// A PM2 application is what runs it, which is Node unless the ecosystem file
// says otherwise; a binary is no product.
test("pm2Product reads the interpreter", () => {
  expect(seen(pm2Product(undefined))).toBe("nodejs")
  expect(seen(pm2Product(""))).toBe("nodejs")
  expect(seen(pm2Product("node"))).toBe("nodejs")
  expect(seen(pm2Product("/usr/bin/bun"))).toBe("bun")
  expect(seen(pm2Product("python3"))).toBe("python")
  expect(pm2Product("none")).toBeUndefined()
})

// A cron line is drawn as the program its command starts.
test("programProduct reads a cron command", () => {
  expect(seen(programProduct("docker system prune -f"))).toBe("docker")
  expect(seen(programProduct("/usr/bin/certbot renew --quiet"))).toBe("lets-encrypt")
  expect(seen(programProduct("pg_dump -U app app > /backup/app.sql"))).toBe("postgresql")
  expect(seen(programProduct("/usr/bin/php /var/www/artisan schedule:run"))).toBe("php")
  expect(programProduct("/usr/local/bin/backup")).toBeUndefined()
  expect(programProduct("cd / && run-parts --report /etc/cron.hourly")).toBeUndefined()
})

// A database is drawn as the product that answered. The ids are the backend's
// (`products` in `internal/dbx/discover_engines.go`): a flavour's for a server
// a driver opens, the engine's own for one the inventory only sees.
describe("the database engines", () => {
  const src = (id) => ProductGlyph({ id })?.props.src

  test("every flavour and engine a licensed collection draws has its own file", () => {
    for (const id of [
      "timescaledb",
      "cockroachdb",
      "yugabytedb",
      "tidb",
      "ferretdb",
      "memcached",
      "elasticsearch",
      "opensearch",
      "etcd",
      "cassandra",
      "scylladb",
      "neo4j",
      "couchdb",
      "duckdb",
      "nats",
      "kafka",
      "influxdb",
      "rabbitmq",
      "qdrant",
      "meilisearch",
      "typesense",
    ]) {
      expect({ id, drawn: hasProductLogo(id) }).toEqual({ id, drawn: true })
      expect(src(seen(id))).toBe(`/logos/${id}.svg`)
    }
  })

  test("a flavour is never its driver's product under another name", () => {
    const own = {
      timescaledb: "postgres",
      cockroachdb: "postgres",
      yugabytedb: "postgres",
      mariadb: "mysql",
      tidb: "mysql",
      valkey: "redis",
      ferretdb: "mongodb",
    }
    for (const [flavor, driver] of Object.entries(own)) {
      expect(src(flavor)).not.toBe(src(driver))
    }
  })

  test("SQL Edge, which no collection draws, is the SQL Server engine it is", () => {
    expect(src(seen("azure-sql-edge"))).toBe(src("sqlserver"))
  })

  test("a product in no licensed collection has no mark rather than a guessed one", () => {
    for (const id of ["percona", "keydb", "dragonfly"]) {
      expect({ id, drawn: hasProductLogo(id) }).toEqual({ id, drawn: false })
      expect(ProductGlyph({ id })).toBeNull()
    }
  })

  test("the images the inventory recognises are the same product on the Docker page", () => {
    const images = {
      "timescale/timescaledb:latest-pg16": "timescaledb",
      "timescale/timescaledb-ha:pg16": "timescaledb",
      "cockroachdb/cockroach:v24.1.0": "cockroachdb",
      "yugabytedb/yugabyte:2.21": "yugabytedb",
      "pingcap/tidb:v8.1.0": "tidb",
      "ghcr.io/ferretdb/ferretdb:1.24": "ferretdb",
      "mcr.microsoft.com/azure-sql-edge:latest": "azure-sql-edge",
      "memcached:1.6-alpine": "memcached",
      "docker.elastic.co/elasticsearch/elasticsearch:8.14.0": "elasticsearch",
      "opensearchproject/opensearch:2": "opensearch",
      "quay.io/coreos/etcd:v3.5.14": "etcd",
      "bitnami/cassandra:5": "cassandra",
      "scylladb/scylla:6.0": "scylladb",
      "neo4j:5-community": "neo4j",
      "apache/couchdb:3": "couchdb",
      "nats:2.10-alpine": "nats",
      "apache/kafka:3.7.0": "kafka",
      "confluentinc/cp-kafka:7.6.1": "kafka",
    }
    for (const [image, id] of Object.entries(images)) {
      expect(seen(imageProduct(image))).toBe(id)
    }
    // A sidecar or another product that only carries the name is not the server.
    expect(imageProduct("prom/memcached-exporter:v0.14.3")).toBe("docker")
    expect(imageProduct("provectuslabs/kafka-ui:latest")).toBe("docker")
    expect(imageProduct("redpandadata/redpanda:v24.1.1")).toBe("docker")
    expect(imageProduct("percona/percona-server:8.0")).toBe("docker")
    expect(imageProduct("eqalpha/keydb:latest")).toBe("docker")
    expect(imageProduct("docker.dragonflydb.io/dragonflydb/dragonfly")).toBe("docker")
  })

  test("a server process and its unit are the product they run", () => {
    const processes = {
      memcached: "memcached",
      etcd: "etcd",
      cockroach: "cockroachdb",
      "tidb-server": "tidb",
      scylla: "scylladb",
      "nats-server": "nats",
      ferretdb: "ferretdb",
      yugabyted: "yugabytedb",
      "yb-tserver": "yugabytedb",
      "yb-master": "yugabytedb",
    }
    for (const [name, id] of Object.entries(processes)) {
      expect(seen(processProduct(name))).toBe(id)
    }
    const units = {
      "memcached.service": "memcached",
      "elasticsearch.service": "elasticsearch",
      "opensearch.service": "opensearch",
      "etcd.service": "etcd",
      "cassandra.service": "cassandra",
      "scylla-server.service": "scylladb",
      "neo4j.service": "neo4j",
      "couchdb.service": "couchdb",
      "nats-server.service": "nats",
      "kafka.service": "kafka",
      "cockroach.service": "cockroachdb",
      "tidb.service": "tidb",
      "yb-tserver.service": "yugabytedb",
      "rabbitmq-server.service": "rabbitmq",
    }
    for (const [unit, id] of Object.entries(units)) {
      expect(seen(unitProduct(unit))).toBe(id)
    }
    // No mark of their own, and not Redis's: the unit keeps its glyph.
    expect(unitProduct("keydb-server.service")).toBeUndefined()
    expect(unitProduct("dragonfly.service")).toBeUndefined()
  })

  test("what runs beside a server under its name is not the server", () => {
    // The same names `imageProduct` refuses on a container.
    for (const unit of [
      "kafka-ui.service",
      "memcached-exporter.service",
      "opensearch-dashboards.service",
      "redis-exporter.service",
      "nats-exporter.service",
    ]) {
      expect({ unit, product: unitProduct(unit) }).toEqual({ unit, product: undefined })
    }
    // An exporter is Prometheus's, and Debian names its packages so.
    expect(seen(unitProduct("prometheus-node-exporter.service"))).toBe("prometheus")
    expect(seen(unitProduct("prometheus-nats-exporter.service"))).toBe("prometheus")
    // A first word in front of something else is still read.
    expect(seen(unitProduct("etcd-defrag.timer"))).toBe("etcd")
    expect(seen(unitProduct("kafka-connect.service"))).toBe("kafka")
  })

  test("a name every object carries is no product", () => {
    for (const name of ["constructor", "toString", "hasOwnProperty", "__proto__", "valueOf"]) {
      expect({ name, image: imageProduct(name) }).toEqual({ name, image: "docker" })
      expect({ name, process: processProduct(name) }).toEqual({ name, process: undefined })
      expect({ name, unit: unitProduct(`${name}.service`) }).toEqual({ name, unit: undefined })
      expect({ name, program: programProduct(name) }).toEqual({ name, program: undefined })
      expect({ name, platform: platformProduct(name) }).toEqual({ name, platform: undefined })
      expect({ name, drawn: hasProductLogo(name) }).toEqual({ name, drawn: false })
      expect(ProductGlyph({ id: name })).toBeNull()
    }
  })

  test("the ports the attention list names and the shells a terminal runs", () => {
    expect(seen(portProduct(11211))).toBe("memcached")
    expect(seen(portProduct(9200))).toBe("elasticsearch")
    // The finding says "Memcached" and "Elasticsearch" of those two by number,
    // and the mark goes with that word. It has no word for these two, and
    // nothing says whether 9042 is Cassandra or ScyllaDB, 9092 Kafka or Redpanda.
    expect(portProduct(9042)).toBeUndefined()
    expect(portProduct(9092)).toBeUndefined()
    const programs = {
      "cockroach sql --insecure": "cockroachdb",
      "duckdb analytics.duckdb": "duckdb",
      "etcdctl get / --prefix": "etcd",
      "cqlsh 127.0.0.1": "cassandra",
      "cypher-shell -u neo4j": "neo4j",
      "nats sub orders.>": "nats",
    }
    for (const [command, id] of Object.entries(programs)) {
      expect(seen(programProduct(command))).toBe(id)
    }
  })
})

test("hasProductLogo", () => {
  expect(hasProductLogo("rust")).toBe(true)
  expect(hasProductLogo("vite")).toBe(true)
  expect(hasProductLogo("s3")).toBe(false)
  expect(hasProductLogo(undefined)).toBe(false)
})

test("ProductLogos rings its tiles in the ground they sit on", () => {
  // Each tile sits in a span that stacks it over the next.
  const rings = (element) =>
    element.props.children[0].map((tile) => tile.props.children.props.className)
  const plain = ProductLogos({ ids: ["redis", "postgresql"] })
  for (const className of rings(plain)) {
    expect(className).toContain("ring-[var(--panel-ground,var(--background))]")
  }
  const onCard = ProductLogos({ ids: ["redis", "postgresql"], ring: "ring-choice-surface" })
  for (const className of rings(onCard)) {
    expect(className).toContain("ring-choice-surface")
    expect(className).not.toContain("--panel-ground")
  }
})

// Last: every id a reader above returned must be a file that is bundled, or the
// tile would draw its fallback glyph while the reader claims to name a product.
test("every product a reader returns has its file in public/logos", () => {
  for (const id of returned) {
    const src = ProductGlyph({ id })?.props.src
    expect({ id, bundled: src !== undefined && existsSync(join(PUBLIC, src)) }).toEqual({
      id,
      bundled: true,
    })
  }
})
