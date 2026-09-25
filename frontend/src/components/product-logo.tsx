"use client"

import { useState } from "react"
import { Box, type Icon } from "@/components/icons"
import { hostOf, productOfHost, wordsProduct } from "@/lib/clients"
import type { NotificationChannelKind } from "@/lib/types"
import { cn } from "@/lib/utils"

/**
 * The file in `public/logos/` for each product a reader picks by name: every
 * reviewed blueprint, keyed by its id; the database engines, keyed the way
 * `/databases/provision/options` and a connection's `driver` name them; and
 * Docker and Compose themselves, for an image or a stack that is none of these.
 *
 * A template catalogue of sixty-two names in one grey face is a wall of words:
 * n8n, Grafana and Redis are recognised by their marks long before their names
 * are read, which is the argument §14 makes for a language's logo on a
 * repository row. The artwork is the product's own, in its own colours, so no
 * hue here is this product's to choose and none of it is a token.
 *
 * The files are bundled rather than fetched because the page's image policy
 * allows this origin only, and because the product runs on networks that
 * cannot reach a CDN (§8). Most come from homarr-labs/dashboard-icons
 * (Apache-2.0), picked in the variant that reads on a dark ground. A few do
 * not, because the collection draws them as a wordmark too small to read at
 * 24px or not at all: Jupyter is its Simple Icons mark (CC0), SQLite and SQL
 * Server are devicon's (MIT), and MySQL is devicon's dolphin lifted to L 0.72 —
 * its own #00618A is the navy §14 says disappears on this ground.
 * `public/logos/NOTICE` carries every source and licence.
 *
 * Aliases say which product a blueprint *is*: Mongo Express is MongoDB's own
 * admin, whoami is Traefik's.
 *
 * The dashboard's own pages draw the products it is made of and reached
 * through the same way: Caddy, Next.js and Go for the three services in its
 * stack, Tailscale and Let's Encrypt for its certificate, GitHub for the
 * repository it updates from.
 *
 * The account pages draw what signs in and what a key is for: a session as
 * its browser over its system (Chrome, Firefox, Safari, Edge, Opera, Brave,
 * Vivaldi; Windows, Apple, Android, Linux) or as the program that sent it
 * (curl), and a key as the service its name says holds it — GitLab, Jenkins,
 * Ansible, Home Assistant, Postman beside the ones above.
 *
 * The deploy pages draw what a project is made of and what it talks to: the
 * language its recipe builds and the framework detection found, the package
 * manager, the host its source or image lives on (GitHub, Codeberg, Quay, an
 * Azure or Amazon registry), the channel a notification goes to, the service
 * a variable's name says holds it, and on the request log the crawler that
 * asked and the site a visitor came from. Where homarr draws these for a dark
 * ground they are its files; the frameworks it lacks are Simple Icons' paths
 * filled with the brand's colour — white for a black mark, the L 0.72 rung
 * for a navy one, as MySQL's dolphin was. Rust and pnpm have two files each:
 * devicon's, drawn for paper, stay the file manager's, and the tile draws a
 * light one, because Rust's black gear and pnpm's charcoal squares vanish on
 * this ground.
 */
