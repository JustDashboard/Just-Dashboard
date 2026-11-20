import { presetFor } from "@/lib/cron"
import { imageProduct, issuerProduct } from "@/components/product-logo"
import { rcloneService } from "@/components/backups/shared"
import type { ContextFact, ContextKind, FileContext } from "@/components/files/server-context-types"

/**
 * What a folder or a file is to this server, in words.
 *
 * The server sends facts (`server-context-types.ts`): a kind, the names it is
 * about, a state, a link. Every sentence the Files page says about them is
 * made here, so the wording is in one place and tested as one
 * (`server-context-words.test.js`). A row says the most telling fact as a short
 * phrase; the details pane says each as a sentence, with what the entry
 * inherits from a folder it is inside.
 */

/** Which fact a row says, most telling first. "Not backed up" is never a row's. */
const RANK: Record<ContextKind, number> = {
  "database-data": 1,
  volume: 2,
  "deployment-source": 3,
  "deployment-data": 4,
  "site-root": 5,
  git: 6,
  compose: 7,
  pm2: 8,
  bind: 9,
  "dashboard-data": 10,
  "dashboard-install": 11,
  "compose-file": 12,
  "unit-file": 13,
  "site-config": 14,
  certificate: 15,
  "database-config": 16,
  "pm2-file": 17,
  "site-log": 18,
  "backup-target": 19,
  backup: 20,
  "backup-excluded": 21,
  "not-backed-up": 99,
}

/** The fact a row or a tile says: the most telling of the entry's own. */
export function rowFact(facts: ContextFact[] | undefined): ContextFact | undefined {
  let best: ContextFact | undefined
  for (const fact of facts ?? []) {
    if (fact.from || fact.kind === "not-backed-up") continue
    if (!best || RANK[fact.kind] < RANK[best.kind]) best = fact
  }
  return best
}

/**
 * Everything known about one entry: the folder's own facts when it is the
 * folder on screen, else the entry's own and what it inherits from the folder
 * (the folder's facts that pass inside, each naming where it comes from). A
 * child left out of a covering backup does not inherit that backup.
 */
export function entryFacts(context: FileContext | null | undefined, path: string): ContextFact[] {
  if (!context) return []
  if (path === context.path) return context.facts
  const own = context.children[path] ?? []
  const excluded = new Set(own.filter((f) => f.kind === "backup-excluded").map((f) => f.href))
  const seen = new Set(own.map(identity))
  const inherited = context.facts
    .filter((f) => f.inherits && !(f.kind === "backup" && excluded.has(f.href)))
    .map((f) => (f.from ? f : { ...f, from: context.path }))
    .filter((f) => !seen.has(identity(f)))
  return [...own, ...inherited]
}

function identity(f: ContextFact) {
  return `${f.kind}\u0000${f.href ?? ""}\u0000${f.names.join("\u0000")}`
}

/** The logo a fact is drawn with, where it has one; `jd` is this dashboard's own mark. */
export function factProduct(fact: ContextFact): string | undefined {
  if (fact.kind === "dashboard-data" || fact.kind === "dashboard-install") return "jd"
  if (fact.kind === "certificate") return issuerProduct(fact.detail)
  if (fact.kind === "backup" || fact.kind === "backup-target" || fact.kind === "backup-excluded") {
    return fact.target === "b2" ? "backblaze" : undefined
  }
  if (fact.image) {
    const fromImage = imageProduct(fact.image)
    if (fromImage !== "docker" || !fact.product) return fromImage
  }
  if (fact.product) return fact.product
  if (fact.source === "docker") return "docker"
  return undefined
}

/** Products' names as a sentence says them. */
const NAMES: Record<string, string> = {
  postgres: "Postgres",
  postgresql: "PostgreSQL",
  timescaledb: "TimescaleDB",
  mysql: "MySQL",
  mariadb: "MariaDB",
  redis: "Redis",
  valkey: "Valkey",
  mongodb: "MongoDB",
  sqlite: "SQLite",
  clickhouse: "ClickHouse",
  elasticsearch: "Elasticsearch",
  cockroachdb: "CockroachDB",
  influxdb: "InfluxDB",
  nginx: "nginx",
  caddy: "Caddy",
}

function productWord(id: string | undefined): string | undefined {
  if (!id) return undefined
  return NAMES[id] ?? (id === "docker" ? undefined : id.charAt(0).toUpperCase() + id.slice(1))
}

/** "a", "a and b", "a, b and 2 more". */
function listed(names: string[], max = 2): string {
  if (names.length <= max) {
    return names.length > 1
      ? `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`
      : (names[0] ?? "")
  }
  return `${names.slice(0, max).join(", ")} and ${names.length - max} more`
}

const MINUTE = 60_000
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR

/** An age in words: "just now", "a minute ago", "3 hours ago", "2 days ago". */
export function agoWords(at: string, now: number): string {
  const age = now - new Date(at).getTime()
  if (!Number.isFinite(age)) return ""
  if (age < 45_000) return "just now"
  const say = (n: number, unit: string) =>
    n === 1 ? `${unit === "hour" ? "an" : "a"} ${unit} ago` : `${n} ${unit}s ago`
  if (age < HOUR) return say(Math.max(1, Math.round(age / MINUTE)), "minute")
  if (age < DAY) return say(Math.round(age / HOUR), "hour")
  if (age < 60 * DAY) return say(Math.round(age / DAY), "day")
  return say(Math.round(age / (30 * DAY)), "month")
}

/** A certificate's time left: "61 days left", "expires today", "expired 3 days ago". */
export function daysLeftWords(until: string, now: number): string {
  const left = new Date(until).getTime() - now
  if (!Number.isFinite(left)) return ""
  const days = Math.floor(Math.abs(left) / DAY)
  if (left < 0)
    return days === 0 ? "expired today" : `expired ${days === 1 ? "a day" : `${days} days`} ago`
  if (days === 0) return "expires today"
  return days === 1 ? "1 day left" : `${days} days left`
}

/** How often a backup runs, as an adverb: "nightly", "hourly", "weekly". */
export function scheduleWords(schedule: string | undefined): string {
  const cron = (schedule ?? "").trim()
  if (!cron) return "by hand"
  switch (presetFor(cron)) {
    case "minute":
    case "5min":
    case "15min":
      return "every few minutes"
    case "hourly":
      return "hourly"
    case "daily": {
      const hour = Number(cron.split(/\s+/)[1])
      return hour <= 6 || hour >= 22 ? "nightly" : "daily"
    }
    case "weekly":
      return "weekly"
    case "monthly":
      return "monthly"
    default:
      return "on a schedule"
  }
}

/** Where a backup goes, as "to …" says it. */
export function targetWords(target: string | undefined, detail?: string): string {
  switch (target) {
    case "b2":
      return "B2"
    case "s3":
      return "S3"
    case "sftp":
      return "an SFTP server"
    case "rclone":
      return rcloneService(detail)
    case "local":
      return "a folder on this server"
    default:
      return "its destination"
  }
}

function backupRun(fact: ContextFact, now: number): string {
  switch (fact.state) {
    case "failed":
      return "last run failed"
    case "never":
      return "not run yet"
    default:
      return fact.at ? `last run ${agoWords(fact.at, now)}` : ""
  }
}

function joinClauses(...parts: (string | false | undefined)[]) {
  return parts.filter(Boolean).join(", ")
}

/** "Postgres volume" for a volume a Postgres container uses, else "Volume". */
function volumeLead(fact: ContextFact) {
  const product = productWord(factProduct(fact))
  return product ? `${product} volume` : "Volume"
}

function databaseWords(fact: ContextFact) {
  const name = productWord(fact.product) ?? "the database"
  return fact.detail ? `${name} ${fact.detail}` : name
}

/** The short phrase a row says for its most telling fact. */
export function phrase(fact: ContextFact, now = Date.now()): string {
  const [first = "", second] = fact.names
  switch (fact.kind) {
    case "volume": {
      if (!second) return `Docker volume ${first}, unused`
      return `${volumeLead(fact)} of ${second}`
    }
    case "bind":
      return `Bind mount of ${first}`
    case "compose":
      return `Stack ${first}`
    case "compose-file":
      return `The stack ${first}'s Compose file`
    case "site-root":
      return fact.state === "disabled"
        ? `Root of ${first}, disabled`
        : `Served at ${first}${productWord(fact.product) ? ` by ${productWord(fact.product)}` : ""}`
    case "site-config":
      return `Site ${first}'s configuration`
    case "site-log":
      return `Log of ${first}`
    case "certificate":
      return fact.until
        ? `Certificate for ${first}, ${daysLeftWords(fact.until, now)}`
        : `Certificate for ${first}`
    case "backup":
      return fact.state === "paused"
        ? `Backup ${first} is paused`
        : `Backed up ${scheduleWords(fact.schedule)} to ${targetWords(fact.target, fact.detail)}`
    case "backup-target":
      return `Archives of the backup ${first}`
    case "backup-excluded":
      return `Left out of the backup ${first}`
    case "not-backed-up":
      return "Not backed up"
    case "git": {
      const where = fact.detail?.startsWith("detached")
        ? fact.detail
        : `on ${fact.detail || "no branch"}`
      const changes =
        fact.state === "conflicts"
          ? "conflicts to resolve"
          : fact.count
            ? `${fact.count} uncommitted`
            : undefined
      return joinClauses(`Git checkout ${where}`, changes)
    }
    case "deployment-source":
      return `The deployment ${first}'s source`
    case "deployment-data":
      return `Data of the deployment ${first}`
    case "database-data":
      return fact.product === "sqlite"
        ? `SQLite database ${first}`
        : `Data directory of ${databaseWords(fact)}`
    case "database-config":
      return `Configuration of ${databaseWords(fact)}`
    case "unit-file":
      return `Unit file of ${first}`
    case "pm2":
      return `PM2 app ${first} runs here`
    case "pm2-file":
      return `${fact.detail === "log" ? "Log" : "Script"} of PM2 app ${first}`
    case "dashboard-data":
      return "Just Dashboard's own data"
    case "dashboard-install":
      return "Where Just Dashboard is installed"
  }
}

