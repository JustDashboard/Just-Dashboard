import type { Listener, PortRange } from "@/lib/types"
import {
  reachGroup,
  reachVerdict,
  reachWords,
  socketAddresses,
  socketPids,
  type ReachGroup,
  type Socket,
} from "@/components/proxy/ports"

/**
 * How the ports page narrows, orders and hands over its rows: the search
 * box's field tokens, the two filters that combine, the sortable columns,
 * grouping by application, the kernel's ephemeral range, the address bar's
 * copy of the view and the export. Kept apart from `ports.ts`, which says
 * where one socket answers; this is what the list does with many.
 */

/** The reach chips: everything, or one of the three answers to who can connect. */
export type ReachFilter = "all" | ReachGroup
export type ProtoFilter = "all" | "tcp" | "udp"

export type SortKey = "reach" | "port" | "process"
export type PortSort = { key: SortKey; dir: "asc" | "desc" }

/** Worst first: what the posture would flag, then what the internet can reach. */
export const DEFAULT_SORT: PortSort = { key: "reach", dir: "desc" }

/** The direction a column sorts in when it is first chosen. */
export const FIRST_DIRECTION: Record<SortKey, PortSort["dir"]> = {
  reach: "desc",
  port: "asc",
  process: "asc",
}

const REACH_FILTERS: readonly ReachFilter[] = ["all", "internet", "private", "local"]
const PROTO_FILTERS: readonly ProtoFilter[] = ["all", "tcp", "udp"]
const SORT_KEYS: readonly SortKey[] = ["reach", "port", "process"]

export function parseReachFilter(value: string | null | undefined): ReachFilter | undefined {
  return REACH_FILTERS.find((filter) => filter === value)
}

export function parseProtoFilter(value: string | null | undefined): ProtoFilter | undefined {
  return PROTO_FILTERS.find((filter) => filter === value)
}

/** "port" ascending, "-port" descending, as the address bar spells a sort. */
export function parseSort(value: string | null | undefined): PortSort | undefined {
  if (!value) return undefined
  const dir = value.startsWith("-") ? "desc" : "asc"
  const key = SORT_KEYS.find((k) => k === value.replace(/^-/, ""))
  return key ? { key, dir } : undefined
}

export function sortParam(sort: PortSort): string {
  return `${sort.dir === "desc" ? "-" : ""}${sort.key}`
}

/** What the page shows, as far as a link can say it. */
export type PortView = { q: string; reach: ReachFilter; proto: ProtoFilter; sort: PortSort }

const VIEW_KEYS = ["q", "reach", "proto", "sort"] as const

/**
 * The view the address bar asks for, or null when it asks nothing. A link
 * that names any part of the view names all of it: the overview's
 * `?reach=internet` must not open inside a search this tab typed an hour ago,
 * so a key it leaves out is that key's default, not what was remembered. A
 * value the page has no filter for is the default too.
 */
export function viewFromParams(params: { get(key: string): string | null }): PortView | null {
  if (!VIEW_KEYS.some((key) => params.get(key) !== null)) return null
  return {
    q: params.get("q") ?? "",
    reach: parseReachFilter(params.get("reach")) ?? "all",
    proto: parseProtoFilter(params.get("proto")) ?? "all",
    sort: parseSort(params.get("sort")) ?? DEFAULT_SORT,
  }
}

/**
 * The query string for a view, defaults left out, spelled so a person can
 * read and type it: `?q=:443&reach=internet&sort=-port` rather than `%3A443`.
 */
export function viewQuery(view: PortView): string {
  const pairs: [string, string][] = []
  if (view.q) pairs.push(["q", view.q])
  if (view.reach !== "all") pairs.push(["reach", view.reach])
  if (view.proto !== "all") pairs.push(["proto", view.proto])
  const sort = sortParam(view.sort)
  if (sort !== sortParam(DEFAULT_SORT)) pairs.push(["sort", sort])
  return pairs.map(([key, value]) => `${key}=${readable(value)}`).join("&")
}

/** `search` with the view's keys replaced by `view`, every other key kept. */
export function withView(search: string, view: PortView): string {
  const others = new URLSearchParams(search)
  for (const key of VIEW_KEYS) others.delete(key)
  const query = [others.toString(), viewQuery(view)].filter(Boolean).join("&")
  return query ? `?${query}` : ""
}

