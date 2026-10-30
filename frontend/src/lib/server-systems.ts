import type { Point } from "@/lib/server-map"
import type { MapItem, MapLink, MapTone, ServerMap } from "@/lib/types"

/**
 * The server map as systems: one diagram for each thing the server runs on
 * purpose — a deployment, a Compose stack, this dashboard, a PM2 app — holding
 * what it is made of and what it is wired to, drawn left to right the way a
 * request travels and the data settles: the names it answers to, the thing
 * itself, the containers it runs, the databases and volumes they keep, the
 * backups that take those.
 *
 * Nothing here guesses a line. A system grows from its anchor along the links
 * the server sent (`CLAIMS`): a deployment takes its release containers, the
 * databases it is bound to or names, the domains routed to it and the checkout
 * it is built from; a database takes the container that serves it and the
 * backups that dump it; a container takes the volumes it mounts. What two
 * systems both reach — one Postgres three deployments use — is drawn in each,
 * marked as shared, so every diagram is whole on its own. What the reader came
 * to see is the server's own; images, networks, accounts, keys, shells and
 * drawings are not on it.
 *
 * Pure and unit-tested (`server-systems.test.js`): the canvas hands this the
 * reading and draws what comes back.
 */

/** The kinds a system is made of. Everything else stays on its own page. */
export const SYSTEM_KINDS: ReadonlySet<string> = new Set([
  "dashboard",
  "projects",
  "stacks",
  "containers",
  "databases",
  "volumes",
  "domains",
  "certificates",
  "streams",
  "repositories",
  "backups",
  "services",
  "pm2",
  "tunnels",
])

export type SystemKind =
  "dashboard" | "project" | "stack" | "pm2" | "database" | "service" | "container" | "loose"

/** What a system is called by what anchors it, in the rail and on its frame. */
export const SYSTEM_LABEL: Record<SystemKind, string> = {
  dashboard: "This dashboard",
  project: "Deployment",
  stack: "Compose stack",
  pm2: "PM2 app",
  database: "Database",
  service: "Service",
  container: "Container",
  loose: "Not wired to anything",
}

/**
 * How a node is drawn. Each is its own shape on the canvas, so a reader knows
 * a domain from a database before reading either name:
 *
 * - `hero` — the deployment or the dashboard a system is: the big card.
 * - `stack` — a Compose project's own card, smaller, with its members' count.
 * - `entry` — where traffic comes in: a domain, a stream, a tailnet, the
 *   dashboard's own address. Rounded on the left, like a workflow's trigger.
 * - `runtime` — a container, drawn by what it is (`Role`).
 * - `process` — a systemd service or a PM2 app.
 * - `datastore` — a database, a cylinder.
 * - `volume` — a disk.
 * - `backup` — a job that takes a copy.
 * - `satellite` — what hangs under a node rather than in the flow: the
 *   checkout a deployment is built from, its logs, a domain's certificate.
 */
export type Face =
  | "hero"
  | "stack"
  | "entry"
  | "runtime"
  | "process"
  | "datastore"
  | "volume"
  | "backup"
  | "satellite"

/** What a container is, read from its image's product and its name. */
export type Role =
  | "web"
  | "api"
  | "worker"
  | "database"
  | "cache"
  | "queue"
  | "proxy"
  | "search"
  | "storage"
  | "monitoring"
  | "app"

export const ROLE_LABEL: Record<Role, string> = {
  web: "Web",
  api: "API",
  worker: "Worker",
  database: "Database",
  cache: "Cache",
  queue: "Queue",
  proxy: "Proxy",
  search: "Search",
  storage: "Storage",
  monitoring: "Monitoring",
  app: "App",
}

const PRODUCT_ROLE: Record<string, Role> = {}
const roles: [Role, string[]][] = [
  [
    "database",
    [
      "postgres",
      "postgresql",
      "postgis",
      "pgvector",
      "timescaledb",
      "mysql",
      "mariadb",
      "mongodb",
      "clickhouse",
      "cockroachdb",
      "couchdb",
      "cassandra",
      "scylladb",
      "neo4j",
      "influxdb",
      "sqlserver",
      "azure-sql-edge",
      "oracle",
      "tidb",
      "yugabytedb",
      "ferretdb",
      "etcd",
      "sqlite",
    ],
  ],
  ["cache", ["redis", "valkey", "memcached"]],
  ["queue", ["rabbitmq", "nats", "kafka"]],
  ["proxy", ["caddy", "traefik", "haproxy"]],
  ["search", ["meilisearch", "typesense", "elasticsearch", "opensearch", "qdrant"]],
  ["storage", ["minio"]],
  [
    "monitoring",
    ["grafana", "prometheus", "uptime-kuma", "beszel", "dozzle", "healthchecks", "sentry"],
  ],
  [
    "web",
    [
      "nextjs",
      "nuxt",
      "react",
      "vuejs",
      "angular",
      "svelte",
      "astro",
      "remix",
      "gatsby",
      "vite",
      "vitepress",
      "docusaurus",
      "eleventy",
      "solid",
      "ember",
      "nginx-static",
      "ghost",
      "wordpress",
    ],
  ],
  [
    "api",
    [
      "fastapi",
      "flask",
      "django",
      "express",
      "fastify",
      "nestjs",
      "hono",
      "koa",
      "spring-boot",
      "laravel",
      "symfony",
      "quarkus",
    ],
  ],
]
for (const [role, products] of roles) for (const p of products) PRODUCT_ROLE[p] = role

