import type { ListeningPort, PM2Process, ProcessRow } from "@/lib/types"
import {
  isCluster,
  memoryLimitShare,
  pm2AppKey,
  pm2Apps,
  pm2Failing,
  pm2Select,
  type PM2App,
} from "@/components/procs/pm2-shared"

/**
 * Where the PM2 list keeps its question. An application's page reads the
 * same keys for its rail, so the list beside an application is the table it
 * was opened from.
 */
export const PM2_FILTER_KEYS = {
  query: "processes.pm2.query",
  state: "processes.pm2.state.v2",
  app: "processes.pm2.app",
} as const

export type PM2StateFilter = "" | "online" | "stopped" | "errored"

export type PM2Filter = { query: string; state: PM2StateFilter; app: string }

/** Whether one PM2 process is among what the list's search, state chip and band ask for. */
export function pm2Matches(process: PM2Process, filter: PM2Filter): boolean {
  if (filter.state === "online" && process.status !== "online") return false
  if (filter.state === "stopped" && process.status !== "stopped") return false
  if (filter.state === "errored" && !pm2Failing(process)) return false
  if (filter.app && pm2AppKey(process) !== filter.app) return false
  const needle = filter.query.trim().toLowerCase()
  if (!needle) return true
  return (
    process.name.toLowerCase().includes(needle) ||
    process.scriptPath.toLowerCase().includes(needle) ||
    process.daemonId.toLowerCase().includes(needle)
  )
}

/**
 * An application's own page. Its name is the address, because that is all the
 * pages that link here know of it: a process says "PM2 · api", a port says
 * "api". The account is added only where it is needed to tell two
 * applications of one name apart.
 */
export function pm2AppHref(
  app: Pick<PM2Process, "name"> & { daemonId?: string },
  options: { tab?: string; account?: boolean; worker?: number } = {},
): string {
  const query = new URLSearchParams()
  if (options.account && app.daemonId) query.set("account", app.daemonId)
  if (options.tab && options.tab !== "overview") query.set("tab", options.tab)
  if (options.worker !== undefined) query.set("worker", String(options.worker))
  const search = query.toString()
  return `/processes/pm2/${encodeURIComponent(app.name)}${search ? `?${search}` : ""}`
}

export type PM2Resolved =
  | { kind: "found"; app: PM2App }
  /** Two accounts each run an application of this name, and the address names neither. */
  | { kind: "ambiguous"; apps: PM2App[] }
  | { kind: "missing" }

/** The application an address names, among what PM2 runs now. */
export function pm2Resolve(processes: PM2Process[], name: string, account?: string): PM2Resolved {
  const apps = pm2Apps(processes)
    .filter((app) => app.name === name && (!account || app.daemonId === account))
    .sort((a, b) => (a.daemonId < b.daemonId ? -1 : 1))
  if (apps.length === 0) return { kind: "missing" }
  if (apps.length > 1) return { kind: "ambiguous", apps }
  return { kind: "found", app: apps[0] }
}

/**
 * Where `/processes/pm2?app=…` goes now. That address opened the sheet an
 * application was read in until 0.8.7, and it is still what the server map,
 * the search palette, a port's owner and every shared link carry: an exact
 * `account:id`, or a bare name. Either lands on the application's page; a
 * key that names nothing still goes there, where the page says it is gone.
 */
export function pm2LegacyHref(processes: PM2Process[], key: string): string {
  const process = pm2Select(processes, key)
  if (!process) return pm2AppHref({ name: key })
  const shared = processes.some((p) => p.name === process.name && p.daemonId !== process.daemonId)
  return pm2AppHref(process, { account: shared })
}

/** The worker an application is read through: the first that is running, else the first. */
export function pm2Lead(app: PM2App): PM2Process {
  const ordered = [...app.instances].sort((a, b) => a.id - b.id)
  return ordered.find((p) => p.status === "online" && p.pid > 0) ?? ordered[0]
}

/**
 * An application's state in one word. A fork is its process's; a cluster is
 * online only while every worker is, and says how many are when some are not,
 * because "online" over two dead workers is the reading that hides them.
 */
export function pm2AppState(app: PM2App): { status: string; word: string; partial: boolean } {
  const count = app.instances.length
  if (app.online === count) return { status: "online", word: "online", partial: false }
  const failing = app.instances.find(pm2Failing)
  if (app.online === 0) {
    const status = failing?.status ?? "stopped"
    return { status, word: status, partial: false }
  }
  return {
    status: failing?.status ?? "stopped",
    word: `${app.online} of ${count} online`,
    partial: true,
  }
}

/** How an application runs, as the fact after its name. */
export function pm2ModeWords(app: PM2App): string {
  if (!isCluster(app.instances[0])) return "fork"
  return `cluster of ${app.instances.length}`
}

/** PM2's count of an application's restarts, as a sentence. */
export function pm2RestartWords(restarts: number): string {
  if (restarts === 0) return "Never restarted"
  return `Restarted ${restarts === 1 ? "once" : `${restarts} times`}`
}

