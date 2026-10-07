import type { PM2Daemon, PM2Process } from "@/lib/types"

/** Names and numeric ids are per daemon; the pair is the identity. */
export function pm2Key(process: Pick<PM2Process, "daemonId" | "id">): string {
  return `${process.daemonId}:${process.id}`
}

/** An application is a name under one daemon: every instance of a cluster shares it. */
export function pm2AppKey(process: Pick<PM2Process, "daemonId" | "name">): string {
  return `${process.daemonId}/${process.name}`
}

export function isCluster(process: Pick<PM2Process, "execMode">): boolean {
  return process.execMode === "cluster_mode" || process.execMode === "cluster"
}

/** Anything but online or stopped on purpose: errored, launching, one-launch-status. */
export function pm2Failing(process: Pick<PM2Process, "status">): boolean {
  return process.status !== "online" && process.status !== "stopped"
}

/**
 * One application as PM2's list draws it several times: a cluster is one
 * entry per instance, each with its own id, PID and readings. The band sums
 * them, because "api is using 0.6 cores" is the answer and four rows of 0.15
 * are the arithmetic.
 */
export type PM2App = {
  key: string
  name: string
  daemonId: string
  interpreter?: string
  instances: PM2Process[]
  online: number
  cpu: number
  memory: number
}

export function pm2Apps(processes: PM2Process[]): PM2App[] {
  const apps = new Map<string, PM2App>()
  for (const process of processes) {
    const key = pm2AppKey(process)
    let app = apps.get(key)
    if (!app) {
      app = {
        key,
        name: process.name,
        daemonId: process.daemonId,
        interpreter: process.interpreter,
        instances: [],
        online: 0,
        cpu: 0,
        memory: 0,
      }
      apps.set(key, app)
    }
    app.instances.push(process)
    if (process.status === "online") app.online++
    app.cpu += process.cpu
    app.memory += process.memory
  }
  return [...apps.values()]
}

/**
 * The list as it is read: an application that is crashing first, because it
 * is the one row that needs a person, then by name with a cluster's instances
 * in their order — the order `pm2 ls` prints, so a cluster stays together.
 */
export function pm2Order(processes: PM2Process[]): PM2Process[] {
  return [...processes].sort((a, b) => {
    const failing = Number(pm2Failing(b)) - Number(pm2Failing(a))
    if (failing !== 0) return failing
    if (a.name !== b.name) return a.name < b.name ? -1 : 1
    if (a.daemonId !== b.daemonId) return a.daemonId < b.daemonId ? -1 : 1
    return a.id - b.id
  })
}

/**
 * Where a daemon's saved list and what it runs disagree. An application
 * started since the last save does not come back after a reboot; one deleted
 * since does. Unknown — no list, or one that could not be read — is null,
 * because "nothing differs" would be a claim the page cannot make.
 */
export function savedDrift(
  daemon: Pick<PM2Daemon, "account" | "savedApps">,
  processes: Pick<PM2Process, "daemonId" | "name">[],
): { unsaved: string[]; removed: string[] } | null {
  if (!daemon.savedApps) return null
  const saved = new Set(daemon.savedApps)
  const running = new Set(processes.filter((p) => p.daemonId === daemon.account).map((p) => p.name))
  return {
    unsaved: [...running].filter((name) => !saved.has(name)).sort(),
    removed: [...saved].filter((name) => !running.has(name)).sort(),
  }
}

/**
 * How full an application is against the memory PM2 restarts it at, when it
 * has one. That is the ceiling that matters for it: 300 MB is nothing on an
 * 8 GB host and two thirds of the way to a restart under a 450 MB limit.
 */
export function memoryLimitShare(
  process: Pick<PM2Process, "memory" | "maxMemoryRestart">,
): number | undefined {
  if (!process.maxMemoryRestart) return undefined
  return (process.memory / process.maxMemoryRestart) * 100
}

/**
 * Which instance a deep link opens. `?app=` carries an exact identity from
 * this page and a bare name from the live table's owner link, and a bare
 * name of a cluster names every instance — so it opens the first that is
 * running rather than nothing, which is what an ambiguous name used to open.
 */
export function pm2Select(processes: PM2Process[], key: string | null): PM2Process | null {
  if (!key) return null
  const exact = processes.find((p) => pm2Key(p) === key)
  if (exact) return exact
  const named = processes.filter((p) => p.name === key)
  if (named.length === 0) return null
  const daemons = new Set(named.map((p) => p.daemonId))
  // The same name under two accounts is two applications; a name alone
  // cannot choose between them.
  if (daemons.size > 1) return null
  const ordered = [...named].sort((a, b) => a.id - b.id)
  return ordered.find((p) => p.status === "online") ?? ordered[0]
}