/**
 * The products that say what language a thing is written in and nothing about
 * what it does — a Node image is a frontend in one stack and a queue worker in
 * the next — so its name is read for that instead.
 */
const GENERIC = new Set([
  "docker",
  "nodejs",
  "python",
  "go",
  "rust",
  "ruby",
  "php",
  "java",
  "dotnet",
  "deno",
  "bun",
  "elixir",
  "alpine",
  "debian",
  "ubuntu",
  "nginx",
  "apache",
  "pm2",
])

const NAME_ROLE: [Role, RegExp][] = [
  ["proxy", /^(proxy|gateway|ingress|lb|edge|caddy|traefik|nginx|pgbouncer|bouncer|pooler)$/],
  ["web", /^(web|frontend|front|ui|www|site|client|app)$/],
  ["api", /^(api|backend|server|graphql|rest|grpc)$/],
  ["worker", /^(worker|workers|jobs|job|queue|cron|scheduler|consumer|celery|sidekiq)$/],
  ["database", /^(db|database|postgres|pg|mysql|mariadb|mongo)$/],
  ["cache", /^(cache|redis|valkey|memcached)$/],
]

/**
 * What a container is for: what its image is when the image says (Postgres is
 * a database wherever it runs), and otherwise what its name says of it
 * (`shop-worker-1`, `just-dashboard-backend-1`). A role decides only how the
 * box is drawn; no line is drawn from it.
 */
export function containerRole(item: Pick<MapItem, "name" | "product">): Role {
  const product = item.product ?? ""
  const named = PRODUCT_ROLE[product]
  if (named && !GENERIC.has(product)) return named
  const words = item.name.toLowerCase().split(/[^a-z]+/)
  for (const [role, pattern] of NAME_ROLE) if (words.some((w) => pattern.test(w))) return role
  if (product === "nginx" || product === "apache") return "web"
  return named ?? "app"
}

// ---------------------------------------------------------------------------
// Who belongs to whom.
// ---------------------------------------------------------------------------

/**
 * What a thing of each kind takes into its system, and along which links. A
 * line the server knows several ways counts by any of them (`also`). It runs
 * one way, from what a system is towards what it keeps: a container takes the
 * volume it mounts, never the stack it is in, so a database container in
 * somebody else's stack does not pull that stack into a deployment's diagram.
 */
const CLAIMS: Record<string, [kinds: string[], vias: string[]][]> = {
  dashboard: [
    [["stacks"], ["self"]],
    [["domains", "streams"], ["route"]],
    [["dashboard"], ["route", "mount"]],
    [["tunnels"], ["tailnet"]],
    [["containers"], ["route"]],
  ],
  projects: [
    [["containers", "stacks"], ["release"]],
    [["databases"], ["binding", "reference", "dependency", "env", "session"]],
    [["domains"], ["route", "reference", "dependency"]],
    [["repositories"], ["source"]],
    [["volumes", "backups"], ["dependency"]],
  ],
  stacks: [[["containers", "volumes"], ["member"]]],
  containers: [
    [["volumes"], ["mount"]],
    [["databases"], ["serves", "pooler", "env", "session"]],
    [["domains", "streams"], ["route"]],
    [["backups"], ["backup"]],
    [["dashboard"], ["mount"]],
  ],
  databases: [
    [["backups"], ["backup"]],
    [
      ["containers", "services"],
      ["serves", "pooler"],
    ],
  ],
  domains: [[["certificates"], ["certificate"]]],
  volumes: [[["backups"], ["backup"]]],
  services: [
    [["databases"], ["serves"]],
    [["domains", "streams"], ["route"]],
  ],
  pm2: [
    [["repositories"], ["workdir"]],
    [["domains", "streams"], ["route"]],
  ],
  repositories: [[["backups"], ["backup"]]],
  tunnels: [],
  streams: [],
  certificates: [],
  backups: [],
}

type Entry = { item: MapItem; kind: string }
type Adjacent = { other: string; link: MapLink }

type Graph = {
  items: Map<string, Entry>
  adjacent: Map<string, Adjacent[]>
  links: MapLink[]
}

const vias = (link: MapLink) => [link.via, ...(link.also ?? [])]

function graphOf(map: ServerMap): Graph {
  const items = new Map<string, Entry>()
  for (const kind of map.kinds) {
    if (!SYSTEM_KINDS.has(kind.key) || kind.state === "absent") continue
    for (const item of kind.items) items.set(item.id, { item, kind: kind.key })
  }
  const adjacent = new Map<string, Adjacent[]>()
  const links: MapLink[] = []
  for (const link of map.links) {
    if (!items.has(link.from) || !items.has(link.to) || link.from === link.to) continue
    links.push(link)
    for (const [a, b] of [
      [link.from, link.to],
      [link.to, link.from],
    ]) {
      const list = adjacent.get(a) ?? []
      list.push({ other: b, link })
      adjacent.set(a, list)
    }
  }
  return { items, adjacent, links }
}

