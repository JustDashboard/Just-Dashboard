/**
 * `GET /files/context`: what a folder and each entry in it is to this server
 * (`internal/filecontext` and `handlers_files_context.go`). A fact is data —
 * a kind, the names it is about, its state, the page that owns it — and
 * `server-context-words.ts` turns it into words.
 */

export type ContextKind =
  | "volume"
  | "bind"
  | "compose"
  | "compose-file"
  | "site-root"
  | "site-config"
  | "site-log"
  | "certificate"
  | "backup"
  | "backup-target"
  | "not-backed-up"
  | "backup-excluded"
  | "git"
  | "deployment-source"
  | "deployment-data"
  | "database-data"
  | "database-config"
  | "unit-file"
  | "pm2"
  | "pm2-file"
  | "dashboard-data"
  | "dashboard-install"

export type ContextSource =
  "docker" | "proxy" | "backups" | "git" | "deploy" | "databases" | "processes" | "dashboard"

export type ContextFact = {
  kind: ContextKind
  source: ContextSource
  /** What the fact is about, most specific first: the volume, then the containers using it. */
  names: string[]
  /** The logo the server knows for it; `image` is a container's, which the page reads one from. */
  product?: string
  image?: string
  state?: string
  /** A branch, a version, a mount's destination, a certificate's issuer, a PM2 file's role. */
  detail?: string
  count?: number
  /** A backup's last success. */
  at?: string
  /** A certificate's expiry. */
  until?: string
  schedule?: string
  target?: string
  href?: string
  /** The folder the fact is about when the path is inside it. */
  from?: string
  /** What is inside the path shares the fact. */
  inherits?: boolean
}

export type FileContext = {
  /** The folder, resolved: the listing's own path. */
  path: string
  facts: ContextFact[]
  /** Each entry's own facts, by the path the listing gives it. */
  children: Record<string, ContextFact[]>
  /** The sources this answer could not include. */
  partial: ContextSource[]
  truncated?: boolean
  checkedAt: string
}