const LOGOS: Record<string, string> = {
  actual: "actual-budget.svg",
  adminer: "adminer.svg",
  almalinux: "almalinux.svg",
  alpine: "alpine.svg",
  amd: "amd.svg",
  android: "android.svg",
  angular: "angular.svg",
  ansible: "ansible.svg",
  apple: "apple.svg",
  arch: "arch.svg",
  arm: "arm.svg",
  astro: "astro.svg",
  audiobookshelf: "audiobookshelf.svg",
  aws: "aws.svg",
  azure: "azure.svg",
  backblaze: "backblaze.svg",
  beszel: "beszel.svg",
  bing: "bing.svg",
  bitbucket: "bitbucket.svg",
  brave: "brave.svg",
  bun: "bun.svg",
  caddy: "caddy.svg",
  centos: "centos.svg",
  chrome: "chrome.svg",
  claude: "claude.svg",
  clickhouse: "clickhouse.svg",
  cloudflare: "cloudflare.svg",
  "code-server": "code-server.webp",
  codeberg: "codeberg.svg",
  curl: "curl.svg",
  cyberchef: "cyberchef.svg",
  dart: "dart.svg",
  debian: "debian.svg",
  deno: "deno.svg",
  directus: "directus.svg",
  discord: "discord.svg",
  django: "django.svg",
  docker: "docker.svg",
  "docker-compose": "docker-compose.webp",
  docusaurus: "docusaurus.svg",
  docuseal: "docuseal.svg",
  dotnet: "dotnet.svg",
  dozzle: "dozzle.svg",
  drawio: "drawio.svg",
  duckduckgo: "duckduckgo.svg",
  edge: "edge.svg",
  eleventy: "eleventy.svg",
  elixir: "elixir.svg",
  ember: "ember.svg",
  express: "express.svg",
  facebook: "facebook.svg",
  fail2ban: "fail2ban.webp",
  fastapi: "fastapi.svg",
  fastify: "fastify.svg",
  fedora: "fedora.svg",
  filebrowser: "filebrowser.svg",
  firefox: "firefox.svg",
  flask: "flask.svg",
  forgejo: "forgejo.svg",
  freshrss: "freshrss.svg",
  gatsby: "gatsby.svg",
  git: "git.svg",
  gitea: "gitea.svg",
  github: "github.svg",
  gitlab: "gitlab.svg",
  go: "go.svg",
  google: "google.svg",
  "google-cloud": "google-cloud.svg",
  gotify: "gotify.svg",
  gradio: "gradio.svg",
  grafana: "grafana.svg",
  harbor: "harbor.svg",
  healthchecks: "healthchecks.svg",
  "home-assistant": "home-assistant.svg",
  homepage: "homepage.webp",
  hono: "hono.svg",
  influxdb: "influxdb.svg",
  intel: "intel.svg",
  "it-tools": "it-tools.svg",
  java: "java.svg",
  jellyfin: "jellyfin.svg",
  jenkins: "jenkins.svg",
  jupyter: "jupyter.svg",
  kavita: "kavita.svg",
  koa: "koa.svg",
  kotlin: "kotlin.svg",
  kubernetes: "kubernetes.svg",
  laravel: "laravel.svg",
  "lets-encrypt": "lets-encrypt.svg",
  linkding: "linkding.svg",
  linkedin: "linkedin.svg",
  linux: "linux.svg",
  linuxmint: "linuxmint.svg",
  mailgun: "mailgun.svg",
  mariadb: "mariadb.svg",
  meilisearch: "meilisearch.svg",
  memos: "memos.webp",
  metabase: "metabase.svg",
  "minecraft-bedrock": "minecraft.webp",
  "minecraft-java": "minecraft.webp",
  minio: "minio.svg",
  "mongo-express": "mongodb.svg",
  mongodb: "mongodb.svg",
  mysql: "mysql.svg",
  n8n: "n8n.svg",
  navidrome: "navidrome.svg",
  neovim: "neovim.svg",
  nestjs: "nestjs.svg",
  nextcloud: "nextcloud.svg",
  nextjs: "nextjs.svg",
  nginx: "nginx.svg",
  "nginx-static": "nginx.svg",
  nocodb: "nocodb.svg",
  nodejs: "nodejs.svg",
  npm: "npm.svg",
  ntfy: "ntfy.svg",
  nuxt: "nuxt.svg",
  ollama: "ollama.svg",
  "open-webui": "open-webui.svg",
  openai: "openai.svg",
  opengist: "opengist.svg",
  opensuse: "opensuse.svg",
  opera: "opera.svg",
  oracle: "oracle.svg",
  pgadmin: "pgadmin.svg",
  pgvector: "postgresql.svg",
  php: "php.svg",
  phpmyadmin: "phpmyadmin.svg",
  pm2: "pm2.svg",
  pnpm: "pnpm-light.svg",
  portainer: "portainer.svg",
  postgis: "postgresql.svg",
  postgres: "postgresql.svg",
  postgresql: "postgresql.svg",
  posthog: "posthog.svg",
  postman: "postman.svg",
  prometheus: "prometheus.svg",
  python: "python.svg",
  qdrant: "qdrant.svg",
  qemu: "qemu.svg",
  quarkus: "quarkus.svg",
  quay: "quay.svg",
  rabbitmq: "rabbitmq.svg",
  react: "react.svg",
  "react-router": "react-router.svg",
  reddit: "reddit.svg",
  redis: "redis.svg",
  remix: "remix.svg",
  rocky: "rocky.svg",
  ruby: "ruby.svg",
  rust: "rust-light.svg",
  safari: "safari.svg",
  scala: "scala.svg",
  searxng: "searxng.svg",
  seerr: "seerr.svg",
  sendgrid: "sendgrid.svg",
  sentry: "sentry.svg",
  shlink: "shlink.svg",
  slack: "slack.svg",
  solid: "solid.svg",
  "spring-boot": "spring-boot.svg",
  sqlite: "sqlite.svg",
  sqlserver: "sqlserver.svg",
  "stirling-pdf": "stirling-pdf.svg",
  streamlit: "streamlit.svg",
  stripe: "stripe.svg",
  supabase: "supabase.svg",
  svelte: "svelte.svg",
  symfony: "symfony.svg",
  syncthing: "syncthing.svg",
  tailscale: "tailscale.svg",
  tanstack: "tanstack.svg",
  telegram: "telegram.svg",
  // No product's mark: the terminal window `ProgramMark` draws for a program
  // with none, keyed so a command-line identity (the GitHub CLI) takes a tile.
  terminal: "terminal.svg",
  terraform: "terraform.svg",
  traefik: "traefik.svg",
  trilium: "trilium.svg",
  typescript: "typescript.svg",
  typesense: "typesense.svg",
  ubuntu: "ubuntu.svg",
  "uptime-kuma": "uptime-kuma.svg",
  valkey: "valkey.svg",
  vaultwarden: "vaultwarden.svg",
  vim: "vim.svg",
  vite: "vitejs.svg",
  vitepress: "vitepress.svg",
  vivaldi: "vivaldi.svg",
  vuejs: "vuejs.svg",
  wallabag: "wallabag.svg",
  webhook: "webhook.svg",
  whoami: "traefik.svg",
  windows: "windows.svg",
  x: "x.svg",
  yarn: "yarn.svg",
  ycombinator: "ycombinator.svg",
}

