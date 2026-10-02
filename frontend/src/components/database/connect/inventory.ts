import type { DbCredentialServer, DbDriver, DbFleet } from "@/lib/types"
import type { DbInstance, DbInventory, DbInventoryScan } from "@/components/database/fleet/types"

/**
 * What to do with a thing discovery found, decided apart from its drawing:
 * which of them are waiting to be connected, what one press on each does,
 * where each one is, and what the collectors that could not read left unsaid.
 */

/** What one press on a found instance does. */
export type FoundAction =
  /** Sign in with what the container states, or with nothing. */
  | { kind: "connect" }
  /** Ask for the password first: nothing here states it. */
  | { kind: "credentials"; choice: boolean }
  /** A database file the dashboard can open in place. */
  | { kind: "open" }
  /** A container that is down: starting it is what makes it connectable. */
  | { kind: "start-container"; id: string }
  /** A unit of the engine's own that is down. */
  | { kind: "start-unit"; unit: string }
  /** Nothing to press here; the place that owns it is one link away. */
  | { kind: "look"; href: string; label: string }
  | { kind: "none" }

/** The page a container, a compose service, a unit or a file's directory has elsewhere in the product. */
export function instanceHref(instance: DbInstance): { href: string; label: string } | null {
  if (instance.container?.id) {
    return {
      href: `/docker/containers/${encodeURIComponent(instance.container.id)}`,
      label: "Open container",
    }
  }
  if (instance.container?.composeProject) {
    return {
      href: `/docker/stacks/${encodeURIComponent(instance.container.composeProject)}`,
      label: "Open stack",
    }
  }
  if (instance.host?.unit) {
    return {
      href: `/processes/services?unit=${encodeURIComponent(instance.host.unit)}`,
      label: "Open service",
    }
  }
  if (instance.file && instance.kind !== "embedded") {
    const path = instance.kind === "data" ? instance.file.path : parentOf(instance.file.path)
    return { href: `/files?path=${encodeURIComponent(path)}`, label: "Open folder" }
  }
  return null
}

function parentOf(path: string) {
  const cut = path.lastIndexOf("/")
  return cut > 0 ? path.slice(0, cut) : "/"
}

/** Docker's and systemd's words for a server that exists and is not serving. */
const STARTABLE = new Set(["exited", "created", "dead", "inactive", "failed"])

/**
 * The one thing to do with a found instance. `hostAccount` is whether the
 * engine lets the dashboard make its own account from this machine — the
 * second way in to a native server that authenticates local connections by
 * the operating-system user.
 */
export function foundAction(instance: DbInstance, hostAccount = false): FoundAction {
  if (instance.self) return { kind: "none" }
  if (instance.connectable && instance.driver !== "") {
    if (instance.kind === "file") return { kind: "open" }
    switch (instance.credentials) {
      case "env":
      case "args":
      case "open":
      case "secret-file":
        return { kind: "connect" }
      case "peer":
        return { kind: "credentials", choice: hostAccount }
      default:
        return { kind: "credentials", choice: false }
    }
  }
  if (instance.kind === "server" && instance.driver !== "" && STARTABLE.has(instance.state)) {
    if (instance.container?.id) return { kind: "start-container", id: instance.container.id }
    if (instance.host?.unit) return { kind: "start-unit", unit: instance.host.unit }
  }
  const elsewhere = instanceHref(instance)
  return elsewhere ? { kind: "look", ...elsewhere } : { kind: "none" }
}

/** The name a row's press is given, and the word drawn before its arrow. */
export function foundActionWords(
  action: FoundAction,
  name: string,
): { verb: string; word: string } {
  switch (action.kind) {
    case "connect":
    case "credentials":
      return { verb: `Connect ${name}`, word: "Connect" }
    case "open":
      return { verb: `Open ${name}`, word: "Open file" }
    case "start-container":
      return { verb: `Start ${name}`, word: "Start container" }
    case "start-unit":
      return { verb: `Start ${name}`, word: "Start service" }
    case "look":
      return { verb: `${action.label} ${name}`, word: action.label }
    case "none":
      return { verb: name, word: "" }
  }
}

/** The address a connection would dial, or the path of a file. */
export function instanceAddress(instance: DbInstance): string {
  if (instance.file) return instance.file.path
  const primary = instance.endpoints.find((endpoint) => endpoint.primary) ?? instance.endpoints[0]
  if (!primary) return ""
  if (primary.kind === "unix" || primary.kind === "file") return primary.path ?? ""
  return primary.port ? `${primary.host ?? ""}:${primary.port}` : (primary.host ?? "")
}

/**
 * Where a found instance is, as the literal a row prints: the container, the
 * unit, the process, the file — and the address beside a server's.
 */
