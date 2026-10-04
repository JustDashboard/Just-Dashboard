import type {
  DeploymentRelease,
  DeploymentRuntimeService,
  DockerEvent,
  WorkloadProfile,
} from "@/lib/types"
import {
  REQUEST_RANGES,
  STATUS_CLASSES,
  resolveRequestRange,
  type RequestRange,
  type StatusClass,
} from "@/lib/requests"
import { isDockerService } from "@/components/deploy/runtime-service"

/**
 * The deployment Logs page's decisions, kept apart from its drawing so they
 * can be tested in a millisecond: which question the request record is asked
 * and how an address writes it down, which view a deployment opens on, which
 * container was answering at a moment, and how a crash loop folds into the
 * one row it is.
 */

export type LogsView = "requests" | "insights" | "output" | "builds" | "events"

export const LOGS_VIEWS: readonly LogsView[] = [
  "requests",
  "insights",
  "output",
  "builds",
  "events",
]

/**
 * What a deployment opens on when the address names no view. Requests answers
 * "is it working" for anything with a public route; a game server, a worker
 * and a deployment nobody routes to have no request record, and what they
 * wrote is the only reading they have.
 */
export function defaultView(profile: WorkloadProfile | undefined, routed: boolean | undefined) {
  if (profile === "game" || profile === "worker" || routed === false) return "output"
  return "requests"
}

export type RequestQuery = {
  range: RequestRange
  since?: string
  until?: string
  path: string
  client: string
  /** One of the deployment's hostnames, from the Domains list. */
  host: string
  /** An agent family as the Agents list names it — "Chrome", "Googlebot". */
  agent: string
  /** A referring site as Came from names it — "www.google.com". */
  referer: string
  /** Exact codes, from the Status codes list — "every 404", not "every 4xx". */
  statuses: number[]
  /** At least this slow, from a mark on the response-time ladder. */
  minMs?: number
  /** At most this slow: with `minMs`, one band of the ladder. */
  maxMs?: number
  classes: StatusClass[]
  methods: string[]
  /** Page views only: no prefetches, scripts, icons, probes. */
  pages: boolean
}

export const EMPTY_REQUEST_QUERY: RequestQuery = {
  range: "1h",
  path: "",
  client: "",
  host: "",
  agent: "",
  referer: "",
  statuses: [],
  classes: [],
  methods: [],
  pages: false,
}

/**
 * A query kept before a narrowing existed has no slot for it, and one read
 * back from the tab must not read `undefined` as a narrowing in force.
 */
export function completeQuery(query: Partial<RequestQuery>): RequestQuery {
  return { ...EMPTY_REQUEST_QUERY, ...query }
}

export function isNarrowed(query: RequestQuery) {
  return (
    query.path !== "" ||
    query.client !== "" ||
    query.host !== "" ||
    query.agent !== "" ||
    query.referer !== "" ||
    query.statuses.length > 0 ||
    query.minMs !== undefined ||
    query.maxMs !== undefined ||
    query.classes.length > 0 ||
    query.methods.length > 0 ||
    query.pages
  )
}

/**
 * The API's question. A preset is resolved against `now` each time it is
 * asked — by the poll, by the export — rather than when the query last
 * changed: the page used to pin it there, so half an hour on "Last hour" was
 * a window ninety minutes long that still called itself an hour.
 */
export function requestParams(query: RequestQuery, now: number) {
  const custom = query.range === "custom"
  return {
    since: custom ? query.since : resolveRequestRange(query.range, now),
    until: custom ? query.until : undefined,
    ...requestNarrowing(query),
  }
}

/** The question without its window: what a live tail asks of what arrives. */
export function requestNarrowing(query: RequestQuery) {
  return {
    path: query.path || undefined,
    client: query.client || undefined,
    host: query.host || undefined,
    agent: query.agent || undefined,
    referer: query.referer || undefined,
    status: query.statuses.length ? query.statuses.join(",") : undefined,
    minMs: query.minMs,
    maxMs: query.maxMs,
    classes: query.classes.length ? query.classes.join(",") : undefined,
    methods: query.methods.length ? query.methods.join(",") : undefined,
    pages: query.pages ? "true" : undefined,
    limit: 500,
  }
}

/** The words of the page's address the query owns: the API's own, so a link reads like the question. */
export const QUERY_PARAMS = [
  "range",
  "since",
  "until",
  "path",
  "client",
  "host",
  "agent",
  "referer",
  "status",
  "minMs",
  "maxMs",
  "classes",
  "methods",
  "pages",
] as const

/**
 * The query as the address writes it: only what says something, so the page
 * at rest is its bare address and a link carries exactly its narrowing.
 */