/**
 * Whether the product has a file, for a caller choosing between a logo and
 * something else — a project's favicon, a kind's glyph — before it draws
 * either, rather than finding out from an empty tile.
 */
export function hasProductLogo(id: string | undefined): id is string {
  return id !== undefined && id in LOGOS
}

/**
 * Image names that are not their product's own key. The language images are
 * the ones the build recipes use (`recipeBaseCatalogue` in the backend) —
 * `node`, `golang`, Temurin and the Maven and Gradle builders, FrankenPHP and
 * Composer, .NET's `aspnet` runtime — so a project built by a recipe is drawn
 * as the same language on the Docker page.
 */
const IMAGE_ALIASES: Record<string, string> = {
  mongo: "mongodb",
  nginx: "nginx-static",
  "portainer-ce": "portainer",
  "actual-server": "actual",
  "mssql-server": "sqlserver",
  "clickhouse-server": "clickhouse",
  node: "nodejs",
  golang: "go",
  "eclipse-temurin": "java",
  openjdk: "java",
  maven: "java",
  gradle: "java",
  frankenphp: "php",
  composer: "php",
  aspnet: "dotnet",
}

/**
 * Which product an image reference is, from its last path segment:
 * `ghcr.io/owner/n8n:1.2` is n8n. Anything this cannot name is drawn as a
 * Docker image, which is at least true of every one of them.
 */
export function imageProduct(reference: string) {
  const name = (reference.split("@")[0].split("/").pop() ?? "").split(":")[0].toLowerCase()
  const id = IMAGE_ALIASES[name] ?? name
  return id in LOGOS ? id : "docker"
}

/**
 * The products a set of images is, most-named first and each once: a stack of
 * three Postgres replicas and an API is Postgres and Docker, not four marks.
 */
export function imageProducts(references: string[]) {
  const counts = new Map<string, number>()
  for (const reference of references) {
    const id = imageProduct(reference)
    counts.set(id, (counts.get(id) ?? 0) + 1)
  }
  // Docker's whale says "an image", which every one of them is: it is only
  // worth a place when nothing more specific is.
  const named = [...counts.keys()].filter((id) => id !== "docker")
  const ids = named.length > 0 ? named : [...counts.keys()]
  return ids.sort((a, b) => (counts.get(b) ?? 0) - (counts.get(a) ?? 0))
}

