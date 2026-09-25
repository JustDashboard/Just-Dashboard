"use client"

import {
  Archive,
  Box,
  ChartActivity,
  Clock,
  CloudUpload,
  Database,
  FirewallCheck,
  FolderOpen,
  GitHubMark,
  Globe,
  Key,
  ListOrdered,
  LockClosed,
  Logs,
  Notes,
  Puzzle,
  Question,
  SecureConnection,
  Servers,
  ShieldOff,
  SignIn,
  Terminal,
  UserSettings,
  Users,
  type Icon,
} from "@/components/icons"
import { InitialsMark, UserAvatar } from "@/components/account/user-avatar"
import { DashboardMark } from "@/components/backups/marks"
import { ProductLogo } from "@/components/product-logo"
import { useAuth } from "@/hooks/use-auth"
import { LANES, hueFor } from "@/lib/hue"
import type { AuditEntry } from "@/lib/types"
import { cn } from "@/lib/utils"

/**
 * An audit entry drawn as what it touched and who touched it (design-system
 * §14). Every action is named for the part of the product it changed —
 * `docker.container.restart`, `system.packages.install` — so each is drawn with
 * that part's own mark: the product where the part is one (Docker, Git, PM2,
 * fail2ban, the terminal) and the sidebar's glyph for it where it is not, which
 * is the wayfinding mark the reader already knows the section by. A person is
 * their initials in the hue their name has in the rail and on a run; a webhook
 * is the webhook's mark and the dashboard's own work is the dashboard's.
 */

export type AuditSection = {
  key: string
  label: string
  /** What the backend's action filter matches, as a substring of the action. */
  filter: string
  glyph: Icon
  product?: string
  match: (action: string) => boolean
}

const starts =
  (...prefixes: string[]) =>
  (action: string) =>
    prefixes.some((prefix) => action === prefix || action.startsWith(prefix + "."))

/** In the order they are tried: the more specific name first. */
export const AUDIT_SECTIONS: AuditSection[] = [
  { key: "signin", label: "Sign-in", filter: "auth.", glyph: SignIn, match: starts("auth") },
  {
    key: "account",
    label: "Account",
    filter: "account.",
    glyph: UserSettings,
    match: starts("account"),
  },
  {
    key: "docker",
    label: "Docker",
    filter: "docker.",
    glyph: Box,
    product: "docker",
    match: starts("docker"),
  },
  {
    key: "deploy",
    label: "Deployments",
    filter: "deploy.",
    glyph: CloudUpload,
    match: starts("deploy", "game_command"),
  },
  {
    key: "database",
    label: "Databases",
    filter: "database.",
    glyph: Database,
    match: starts("database"),
  },
  { key: "files", label: "Files", filter: "file.", glyph: FolderOpen, match: starts("file") },
  {
    key: "terminal",
    label: "Terminal",
    filter: "terminal.",
    glyph: Terminal,
    product: "terminal",
    match: starts("terminal"),
  },
  {
    key: "github",
    label: "GitHub",
    filter: "github.",
    glyph: GitHubMark,
    product: "github",
    match: starts("github"),
  },
  {
    key: "git",
    label: "Git",
    filter: "git.",
    glyph: GitHubMark,
    product: "git",
    match: starts("git"),
  },
  {
    key: "certificates",
    label: "Certificates",
    filter: "certificates.",
    glyph: LockClosed,
    match: starts("certificates"),
  },
  { key: "proxy", label: "Proxy", filter: "proxy.", glyph: Globe, match: starts("proxy") },
  {
    key: "intrusion",
    label: "Intrusion",
    filter: "ban",
    glyph: ShieldOff,
    product: "fail2ban",
    match: starts("fail2ban", "ban", "unban"),
  },
  {
    key: "firewall",
    label: "Firewall",
    filter: "firewall.",
    glyph: FirewallCheck,
    match: starts("firewall"),
  },
  { key: "ssh", label: "SSH", filter: "ssh.", glyph: SecureConnection, match: starts("ssh") },
  { key: "backups", label: "Backups", filter: "backup", glyph: Archive, match: starts("backup") },
  {
    key: "packages",
    label: "Packages",
    filter: "system.packages",
    glyph: Puzzle,
    match: starts("system.packages", "system.updates"),
  },
  {
    key: "users",
    label: "System users",
    filter: "system.user",
    glyph: Users,
    match: starts("system.user"),
  },
  {
    key: "pm2",
    label: "PM2",
    filter: "pm2.",
    glyph: ChartActivity,
    product: "pm2",
    match: starts("pm2"),
  },
  {
    key: "services",
    label: "Services",
    filter: "systemd.",
    glyph: Servers,
    match: starts("systemd"),
  },
  {
    key: "processes",
    label: "Processes",
    filter: "process.",
    glyph: ListOrdered,
    match: starts("process"),
  },
  { key: "scheduled", label: "Scheduled", filter: "cron.", glyph: Clock, match: starts("cron") },
  { key: "logs", label: "Logs", filter: "logs", glyph: Logs, match: starts("logs") },
  {
    key: "dashboard",
    label: "Dashboard",
    filter: "dashboard.",
    glyph: Notes,
    match: starts("dashboard"),
  },
]

