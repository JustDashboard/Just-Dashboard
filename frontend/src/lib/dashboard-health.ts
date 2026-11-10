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

export type DashboardHealth = {
  ok: boolean
  store: StoreHealth
}

/** One thing wrong with the dashboard, in the words the Overview says it in. */
export type HealthProblem = {
  key: string
  title: string
  body: string
  /** What the subsystem underneath said, for the line under the sentence. */
  detail?: string
}

/** The problems a health report holds, worst first; none while it is unread or healthy. */
export function healthProblems(health: DashboardHealth | undefined): HealthProblem[] {
  const store = health?.store
  if (!store || store.ok) return []
  const where = `${bytes(store.freeBytes)} of ${bytes(store.totalBytes)} is free on the disk holding the dashboard's data (${store.path}).`
  switch (store.code) {
    case "disk_full":
      return [
        {
          key: "disk_full",
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
          title: "The dashboard's database cannot be read",
          body: `It stopped answering ${relativeTime(store.since)}. Pages that need it fail until it is repaired or restored from a backup; the backend's log has the errors.`,
          detail: store.detail,
        },
      ]
    default:
      return [
        {
          key: store.code ?? "store_unwritable",
          title: "The dashboard cannot save changes",
          body: `Its database has refused every write since ${relativeTime(store.since)}. Pages still read, but sign-ins, settings and history are not saved; the backend's log has the errors.`,
          detail: store.detail,
        },
      ]
  }
}