/** Process names that are not their product's own key. */
const PROCESS_ALIASES: Record<string, string> = {
  postgres: "postgresql",
  postmaster: "postgresql",
  mysqld: "mysql",
  mariadbd: "mariadb",
  "redis-server": "redis",
  "valkey-server": "valkey",
  nginx: "nginx-static",
  dockerd: "docker",
  containerd: "docker",
  "containerd-shim": "docker",
  "containerd-shim-runc-v2": "docker",
  "docker-proxy": "docker",
  mongod: "mongodb",
  "clickhouse-server": "clickhouse",
  "grafana-server": "grafana",
  tailscaled: "tailscale",
  "fail2ban-server": "fail2ban",
  "fail2ban-client": "fail2ban",
  apache2: "apache",
  httpd: "apache",
  "pm2 v5": "pm2",
  "pm2 v6": "pm2",
}

/**
 * Which product a running process is, by its name — `postgres` is Postgres,
 * `dockerd` is Docker. Nothing for a name this does not know: most of a
 * process table is `bash` and `kworker`, and a guessed logo on those would be
 * the drawing lying about the row.
 */
export function processProduct(name: string) {
  const bare = name.toLowerCase().replace(/[:\s].*$/, "")
  const id = PROCESS_ALIASES[name.toLowerCase()] ?? PROCESS_ALIASES[bare] ?? bare
  // Compose's mark is a stack's, not a program's, and a process called `X` is
  // the X server rather than the site whose mark shares its key.
  return id in LOGOS && id !== "docker-compose" && id !== "x" ? id : undefined
}

/**
 * The program in a terminal's foreground, as the product it is — the command
 * name the backend reads off the PTY. Editors, runtimes, package managers and
 * database shells are the ones a session spends its time in and the ones worth
 * telling apart in a rail of tabs; a shell at its prompt, `htop` or `tail` has
 * none, and `ProgramMark` draws it as a terminal.
 */
const PROGRAMS: Record<string, string> = {
  node: "nodejs",
  nodejs: "nodejs",
  npm: "npm",
  npx: "npm",
  bun: "bun",
  bunx: "bun",
  deno: "deno",
  python: "python",
  python3: "python",
  pip: "python",
  pip3: "python",
  uv: "python",
  go: "go",
  git: "git",
  lazygit: "git",
  vim: "vim",
  vi: "vim",
  nvim: "neovim",
  docker: "docker",
  "docker-compose": "docker-compose",
  lazydocker: "docker",
  psql: "postgresql",
  pg_dump: "postgresql",
  "redis-cli": "redis",
  "valkey-cli": "valkey",
  mysql: "mysql",
  mariadb: "mariadb",
  mongosh: "mongodb",
  mongo: "mongodb",
  sqlite3: "sqlite",
  claude: "claude",
  kubectl: "kubernetes",
  k9s: "kubernetes",
  helm: "kubernetes",
  terraform: "terraform",
  tofu: "terraform",
  caddy: "caddy",
  nginx: "nginx-static",
  tailscale: "tailscale",
  pm2: "pm2",
}

export function programProduct(command: string | undefined) {
  const name = (command ?? "").trim().split(/\s+/)[0]?.split("/").pop()?.toLowerCase() ?? ""
  return PROGRAMS[name]
}

/**
 * The host's own marks: the distribution it runs, the processor it runs on and
 * the hypervisor under it, from what the host reports about itself — the
 * platform id `/etc/os-release` gives, the CPU's model string, the
 * virtualisation role. Anything this cannot name has no mark, rather than a
 * guess: a Tux on an unrecognised distribution says less than nothing.
 */
const PLATFORMS: Record<string, string> = {
  ubuntu: "ubuntu",
  debian: "debian",
  raspbian: "debian",
  fedora: "fedora",
  arch: "arch",
  archarm: "arch",
  alpine: "alpine",
  centos: "centos",
  rocky: "rocky",
  almalinux: "almalinux",
  linuxmint: "linuxmint",
  opensuse: "opensuse",
  "opensuse-leap": "opensuse",
  "opensuse-tumbleweed": "opensuse",
}

export function platformProduct(platform: string | undefined) {
  return PLATFORMS[(platform ?? "").toLowerCase()]
}