/**
 * The action without the method a route that named nothing is recorded
 * under: `post:docker.containers` is still Docker's.
 */
function bareAction(action: string | undefined) {
  return (action ?? "").replace(/^[a-z]+:/, "")
}

export function auditSection(action: string | undefined): AuditSection | undefined {
  const bare = bareAction(action)
  return AUDIT_SECTIONS.find((section) => section.match(bare))
}

/**
 * Certbot issues and renews through Let's Encrypt, so those two are drawn as
 * the authority; an imported or revoked certificate is whoever signed it, and
 * keeps the section's lock.
 */
function actionProduct(action: string, section: AuditSection | undefined) {
  if (/^certificates\.(issue|renew)\b/.test(action)) return "lets-encrypt"
  return section?.product
}

/** What an entry touched, on the tile a product's logo takes. */
export function ActionMark({ action, className }: { action: string; className?: string }) {
  const bare = bareAction(action)
  const section = auditSection(bare)
  if (section?.key === "dashboard") return <DashboardMark className={className} />
  return (
    <ProductLogo
      id={actionProduct(bare, section)}
      size="sm"
      fallback={section?.glyph ?? Notes}
      className={className}
    />
  )
}

/**
 * The action as the log console draws a program: the part of the product in
 * the hue its name takes (`LANES`, which has no red or amber to be mistaken
 * for a failure), what was done to it in ink, and a route's method stepped
 * back in front of both.
 */
export function ActionName({ action, className }: { action: string; className?: string }) {
  const method = /^[a-z]+:/.exec(action ?? "")?.[0]
  const bare = bareAction(action)
  const dot = bare.indexOf(".")
  const head = dot < 0 ? bare : bare.slice(0, dot)
  const tail = dot < 0 ? "" : bare.slice(dot)
  return (
    <span className={cn("min-w-0 truncate font-mono", className)} title={action}>
      {method && <span className="text-muted-foreground/60">{method}</span>}
      <span style={{ color: hueFor(head, LANES) }}>{head}</span>
      <span className="text-foreground">{tail}</span>
    </span>
  )
}

/** The kinds of caller that are not a person, and what each is called in a row. */
const MACHINES: Record<string, string> = {
  webhook: "webhook",
  system: "the dashboard",
  reconciler: "the dashboard",
}

export function isMachine(entry: Pick<AuditEntry, "actor">) {
  return entry.actor in MACHINES
}

/** Who an entry names, in words: the account, else what acted without one. */
export function actorName(entry: Pick<AuditEntry, "actor" | "username">) {
  return entry.username || MACHINES[entry.actor] || "anonymous"
}

/**
 * Who acted, drawn as them: you as your own picture, anyone else as their
 * initials in their hue — with a key in the corner when they came through an
 * API key rather than a session — a webhook as the webhook's mark, the
 * dashboard's own reconcilers as the dashboard, and a caller with no name at
 * all as a question on the same tile.
 */
export function ActorMark({ entry }: { entry: Pick<AuditEntry, "actor" | "username"> }) {
  const { status } = useAuth()
  if (entry.actor === "webhook") return <ProductLogo id="webhook" size="sm" />
  if (MACHINES[entry.actor]) return <DashboardMark />
  if (!entry.username) return <ProductLogo size="sm" fallback={Question} />
  const me = status?.user?.username === entry.username ? status.user : undefined
  return (
    <span className="relative flex shrink-0">
      {me ? (
        <UserAvatar user={me} scope="self" size="sm" className="size-8" />
      ) : (
        <InitialsMark name={entry.username} size="sm" className="size-8" />
      )}
      {entry.actor === "token" && (
        <span
          aria-hidden
          className="absolute -right-1 -bottom-1 flex size-4 items-center justify-center rounded-sm border border-hairline bg-background"
        >
          <Key className="size-2.5 text-muted-foreground" />
        </span>
      )}
    </span>
  )
}
