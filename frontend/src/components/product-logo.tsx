"use client"

import { useState } from "react"
import { Box, type Icon } from "@/components/icons"
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
 */
const LOGOS: Record<string, string> = {
  actual: "actual-budget.svg",
  adminer: "adminer.svg",
  almalinux: "almalinux.svg",
  alpine: "alpine.svg",
  amd: "amd.svg",
  arch: "arch.svg",
  arm: "arm.svg",
  audiobookshelf: "audiobookshelf.svg",
  backblaze: "backblaze.svg",
  beszel: "beszel.svg",
  bun: "bun.svg",
  caddy: "caddy.svg",
  centos: "centos.svg",
  claude: "claude.svg",
  clickhouse: "clickhouse.svg",
  "code-server": "code-server.webp",
  cyberchef: "cyberchef.svg",
  debian: "debian.svg",
  deno: "deno.svg",
  directus: "directus.svg",
  docker: "docker.svg",
  "docker-compose": "docker-compose.webp",
  docuseal: "docuseal.svg",
  dozzle: "dozzle.svg",
  drawio: "drawio.svg",
  fedora: "fedora.svg",
  filebrowser: "filebrowser.svg",
  freshrss: "freshrss.svg",
  git: "git.svg",
  gitea: "gitea.svg",
  github: "github.svg",
  go: "go.svg",
  gotify: "gotify.svg",
  grafana: "grafana.svg",
  healthchecks: "healthchecks.svg",
  homepage: "homepage.webp",
  influxdb: "influxdb.svg",
  intel: "intel.svg",
  "it-tools": "it-tools.svg",
  jellyfin: "jellyfin.svg",
  jupyter: "jupyter.svg",
  kavita: "kavita.svg",
  kubernetes: "kubernetes.svg",
  "lets-encrypt": "lets-encrypt.svg",
  linkding: "linkding.svg",
  linuxmint: "linuxmint.svg",
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
  nextcloud: "nextcloud.svg",
  nextjs: "nextjs.svg",
  "nginx-static": "nginx.svg",
  nocodb: "nocodb.svg",
  nodejs: "nodejs.svg",
  npm: "npm.svg",
  ntfy: "ntfy.svg",
  ollama: "ollama.svg",
  "open-webui": "open-webui.svg",
  opengist: "opengist.svg",
  opensuse: "opensuse.svg",
  oracle: "oracle.svg",
  pgadmin: "pgadmin.svg",
  phpmyadmin: "phpmyadmin.svg",
  pm2: "pm2.svg",
  portainer: "portainer.svg",
  postgres: "postgresql.svg",
  postgresql: "postgresql.svg",
  prometheus: "prometheus.svg",
  python: "python.svg",
  qdrant: "qdrant.svg",
  qemu: "qemu.svg",
  rabbitmq: "rabbitmq.svg",
  redis: "redis.svg",
  rocky: "rocky.svg",
  searxng: "searxng.svg",
  seerr: "seerr.svg",
  shlink: "shlink.svg",
  sqlite: "sqlite.svg",
  sqlserver: "sqlserver.svg",
  "stirling-pdf": "stirling-pdf.svg",
  syncthing: "syncthing.svg",
  tailscale: "tailscale.svg",
  terraform: "terraform.svg",
  traefik: "traefik.svg",
  trilium: "trilium.svg",
  typesense: "typesense.svg",
  ubuntu: "ubuntu.svg",
  "uptime-kuma": "uptime-kuma.svg",
  valkey: "valkey.svg",
  vaultwarden: "vaultwarden.svg",
  vim: "vim.svg",
  wallabag: "wallabag.svg",
  whoami: "traefik.svg",
}

/** Image names that are not their product's own key. */
const IMAGE_ALIASES: Record<string, string> = {
  mongo: "mongodb",
  nginx: "nginx-static",
  "portainer-ce": "portainer",
  "actual-server": "actual",
  "mssql-server": "sqlserver",
  "clickhouse-server": "clickhouse",
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
  return id in LOGOS && id !== "docker-compose" ? id : undefined
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
        // `ProjectMark`'s favicon is.
        // eslint-disable-next-line @next/next/no-img-element
        <img
          src={`/logos/${file}`}
          alt=""
          loading="lazy"
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
 */
export function ProductLogos({ ids, size = "sm" }: { ids: string[]; size?: "sm" | "md" }) {
  const shown = ids.slice(0, 3)
  const more = ids.length - shown.length
  return (
    <span aria-hidden="true" className="flex shrink-0 items-center">
      {shown.map((id, index) => (
        <ProductLogo
          key={id}
          id={id}
          size={size}
          className={cn(index > 0 && (size === "sm" ? "-ml-3" : "-ml-4"), "ring-2 ring-card")}
        />
      ))}
      {more > 0 && <span className="numeric ml-1 text-micro text-muted-foreground">+{more}</span>}
    </span>
  )
}
