import { describe, expect, test } from "bun:test"
import { existsSync } from "node:fs"
import { join } from "node:path"
import {
  ProductGlyph,
  ProductLogos,
  buildMethodProduct,
  channelProduct,
  frameworkProduct,
  gitProviderProduct,
  hasProductLogo,
  hostProduct,
  imageProduct,
  issuerProduct,
  packageManagerProduct,
  portProduct,
  processProduct,
  recipeProduct,
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
  expect(processProduct("java")).toBe("java")
  expect(processProduct("postgres")).toBe("postgresql")
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
