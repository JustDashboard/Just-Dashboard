"use client"

import {
  BookOpen,
  Box,
  Code,
  Cpu,
  Database,
  DesktopDevice,
  Envelope,
  GitBranch,
  Globe,
  Image,
  Layers,
  Music,
  NetworkDevice,
  Pencil,
  Puzzle,
  SettingsGear,
  Terminal,
  TextFormat,
  Video,
  Wrench,
  type Icon,
} from "@/components/icons"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"
import { cn } from "@/lib/utils"

/**
 * The Packages page drawn as the software it lists (design-system §14): a
 * package as the product it is, where its name says so, and as the kind of
 * thing its section says it is where it does not; an upgrade's origin as the
 * archive that published it; the host's package manager as the distribution
 * whose tool it is.
 *
 * Every reader here returns nothing for a name it cannot place. Most of an
 * installed list is `libc6` and `libtinfo6`, and a guessed logo on those would
 * be the drawing lying about the row — they keep their section's glyph on the
 * same tile, so a column of names still lines up.
 */

/** Names that are one product whatever the leading-word rule below would make of them. */
const EXACT: Record<string, string> = {
  fail2ban: "fail2ban",
  gh: "github",
  glab: "gitlab",
  "code-server": "code-server",
  mongosh: "mongodb",
  apt: "debian",
  dpkg: "debian",
  debianutils: "debian",
  dnf: "fedora",
  pacman: "arch",
  zypper: "opensuse",
  awscli: "aws",
  "default-jdk": "java",
  "default-jre": "java",
  maven: "java",
  gradle: "java",
}

/**
 * Names that carry their product somewhere other than their first word — a
 * certbot plugin packaged as a Python library, an nginx module packaged as a
 * library — or share a first word with a different product.
 */
const PATTERNS: [RegExp, string][] = [
  [/certbot/, "lets-encrypt"],
  [/^libnginx-mod/, "nginx"],
  [/^docker-compose/, "docker-compose"],
  [/^google-chrome/, "chrome"],
  [/^google-cloud/, "google-cloud"],
  [/^microsoft-edge/, "edge"],
  [/^amd64-microcode/, "amd"],
]

/**
 * The product a package's leading word names, once its digits and version are
 * dropped: `postgresql-16` and `postgresql16-server` are PostgreSQL,
 * `python3-requests` is Python, `php8.3-fpm` is PHP, `linux-image-6.8.0` is the
 * kernel. Curated rather than read against every logo the product carries —
 * `x11-common` is no post on X.
 */
const WORDS: Record<string, string> = {
  nginx: "nginx",
  caddy: "caddy",
  traefik: "traefik",
  postgresql: "postgresql",
  postgres: "postgresql",
  mysql: "mysql",
  mariadb: "mariadb",
  redis: "redis",
  valkey: "valkey",
  mongodb: "mongodb",
  sqlite: "sqlite",
  clickhouse: "clickhouse",
  influxdb: "influxdb",
  rabbitmq: "rabbitmq",
  meilisearch: "meilisearch",
  typesense: "typesense",
  minio: "minio",
  docker: "docker",
  containerd: "docker",
  moby: "docker",
  nodejs: "nodejs",
  node: "nodejs",
  npm: "npm",
  yarn: "yarn",
  yarnpkg: "yarn",
  pnpm: "pnpm",
  bun: "bun",
  deno: "deno",
  python: "python",
  php: "php",
  phpmyadmin: "phpmyadmin",
  ruby: "ruby",
  golang: "go",
  go: "go",
  rust: "rust",
  rustc: "rust",
  cargo: "rust",
  openjdk: "java",
  java: "java",
  kotlin: "kotlin",
  scala: "scala",
  dotnet: "dotnet",
  aspnetcore: "dotnet",
  elixir: "elixir",
  dart: "dart",
  git: "git",
  gitlab: "gitlab",
  vim: "vim",
  neovim: "neovim",
  curl: "curl",
  grafana: "grafana",
  prometheus: "prometheus",
  tailscale: "tailscale",
  cloudflared: "cloudflare",
  kubectl: "kubernetes",
  kubeadm: "kubernetes",
  kubelet: "kubernetes",
  kubernetes: "kubernetes",
  helm: "kubernetes",
  terraform: "terraform",
  ansible: "ansible",
  jenkins: "jenkins",
  nextcloud: "nextcloud",
  jellyfin: "jellyfin",
  syncthing: "syncthing",
  ollama: "ollama",
  qemu: "qemu",
  intel: "intel",
  firefox: "firefox",
  brave: "brave",
  vivaldi: "vivaldi",
  opera: "opera",
  azure: "azure",
  aws: "aws",
  linux: "linux",
  ubuntu: "ubuntu",
  debian: "debian",
  fedora: "fedora",
  alpine: "alpine",
  apk: "alpine",
  archlinux: "arch",
  centos: "centos",
  rocky: "rocky",
  almalinux: "almalinux",
  opensuse: "opensuse",
  linuxmint: "linuxmint",
}