/** Everything an anchor's system takes, the anchor first, in the order it was reached. */
function claim(graph: Graph, anchor: string, stop: ReadonlySet<string>): string[] {
  const seen = new Set([anchor])
  const order = [anchor]
  for (let i = 0; i < order.length; i++) {
    const id = order[i]
    const entry = graph.items.get(id)
    if (!entry) continue
    const rules = CLAIMS[entry.kind] ?? []
    // Stable: a system's members come out in the order the server listed
    // its links, so the same reading lays out the same diagram.
    for (const { other, link } of graph.adjacent.get(id) ?? []) {
      if (seen.has(other) || stop.has(other)) continue
      const kind = graph.items.get(other)?.kind ?? ""
      const known = vias(link)
      if (
        rules.some(([kinds, ways]) => kinds.includes(kind) && known.some((v) => ways.includes(v)))
      ) {
        seen.add(other)
        order.push(other)
      }
    }
  }
  return order
}

const TONE_RANK: Record<MapTone, number> = { danger: 0, warning: 1, ok: 2, unknown: 3, idle: 4 }

function byWorstThenName(a: Entry, b: Entry) {
  return TONE_RANK[a.item.tone] - TONE_RANK[b.item.tone] || a.item.name.localeCompare(b.item.name)
}

type Draft = { id: string; kind: SystemKind; anchor: Entry; members: string[] }

/**
 * Which systems the server has, each with what it holds, in the order the
 * rail lists them: this dashboard, the deployments, the stacks, the PM2 apps,
 * the containers on their own, the databases nothing above claimed, the
 * services wired to anything, and last what is wired to nothing at all.
 */
function draftSystems(graph: Graph): Draft[] {
  const drafts: Draft[] = []
  const claimed = new Set<string>()
  const anchors = new Set<string>()
  const ofKind = (kind: string) =>
    [...graph.items.values()].filter((e) => e.kind === kind).sort(byWorstThenName)

  const phase = (kind: SystemKind, candidates: Entry[]) => {
    const fresh = candidates.filter((e) => !claimed.has(e.item.id))
    for (const e of fresh) anchors.add(e.item.id)
    for (const e of fresh) {
      const others = new Set([...anchors].filter((id) => id !== e.item.id))
      const members = claim(graph, e.item.id, others)
      for (const id of members) claimed.add(id)
      drafts.push({ id: `system:${e.item.id}`, kind, anchor: e, members })
    }
  }

  phase(
    "dashboard",
    ofKind("dashboard").filter((e) => e.item.id === "dashboard:self"),
  )
  phase("project", ofKind("projects"))
  phase("stack", ofKind("stacks"))
  phase("pm2", ofKind("pm2"))
  // A container on its own is its own system, and takes the database it
  // serves or names; a database nothing claimed after that takes the
  // service it runs under, so a native Postgres is named by its database.
  phase("container", ofKind("containers"))
  phase("database", ofKind("databases"))
  // A service is on the map only when something is wired to it: a thousand
  // systemd units are the services page's to list.
  phase(
    "service",
    ofKind("services").filter((e) => (graph.adjacent.get(e.item.id) ?? []).length > 0),
  )

  // What nothing claimed: one diagram for all of it, each thing taking what
  // hangs off it (a domain its certificate, a volume its backup).
  const LOOSE = [
    "domains",
    "streams",
    "tunnels",
    "repositories",
    "volumes",
    "backups",
    "certificates",
  ]
  const rest: string[] = []
  const restSeen = new Set<string>()
  for (const kind of LOOSE) {
    for (const e of ofKind(kind)) {
      if (claimed.has(e.item.id) || restSeen.has(e.item.id)) continue
      // What hangs off a loose thing is drawn under it even where another
      // system draws it too — a wildcard certificate, a shared backup.
      for (const id of claim(graph, e.item.id, anchors)) {
        if (restSeen.has(id)) continue
        restSeen.add(id)
        rest.push(id)
      }
    }
  }
  if (rest.length > 0) {
    const first = graph.items.get(rest[0])!
    drafts.push({ id: "system:loose", kind: "loose", anchor: first, members: rest })
  }
  return drafts
}

// ---------------------------------------------------------------------------
// The shapes, and where each one goes.
// ---------------------------------------------------------------------------

export type SystemNode = {
  /** Unique on the canvas: the system's id and the thing's (`system:project:1/db:3`). */
  id: string
  /** The thing's own id, `logs:<anchor>` for the logs a system opens on. */
  itemId: string
  kind: string
  item: MapItem
  face: Face
  role?: Role
  /** What the line into a satellite is labelled, n8n's sub-node port. */
  port?: string
  /** The node a satellite hangs under. */
  host?: string
  /** The Compose stack frame a container is drawn inside. */
  frame?: string
  /** The other systems that draw the same thing. */
  shared: string[]
  width: number
  height: number
  /** Relative to the system's own top left, inside its frame. */
  x: number
  y: number
}

export type SystemEdge = {
  id: string
  source: string
  target: string
  via: string
  also: string[]
  /** `flow` runs right to left between columns; `sub` drops to a satellite; `down` joins one column. */
  shape: "flow" | "sub" | "down"
}

/** A Compose stack drawn as the frame round its containers. */
export type StackFrame = {
  id: string
  itemId: string
  item: MapItem
  x: number
  y: number
  width: number
  height: number
}

export type System = {
  id: string
  kind: SystemKind
  /** What the system is named by: the deployment, the stack, the app. */
  anchor: MapItem
  anchorKind: string
  nodes: SystemNode[]
  edges: SystemEdge[]
  frames: StackFrame[]
  /** The content's size; the frame adds `FRAME.head` and `FRAME.pad` round it. */
  width: number
  height: number
  /** Every name and state in it, lowercased, for the rail's search. */
  haystack: string
}