export function cpuProduct(model: string | undefined, arch?: string) {
  if (/\b(amd|epyc|ryzen|opteron|threadripper)\b/i.test(model ?? "")) return "amd"
  if (/\b(intel|xeon|core\(tm\)|pentium|celeron|atom)\b/i.test(model ?? "")) return "intel"
  if (/^(arm|aarch64)/i.test(arch ?? "") || /\b(arm|cortex|neoverse)\b/i.test(model ?? "")) {
    return "arm"
  }
  return undefined
}

export function virtualizationProduct(virtualization: string | undefined) {
  return /\b(kvm|qemu)\b/i.test(virtualization ?? "") ? "qemu" : undefined
}

/**
 * The hosts that are one product's, matched with every name under them:
 * `api.github.com` and `ghcr.io` are GitHub's, a registry at
 * `123.dkr.ecr.eu-west-1.amazonaws.com` is Amazon's, `europe-docker.pkg.dev`
 * is Google Cloud's Artifact Registry.
 */
const HOSTS: Record<string, string> = {
  "github.com": "github",
  "ghcr.io": "github",
  "githubusercontent.com": "github",
  "gitlab.com": "gitlab",
  "bitbucket.org": "bitbucket",
  "codeberg.org": "codeberg",
  "gitea.com": "gitea",
  "docker.io": "docker",
  "docker.com": "docker",
  "quay.io": "quay",
  "azurecr.io": "azure",
  "amazonaws.com": "aws",
  "ecr.aws": "aws",
  "gcr.io": "google-cloud",
  "pkg.dev": "google-cloud",
}

/**
 * The words a self-hosted forge or registry is usually named by:
 * `gitlab.example.com`, `forgejo.lan`, `harbor.corp.internal`. The host
 * saying it is the reading; a host that says nothing (`git.example.com`) is
 * no product.
 */
const HOST_WORDS: Record<string, string> = {
  github: "github",
  gitlab: "gitlab",
  bitbucket: "bitbucket",
  gitea: "gitea",
  forgejo: "forgejo",
  codeberg: "codeberg",
  harbor: "harbor",
  quay: "quay",
}

/**
 * Where a Git remote or an image registry lives, from whatever names it: a
 * URL, an scp-style `git@host:owner/repo`, an image reference's registry or a
 * bare host. A credential's target, a project's source and the Host field as
 * it is typed all read through this, so the one host is drawn the same way in
 * each.
 */
export function hostProduct(hostOrUrl: string | undefined): string | undefined {
  const host = hostOf(hostOrUrl ?? "")
  if (!host.includes(".")) return undefined
  return productOfHost(host, HOSTS) ?? wordsProduct(host.split(/[.-]/), HOST_WORDS)
}

/** The forges a trigger or a source names by `provider`. */
const GIT_PROVIDERS = new Set(["github", "gitlab", "bitbucket", "gitea"])

/**
 * The forge a webhook trigger or a Git source says it is. `generic_hook`,
 * `api` and the legacy hook are no forge: they keep their glyph.
 */
export function gitProviderProduct(provider: string | undefined): string | undefined {
  return provider && GIT_PROVIDERS.has(provider) ? provider : undefined
}

/** The language each automatic recipe builds; a site generator's is the nginx that serves it. */
const RECIPES: Record<string, string> = {
  node: "nodejs",
  go: "go",
  python: "python",
  rust: "rust",
  java: "java",
  dotnet: "dotnet",
  deno: "deno",
  php: "php",
  site: "nginx-static",
  ruby: "ruby",
  elixir: "elixir",
  scala: "scala",
  dart: "dart",
}

/**
 * The language a recipe builds — or Bun, for a Node recipe whose package
 * manager is Bun, because then Bun is the runtime the project runs on as well
 * as the tool it installs with.
 */
export function recipeProduct(
  recipe: string | undefined,
  packageManager?: string,
): string | undefined {
  if (recipe === "node" && packageManager === "bun") return "bun"
  return recipe ? RECIPES[recipe] : undefined
}

const PACKAGE_MANAGERS = new Set(["bun", "npm", "pnpm", "yarn"])

export function packageManagerProduct(packageManager: string | undefined): string | undefined {
  return packageManager && PACKAGE_MANAGERS.has(packageManager) ? packageManager : undefined
}