export function instanceWhere(instance: DbInstance): { kind: string; text: string } {
  const address = instanceAddress(instance)
  if (instance.kind === "file" || instance.kind === "data") {
    return { kind: instance.kind === "data" ? "data directory" : "file", text: address }
  }
  if (instance.kind === "embedded") {
    return {
      kind: "inside a container",
      text: `${instance.container?.name ?? instance.file?.containers?.[0] ?? "container"}:${address}`,
    }
  }
  if (instance.container) {
    const compose =
      instance.container.composeProject && instance.container.composeService
        ? `${instance.container.composeProject}/${instance.container.composeService}`
        : instance.container.name
    return {
      kind: instance.state === "declared" ? "compose service" : "container",
      text: address ? `${compose} · ${address}` : compose,
    }
  }
  const owner = instance.host?.unit ?? instance.host?.process ?? ""
  return {
    kind: instance.host?.unit ? "unit" : "process",
    text: [owner, address].filter(Boolean).join(" · "),
  }
}

/**
 * A file that is some program's private state — an editor's, a browser
 * profile's, a package's under `/var/lib`, the dashboard's own store — rather
 * than a database anybody would open from here.
 */
export function privateState(instance: DbInstance): boolean {
  const holder = instance.file?.holder
  return Boolean(instance.self) || holder === "tool" || holder === "system"
}

export type FoundShelves = {
  /** Servers, running first, in the inventory's own order. */
  servers: DbInstance[]
  /** Database files, data directories and files inside containers. */
  files: DbInstance[]
  /** Files that are a program's own state: counted, behind a fold. */
  kept: DbInstance[]
  /** What the operator said to leave alone. */
  ignored: DbInstance[]
  /** Ignored keys whose instance is no longer on the machine. */
  gone: string[]
}

/**
 * Everything discovery found that no saved connection points at, on the
 * shelves the list draws. The inventory's order is kept inside each: it is
 * stable between polls, so a row does not move under a pointer.
 */
export function foundShelves(inventory: DbInventory | undefined): FoundShelves {
  const shelves: FoundShelves = { servers: [], files: [], kept: [], ignored: [], gone: [] }
  if (!inventory) return shelves
  const present = new Set<string>()
  for (const instance of inventory.instances) {
    present.add(instance.key)
    if (instance.connections.length > 0) continue
    if (instance.ignored) shelves.ignored.push(instance)
    else if (privateState(instance)) shelves.kept.push(instance)
    else if (instance.kind === "server") shelves.servers.push(instance)
    else shelves.files.push(instance)
  }
  shelves.gone = inventory.ignored.filter((key) => !present.has(key))
  return shelves
}

/**
 * The servers `POST /databases/sync` would connect by itself: running, opened
 * by a driver, recognised by more than a port, and stating their credentials.
 */
export function connectsItself(instance: DbInstance): boolean {
  return (
    instance.kind === "server" &&
    instance.connectable &&
    instance.driver !== "" &&
    instance.confidence !== "port" &&
    ["env", "args", "open", "secret-file"].includes(instance.credentials)
  )
}

/**
 * Why a server that is up is not connected yet, in one sentence: the thing
 * between it and a connection is a password nothing on this machine states.
 * A server that cannot be connected at all carries the inventory's own
 * `reason` instead; one that states its credentials has nothing to explain.
 */
export function whyWaiting(instance: DbInstance): string | undefined {
  if (instance.kind !== "server" || !instance.connectable || instance.driver === "")
    return undefined
  switch (instance.credentials) {
    case "peer":
      return "Its accounts are kept in its own catalogue, where the dashboard cannot read a password."
    case "needed":
    case "unknown":
      return instance.container
        ? "Its container states no password — connect it with the one it uses."
        : "Nothing on this server states its password — connect it with the one it uses."
    default:
      return undefined
  }
}

/** One address, however it is spelled: every loopback spelling is this machine. */
function addressOf(host: string | undefined, port: number | undefined): string {
  const bare = (host ?? "").replace(/^\[|\]$/g, "").toLowerCase()
  const local = ["", "localhost", "127.0.0.1", "::1", "0.0.0.0"].includes(bare)
  return `${local ? "local" : bare}:${port ?? ""}`
}

/**
 * A server the fleet says is waiting for a password, as the instance the
 * inventory would have listed: what the password form is opened on when the
 * inventory itself could not be read. It has no key, which is what tells the
 * form to sign in by address (`POST /databases/host`) rather than by key.
 */
export function hostInstance(server: DbCredentialServer): DbInstance {
  return {
    key: "",
    kind: "server",
    name: server.name,
    engine: server.driver,
    driver: server.driver as DbDriver,
    label: "",
    source: "host",
    state: "running",
    endpoints: [{ kind: "tcp", host: server.host, port: server.port, primary: true }],
    host: { process: server.process },
    user: server.user,
    database: server.database,
    // An installed server authenticates this machine's own accounts by who
    // they are, which is what lets the dashboard make itself one.
    credentials: "peer",
    confidence: "process",
    evidence: [],
    connectable: true,
    connections: [],
  }
}