/** The sizes the shapes are drawn at, by face and, for a container, by role. */
export const FACE_SIZE: Record<Face, { width: number; height: number }> = {
  hero: { width: 288, height: 164 },
  stack: { width: 256, height: 132 },
  entry: { width: 232, height: 64 },
  runtime: { width: 216, height: 88 },
  process: { width: 216, height: 84 },
  datastore: { width: 228, height: 120 },
  volume: { width: 192, height: 56 },
  backup: { width: 220, height: 88 },
  satellite: { width: 112, height: 96 },
}

const ROLE_SIZE: Partial<Record<Role, { width: number; height: number }>> = {
  web: { width: 224, height: 104 },
  api: { width: 216, height: 92 },
  database: { width: 212, height: 100 },
  cache: { width: 196, height: 72 },
  queue: { width: 196, height: 72 },
  worker: { width: 204, height: 80 },
  proxy: { width: 212, height: 76 },
}

/** The frame round a system and round a stack, and the gaps between what is in them. */
export const FRAME = { head: 64, pad: 32, gap: 96 }
const STACK = { head: 40, pad: 14, gap: 12 }
const COLUMN_GAP = 96
const ROW_GAP = 24
const SAT_GAP = 44
const SAT_SPACE = 12

/**
 * Left to right: how the system is reached, what it is, what it runs, what
 * that keeps, what takes a copy of it. A tailnet stands before the address it
 * carries.
 */
function columnOf(kind: string, itemId: string): number {
  if (itemId === "dashboard:site") return 1
  if (itemId === "dashboard:store") return 4
  switch (kind) {
    case "tunnels":
      return 0
    case "domains":
    case "streams":
    case "certificates":
      return 1
    case "projects":
    case "dashboard":
    case "stacks":
    case "repositories":
      return 2
    case "containers":
    case "services":
    case "pm2":
      return 3
    case "databases":
    case "volumes":
      return 4
    case "backups":
      return 5
    default:
      return 3
  }
}

function faceOf(kind: string, item: MapItem): { face: Face; role?: Role } {
  if (item.id === "dashboard:site") return { face: "entry" }
  if (item.id === "dashboard:store") return { face: "datastore" }
  switch (kind) {
    case "projects":
    case "dashboard":
      return { face: "hero" }
    case "stacks":
      return { face: "stack" }
    case "domains":
    case "streams":
    case "tunnels":
      return { face: "entry" }
    case "containers":
      return { face: "runtime", role: containerRole(item) }
    case "services":
    case "pm2":
      return { face: "process", role: containerRole(item) }
    case "databases":
      return { face: "datastore" }
    case "volumes":
      return { face: "volume" }
    case "backups":
      return { face: "backup" }
    default:
      return { face: "satellite" }
  }
}

function sizeOf(face: Face, role?: Role) {
  return (face === "runtime" && role && ROLE_SIZE[role]) || FACE_SIZE[face]
}

/** Where a thing's logs are read, for the systems that open on them. */
function logsHref(kind: string, item: MapItem): string | undefined {
  switch (kind) {
    case "projects":
      return `${item.href}/logs`
    case "dashboard":
      return "/logs"
    case "containers":
      return `${item.href}?tab=logs`
    case "pm2":
      return item.href
    default:
      return undefined
  }
}

type Built = Omit<System, "haystack">