/** A link to the ports page opened on a view; what it leaves out is the default. */
export function portsHref(view: Partial<PortView>): string {
  const query = viewQuery({ q: "", reach: "all", proto: "all", sort: DEFAULT_SORT, ...view })
  return query ? `/proxy/ports?${query}` : "/proxy/ports"
}

// Colons, commas and brackets are what an endpoint and a port list are
// written with, and every browser reads them unescaped in a query.
function readable(value: string): string {
  return encodeURIComponent(value)
    .replace(/%3A/gi, ":")
    .replace(/%2C/gi, ",")
    .replace(/%5B/gi, "[")
    .replace(/%5D/gi, "]")
    .replace(/%20/g, "+")
}

/**
 * One word of the search box. A field token narrows by one property —
 * `proto:udp`, `user:postgres`, `pid:812`, `port:8000-8100`, `:443`, an
 * endpoint as `ss` prints it (`0.0.0.0:80`, `[::]:22`, `*:53`) — and
 * anything else is text, looked for in the port, process, command, user,
 * addresses, endpoints and reach. A token that looks like a field but does
 * not parse (`pid:abc`) is text as typed, so it finds nothing rather than
 * being quietly dropped.
 */
export type QueryTerm =
  | { kind: "text"; value: string }
  | { kind: "port"; spans: [number, number][] }
  | { kind: "endpoint"; address: string; port: number }
  | { kind: "address"; address: string }
  | { kind: "proto"; value: "tcp" | "udp" }
  | { kind: "user"; value: string }
  | { kind: "pid"; value: number }

const IPV4 = /^\d{1,3}(?:\.\d{1,3}){3}$/

export function parseQuery(input: string): QueryTerm[] {
  return input.trim().toLowerCase().split(/\s+/).filter(Boolean).map(parseTerm)
}

function parseTerm(token: string): QueryTerm {
  const text: QueryTerm = { kind: "text", value: token }
  const field = /^(proto|port|user|pid):(.+)$/.exec(token)
  if (field) {
    const [, key, value] = field
    if (key === "proto") return value === "tcp" || value === "udp" ? { kind: "proto", value } : text
    if (key === "user") return { kind: "user", value }
    if (key === "pid") return /^\d+$/.test(value) ? { kind: "pid", value: Number(value) } : text
    const spans = portSpans(value)
    return spans ? { kind: "port", spans } : text
  }
  if (token.startsWith(":")) {
    const port = portNumber(token.slice(1))
    return port ? { kind: "port", spans: [[port, port]] } : text
  }
  const bracketed = /^\[([0-9a-f:.]+(?:%[\w.-]+)?)\](?::(\d+))?$/.exec(token)
  if (bracketed) {
    const [, address, port] = bracketed
    if (port === undefined) return { kind: "address", address }
    const number = portNumber(port)
    return number ? { kind: "endpoint", address, port: number } : text
  }
  const colon = token.lastIndexOf(":")
  if (colon > 0) {
    const address = token.slice(0, colon)
    const port = portNumber(token.slice(colon + 1))
    if (port && (address === "*" || IPV4.test(address))) return { kind: "endpoint", address, port }
  }
  return text
}

function portNumber(value: string): number | undefined {
  if (!/^\d{1,5}$/.test(value)) return undefined
  const port = Number(value)
  return port >= 1 && port <= 65535 ? port : undefined
}

/** "5432,6379" or "8000-8100": the ports a `port:` token names, or nothing if any part is not one. */
function portSpans(value: string): [number, number][] | undefined {
  const spans: [number, number][] = []
  for (const part of value.split(",")) {
    const [from, to, extra] = part.split("-")
    const low = portNumber(from)
    const high = to === undefined ? low : portNumber(to)
    if (!low || !high || extra !== undefined || low > high) return undefined
    spans.push([low, high])
  }
  return spans
}

/** Every term must hold; a `port:` list holds when any of its ports does. */
export function matchesQuery(socket: Socket, terms: QueryTerm[]): boolean {
  return terms.every((term) => matchesTerm(socket, term))
}

function matchesTerm(socket: Socket, term: QueryTerm): boolean {
  switch (term.kind) {
    case "proto":
      return socket.protocol === term.value
    case "port":
      return term.spans.some(([low, high]) => socket.port >= low && socket.port <= high)
    case "pid":
      return socketPids(socket).includes(term.value)
    case "user":
      return (socket.user ?? "").toLowerCase().includes(term.value)
    case "address":
      return socketAddresses(socket).some((address) => address.toLowerCase() === term.address)
    case "endpoint":
      if (socket.port !== term.port) return false
      // `*` is how ss writes a wildcard, whichever family it is in.
      if (term.address === "*") return socket.scope === "all"
      return socketAddresses(socket).some((address) => address.toLowerCase() === term.address)
    case "text":
      return haystack(socket).includes(term.value)
  }
}