export type PM2Reading = {
  /** A share of one core, summed over the workers that are running. */
  cpu: number
  rss: number
  /** Disk rates, once the process table has measured a window for any worker. */
  io?: { read: number; write: number }
  threads: number
  /** Every socket any worker listens on, once each. */
  listening: ListeningPort[]
  connections: number
  /** The fullest worker against the memory PM2 restarts it at, when it has a limit. */
  limitShare?: number
  /** The workers' recent windows, summed where every one of them has a reading. */
  history: { cpu: number[]; rss: number[]; io: number[] }
  /** Whether the process table's own readings are in the sums, or only PM2's. */
  measured: boolean
}

/**
 * What an application is using: its running workers summed, each from the
 * process table's reading of its PID where there is one — that reading is two
 * seconds old and carries disk rates, sockets and a history — and from PM2's
 * own figure where there is not.
 */
export function pm2Reading(app: PM2App, rows: (ProcessRow | null | undefined)[]): PM2Reading {
  const byPid = new Map<number, ProcessRow>()
  for (const row of rows) if (row) byPid.set(row.pid, row)
  const reading: PM2Reading = {
    cpu: 0,
    rss: 0,
    threads: 0,
    listening: [],
    connections: 0,
    history: { cpu: [], rss: [], io: [] },
    measured: false,
  }
  const seen = new Set<string>()
  const histories: NonNullable<ProcessRow["history"]>[] = []
  for (const worker of app.instances) {
    if (worker.status !== "online") continue
    const row = worker.pid > 0 ? byPid.get(worker.pid) : undefined
    const rss = row?.rss ?? worker.memory
    reading.cpu += row && row.cpuReady !== false ? row.cpuPercent : worker.cpu
    reading.rss += rss
    const share = memoryLimitShare({ memory: rss, maxMemoryRestart: worker.maxMemoryRestart })
    if (share !== undefined) reading.limitShare = Math.max(reading.limitShare ?? 0, share)
    if (!row) continue
    reading.measured = true
    reading.threads += row.threads
    reading.connections += row.connections ?? 0
    if (row.ioReady) {
      reading.io = {
        read: (reading.io?.read ?? 0) + (row.ioReadRate ?? 0),
        write: (reading.io?.write ?? 0) + (row.ioWriteRate ?? 0),
      }
    }
    for (const port of row.listening ?? []) {
      const key = `${port.proto}/${port.address}/${port.port}`
      if (seen.has(key)) continue
      seen.add(key)
      reading.listening.push(port)
    }
    if (row.history?.length) histories.push(row.history)
  }
  // Summed from the newest window back, over the stretch every worker has: a
  // worker restarted a minute ago would otherwise make the application look
  // as if it had doubled a minute ago.
  const length = histories.length > 0 ? Math.min(...histories.map((h) => h.length)) : 0
  for (let back = length; back > 0; back--) {
    let cpu = 0
    let rss = 0
    let io = 0
    for (const history of histories) {
      const point = history[history.length - back]
      cpu += point.cpu
      rss += point.rss
      io += point.read + point.write
    }
    reading.history.cpu.push(cpu)
    reading.history.rss.push(rss)
    reading.history.io.push(io)
  }
  return reading
}

/** Which of its cluster's workers each process is, counted from one, and how many there are. */
export function pm2Positions(processes: PM2Process[]): Map<string, [number, number]> {
  const out = new Map<string, [number, number]>()
  for (const app of pm2Apps(processes)) {
    if (!isCluster(app.instances[0])) continue
    const ordered = [...app.instances].sort((a, b) => a.id - b.id)
    ordered.forEach((p, index) => out.set(`${p.daemonId}:${p.id}`, [index + 1, ordered.length]))
  }
  return out
}

export type PM2AppAction = "start" | "reload" | "restart" | "stop" | "reset" | "flush" | "delete"

/** The workers a verb pressed on the application reaches, in PM2's own order. */
export function pm2AppTargets(app: PM2App, action: PM2AppAction): PM2Process[] {
  const workers = [...app.instances].sort((a, b) => a.id - b.id)
  switch (action) {
    case "start":
      return workers.filter((p) => p.status !== "online")
    case "reload":
    case "stop":
      return workers.filter((p) => p.status === "online")
    // PM2 flushes by name, and a cluster's workers write to the same files.
    case "flush":
      return [pm2Lead(app)]
    default:
      return workers
  }
}

/** What each verb does, for the Control view, where a verb is read before it is pressed. */
export function pm2VerbWords(key: string, cluster: boolean): string {
  switch (key) {
    case "start":
      return cluster ? "Starts the workers that are down." : "Starts it under PM2 again."
    case "reload":
      return cluster ? "Replaces its workers one at a time." : "Restarts it, with a short gap."
    case "restart":
      return "Stops it and starts it again."
    case "stop":
      return "Stops it until it is started again."
    case "scale":
      return "Sets how many workers the cluster runs."
    case "reset":
      return "Sets PM2's count of its restarts back to zero."
    case "flush":
      return "Empties its log files. Rotated copies are untouched."
    case "delete":
      return "Removes it from PM2. Its files stay on disk."
    default:
      return ""
  }
}