/** One system's nodes, frames and lines, laid out. */
function buildSystem(graph: Graph, draft: Draft, sharedBy: Map<string, string[]>): Built {
  const sid = draft.id
  const nodeId = (itemId: string) => `${sid}/${itemId}`
  const members = new Set(draft.members)
  const anchorId = draft.anchor.item.id
  const nodes = new Map<string, SystemNode>()

  // A stack is a frame round its containers wherever a deployment or this
  // dashboard runs it; a stack that is a system of its own is its card.
  const framed = new Map<string, string>()
  for (const id of draft.members) {
    const e = graph.items.get(id)
    if (e?.kind !== "stacks" || id === anchorId) continue
    for (const { other, link } of graph.adjacent.get(id) ?? []) {
      if (link.from === id && members.has(other) && vias(link).includes("member")) {
        if (graph.items.get(other)?.kind === "containers") framed.set(other, id)
      }
    }
  }

  const add = (itemId: string, extra: Partial<SystemNode> = {}) => {
    const e = graph.items.get(itemId)
    if (!e) return
    const { face, role } = faceOf(e.kind, e.item)
    const size = sizeOf(extra.face ?? face, role)
    const id = extra.id ?? nodeId(itemId)
    nodes.set(id, {
      id,
      itemId,
      kind: e.kind,
      item: e.item,
      face,
      role,
      shared: (sharedBy.get(itemId) ?? []).filter((s) => s !== sid),
      width: size.width,
      height: size.height,
      x: 0,
      y: 0,
      ...extra,
    })
  }

  const satellites: SystemNode[] = []
  const satellite = (host: SystemNode, itemId: string, port: string, item?: MapItem) => {
    const e = graph.items.get(itemId)
    const node: SystemNode = {
      id: `${host.id}~${itemId}`,
      itemId,
      kind: e?.kind ?? "logs",
      item: item ?? e!.item,
      face: "satellite",
      port,
      host: host.id,
      shared: (sharedBy.get(itemId) ?? []).filter((s) => s !== sid),
      ...FACE_SIZE.satellite,
      x: 0,
      y: 0,
    }
    satellites.push(node)
    return node
  }

  // What hangs under rather than in the flow: a checkout under what is built
  // from it or runs from it, a certificate under each name it covers.
  const satelliteOf = new Map<string, { host: string; port: string }[]>()
  for (const link of graph.links) {
    if (!members.has(link.from) || !members.has(link.to)) continue
    const ways = vias(link)
    const fromKind = graph.items.get(link.from)?.kind
    const toKind = graph.items.get(link.to)?.kind
    const hang = (host: string, sat: string, port: string) => {
      const list = satelliteOf.get(sat) ?? []
      if (!list.some((h) => h.host === host)) list.push({ host, port })
      satelliteOf.set(sat, list)
    }
    if (toKind === "repositories" && (ways.includes("source") || ways.includes("workdir"))) {
      hang(link.from, link.to, "Source")
    } else if (fromKind === "domains" && toKind === "certificates") {
      hang(link.from, link.to, "TLS")
    } else if (fromKind === "certificates" && toKind === "domains") {
      hang(link.to, link.from, "TLS")
    }
  }

  for (const id of draft.members) {
    if (satelliteOf.has(id)) continue
    const e = graph.items.get(id)
    if (!e) continue
    if (e.kind === "stacks" && id !== anchorId) continue
    add(id, framed.has(id) ? { frame: framed.get(id) } : {})
  }
  for (const [sat, hosts] of satelliteOf) {
    for (const { host, port } of hosts) {
      const node = nodes.get(nodeId(host))
      if (node) satellite(node, sat, port)
      else add(sat)
    }
  }
  const anchorNode = nodes.get(nodeId(anchorId))
  if (anchorNode && draft.kind !== "loose") {
    const href = logsHref(draft.anchor.kind, draft.anchor.item)
    if (href) {
      satellite(anchorNode, `logs:${anchorId}`, "Logs", {
        id: `logs:${anchorId}`,
        name: "Logs",
        tone: "idle",
        state: "read the output",
        href,
        product: "terminal",
      })
    }
  }

  // The lines: every link between two things drawn here, but a container's
  // own stack holds it rather than a line to it, and a release that runs as
  // a stack is one line to the stack's frame rather than one to each member.
  const edges: SystemEdge[] = []
  const frameIds = new Set(framed.values())
  const toFrame = new Set<string>()
  for (const link of graph.links) {
    if (frameIds.has(link.to) && members.has(link.from)) toFrame.add(`${link.from}>${link.to}`)
  }
  // A volume a stack declares and one of its containers mounts is joined by
  // the mount; the stack's own line to it would say the same thing twice.
  const mounted = new Set<string>()
  for (const link of graph.links) {
    if (members.has(link.from) && members.has(link.to) && vias(link).includes("mount")) {
      mounted.add(link.to)
    }
  }
  const satNode = new Map<string, SystemNode>()
  for (const s of satellites) if (!satNode.has(s.itemId)) satNode.set(s.itemId, s)
  const hangs = (a: string, b: string) =>
    (satelliteOf.get(b) ?? []).some((h) => h.host === a) ||
    (satelliteOf.get(a) ?? []).some((h) => h.host === b)
  const drawn = new Set<string>()
  const seat = (itemId: string) =>
    nodes.get(nodeId(itemId)) ??
    satNode.get(itemId) ??
    (frameIds.has(itemId) ? ({ id: `${sid}/frame:${itemId}` } as SystemNode) : undefined)
  for (const link of graph.links) {
    if (!members.has(link.from) || !members.has(link.to)) continue
    if (hangs(link.from, link.to)) continue
    const ways = vias(link)
    const fromKind = graph.items.get(link.from)?.kind
    if (ways.includes("member") && framed.get(link.to) === link.from) continue
    if (ways.includes("member") && fromKind === "stacks" && mounted.has(link.to)) continue
    const frameOfTarget = framed.get(link.to)
    if (frameOfTarget && toFrame.has(`${link.from}>${frameOfTarget}`)) continue
    const a = seat(link.from)
    const b = seat(link.to)
    if (!a || !b) continue
    const key = [a.id, b.id].sort().join(">")
    if (drawn.has(key)) continue
    drawn.add(key)
    edges.push({
      id: `${a.id}>${b.id}`,
      source: a.id,
      target: b.id,
      via: link.via,
      also: link.also ?? [],
      shape: "flow",
    })
  }
  for (const sat of satellites) {
    edges.push({
      id: `${sat.host}>${sat.id}`,
      source: sat.host!,
      target: sat.id,
      via: sat.port === "Logs" ? "logs" : sat.port === "TLS" ? "certificate" : "source",
      also: [],
      shape: "sub",
    })
  }

  const laid = place(sid, [...nodes.values()], satellites, edges, framed, graph, anchorId)
  // A line runs from the left column to the right; one inside a column drops
  // from the upper box to the lower.
  const at = new Map(laid.nodes.map((n) => [n.id, n]))
  for (const f of laid.frames) at.set(`${sid}/frame:${f.itemId}`, f as unknown as SystemNode)
  for (const edge of edges) {
    if (edge.shape === "sub") continue
    const s = at.get(edge.source)
    const t = at.get(edge.target)
    if (!s || !t) continue
    if (Math.abs(s.x + s.width / 2 - (t.x + t.width / 2)) < 1) {
      edge.shape = "down"
      if (s.y > t.y) [edge.source, edge.target] = [edge.target, edge.source]
    } else if (s.x > t.x) {
      ;[edge.source, edge.target] = [edge.target, edge.source]
    }
  }
  return {
    id: sid,
    kind: draft.kind,
    anchor: draft.anchor.item,
    anchorKind: draft.anchor.kind,
    nodes: laid.nodes,
    edges: edges.filter((e) => at.has(e.source) && at.has(e.target)),
    frames: laid.frames,
    width: laid.width,
    height: laid.height,
  }
}

