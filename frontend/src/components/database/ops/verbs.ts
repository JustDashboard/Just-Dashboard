"use client"

import { useCallback, useMemo, useState } from "react"
import { usePathname, useRouter } from "next/navigation"
import { Archive, SettingsGear, Trash } from "@/components/icons"
import { ApiError, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { Job } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import type { Verb } from "@/components/verbs"
import { useForgetConnection } from "@/components/database/ops/settings-forget"
import { useDatabase } from "@/components/database/shell/database-context"
import type { DatabaseVerbSource, DatabaseVerbTools } from "@/components/database/shell/types"

/** How often a dump begun from the menu is asked whether it has ended. */
const EVERY_MS = 1500

/**
 * Follows a dump to its end and says how it ended. It belongs to no
 * component: the menu that began it is on every page of the database, and the
 * reader may have moved to another of them by the time the dump is done.
 */
async function reportBackup(job: Job, name: string, show: () => void) {
  let current = job
  try {
    while (current.status === "running") {
      await new Promise((resolve) => window.setTimeout(resolve, EVERY_MS))
      current = (await get<{ job: Job }>(`/jobs/${encodeURIComponent(job.id)}`)).job
    }
  } catch {
    // The job goes on without being watched; Backups shows how it ended.
    return
  }
  const action = { label: "Backups", onClick: show }
  if (current.status === "succeeded") notify.success(`Backup of ${name} finished`, { action })
  else if (current.status === "failed") {
    notify.error(`Backup of ${name} failed`, current.error ?? "The dump did not finish.", {
      action,
    })
  } else notify.info(`Backup of ${name} was stopped`, { action })
}

/**
 * What is done with the database as a thing kept — back it up now, open its
 * settings, forget the connection — as the verbs the database's menu draws on
 * every page (`shell/database-verbs.tsx`). They are declared beside the pages
 * that do the same work in full.
 *
 * A verb is in the list only when it can be used: the role holds what the
 * route asks (`service.control` for a dump; `system.admin` and `destructive`
 * to forget), the engine has dumps at all, and the server is there to dump.
 * All three are allowed on a protected connection, as the routes are.
 *
 * Back up now takes the dump the Backups page takes with nothing chosen: the
 * whole database, the tool's own defaults — and, on a server that numbers its
 * databases, the connection's own number only, as that page's form opens on.
 * A request that names none would dump every tenant of the server from one
 * press in a menu. On that page the dump is shown in place; anywhere else a
 * toast says it began, with the way there, and another says how it ended.
 */
export const useOperateVerbs: DatabaseVerbSource = ({ confirm }: DatabaseVerbTools) => {
  const { id, conn, engine, status, href, select } = useDatabase()
  const { can } = useAuth()
  const router = useRouter()
  const pathname = usePathname()
  const forget = useForgetConnection(confirm)
  const [starting, setStarting] = useState(false)

  const name = conn.name
  const backups = href("backups")
  const onBackups = pathname === backups.split("?")[0]
  const check = status.refresh
  const numbered = engine.can("dumpDatabases")
  const own = Number(conn.database || "0")

  const backUp = useCallback(async () => {
    const show = (jobId: string) => () => router.push(href("backups", { job: jobId }))
    setStarting(true)
    try {
      const job = await post<Job>(`/databases/${id}/backup`, numbered ? { databases: [own] } : {})
      // On the page that shows a dump in place, the address is what attaches it.
      if (onBackups) select({ job: job.id })
      else {
        notify.info(`Backing up ${name}`, {
          description: "The dump runs on the server.",
          action: { label: "Show", onClick: show(job.id) },
        })
        void reportBackup(job, name, show(job.id)).finally(check)
      }
    } catch (err) {
      if (err instanceof ApiError && err.code === "transfer_running") {
        notify.info(`${name} is already being dumped, restored or copied`, {
          description: "One of them runs at a time for a connection.",
          action: err.resource ? { label: "Show", onClick: show(err.resource) } : undefined,
        })
      } else notify.error(`Could not back up ${name}`, err)
    } finally {
      setStarting(false)
    }
  }, [id, name, numbered, own, onBackups, select, router, href, check])

  const mayDump = can("service.control") && engine.has("backups")
  const mayForget = can("system.admin") && can("destructive")
  const answering = status.state === "running"
  const hasSettings = engine.has("settings")

  return useMemo(() => {
    const verbs: Verb[] = []
    if (mayDump) {
      verbs.push({
        key: "backup",
        label: starting ? "Backing up…" : "Back up now",
        icon: Archive,
        group: "Database",
        progressive: "Backing up…",
        disabled: starting || !answering,
        run: () => void backUp(),
      })
    }
    if (hasSettings) {
      verbs.push({
        key: "settings",
        label: "Settings",
        icon: SettingsGear,
        group: "Database",
        run: () => router.push(href("settings")),
      })
    }
    if (mayForget) {
      verbs.push({
        key: "forget",
        label: "Forget this connection",
        icon: Trash,
        group: "Database",
        danger: true,
        run: forget,
      })
    }
    return verbs
  }, [mayDump, mayForget, hasSettings, starting, answering, backUp, forget, router, href])
}