/**
 * The libraries a product ships as its own client — `libpq5` is how every
 * program talks to PostgreSQL, `libcurl4` is curl's — read after the `lib`
 * prefix. Any other library is its own project, not the product its name
 * happens to begin with.
 */
const LIBRARIES: Record<string, string> = {
  pq: "postgresql",
  mysqlclient: "mysql",
  mariadb: "mariadb",
  sqlite: "sqlite",
  curl: "curl",
  python: "python",
  ruby: "ruby",
  node: "nodejs",
  php: "php",
}

/** The product a package is, by its name, or nothing for a name that does not say. */
export function packageProduct(name: string): string | undefined {
  const bare = name.toLowerCase().split(":")[0]
  if (EXACT[bare]) return EXACT[bare]
  const pattern = PATTERNS.find(([re]) => re.test(bare))
  if (pattern) return pattern[1]
  if (bare.startsWith("lib")) {
    return LIBRARIES[bare.slice(3).match(/^[a-z]+/)?.[0] ?? ""]
  }
  return WORDS[bare.match(/^[a-z]+/)?.[0] ?? ""]
}

/**
 * What a package is, where no product says so: the archive's own section,
 * as the glyph for that kind of thing. Debian and Arch name these; an RPM's
 * group is rarely set, and the tile keeps the puzzle piece.
 */
const SECTION_GLYPH: Record<string, Icon> = {
  libs: Layers,
  libdevel: Layers,
  oldlibs: Layers,
  devel: Code,
  interpreters: Code,
  python: Code,
  perl: Code,
  ruby: Code,
  php: Code,
  javascript: Code,
  golang: Code,
  rust: Code,
  java: Code,
  database: Database,
  web: Globe,
  httpd: Globe,
  net: NetworkDevice,
  admin: SettingsGear,
  utils: Wrench,
  kernel: Cpu,
  shells: Terminal,
  editors: Pencil,
  doc: BookOpen,
  mail: Envelope,
  fonts: TextFormat,
  vcs: GitBranch,
  video: Video,
  sound: Music,
  graphics: Image,
  x11: DesktopDevice,
  gnome: DesktopDevice,
  kde: DesktopDevice,
  xfce: DesktopDevice,
  metapackages: Box,
}

/** Debian writes a component in front of a section it files outside main: `universe/net`. */
export function sectionGlyph(section: string | undefined): Icon {
  const bare = (section ?? "").toLowerCase().split("/").pop() ?? ""
  return SECTION_GLYPH[bare] ?? Puzzle
}

/** A package on the tile a product's logo takes: its product, else its section's glyph. */
export function PackageMark({
  name,
  section,
  className,
}: {
  name: string
  section?: string
  className?: string
}) {
  return (
    <ProductLogo
      id={packageProduct(name)}
      size="sm"
      fallback={sectionGlyph(section)}
      className={className}
    />
  )
}