type Block = {
  key: string
  /** The node, or a stack's members top to bottom. */
  nodes: SystemNode[]
  frame?: string
  width: number
  height: number
  column: number
  /** Where in its column it would sit if nothing else were there. */
  ideal: number
  order: number
}

/**
 * Columns, then rows: each column's boxes ordered to cross as few lines as the
 * column before it allows (two sweeps of barycentres), then each box set level
 * with what it is wired to where there is room, pushed down where there is
 * not. A box with satellites takes the room they hang in; a stack's members
 * stay together inside their frame.
 */
function place(
  sid: string,
  main: SystemNode[],
  satellites: SystemNode[],
  edges: SystemEdge[],
  framed: Map<string, string>,
  graph: Graph,
  anchorId: string,
) {
  const satsOf = new Map<string, SystemNode[]>()
  for (const s of satellites) {
    const list = satsOf.get(s.host!) ?? []
    list.push(s)
    satsOf.set(s.host!, list)
  }
  const satRow = (n: SystemNode) => {
    const list = satsOf.get(n.id) ?? []
    return list.length === 0
      ? 0
      : list.length * FACE_SIZE.satellite.width + (list.length - 1) * SAT_SPACE
  }
  const ownHeight = (n: SystemNode) =>
    n.height + ((satsOf.get(n.id)?.length ?? 0) > 0 ? SAT_GAP + FACE_SIZE.satellite.height : 0)
  const ownWidth = (n: SystemNode) => Math.max(n.width, satRow(n))

  // The columns that hold something, packed so no gap is left by a kind the
  // system does not have.
  const used = [...new Set(main.map((n) => columnOf(n.kind, n.itemId)))].sort((a, b) => a - b)
  const col = (n: SystemNode) => used.indexOf(columnOf(n.kind, n.itemId))

  const blocks: Block[] = []
  const blockOf = new Map<string, Block>()
  const stacks = new Map<string, Block>()
  const sorted = [...main].sort(
    (a, b) =>
      (a.itemId === anchorId ? -1 : 0) - (b.itemId === anchorId ? -1 : 0) ||
      TONE_RANK[a.item.tone] - TONE_RANK[b.item.tone] ||
      a.item.name.localeCompare(b.item.name) ||
      a.id.localeCompare(b.id),
  )
  for (const n of sorted) {
    if (n.frame) {
      let block = stacks.get(n.frame)
      if (!block) {
        block = {
          key: `frame:${n.frame}`,
          nodes: [],
          frame: n.frame,
          width: 0,
          height: 0,
          column: col(n),
          ideal: 0,
          order: blocks.length,
        }
        stacks.set(n.frame, block)
        blocks.push(block)
      }
      block.nodes.push(n)
      blockOf.set(n.id, block)
      continue
    }
    const block: Block = {
      key: n.id,
      nodes: [n],
      width: ownWidth(n),
      height: ownHeight(n),
      column: col(n),
      ideal: 0,
      order: blocks.length,
    }
    blocks.push(block)
    blockOf.set(n.id, block)
  }
  for (const block of stacks.values()) {
    block.width = Math.max(...block.nodes.map(ownWidth)) + STACK.pad * 2
    block.height =
      STACK.head +
      STACK.pad * 2 +
      block.nodes.reduce((sum, n) => sum + ownHeight(n), 0) +
      STACK.gap * (block.nodes.length - 1)
  }
  for (const [frame, block] of stacks) blockOf.set(`${sid}/frame:${frame}`, block)

  // Which blocks each block is wired to.
  const near = new Map<Block, Block[]>()
  for (const edge of edges) {
    if (edge.shape === "sub") continue
    const a = blockOf.get(edge.source)
    const b = blockOf.get(edge.target)
    if (!a || !b || a === b) continue
    near.set(a, [...(near.get(a) ?? []), b])
    near.set(b, [...(near.get(b) ?? []), a])
  }

  const columns: Block[][] = used.map((_, i) => blocks.filter((b) => b.column === i))
  const rank = (b: Block) => columns[b.column].indexOf(b) / Math.max(1, columns[b.column].length)
  const sweep = (from: number, to: number, step: number) => {
    for (let c = from; c !== to; c += step) {
      const list = columns[c]
      const bary = new Map<Block, number>()
      for (const b of list) {
        const ref = (near.get(b) ?? []).filter((o) => o.column === c - step)
        bary.set(b, ref.length ? ref.reduce((s, o) => s + rank(o), 0) / ref.length : rank(b))
      }
      list.sort((a, b) => bary.get(a)! - bary.get(b)! || a.order - b.order)
    }
  }
  for (let pass = 0; pass < 2; pass++) {
    sweep(1, columns.length, 1)
    sweep(columns.length - 2, -1, -1)
  }

  // Rows: the anchor's column first, centred on the system's line, and each
  // column out from it set level with what it is wired to.
  const anchorBlock = blockOf.get(`${sid}/${anchorId}`)
  const start = anchorBlock ? anchorBlock.column : 0
  const top = new Map<Block, number>()
  const centre = (b: Block) => top.get(b)! + headCentre(b)
  const headCentre = (b: Block) => (b.frame ? b.height / 2 : b.nodes[0].height / 2)
  const pack = (list: Block[], placed: boolean) => {
    let bottom = -Infinity
    const want: number[] = []
    for (const b of list) {
      const ref = placed ? (near.get(b) ?? []).filter((o) => top.has(o)) : []
      const ideal = ref.length ? ref.reduce((s, o) => s + centre(o), 0) / ref.length : NaN
      want.push(ideal)
    }
    // A box with nothing placed to be level with follows the one above it.
    const ys: number[] = []
    for (let i = 0; i < list.length; i++) {
      const b = list[i]
      const ideal = Number.isNaN(want[i]) ? -Infinity : want[i] - headCentre(b)
      const y = Math.max(ideal, bottom === -Infinity ? ideal : bottom + ROW_GAP)
      ys.push(y === -Infinity ? (bottom === -Infinity ? 0 : bottom + ROW_GAP) : y)
      bottom = ys[i] + b.height
    }
    // Pushed down where the boxes above took the room: the column is lifted
    // by the average push, so a long column is centred on what it follows.
    const pushed = list
      .map((b, i) => (Number.isNaN(want[i]) ? null : ys[i] + headCentre(b) - want[i]))
      .filter((d): d is number => d !== null)
    let lift = pushed.length ? pushed.reduce((s, d) => s + d, 0) / pushed.length : 0
    if (!placed || pushed.length === 0) {
      const height = bottom - ys[0]
      lift = ys[0] + height / 2
    }
    list.forEach((b, i) => top.set(b, ys[i] - lift))
  }
  pack(columns[start] ?? [], false)
  for (let c = start + 1; c < columns.length; c++) pack(columns[c], true)
  for (let c = start - 1; c >= 0; c--) pack(columns[c], true)

  // Columns side by side, each as wide as its widest block, boxes centred.
  const widths = columns.map((list) => Math.max(0, ...list.map((b) => b.width)))
  const lefts: number[] = []
  let x = 0
  for (const w of widths) {
    lefts.push(x)
    x += w + COLUMN_GAP
  }
  const placedNodes: SystemNode[] = []
  const frames: StackFrame[] = []
  for (const list of columns) {
    for (const b of list) {
      const left = lefts[b.column] + (widths[b.column] - b.width) / 2
      let y = top.get(b)!
      if (b.frame) {
        const e = graph.items.get(b.frame)
        frames.push({
          id: `${sid}/frame:${b.frame}`,
          itemId: b.frame,
          item: e!.item,
          x: left,
          y,
          width: b.width,
          height: b.height,
        })
        y += STACK.head + STACK.pad
      }
      for (const n of b.nodes) {
        const inner = b.frame ? b.width - STACK.pad * 2 : b.width
        const nx = left + (b.frame ? STACK.pad : 0) + (inner - n.width) / 2
        placedNodes.push({ ...n, x: nx, y })
        const sats = satsOf.get(n.id) ?? []
        const row = satRow(n)
        sats.forEach((s, i) => {
          placedNodes.push({
            ...s,
            x: nx + n.width / 2 - row / 2 + i * (FACE_SIZE.satellite.width + SAT_SPACE),
            y: y + n.height + SAT_GAP,
          })
        })
        y += ownHeight(n) + STACK.gap
      }
    }
  }

  // Moved so the content starts at 0, 0.
  const rects = [...placedNodes, ...frames]
  const minX = Math.min(0, ...rects.map((r) => r.x))
  const minY = Math.min(...rects.map((r) => r.y), 0)
  const shift = <T extends { x: number; y: number }>(r: T) => ({
    ...r,
    x: r.x - minX,
    y: r.y - minY,
  })
  const nodesOut = placedNodes.map(shift)
  const framesOut = frames.map(shift)
  const all = [...nodesOut, ...framesOut]
  return {
    nodes: nodesOut,
    frames: framesOut,
    width: Math.max(0, ...all.map((r) => r.x + r.width)),
    height: Math.max(0, ...all.map((r) => r.y + r.height)),
  }
}

