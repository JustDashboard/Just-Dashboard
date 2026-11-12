import { bytes, relativeTime } from "@/lib/format"

/**
 * `GET /dashboard/health`: whether the dashboard itself is in working order.
 *
 * It is not the container's liveness, which stays a fixed "ok" so that a full disk cannot
 * restart-loop the stack or roll an update back. This is the reading for somebody signed in.
 */
export type StoreHealth = {
  ok: boolean
  /** `disk_full`, `store_unwritable` or `store_unreadable` while there is a problem. */
  code?: string
  /** The store's own words for the failure. */
  detail?: string
  since?: string
  checkedAt: string
  lastWriteAt?: string
  /** The dashboard's data directory, and the filesystem it is on. */
  path: string
  freeBytes: number
  totalBytes: number
  /** The space kept back for a full disk: its size, whether it is on disk, whether it was spent. */
  reserveBytes: number
  reserveHeld: boolean
  reserveReleased: boolean
}

/** A background task of the dashboard that panicked in the last half hour. */
export type TroubledTask = {
  name: string
  running: boolean
  panics: number
  lastPanic?: string
  lastPanicAt?: string
  /** When a task waiting out its pause is started again. */
  restartAt?: string
}

export type DashboardHealth = {
  ok: boolean
  store: StoreHealth
  tasks?: TroubledTask[]
}

/** One thing wrong with the dashboard, in the words the Overview says it in. */
export type HealthProblem = {
  key: string
  /** Danger is the dashboard unable to do its job; a warning is part of it failing and recovering. */
  tone: "danger" | "warning"
  title: string
  body: string
  /** What the subsystem underneath said, for the line under the sentence. */
  detail?: string
}

/** The problems a health report holds, worst first; none while it is unread or healthy. */
export function healthProblems(health: DashboardHealth | undefined): HealthProblem[] {
  return [...storeProblems(health?.store), ...taskProblems(health?.tasks)]
}

/**
 * A crashing task is said once however many there are: a loop is restarted by itself and a
 * one-off (a job, a worker) has failed with its own reason, so what the reader can do is the same
 * for all of them, which is to send the log to whoever maintains this.
 */
function taskProblems(tasks: TroubledTask[] | undefined): HealthProblem[] {
  if (!Array.isArray(tasks) || tasks.length === 0) return []
  const worst = [...tasks].sort((a, b) => b.panics - a.panics)[0]
  const others = tasks.length - 1
  const times = worst.panics === 1 ? "once" : `${worst.panics} times`
  return [
    {
      key: "tasks",
      tone: "warning",
      title:
        tasks.length === 1
          ? "A background task of the dashboard is crashing"
          : `${tasks.length} background tasks of the dashboard are crashing`,
      body: `The ${worst.name} failed ${times}, last ${relativeTime(worst.lastPanicAt)}${others > 0 ? `, and ${others} more with it` : ""}. The rest of the dashboard keeps working and a task that runs all the time is started again by itself, but what it does is missing while it fails. The backend's log has the stack trace to report.`,
      detail: worst.lastPanic,
    },
  ]
}

function storeProblems(store: StoreHealth | undefined): HealthProblem[] {
  if (!store || store.ok) return []
  const where = `${bytes(store.freeBytes)} of ${bytes(store.totalBytes)} is free on the disk holding the dashboard's data (${store.path}).`
  switch (store.code) {
    case "disk_full":
      return [
        {
          key: "disk_full",
          tone: "danger",
          title: "The server's disk is full",
          body: store.reserveReleased
            ? `${where} The dashboard gave up its ${bytes(store.reserveBytes, 0)} reserve so that signing in and saving keep working while you free space; that room runs out too.`
            : `${where} Sign-ins, settings and history may not be saved until space is freed.`,
          detail: store.detail,
        },
      ]
    case "store_unreadable":
      return [
        {
          key: "store_unreadable",
          tone: "danger",
          title: "The dashboard's database cannot be read",
          body: `It stopped answering ${relativeTime(store.since)}. Pages that need it fail until it is repaired or restored from a backup; the backend's log has the errors.`,
          detail: store.detail,
        },
      ]
    default:
      return [
        {
          key: store.code ?? "store_unwritable",
          tone: "danger",
          title: "The dashboard cannot save changes",
          body: `Its database has refused every write since ${relativeTime(store.since)}. Pages still read, but sign-ins, settings and history are not saved; the backend's log has the errors.`,
          detail: store.detail,
        },
      ]
  }
}