/**
 * Who published an upgrade, from the origin the manager prints beside it:
 * apt's `Ubuntu:24.04/noble-security`, a vendor's own archive
 * (`apt.postgresql.org`, `Docker CE`, `Tailscale`), a dnf repository id
 * (`pgdg16`, `docker-ce-stable`). A mirror or a PPA named for a person is
 * nobody's product.
 */
const ORIGIN_WORDS: Record<string, string> = {
  ubuntu: "ubuntu",
  debian: "debian",
  fedora: "fedora",
  rocky: "rocky",
  almalinux: "almalinux",
  centos: "centos",
  opensuse: "opensuse",
  alpine: "alpine",
  docker: "docker",
  postgresql: "postgresql",
  pgdg: "postgresql",
  nodesource: "nodejs",
  node: "nodejs",
  tailscale: "tailscale",
  caddy: "caddy",
  grafana: "grafana",
  mongodb: "mongodb",
  mariadb: "mariadb",
  mysql: "mysql",
  redis: "redis",
  github: "github",
  gitlab: "gitlab",
  cloudflare: "cloudflare",
  hashicorp: "terraform",
  kubernetes: "kubernetes",
}

export function originProduct(origin: string | undefined): string | undefined {
  const words = (origin ?? "").toLowerCase().split(/[^a-z]+/)
  return words.map((word) => ORIGIN_WORDS[word]).find(Boolean)
}

/**
 * The distribution whose tool a manager is, for a host whose own
 * distribution the page could not read: apt is Debian's, dnf Fedora's,
 * pacman Arch's. yum and zypper are drawn by the distribution only when the
 * host says which.
 */
const MANAGERS: Record<string, string> = {
  apt: "debian",
  dnf: "fedora",
  pacman: "arch",
  apk: "alpine",
}

export function managerProduct(manager: string | undefined): string | undefined {
  return MANAGERS[(manager ?? "").toLowerCase()]
}

/** An upgrade's origin, as the archive that published it and the pocket it came from. */
export function OriginFact({ origin, className }: { origin?: string; className?: string }) {
  if (!origin) return <span className="text-muted-foreground/60">—</span>
  // apt prints the architecture after the pocket; the table already says it.
  const text = origin.replace(/\s*\[[^\]]*\]$/, "")
  const product = originProduct(text)
  return (
    <span className={cn("inline-flex min-w-0 items-center gap-1.5", className)} title={origin}>
      {product && <ProductGlyph id={product} />}
      <span className="truncate font-mono">{text}</span>
    </span>
  )
}

/**
 * Where two versions of one package part. Cut back to the last separator both
 * share, so `1.24.0` → `1.25.0` changes `25.0` rather than `5.0`: the reader
 * compares the field that moved, not the digit.
 */
export function versionChange(from: string | undefined, to: string) {
  if (!from) return { kept: "", changed: to }
  let at = 0
  while (at < from.length && at < to.length && from[at] === to[at]) at++
  if (at === to.length) return { kept: to, changed: "" }
  // A field ends where both versions end one: `3.0.13-0ubuntu3` is all of
  // `3.0.13-0ubuntu3.4` up to a separator, but `1.2` is not a field of `1.24`.
  const separator = (c: string | undefined) => c !== undefined && /[.\-+~:]/.test(c)
  const boundary = (i: number) =>
    i === 0 ||
    separator(to[i - 1]) ||
    (separator(to[i]) && (i === from.length || separator(from[i])))
  while (!boundary(at)) at--
  return { kept: to.slice(0, at), changed: to.slice(at) }
}

/**
 * The version an upgrade lands on, with the part it keeps from the one
 * installed stepped back and the part it changes in ink — amber for a
 * security fix, the one change on this page that is a reading of risk.
 */
export function VersionTo({
  from,
  to,
  security,
  className,
}: {
  from?: string
  to: string
  security?: boolean
  className?: string
}) {
  const { kept, changed } = versionChange(from, to)
  return (
    <span className={cn("font-mono", className)}>
      <span className="text-muted-foreground">{kept}</span>
      <span className={cn("font-medium", security ? "text-warning" : "text-foreground")}>
        {changed}
      </span>
    </span>
  )
}