/** Every system on the server, each laid out on its own. */
export function detectSystems(map: ServerMap | undefined): System[] {
  if (!map) return []
  const graph = graphOf(map)
  const drafts = draftSystems(graph)
  const sharedBy = new Map<string, string[]>()
  for (const d of drafts) {
    for (const id of d.members) sharedBy.set(id, [...(sharedBy.get(id) ?? []), d.id])
  }
  return drafts.map((d) => {
    const built = buildSystem(graph, d, sharedBy)
    const haystack = [
      built.anchor.name,
      SYSTEM_LABEL[built.kind],
      ...built.nodes.flatMap((n) => [
        n.item.name,
        n.item.state,
        n.item.detail ?? "",
        n.item.product ?? "",
      ]),
    ]
      .join("\n")
      .toLowerCase()
    return { ...built, haystack }
  })
}

/** The name a system goes by: the loose one has none of its own. */
export function systemTitle(system: System) {
  return system.kind === "loose" ? "Everything else" : system.anchor.name
}

// ---------------------------------------------------------------------------
// The atlas: every system's frame on one canvas.
// ---------------------------------------------------------------------------

export type Placed = { system: System; x: number; y: number; width: number; height: number }

/**
 * Every system's frame, in rows that read left to right in the rail's order,
 * as wide as about one and a half times the square their areas make — so a
 * server with three systems is one row and one with thirty is a page.
 */
