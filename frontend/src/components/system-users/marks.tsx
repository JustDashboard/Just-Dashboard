"use client"

import { SettingsGear, Shield, ShieldCheck, UserMinus } from "@/components/icons"
import { InitialsMark } from "@/components/account/user-avatar"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"
import type { SystemUser } from "@/lib/types"
import { cn } from "@/lib/utils"

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

/** The groups that may run anything as root; membership is what makes an account an administrator. */
const ADMIN_GROUPS = new Set(["sudo", "wheel", "admin"])

export function isAdmin(user: Pick<SystemUser, "groups">) {
  return user.groups.some((group) => ADMIN_GROUPS.has(group))
}

/** The account on the tile its list lines up on: a face for a person, a product or a glyph for the rest. */
export function AccountMark({
  user,
  size = "md",
}: {
  user: Pick<SystemUser, "username" | "system" | "uid">
  size?: "sm" | "md"
}) {
  const box = size === "md" ? "size-9" : "size-8"
  if (!user.system) return <InitialsMark name={user.username} size="md" className={box} />
  const glyph = user.uid === 0 ? Shield : user.uid === 65534 ? UserMinus : SettingsGear
  return <ProductLogo id={accountProduct(user)} size="sm" fallback={glyph} className={box} />
}

/**
 * An account's groups as one line, the ones that change what it can do first:
 * an administrator's group behind a shield, `docker` behind Docker's mark —
 * a member of it can mount the host's root into a container, which is root
 * by another name — and the rest after them, quieter.
 */
export function GroupList({ groups, className }: { groups: string[]; className?: string }) {
  if (groups.length === 0) {
    return <span className={cn("text-muted-foreground/60", className)}>no groups</span>
  }
  const admin = groups.filter((group) => ADMIN_GROUPS.has(group))
  const docker = groups.filter((group) => group === "docker")
  const rest = groups.filter((group) => !ADMIN_GROUPS.has(group) && group !== "docker")
  return (
    <span
      className={cn("inline-flex min-w-0 items-center gap-2", className)}
      title={groups.join(", ")}
    >
      {admin.map((group) => (
        <span key={group} className="inline-flex shrink-0 items-center gap-1 text-foreground">
          <ShieldCheck aria-hidden className="size-3.5" />
          {group}
        </span>
      ))}
      {docker.map((group) => (
        <span key={group} className="inline-flex shrink-0 items-center gap-1 text-foreground">
          <ProductGlyph id="docker" />
          {group}
        </span>
      ))}
      {rest.length > 0 && (
        <span className="min-w-0 truncate text-muted-foreground">{rest.join(", ")}</span>
      )}
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