/**
 * The frameworks detection names, by the backend's own ids. One with a mark
 * of its own is drawn as itself; the Rust, Java and Kotlin frameworks that
 * have none are drawn as their language, which is still what the project is.
 * Nitro, Parcel, Elysia, hapi and Slim are neither, and the caller falls back
 * to the recipe.
 */
const FRAMEWORKS: Record<string, string> = {
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
  directus: "directus",
  go: "go",
  gin: "go",
  echo: "go",
  fiber: "go",
  chi: "go",
  gorilla: "go",
  python: "python",
  django: "django",
  fastapi: "fastapi",
  flask: "flask",
  streamlit: "streamlit",
  gradio: "gradio",
  litestar: "python",
  starlette: "python",
  sanic: "python",
  quart: "python",
  falcon: "python",
  bottle: "python",
  aiohttp: "python",
  tornado: "python",
  dash: "python",
  panel: "python",
  chainlit: "python",
  nicegui: "python",
  reflex: "python",
  mesop: "python",
  rust: "rust",
  axum: "rust",
  "actix-web": "rust",
  rocket: "rust",
  warp: "rust",
  poem: "rust",
  salvo: "rust",
  loco: "rust",
  leptos: "rust",
  trunk: "rust",
  dioxus: "rust",
  shuttle: "rust",
  java: "java",
  "spring-boot": "spring-boot",
  quarkus: "quarkus",
  micronaut: "java",
  javalin: "java",
  helidon: "java",
  vertx: "java",
  ktor: "kotlin",
  dotnet: "dotnet",
  aspnet: "dotnet",
  "blazor-wasm": "dotnet",
  deno: "deno",
  fresh: "deno",
  php: "php",
  laravel: "laravel",
  symfony: "symfony",
  jekyll: "ruby",
  mkdocs: "python",
  zensical: "python",
  sphinx: "python",
  pelican: "python",
  lume: "deno",
  vuepress: "vuejs",
  slidev: "vuejs",
  ruby: "ruby",
  rails: "ruby",
  hanami: "ruby",
  sinatra: "ruby",
  elixir: "elixir",
  phoenix: "elixir",
  scala: "scala",
  play: "scala",
  http4s: "scala",
  dart: "dart",
  dart_frog: "dart",
  shelf: "dart",
}

export function frameworkProduct(framework: string | undefined): string | undefined {
  return framework ? FRAMEWORKS[framework] : undefined
}

/**
 * What a build produces, as the product it runs on: a recipe as its
 * language, a Dockerfile as Docker, a static site as the nginx that serves
 * it, a Compose file as Compose, and an image as the product the image is.
 */
export function buildMethodProduct(
  method: string | undefined,
  {
    recipe,
    packageManager,
    image,
  }: { recipe?: string; packageManager?: string; image?: string } = {},
): string | undefined {
  switch (method) {
    case "recipe":
      return recipeProduct(recipe, packageManager)
    case "dockerfile":
      return "docker"
    case "static":
      return "nginx"
    case "compose":
    case "legacy_compose":
      return "docker-compose"
    case "image":
      return image ? imageProduct(image) : "docker"
    default:
      return undefined
  }
}

/**
 * A notification channel as the service it posts to. A signed webhook is
 * drawn with the webhook's own mark — the protocol's, which misattributes
 * nothing — unless `webhookProduct` can say whose it is; e-mail is a protocol
 * with no mark and keeps its glyph.
 */
export function channelProduct(kind: NotificationChannelKind): string | undefined {
  return kind === "email" ? undefined : kind
}

/** The services whose own host a webhook URL can point at. */
const WEBHOOK_HOSTS: Record<string, string> = {
  "discord.com": "discord",
  "discordapp.com": "discord",
  "slack.com": "slack",
  "telegram.org": "telegram",
  "hc-ping.com": "healthchecks",
}

/**
 * The self-hosted receivers a webhook is usually pointed at, by the word
 * their host is named with: `n8n.example.com`, `ntfy.sh`, `hass.lan`.
 */
const WEBHOOK_WORDS: Record<string, string> = {
  n8n: "n8n",
  ntfy: "ntfy",
  gotify: "gotify",
  homeassistant: "home-assistant",
  hass: "home-assistant",
  healthchecks: "healthchecks",
  uptimekuma: "uptime-kuma",
  kuma: "uptime-kuma",
}

/**
 * Which product receives a webhook, from its URL's host — nothing when the
 * host does not say, and the caller draws the webhook's own mark.
 */