export function layoutAtlas(systems: System[]): Placed[] {
  const framed = systems.map((system) => ({
    system,
    width: Math.max(system.width + FRAME.pad * 2, 360),
    height: system.height + FRAME.head + FRAME.pad * 2,
  }))
  if (framed.length === 0) return []
  const area = framed.reduce((s, f) => s + f.width * f.height, 0)
  const widest = Math.max(...framed.map((f) => f.width))
  const limit = Math.max(widest, Math.sqrt(area) * 1.6)
  const out: Placed[] = []
  let x = 0
  let y = 0
  let rowHeight = 0
  for (const f of framed) {
    if (x > 0 && x + f.width > limit) {
      x = 0
      y += rowHeight + FRAME.gap
      rowHeight = 0
    }
    out.push({ system: f.system, x, y, width: f.width, height: f.height })
    x += f.width + FRAME.gap
    rowHeight = Math.max(rowHeight, f.height)
  }
  return out
}

/** The systems a search keeps: by any name, state or product in them. */
export function searchSystems(systems: System[], query: string): Set<string> | null {
  const q = query.trim().toLowerCase()
  if (!q) return null
  return new Set(systems.filter((s) => s.haystack.includes(q)).map((s) => s.id))
}

/** The nodes in a system a search lights. */
export function searchNodes(system: System, query: string): Set<string> {
  const q = query.trim().toLowerCase()
  if (!q) return new Set()
  return new Set(
    system.nodes
      .filter((n) =>
        [n.item.name, n.item.state, n.item.detail ?? "", n.item.product ?? "", n.role ?? ""]
          .join("\n")
          .toLowerCase()
          .includes(q),
      )
      .map((n) => n.id),
  )
}

/** A count of a system's things by face, for the rail's line under its name. */
export function systemCensus(system: System) {
  const count = (pred: (n: SystemNode) => boolean) =>
    system.nodes.filter((n) => pred(n) && !n.itemId.startsWith("logs:")).length
  return {
    containers: count((n) => n.kind === "containers"),
    databases: count((n) => n.kind === "databases" || n.itemId === "dashboard:store"),
    domains: count((n) => n.kind === "domains" || n.itemId === "dashboard:site"),
    volumes: count((n) => n.kind === "volumes"),
    backups: count((n) => n.kind === "backups"),
  }
}

/** What the chosen node is wired to inside its own system, both ways. */
export function neighbours(system: System, nodeId: string): Set<string> {
  const out = new Set<string>([nodeId])
  for (const e of system.edges) {
    if (e.source === nodeId) out.add(e.target)
    if (e.target === nodeId) out.add(e.source)
  }
  return out
}

// ---------------------------------------------------------------------------
// What the reader moved.
// ---------------------------------------------------------------------------

export type Settled = {
  nodes: SystemNode[]
  frames: StackFrame[]
  /** The content's bounds once moved boxes are counted: it can grow past 0, 0. */
  minX: number
  minY: number
  maxX: number
  maxY: number
}

/**
 * A system with the boxes the reader dragged where they put them. A
 * satellite nobody moved follows the box it hangs under, and a stack's frame
 * is drawn round wherever its containers now are.
 */
export function settle(system: System, positions: Record<string, Point>): Settled {
  if (!system.nodes.some((n) => positions[n.id])) {
    return {
      nodes: system.nodes,
      frames: system.frames,
      minX: 0,
      minY: 0,
      maxX: system.width,
      maxY: system.height,
    }
  }
  const delta = new Map<string, Point>()
  let nodes = system.nodes.map((n) => {
    const p = positions[n.id]
    if (!p) return n
    delta.set(n.id, { x: p.x - n.x, y: p.y - n.y })
    return { ...n, x: p.x, y: p.y }
  })
  if (delta.size > 0) {
    nodes = nodes.map((n) => {
      const d = n.host && !positions[n.id] ? delta.get(n.host) : undefined
      return d ? { ...n, x: n.x + d.x, y: n.y + d.y } : n
    })
  }
  const frames = system.frames.map((f) => {
    const members = nodes.filter((n) => n.frame === f.itemId)
    if (delta.size === 0 || members.length === 0) return f
    const minX = Math.min(...members.map((n) => n.x))
    const minY = Math.min(...members.map((n) => n.y))
    const maxX = Math.max(...members.map((n) => n.x + n.width))
    const maxY = Math.max(...members.map((n) => n.y + n.height))
    return {
      ...f,
      x: minX - STACK.pad,
      y: minY - STACK.head - STACK.pad,
      width: maxX - minX + STACK.pad * 2,
      height: maxY - minY + STACK.head + STACK.pad * 2,
    }
  })
  const rects = [...nodes, ...frames]
  return {
    nodes,
    frames,
    minX: Math.min(0, ...rects.map((r) => r.x)),
    minY: Math.min(0, ...rects.map((r) => r.y)),
    maxX: Math.max(system.width, ...rects.map((r) => r.x + r.width)),
    maxY: Math.max(system.height, ...rects.map((r) => r.y + r.height)),
  }
}