export function queryParams(query: RequestQuery): [string, string][] {
  const out: [string, string][] = []
  const custom = query.range === "custom" && Boolean(query.since)
  if (custom) {
    out.push(["range", "custom"], ["since", query.since!])
    if (query.until) out.push(["until", query.until])
  } else if (query.range !== EMPTY_REQUEST_QUERY.range && query.range !== "custom") {
    out.push(["range", query.range])
  }
  for (const key of ["path", "client", "host", "agent", "referer"] as const) {
    if (query[key]) out.push([key, query[key]])
  }
  if (query.statuses.length) out.push(["status", query.statuses.join(",")])
  if (query.minMs !== undefined) out.push(["minMs", String(query.minMs)])
  if (query.maxMs !== undefined) out.push(["maxMs", String(query.maxMs)])
  if (query.classes.length) out.push(["classes", query.classes.join(",")])
  if (query.methods.length) out.push(["methods", query.methods.join(",")])
  if (query.pages) out.push(["pages", "true"])
  return out
}

const PRESETS = new Set<string>(REQUEST_RANGES.map((range) => range.id))

/**
 * The query an address carries, or nothing when it names none — so the one
 * this tab remembers stands. Anything the server would refuse, or read as a
 * different question, is dropped here rather than sent.
 */
export function queryFromParams(params: URLSearchParams): RequestQuery | undefined {
  if (!QUERY_PARAMS.some((key) => params.has(key))) return undefined
  const query = completeQuery({})
  const range = params.get("range") ?? ""
  const since = instant(params.get("since"))
  if (range === "custom" && since) {
    query.range = "custom"
    query.since = since
    query.until = instant(params.get("until"))
  } else if (PRESETS.has(range)) {
    query.range = range as RequestRange
  }
  for (const key of ["path", "client", "host", "agent", "referer"] as const) {
    query[key] = (params.get(key) ?? "").trim()
  }
  query.statuses = list(params.get("status"))
    .map(Number)
    .filter((code) => Number.isInteger(code) && code >= 100 && code <= 599)
  query.minMs = millis(params.get("minMs"))
  query.maxMs = millis(params.get("maxMs"))
  query.classes = list(params.get("classes")).filter((klass): klass is StatusClass =>
    (STATUS_CLASSES as readonly string[]).includes(klass),
  )
  query.methods = list(params.get("methods"))
    .map((method) => method.toUpperCase())
    .filter((method) => /^[A-Z]{1,16}$/.test(method))
  query.pages = params.get("pages") === "true"
  return query
}

/** Half the window a moment opens on: an hour with the moment in the middle. */
const AROUND_MS = 30 * 60_000

/**
 * The requests around a moment a link names — an alert firing — as a window
 * of their own. The end stops at now: a window reaching past it draws a chart
 * whose right half is minutes that have not happened.
 */
export function queryAround(moment: string, now: number): RequestQuery | undefined {
  const at = Date.parse(moment)
  if (!Number.isFinite(at)) return undefined
  const until = Math.max(Math.min(at + AROUND_MS, now), at + 60_000)
  return completeQuery({
    range: "custom",
    since: new Date(at - AROUND_MS).toISOString(),
    until: new Date(until).toISOString(),
  })
}

function list(raw: string | null) {
  return (raw ?? "")
    .split(",")
    .map((part) => part.trim())
    .filter(Boolean)
}

function instant(raw: string | null) {
  return raw && Number.isFinite(Date.parse(raw)) ? raw : undefined
}

function millis(raw: string | null) {
  if (raw === null || raw.trim() === "") return undefined
  const value = Number(raw)
  return Number.isFinite(value) && value >= 0 ? value : undefined
}

/** Which of a release's containers is the application: what leads a list of them. */
export type Lead = {
  /** The Compose service readiness follows, where the settings name one. */
  primary?: string
  /** The project's own: drawn as the project rather than as a product of its own — not its database. */
  own?: (service: DeploymentRuntimeService) => boolean
}

/**
 * The containers the Output view offers, in the order a reader wants them:
 * the live release's before any other, and within it the service readiness
 * follows, else the project's own, before its neighbours — the application
 * before its database, which a list by name put first.
 */
export function orderedServices(services: DeploymentRuntimeService[], lead: Lead = {}) {
  const rank = (service: DeploymentRuntimeService) => [
    service.liveRelease ? 0 : 1,
    lead.primary && service.service === lead.primary ? 0 : 1,
    lead.own?.(service) ? 0 : 1,
  ]
  return [...services].sort((a, b) => {
    const [x, y] = [rank(a), rank(b)]
    return x[0] - y[0] || x[1] - y[1] || x[2] - y[2] || a.name.localeCompare(b.name)
  })
}

/**
 * The compose project the live release runs as, when it runs more than one
 * container — the "All services" a picker offers. One container is already
 * all of them.
 */
export function liveStack(services: DeploymentRuntimeService[]) {
  const live = services.filter(
    (service) => isDockerService(service) && service.liveRelease && service.stack,
  )
  const stack = live[0]?.stack
  return stack && live.filter((service) => service.stack === stack).length > 1 ? stack : undefined
}