export function webhookProduct(url: string | undefined): string | undefined {
  const host = hostOf(url ?? "")
  return productOfHost(host, WEBHOOK_HOSTS) ?? wordsProduct(host.split(/[.-]/), WEBHOOK_WORDS)
}

/**
 * Let's Encrypt, from the issuer's common name a certificate carries: its
 * intermediates are `R10`, `E6` and, from the 2025 hierarchy, `YR1`, `YE1` —
 * names no other public CA uses — and older chains say "Let's Encrypt" or
 * "ISRG" outright. The proxy issues through certbot, so every managed
 * certificate is one of these; an imported one is whatever it says.
 */
export function issuerProduct(issuer: string | undefined): string | undefined {
  const name = (issuer ?? "").trim()
  return /^Y?[RE]\d{1,2}$/.test(name) || /let'?s ?encrypt|^ISRG\b/i.test(name)
    ? "lets-encrypt"
    : undefined
}

/**
 * The services a variable's name says hold it, at any position:
 * `STRIPE_SECRET_KEY`, `NEXT_PUBLIC_SENTRY_DSN`, `AWS_REGION`. `S3_` names a
 * protocol a dozen providers speak and stays unnamed (§14), and so does
 * `DATABASE_URL`, which says nothing about which database.
 */
const VARIABLE_SERVICES: Record<string, string> = {
  stripe: "stripe",
  sentry: "sentry",
  openai: "openai",
  anthropic: "claude",
  claude: "claude",
  aws: "aws",
  azure: "azure",
  gcp: "google-cloud",
  cloudflare: "cloudflare",
  supabase: "supabase",
  slack: "slack",
  discord: "discord",
  telegram: "telegram",
  google: "google",
  posthog: "posthog",
  mailgun: "mailgun",
  sendgrid: "sendgrid",
  github: "github",
  gh: "github",
  gitlab: "gitlab",
  npm: "npm",
  docker: "docker",
  redis: "redis",
  valkey: "valkey",
  postgres: "postgres",
  postgresql: "postgres",
  pg: "postgres",
  mysql: "mysql",
  mariadb: "mariadb",
  mongo: "mongodb",
  mongodb: "mongodb",
  clickhouse: "clickhouse",
  minio: "minio",
  meili: "meilisearch",
  meilisearch: "meilisearch",
  typesense: "typesense",
  qdrant: "qdrant",
  rabbitmq: "rabbitmq",
  amqp: "rabbitmq",
  influx: "influxdb",
  influxdb: "influxdb",
  grafana: "grafana",
  prometheus: "prometheus",
  tailscale: "tailscale",
  ntfy: "ntfy",
  gotify: "gotify",
}

/**
 * The runtimes and frameworks that read a variable by its first word:
 * `NEXT_PUBLIC_*` is inlined by Next.js, `VITE_*` by Vite, `NODE_ENV` read by
 * Node. Only the first word, because `API_NODE_URL` is no Node setting.
 */
const VARIABLE_PREFIXES: Record<string, string> = {
  next: "nextjs",
  nextauth: "nextjs",
  vite: "vite",
  node: "nodejs",
  bun: "bun",
  python: "python",
}

/**
 * A variable as the product its name says holds it — `keyProduct`'s reading
 * of a key's name, applied to an environment variable. The service wins over
 * the framework prefix: `NEXT_PUBLIC_SUPABASE_URL` is Supabase's URL, exposed
 * through Next.js.
 */
export function variableProduct(name: string): string | undefined {
  const words = name.toLowerCase().split("_").filter(Boolean)
  return wordsProduct(words, VARIABLE_SERVICES) ?? VARIABLE_PREFIXES[words[0]]
}

/**
 * A product's logo on a recessed tile, the size of the mark a deployment card
 * carries (`ProjectMark`), so a template and the project it becomes are drawn
 * the same way.
 *
 * A product with no file, or a file that fails to load, keeps the tile with a
 * plain glyph in it: a card whose title starts at a different place from its
 * neighbours' is the ragged grid this replaced.
 */