/**
 * The sentence the details pane says: the phrase with what a row has no room
 * for, or, for a fact the entry inherits, what it is inside.
 */
export function sentence(fact: ContextFact, now = Date.now()): string {
  const [first = "", ...rest] = fact.names
  if (fact.from) return inheritedSentence(fact, now)
  switch (fact.kind) {
    case "volume":
      return rest.length > 0
        ? `${volumeLead(fact)} of ${listed(rest, 3)}, Docker volume ${first}`
        : `Docker volume ${first}, not used by any container`
    case "bind":
      return joinClauses(
        `Bind mount of the ${first} container${rest[0] ? `, at ${rest[0]}` : ""}`,
        fact.state && fact.state !== "running" && fact.state,
      )
    case "compose":
      return joinClauses(`Folder of the Compose stack ${first}`, fact.state)
    case "site-root":
      return fact.names.length > 1 && fact.state !== "disabled"
        ? `Served at ${listed(fact.names)}${productWord(fact.product) ? ` by ${productWord(fact.product)}` : ""}`
        : phrase(fact, now)
    case "site-config":
      return `${productWord(fact.product) ?? "Proxy"} configuration of ${first}${fact.state === "disabled" ? ", disabled" : ""}`
    case "certificate": {
      const kind = fact.state === "staging" ? "Test certificate" : "Certificate"
      const names = listed(fact.names)
      if (fact.state === "unreadable") return `${kind} for ${names}, could not be read`
      return joinClauses(`${kind} for ${names}`, fact.until && daysLeftWords(fact.until, now))
    }
    case "backup":
      return fact.state === "paused"
        ? `The backup ${first} would cover it, but it is paused`
        : joinClauses(`${phrase(fact, now)} by ${first}`, backupRun(fact, now))
    case "backup-target":
      return `Where the backup ${first} keeps its archives`
    case "backup-excluded":
      return `Left out of the backup ${first} on purpose`
    case "not-backed-up":
      return "Not backed up: no backup job covers it"
    case "git":
      return phrase(fact, now)
    case "database-data":
      return fact.product === "sqlite" ? phrase(fact, now) : `${phrase(fact, now)}, ${first}`
    default:
      return phrase(fact, now)
  }
}

function inheritedSentence(fact: ContextFact, now: number): string {
  const [first = "", second] = fact.names
  switch (fact.kind) {
    case "volume":
      return second ? `Inside the volume of ${second}` : `Inside Docker volume ${first}`
    case "bind":
      return `Inside the bind mount of ${first}`
    case "compose":
      return `Inside the stack ${first}'s folder`
    case "site-root":
      return `Inside the root of ${first}`
    case "backup":
      return fact.state === "paused"
        ? `Inside ${fact.from}, which the paused backup ${first} would cover`
        : joinClauses(
            `Covered by the ${scheduleWords(fact.schedule)} backup of ${fact.from} to ${targetWords(fact.target, fact.detail)}`,
            backupRun(fact, now),
          )
    case "backup-target":
      return `Inside the archives of the backup ${first}`
    case "git":
      return joinClauses(
        `Inside the Git checkout ${first}`,
        fact.count ? `${fact.count} uncommitted` : undefined,
      )
    case "deployment-source":
      return `Inside the deployment ${first}'s source`
    case "deployment-data":
      return `Inside the data of the deployment ${first}`
    case "database-data":
      return `Inside the data directory of ${databaseWords(fact)}`
    case "pm2":
      return `Inside the folder PM2 app ${first} runs in`
    case "dashboard-data":
      return "Inside Just Dashboard's own data"
    case "dashboard-install":
      return "Inside Just Dashboard's install"
    default:
      return phrase(fact, now)
  }
}

/** The sources an answer left out, as the end of a sentence. */
const SOURCE_WORDS: Record<string, string> = {
  docker: "Docker",
  proxy: "the proxy",
  backups: "the backups",
  git: "Git",
  deploy: "the deployments",
  databases: "the databases",
  processes: "the services",
  dashboard: "the dashboard",
}

export function partialSentence(partial: string[] | undefined): string | undefined {
  if (!partial?.length) return undefined
  return `Still reading ${listed(
    partial.map((s) => SOURCE_WORDS[s] ?? s),
    3,
  )}`
}