function haystack(socket: Socket): string {
  const addresses = socketAddresses(socket)
  return [
    String(socket.port),
    socket.protocol,
    socket.process ?? "",
    socket.cmdline ?? "",
    socket.user ?? "",
    ...addresses,
    ...addresses.map((address) => formatEndpoint(address, socket.port)),
    reachWords(socket),
  ]
    .join("\n")
    .toLowerCase()
}

/** An address and port as a client would dial it: `[::1]:8080`, `127.0.0.1:8080`, `*:53`. */
export function formatEndpoint(address: string, port: number): string {
  if (address === "" || address === "*") return `*:${port}`
  return address.includes(":") ? `[${address}]:${port}` : `${address}:${port}`
}

/**
 * A loopback socket inside the range the kernel hands out to sockets bound to
 * port 0. Of sixty-two sockets on the host this page was built on, thirty-five
 * were these — git-daemon, a language server, a browser's debugging port —
 * and the few that face off the machine were lost among them.
 */
export function isLoopbackEphemeral(
  socket: Pick<Listener, "reach" | "port">,
  range: PortRange | null | undefined,
): boolean {
  if (!range) return false
  return reachGroup(socket) === "local" && inRange(socket.port, range)
}

/**
 * A port on a connection that the kernel did not pick for it: outside the
 * ephemeral range, so somebody chose it, which on this host's side of a
 * connection is almost always a listener's.
 */
export function chosenPort(port: number, range: PortRange | null | undefined): boolean {
  if (!range) return false
  return !inRange(port, range)
}

function inRange(port: number, range: PortRange): boolean {
  return port >= range.low && port <= range.high
}

export type PortFilters = {
  terms: QueryTerm[]
  reach: ReachFilter
  proto: ProtoFilter
  /** The range whose loopback sockets are set aside; null shows them. */
  hide: PortRange | null
}

type Part = "reach" | "proto" | "hide"

function passes(socket: Socket, filters: PortFilters, skip?: Part): boolean {
  if (skip !== "reach" && filters.reach !== "all" && reachGroup(socket) !== filters.reach) {
    return false
  }
  if (skip !== "proto" && filters.proto !== "all" && socket.protocol !== filters.proto) {
    return false
  }
  if (skip !== "hide" && isLoopbackEphemeral(socket, filters.hide)) return false
  return matchesQuery(socket, filters.terms)
}

export function visibleSockets(sockets: Socket[], filters: PortFilters): Socket[] {
  return sockets.filter((socket) => passes(socket, filters))
}

export type Facets = {
  reach: Record<ReachFilter, number>
  proto: Record<"tcp" | "udp", number>
  /** Loopback ephemeral sockets the other filters let through: what hiding them hides. */
  ephemeral: number
}

/**
 * Each chip's count is the rows it would show with the other filters as they
 * are, so a count is never a number the list cannot then be made to show:
 * with a search typed, the reach chips say which answer its matches are in.
 */
export function facetCounts(
  sockets: Socket[],
  filters: PortFilters,
  range: PortRange | null,
): Facets {
  const facets: Facets = {
    reach: { all: 0, internet: 0, private: 0, local: 0 },
    proto: { tcp: 0, udp: 0 },
    ephemeral: 0,
  }
  for (const socket of sockets) {
    if (passes(socket, filters, "reach")) {
      facets.reach.all++
      facets.reach[reachGroup(socket)]++
    }
    if (
      passes(socket, filters, "proto") &&
      (socket.protocol === "tcp" || socket.protocol === "udp")
    ) {
      facets.proto[socket.protocol]++
    }
    if (isLoopbackEphemeral(socket, range) && passes(socket, filters, "hide")) facets.ephemeral++
  }
  return facets
}

const VERDICT_RANK = { critical: 0, warning: 1, notice: 2 } as const

function severityRank(socket: Socket): number {
  const verdict = reachVerdict(socket)
  return verdict ? VERDICT_RANK[verdict] : 3
}

/** The process a row is headed by: its name, or "unknown" where it could not be read. */
export function processName(socket: Pick<Listener, "process">): string {
  return socket.process || "unknown"
}

