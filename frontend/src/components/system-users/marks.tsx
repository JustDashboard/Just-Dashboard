"use client"

import { SettingsGear, Shield, ShieldCheck, UserMinus } from "@/components/icons"
import { InitialsMark } from "@/components/account/user-avatar"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"
import type { SystemUser } from "@/lib/types"

/**
 * The host's accounts drawn as who they are (design-system §14): a person as
 * their initials in the hue their name has everywhere else — the dashboard's
 * own account `ion` and the host's `ion` are one colour — and an account a
 * package made for its daemon as the product that runs under it, so
 * `postgres`, `redis` and `grafana` are found by their marks in a list of
 * forty. An account nothing names keeps a glyph: the superuser a shield, a
 * daemon a cog, `nobody` a crossed-out person.
 */

/**
 * The daemons whose package creates an account named for them. Curated: a
 * system account is named by whoever wrote the package, and `www-data` is
 * nginx's on one host and Apache's on the next, so it is nobody's.
 */
const ACCOUNTS: Record<string, string> = {
  postgres: "postgresql",
  postgresql: "postgresql",
  mysql: "mysql",
  mariadb: "mariadb",
  redis: "redis",
  valkey: "valkey",
  mongodb: "mongodb",
  clickhouse: "clickhouse",
  influxdb: "influxdb",
  rabbitmq: "rabbitmq",
  meilisearch: "meilisearch",
  typesense: "typesense",
  minio: "minio",
  grafana: "grafana",
  prometheus: "prometheus",
  jenkins: "jenkins",
  gitlab: "gitlab",
  git: "git",
  caddy: "caddy",
  nginx: "nginx",
  traefik: "traefik",
  dockremap: "docker",
  ollama: "ollama",
  jellyfin: "jellyfin",
  syncthing: "syncthing",
  nextcloud: "nextcloud",
  _apt: "debian",
}

/** The product a system account runs, by its name — `gitlab-runner` is GitLab's. */
export function accountProduct(user: Pick<SystemUser, "username" | "system">) {
  if (!user.system) return undefined
  const name = user.username.toLowerCase()
  return ACCOUNTS[name] ?? ACCOUNTS[name.split(/[-_]/)[0]]
}

/** The products a group or a shell is, where one is. */
const GROUP_PRODUCTS: Record<string, string> = {
  docker: "docker",
  lxd: "lxd",
  lxc: "lxd",
}

const SHELL_PRODUCTS: Record<string, string> = {
  bash: "shell-bash",
  zsh: "shell-zsh",
  fish: "shell-fish",
}

export function shellProduct(shell: string) {
  return SHELL_PRODUCTS[shell.split("/").pop() ?? ""]
}

/** A login shell as what it is, bare at the line's height; nothing for one with no mark. */
export function ShellGlyph({ shell, className }: { shell: string; className?: string }) {
  const id = shellProduct(shell)
  return id ? <ProductGlyph id={id} className={className} /> : null
}

export function groupProduct(name: string) {
  return GROUP_PRODUCTS[name]
}

/** The account on the tile its list lines up on: a face for a person, a product or a glyph for the rest. */
export function AccountMark({
  user,
  size = "md",
}: {
  user: Pick<SystemUser, "username" | "system" | "uid">
  size?: "sm" | "md" | "lg"
}) {
  const box = size === "lg" ? "size-12 rounded-xl" : size === "md" ? "size-9" : "size-8"
  if (!user.system) return <InitialsMark name={user.username} size="md" className={box} />
  const glyph = user.uid === 0 ? Shield : user.uid === 65534 ? UserMinus : SettingsGear
  return <ProductLogo id={accountProduct(user)} size="sm" fallback={glyph} className={box} />
}

/**
 * One group as a word, drawn as what it hands over: a sudo group behind a shield,
 * docker and lxd behind their marks, and the rest as a plain word.
 */
export function GroupWord({ name, sudo }: { name: string; sudo?: boolean }) {
  const product = groupProduct(name)
  return (
    <span className="inline-flex shrink-0 items-center gap-1">
      {product ? (
        <ProductGlyph id={product} />
      ) : sudo ? (
        <ShieldCheck aria-hidden className="size-3.5 text-warning" />
      ) : null}
      {name}
    </span>
  )
}

/** Who a reading counts, drawn as them: a few faces in a row, then how many more. */
export function FaceRun({ names, max = 4 }: { names: string[]; max?: number }) {
  if (names.length === 0) return null
  return (
    <span aria-hidden className="flex shrink-0 items-center gap-0.5">
      {names.slice(0, max).map((name) => (
        <InitialsMark key={name} name={name} size="xs" />
      ))}
      {names.length > max && (
        <span className="numeric ml-0.5 text-micro text-muted-foreground">
          +{names.length - max}
        </span>
      )}
    </span>
  )
}
