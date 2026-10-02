import type { DbState } from "@/components/database/shell/types"

/**
 * A server being started, stopped or restarted from the dashboard.
 *
 * The request is slow by design — a container gets ninety seconds to shut
 * down, so a stop can take two and a half minutes — and the reader does not
 * stay on the page that began it. So the change is not a component's state:
 * it is held here, by connection, and every surface that draws the database
 * (its home, the menu on each of its pages) reads the same one. It is held
 * until the server's own state has caught up, not until the request returns:
 * a started engine reads `unreachable` for the seconds it takes to accept
 * connections, and "Starting…" is the true word for those too.
 *
 * It also outlives the page. A reload in the middle of a stop used to forget
 * it and offer Start on a server still shutting down, so the change is
 * written to the tab's session and told to the dashboard's other tabs. A tab
 * that did not send the request — this one after a reload, or another one —
 * has no answer to wait for; it ends the change when the server reads the
 * state the action settles in, or when the time the server allows it has
 * passed (`powerEnded`).
 */
export type PowerAction = "start" | "stop" | "restart"

export type PowerChange = {
  action: PowerAction
  /** When it was asked for, in milliseconds. */
  since: number
}

/** What `POST /databases/{id}/power` answers. */
export type PowerResponse = {
  action: PowerAction
  via: "docker" | "systemd"
  /** The container or the unit that was acted on. */
  target: string
  /** Docker's or systemd's own word for it afterwards. */
  state?: string
}

/** How a change ended: what the server answered, and whether it became what was asked of it. */
export type PowerOutcome = {
  answer: PowerResponse
  /**
   * The server read the state the action settles in before the wait ran out.
   * False is a container that started and an engine that never answered: the
   * request succeeded and the database is still not there.
   */
  settled: boolean
}

/** The words for each action: the verb, what the row says meanwhile, and what it is afterwards. */
export const POWER: Record<
  PowerAction,
  { label: string; progressive: string; done: string; settles: DbState }
> = {
  start: { label: "Start", progressive: "Starting…", done: "started", settles: "running" },
  stop: { label: "Stop", progressive: "Stopping…", done: "stopped", settles: "stopped" },
  restart: { label: "Restart", progressive: "Restarting…", done: "restarted", settles: "running" },
}

/**
 * The longest a change is believed with nobody waiting on its request: the
 * two and a half minutes the server gives a stop, and the minute after it an
 * engine is given to accept connections.
 */
export const POWER_LIMIT_MS = 210_000

/**
 * How long a restart nobody saw go down is still called one. A server that
 * reads `running` is either not down yet or already back, and only time
 * tells the two apart.
 */
const RESTART_UNSEEN_MS = 45_000

const STORED = "jd.databases.power"
const CHANNEL = "jd.databases.power"

const changes = new Map<number, PowerChange>()
/** The changes this tab's own request is carrying. Every other one is watched, not awaited. */
const carried = new Set<number>()
const listeners = new Set<() => void>()

type Message = { kind: "held"; id: number; change?: PowerChange } | { kind: "hello" }

let woken = false
let channel: BroadcastChannel | undefined
/**
 * The page is being unloaded. Its request is cut off with it, which reads
 * here as a request that failed — and letting go of the change then would
 * erase it from the session a moment before the reloaded page looks for it.
 * The server is still doing what it was asked.
 */
let leaving = false

function valid(change: unknown): change is PowerChange {
  if (change === null || typeof change !== "object") return false
  const { action, since } = change as Partial<PowerChange>
  return typeof since === "number" && typeof action === "string" && Object.hasOwn(POWER, action)
}

/**
 * Reads what this tab's session and the dashboard's other tabs know, once,
 * the first time anything asks. Not at import: the module is also evaluated
 * on the server, where there is no session to read.
 */