/**
 * The rows in the chosen order. Reach sorts by the verdict a row is drawn
 * with, so descending is what the posture would flag first; every order falls
 * back to the port, then the protocol, so rows keep still between polls.
 */
export function sortSockets(sockets: Socket[], sort: PortSort): Socket[] {
  const sign = sort.dir === "asc" ? 1 : -1
  const byPort = (a: Socket, b: Socket) =>
    a.port - b.port || a.protocol.localeCompare(b.protocol) || a.address.localeCompare(b.address)
  const primary = (a: Socket, b: Socket): number => {
    switch (sort.key) {
      case "reach":
        return severityRank(b) - severityRank(a)
      case "port":
        return a.port - b.port
      case "process":
        return processName(a).localeCompare(processName(b))
    }
  }
  return [...sockets].sort((a, b) => sign * primary(a, b) || byPort(a, b))
}

/** One application's sockets: one program run by one account. */
export type SocketGroup = { key: string; process: string; user: string; sockets: Socket[] }

/**
 * The rows by application, in the order the first of each appears — so the
 * sort still decides which application comes first. Twenty git-daemon
 * sockets on loopback are one line to read past, not twenty.
 */
export function groupByOwner(sockets: Socket[]): SocketGroup[] {
  const groups = new Map<string, SocketGroup>()
  for (const socket of sockets) {
    const process = processName(socket)
    const user = socket.user ?? ""
    const key = JSON.stringify([process, user])
    const group = groups.get(key) ?? { key, process, user, sockets: [] }
    group.sockets.push(socket)
    groups.set(key, group)
  }
  return [...groups.values()]
}

/** The group's worst socket, whose reach the group's line is drawn with. */
export function worstSocket(group: SocketGroup): Socket {
  return group.sockets.reduce((worst, socket) =>
    severityRank(socket) < severityRank(worst) ? socket : worst,
  )
}

/** "22, 80, 443" or "33012, 34001, 35555 +17": the group's ports, lowest first, each once. */
export function groupPorts(group: SocketGroup, shown = 3): string {
  const ports = [...new Set(group.sockets.map((socket) => socket.port))].sort((a, b) => a - b)
  const head = ports.slice(0, shown).join(", ")
  return ports.length > shown ? `${head} +${ports.length - shown}` : head
}

/** Every process behind a group, for its line: one PID or several. */
export function groupPids(group: SocketGroup): number[] {
  return [...new Set(group.sockets.flatMap(socketPids))].sort((a, b) => a - b)
}

/** "8s ago", "3m ago": how old a reading is, to the second while that matters. */
export function sinceWords(ms: number): string {
  const seconds = Math.max(0, Math.floor(ms / 1000))
  if (seconds < 5) return "just now"
  if (seconds < 60) return `${seconds}s ago`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  return `${Math.floor(minutes / 60)}h ago`
}

/** The sockets behind rows, each folded pair as the two sockets the host lists. */
function unfolded(sockets: Socket[]): Listener[] {
  return sockets.flatMap(({ twin, ...socket }) => (twin ? [socket, twin] : [socket]))
}

const CSV_COLUMNS = [
  "protocol",
  "family",
  "address",
  "port",
  "endpoint",
  "reach",
  "network",
  "interface",
  "process",
  "pid",
  "ppid",
  "user",
  "cmdline",
  "level",
] as const

/**
 * The rows as a spreadsheet, one line per socket as the host lists them — a
 * folded pair is two lines, since each family's address is a fact of its own.
 */
export function toCsv(sockets: Socket[]): string {
  const lines = unfolded(sockets).map((socket) =>
    CSV_COLUMNS.map((column) => {
      if (column === "endpoint") return csvCell(formatEndpoint(socket.address, socket.port))
      const value = socket[column]
      return csvCell(value === undefined || value === null ? "" : String(value))
    }).join(","),
  )
  return [CSV_COLUMNS.join(","), ...lines].join("\n") + "\n"
}

/** The rows as JSON, one object per socket in the shape GET /ports answers with. */
export function toJson(sockets: Socket[]): string {
  return JSON.stringify(unfolded(sockets), null, 2) + "\n"
}

function csvCell(value: string): string {
  // Any local account can start a listener whose command line begins with
  // "=", and a spreadsheet opening the export would run it as a formula.
  const inert = /^[=+\-@\t\r]/.test(value) ? `'${value}` : value
  return /[",\n\r]/.test(inert) ? `"${inert.replace(/"/g, '""')}"` : inert
}