export function ProductLogo({
  id,
  size = "md",
  fallback: Fallback = Box,
  className,
}: {
  /** A blueprint id, an engine or driver key, or nothing for a thing with no product. */
  id?: string
  size?: "sm" | "md"
  /**
   * The glyph for a thing that is no product — a volume, a network — so it
   * still takes the tile and lines up with the rows that have a logo.
   */
  fallback?: Icon
  className?: string
}) {
  // Which file failed, rather than whether one did: the settings panel's mark
  // changes product under the same component, and the next one may load.
  const [failed, setFailed] = useState<string>()
  const file = id ? LOGOS[id] : undefined
  return (
    <span
      aria-hidden="true"
      className={cn(
        "flex shrink-0 items-center justify-center rounded-lg border border-hairline bg-background",
        size === "sm" ? "size-8" : "size-10",
        className,
      )}
    >
      {file && file !== failed ? (
        // Same-origin and already sized for the tile: a plain element, as
        // `ProjectMark`'s favicon is. Not lazy: the page scrolls an inner
        // column, which lazy loading does not see into, so a tile below the
        // fold stayed empty until it was scrolled to — for a 3 KB file.
        // eslint-disable-next-line @next/next/no-img-element
        <img
          src={`/logos/${file}`}
          alt=""
          className={cn("object-contain", size === "sm" ? "size-4.5" : "size-6")}
          onError={() => setFailed(file)}
        />
      ) : (
        <Fallback className="size-4 text-muted-foreground" />
      )}
    </span>
  )
}

/**
 * A product's logo bare, inside a line of text: the host a repository lives on,
 * the issuer beside a certificate's state. The tile is for a mark that stands
 * beside a card's words; in a sentence it is a box in the middle of a line, so
 * this is the artwork alone at the line's own height.
 */
export function ProductGlyph({ id, className }: { id: string; className?: string }) {
  const file = LOGOS[id]
  if (!file) return null
  return (
    // eslint-disable-next-line @next/next/no-img-element
    <img
      src={`/logos/${file}`}
      alt=""
      aria-hidden="true"
      className={cn("size-3.5 shrink-0 object-contain", className)}
    />
  )
}

/**
 * The products a reading counts, bare and in a row after its words — the
 * images the running containers are, the engines the connections speak — so
 * "8 running" says eight *of what*. Past `max` it says how many more rather
 * than drawing a strip that outgrows its line.
 */
export function ProductGlyphs({ ids, max = 5 }: { ids: string[]; max?: number }) {
  const shown = ids.filter((id) => id in LOGOS).slice(0, max)
  if (shown.length === 0) return null
  const more = ids.length - shown.length
  return (
    <span aria-hidden="true" className="inline-flex shrink-0 items-center gap-1 align-[-2px]">
      {shown.map((id) => (
        <ProductGlyph key={id} id={id} />
      ))}
      {more > 0 && <span className="numeric text-micro text-muted-foreground">+{more}</span>}
    </span>
  )
}

/**
 * The products inside one thing — a stack's services — as tiles that overlap,
 * the way a group of avatars does: the first is whole and each after it tucks
 * under its neighbour, so three marks take the width of two. Past three it
 * says how many more rather than drawing a wall.
 *
 * The ring that parts one tile from the next is the ground the stack sits
 * on, so it reads as a gap rather than a halo: the panel's ground where a
 * panel declares one, the page's otherwise — `ring-card` drew a lighter band
 * round every tile on a plain panel. A stack on a surface no panel describes,
 * a card you pick, passes that surface's ring.
 */
export function ProductLogos({
  ids,
  size = "sm",
  ring = "ring-[var(--panel-ground,var(--background))]",
}: {
  ids: string[]
  size?: "sm" | "md"
  /** The ring colour, as a class: the ground the stack sits on. */
  ring?: string
}) {
  const shown = ids.slice(0, 3)
  const more = ids.length - shown.length
  return (
    <span aria-hidden="true" className="flex shrink-0 items-center">
      {shown.map((id, index) => (
        // Stacked against document order, so the first — the one the stack
        // is named for — is the tile nothing covers.
        <span
          key={id}
          className={cn("relative", index > 0 && (size === "sm" ? "-ml-3" : "-ml-4"))}
          style={{ zIndex: shown.length - index }}
        >
          <ProductLogo id={id} size={size} className={cn("ring-2", ring)} />
        </span>
      ))}
      {more > 0 && <span className="numeric ml-1 text-micro text-muted-foreground">+{more}</span>}
    </span>
  )
}