function wake() {
  if (woken || typeof window === "undefined") return
  woken = true
  try {
    const stored: unknown = JSON.parse(window.sessionStorage.getItem(STORED) ?? "[]")
    if (Array.isArray(stored)) {
      for (const entry of stored) {
        if (!Array.isArray(entry)) continue
        const [id, change] = entry as [unknown, unknown]
        if (typeof id !== "number" || !valid(change)) continue
        if (Date.now() - change.since < POWER_LIMIT_MS) changes.set(id, change)
      }
    }
  } catch {
    // A session that cannot be read holds no change: the page starts clean.
  }
  window.addEventListener("pagehide", () => {
    leaving = true
  })
  window.addEventListener("pageshow", () => {
    leaving = false
  })
  window.addEventListener("beforeunload", () => {
    leaving = true
    // Still here in a moment: the leaving was called off.
    setTimeout(() => {
      leaving = false
    }, 2_000)
  })
  if (typeof BroadcastChannel === "undefined") return
  channel = new BroadcastChannel(CHANNEL)
  channel.onmessage = (event: MessageEvent<Message>) => {
    const message = event.data
    if (message?.kind === "hello") {
      for (const id of carried) {
        channel?.postMessage({ kind: "held", id, change: changes.get(id) } satisfies Message)
      }
      return
    }
    if (message?.kind !== "held" || typeof message.id !== "number") return
    // This tab's own request is the better witness of its own change.
    if (carried.has(message.id)) return
    hold(message.id, valid(message.change) ? message.change : undefined, false)
  }
  channel.postMessage({ kind: "hello" } satisfies Message)
}

export function subscribePower(listener: () => void): () => void {
  wake()
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

/** The change in flight for a connection, if there is one. */
export function powerChange(id: number): PowerChange | undefined {
  wake()
  return changes.get(id)
}

/** Whether this tab's own request is carrying the change: it ends when that request does. */
export function powerCarried(id: number): boolean {
  return carried.has(id)
}

function hold(id: number, change: PowerChange | undefined, tell = true) {
  if (!change && leaving) return
  if (change) changes.set(id, change)
  else if (!changes.delete(id)) return
  if (typeof window !== "undefined") {
    try {
      window.sessionStorage.setItem(STORED, JSON.stringify([...changes]))
    } catch {
      // Nowhere to write it: the change is still held for this page.
    }
    if (tell) channel?.postMessage({ kind: "held", id, change } satisfies Message)
  }
  for (const listener of listeners) listener()
}

/**
 * Whether a change nobody here is waiting on has ended, judged from what the
 * server reads now.
 *
 * A start ends when the engine answers and a stop when the server is down. A
 * restart is the hard one: `running` is what it reads before it goes down
 * and after it is back, so it ends once it has been seen down and is up
 * again — or, never seen down, once it has been up for longer than a
 * restart leaves a server standing. Any of them ends when the time the
 * server allows has passed.
 */
export function powerEnded(
  change: PowerChange,
  state: DbState | "checking" | "unknown",
  seenDown: boolean,
  now: number,
): boolean {
  const waited = now - change.since
  if (waited >= POWER_LIMIT_MS) return true
  if (state !== POWER[change.action].settles) return false
  if (change.action !== "restart") return true
  return seenDown || waited >= RESTART_UNSEEN_MS
}

/**
 * Ends a change this tab is watching rather than carrying. One its own
 * request carries is left alone: that request's answer ends it.
 */
export function endPower(id: number) {
  if (!carried.has(id)) hold(id, undefined)
}

export type PowerRun = {
  /** Asks the server. */
  request: () => Promise<PowerResponse>
  /** Reads what the server is doing now. */
  read: () => Promise<DbState>
  /** How long to wait for the state to catch up once the request has answered. */
  settleMs?: number
  everyMs?: number
  now?: () => number
  sleep?: (ms: number) => Promise<void>
}

const wait = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms))

/**
 * Carries one change through: the request, then the wait for the server to be
 * what was asked of it. The change is held from the first line to the last,
 * whatever happens in between, and a second change of the same connection is
 * refused while one is in flight — the menu draws none, and this is why.
 *
 * It answers with whether the server got there. A request that succeeded is
 * not a database that is up: a container whose engine never accepts a
 * connection is `settled: false`, and the caller says so instead of
 * reporting a start.
 */
export async function changePower(
  id: number,
  action: PowerAction,
  { request, read, settleMs = 60_000, everyMs = 2_000, now = Date.now, sleep = wait }: PowerRun,
): Promise<PowerOutcome> {
  wake()
  if (changes.has(id)) throw new Error("This server is already being changed.")
  carried.add(id)
  hold(id, { action, since: now() })
  try {
    const answer = await request()
    const deadline = now() + settleMs
    for (;;) {
      // A read that fails is not the state arriving; ask again until the
      // deadline, and leave what the server is to the page's own poll after.
      const state = await read().catch(() => undefined)
      if (state === POWER[action].settles) return { answer, settled: true }
      if (now() >= deadline) return { answer, settled: false }
      await sleep(everyMs)
    }
  } finally {
    carried.delete(id)
    hold(id, undefined)
  }
}