/** A server that is up on this machine and waits for a password to be connected. */
export type WaitingServer = {
  id: string
  name: string
  /** What to draw it as: the instance's flavour or engine, or the fleet's driver. */
  engine: string
  /** The sentence of why it is not connected. */
  reason: string
  /**
   * How it is connected from here: the inventory's instance by its key, a
   * host server by its address, or — for a container the inventory has not
   * listed — nothing yet but the way to the list.
   */
  via:
    | { kind: "instance"; instance: DbInstance }
    | { kind: "host"; server: DbCredentialServer }
    | { kind: "list" }
}

/**
 * Every server on this machine that is running, found, and held back only by
 * a password — the inventory's reading and the fleet's own two lists as one.
 *
 * The inventory is the fuller account and wins wherever it lists the server:
 * it has the key a connection is made by, and it knows what the operator set
 * aside. The fleet's lists arrive with the fleet itself, so they are what is
 * left to show while the inventory is still being read or when it could not
 * be: a host server still gets its password form, by address.
 */
export function waitingServers(
  fleet: Pick<DbFleet, "unreachable" | "needsCredentials"> | undefined,
  inventory: DbInventory | undefined,
): WaitingServer[] {
  const instances = inventory?.instances ?? []
  const said = new Map((fleet?.unreachable ?? []).map((one) => [one.container, one.reason]))
  const out: WaitingServer[] = []
  for (const instance of instances) {
    const open = instance.connections.length === 0 && !instance.ignored && !instance.self
    if (!open || !whyWaiting(instance)) continue
    out.push({
      id: instance.key,
      name: instance.name,
      engine: instance.flavor ?? instance.engine,
      reason: asSentence(said.get(instance.name)) ?? whyWaiting(instance) ?? "",
      via: { kind: "instance", instance },
    })
  }
  // With the inventory in hand there is nothing the fleet's lists can add: a
  // server it leaves out is connected, ignored, or gone.
  if (inventory) return out
  for (const server of fleet?.needsCredentials ?? []) {
    out.push({
      id: `host:${server.driver}:${addressOf(server.host, server.port)}`,
      name: server.name,
      engine: server.driver,
      reason: "Nothing on this server states its password — connect it with the one it uses.",
      via: { kind: "host", server },
    })
  }
  for (const server of fleet?.unreachable ?? []) {
    out.push({
      id: `container:${server.container}`,
      name: server.container,
      engine: server.driver,
      reason: asSentence(server.reason) ?? "",
      via: { kind: "list" },
    })
  }
  return out
}

/**
 * The server's lower-case clause ("the container is exited — start it to
 * connect") as a sentence of its own, which is how a row prints it.
 */
export function asSentence(clause: string | undefined): string | undefined {
  const text = clause?.trim()
  if (!text) return undefined
  const capital = text[0].toUpperCase() + text.slice(1)
  return /[.!?]$/.test(capital) ? capital : `${capital}.`
}

const SCAN_WORDS: Record<DbInventoryScan["source"], string> = {
  docker: "containers",
  compose: "compose services",
  listeners: "listening servers",
  sockets: "unix sockets",
  units: "systemd units",
  files: "database files",
}

/**
 * What the list is silent about and why: a collector that could not read is a
 * stated silence, never an empty healthy list. Collectors that failed for the
 * same reason are named together ("containers and compose services").
 */
export function scanSilences(scans: DbInventoryScan[]): { what: string; reason: string }[] {
  const byReason = new Map<string, string[]>()
  for (const scan of scans) {
    if (scan.ok) continue
    const reason = scan.reason ?? "it could not be read"
    byReason.set(reason, [...(byReason.get(reason) ?? []), SCAN_WORDS[scan.source]])
  }
  return [...byReason].map(([reason, words]) => ({ what: listWords(words), reason }))
}

/** What a collector that did read wants said about how far it got. */
export function scanNotes(scans: DbInventoryScan[]): string[] {
  return scans.filter((scan) => scan.ok && scan.reason).map((scan) => scan.reason as string)
}

/** Whether the slow file scan is still under way. */
export function scanningFiles(scans: DbInventoryScan[]): boolean {
  return scans.some((scan) => scan.source === "files" && scan.running)
}

function listWords(words: string[]): string {
  if (words.length < 2) return words.join("")
  return `${words.slice(0, -1).join(", ")} and ${words[words.length - 1]}`
}

/** The state word a row prints: the machine's own, except where it says less than ours. */
export function instanceState(instance: DbInstance): string {
  if (instance.kind === "file") return instance.file?.wal ? "in use" : "file"
  if (instance.kind === "data") return "no server"
  if (instance.state === "declared") return "not created"
  return instance.state
}