/**
 * Which container was answering at an instant, as far as the page can say.
 *
 * One of the release that had most recently gone live by then — or, for a
 * request older than every release the page knows of, the live release's.
 * When that release is known and its containers are not, it is named
 * instead: what they wrote went with them, and the live container's lines
 * from before it existed would be an answer to a different question.
 *
 * It is a reading, not a record: the ingress does not say which container
 * answered, so a request in the seconds of a swap may have been the other
 * one's.
 */
export type Answering = { container: DeploymentRuntimeService } | { gone: number }

export function containerAt(
  services: DeploymentRuntimeService[],
  releases: Pick<DeploymentRelease, "id" | "number" | "activatedAt">[],
  at: number,
  lead: Lead = {},
): Answering | undefined {
  let release: { id: number; number: number; activated: number } | undefined
  for (const candidate of releases) {
    const activated = candidate.activatedAt ? Date.parse(candidate.activatedAt) : NaN
    if (!Number.isFinite(activated) || activated > at) continue
    if (!release || activated > release.activated) {
      release = { id: candidate.id, number: candidate.number, activated }
    }
  }
  if (release) {
    const of = services.filter((service) => service.releaseId === release.id)
    return of.length > 0 ? { container: orderedServices(of, lead)[0] } : { gone: release.number }
  }
  const live = services.filter((service) => service.liveRelease)
  const pool = live.length > 0 ? live : services
  return pool.length > 0 ? { container: orderedServices(pool, lead)[0] } : undefined
}

/** One row of the Events feed: an event, or a crash loop folded into one. */
export type FeedItem =
  | { kind: "event"; key: string; time: string; event: DockerEvent }
  | {
      kind: "loop"
      key: string
      time: string
      /** The loop's events, newest first. */
      events: DockerEvent[]
      restarts: number
      /** The code it kept exiting with, when that was a failure's. */
      exitCode?: string
      /** Every exit was status 0: a job its restart policy runs again, not a crash. */
      clean: boolean
      /** The OOM reaper ended it: the exit code alone says only "killed". */
      oom: boolean
      /** From the oldest exit to the newest start. */
      spanMs: number
      /** The newest exit, whose last lines say why. */
      exit: DockerEvent
    }

function sameObject(a: DockerEvent, b: DockerEvent) {
  return (a.id ?? a.name) === (b.id ?? b.name)
}

/**
 * What Docker sends beside an exit: the OOM reaper's note and a kill. They
 * share the exit's second, so newest first they sit on either side of it.
 */
const BESIDE_EXIT = new Set(["oom", "kill"])

/**
 * A crash loop as the one row it is. A container that exits and is started
 * again by its restart policy, twenty times with the same code, is twenty
 * pairs of rows saying one thing — and the one row that is different, the
 * exit it finally stayed down after, is somewhere under them. Consecutive
 * pairs of an exit and the start after it, on one container with one exit
 * code, fold into "restarted ×N"; one pair is a restart and stays two rows.
 * An OOM kill is its exit and the reaper's note together, so a loop of them
 * folds the same way.
 *
 * The events arrive newest first, so a pair reads start-then-exit.
 */
export function foldRestarts(events: DockerEvent[], keyOf: (event: DockerEvent) => string) {
  const out: FeedItem[] = []
  let i = 0
  while (i < events.length) {
    const head = events[i]
    let j = i
    let pairs = 0
    let exit: DockerEvent | undefined
    let oom = false
    while (j < events.length) {
      const start = events[j]
      if (start.type !== "container" || start.action !== "start" || !sameObject(start, head)) {
        break
      }
      // The exit this start answered, with what Docker sent beside it.
      let k = j + 1
      let died: DockerEvent | undefined
      let reaped = false
      while (k < events.length) {
        const next = events[k]
        const beside =
          next.type === "container" &&
          sameObject(next, head) &&
          (next.action === "die" ? !died : BESIDE_EXIT.has(next.action))
        if (!beside) break
        if (next.action === "die") died = next
        if (next.action === "oom") reaped = true
        k += 1
      }
      if (!died || (exit && (died.exitCode ?? "") !== (exit.exitCode ?? ""))) break
      exit ??= died
      oom ||= reaped
      pairs += 1
      j = k
    }
    if (pairs >= 2 && exit) {
      const loop = events.slice(i, j)
      const code = exit.exitCode
      out.push({
        kind: "loop",
        key: `loop:${keyOf(loop[0])}`,
        time: loop[0].time,
        events: loop,
        restarts: pairs,
        exitCode: code && code !== "0" ? code : undefined,
        clean: code === "0",
        oom,
        spanMs: Math.max(0, Date.parse(loop[0].time) - Date.parse(loop[loop.length - 1].time)),
        exit,
      })
      i = j
    } else {
      out.push({ kind: "event", key: keyOf(head), time: head.time, event: head })
      i += 1
    }
  }
  return out
}

/** "in 12 min", "in under a minute": how long a loop has been going, in words. */
export function loopSpan(ms: number) {
  if (ms < 60_000) return "in under a minute"
  return `in ${Math.round(ms / 60_000)} min`
}
