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

/** The words for each action: the verb, what the row says meanwhile, and what it is afterwards. */
export const POWER: Record<
  PowerAction,
  { label: string; progressive: string; done: string; settles: DbState }
> = {
  start: { label: "Start", progressive: "Starting…", done: "started", settles: "running" },
  stop: { label: "Stop", progressive: "Stopping…", done: "stopped", settles: "stopped" },
  restart: { label: "Restart", progressive: "Restarting…", done: "restarted", settles: "running" },
}

const changes = new Map<number, PowerChange>()
const listeners = new Set<() => void>()

export function subscribePower(listener: () => void): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

/** The change in flight for a connection, if there is one. */
export function powerChange(id: number): PowerChange | undefined {
  return changes.get(id)
}

function hold(id: number, change: PowerChange | undefined) {
  if (change) changes.set(id, change)
  else changes.delete(id)
  for (const listener of listeners) listener()
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
 */
export async function changePower(
  id: number,
  action: PowerAction,
  { request, read, settleMs = 60_000, everyMs = 2_000, now = Date.now, sleep = wait }: PowerRun,
): Promise<PowerResponse> {
  if (changes.has(id)) throw new Error("This server is already being changed.")
  hold(id, { action, since: now() })
  try {
    const answer = await request()
    const deadline = now() + settleMs
    for (;;) {
      // A read that fails is not the state arriving; ask again until the
      // deadline, and leave what the server is to the page's own poll after.
      const state = await read().catch(() => undefined)
      if (state === POWER[action].settles || now() >= deadline) break
      await sleep(everyMs)
    }
    return answer
  } finally {
    hold(id, undefined)
  }
}
