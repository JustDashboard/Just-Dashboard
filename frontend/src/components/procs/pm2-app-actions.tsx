"use client"

import { useCallback, useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import {
  ArrowUpDown,
  Backspace,
  Copy,
  FolderOpen,
  Play,
  RefreshClockwise,
  RotateClockwise,
  StopCircle,
  Trash,
} from "@/components/icons"
import { del, post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { PM2Process } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import type { Verb } from "@/components/verbs"
import type { ConfirmFn } from "@/components/procs/pm2-actions"
import { isCluster, type PM2App } from "@/components/procs/pm2-shared"
import { pm2AppTargets, pm2Lead, type PM2AppAction } from "@/components/procs/pm2-facts"

const WORDS: Record<PM2AppAction, { progressive: string; past: string }> = {
  start: { progressive: "Starting", past: "started" },
  reload: { progressive: "Reloading", past: "reloaded" },
  restart: { progressive: "Restarting", past: "restarted" },
  stop: { progressive: "Stopping", past: "stopped" },
  reset: { progressive: "Resetting", past: "restart counter reset" },
  flush: { progressive: "Flushing", past: "logs flushed" },
  delete: { progressive: "Deleting", past: "deleted from PM2" },
}

export type PM2AppAct = (app: PM2App, action: PM2AppAction, confirm?: string) => Promise<void>

/**
 * A verb pressed on an application's own page, sent to every worker it means.
 *
 * PM2's actions are addressed to one process id, which is right for a row of
 * the list and wrong for a page that is the application's: the sheet this
 * page replaced drew "Stop" under the name `api` and stopped one worker of
 * four, with the other three still serving. Here a verb goes to each worker
 * in turn through the same per-process routes — so a reload is the rolling
 * reload `pm2 reload api` is, and each call is audited as it always was — and
 * stops at the first that fails, saying which. A worker's own verbs stay on
 * its row under Instances.
 */
export function usePM2AppControl(onChanged?: () => void) {
  const [pending, setPending] = useState<string>()

  const act = useCallback<PM2AppAct>(
    async (app, action, confirm) => {
      const targets = pm2AppTargets(app, action)
      setPending(WORDS[action].progressive)
      let at: PM2Process | undefined
      try {
        for (const worker of targets) {
          at = worker
          const options = { confirm, query: { user: worker.daemonId, id: worker.id } }
          if (action === "delete") await del(`/pm2/${encodeURIComponent(worker.name)}`, options)
          else await post(`/pm2/${encodeURIComponent(worker.name)}/${action}`, undefined, options)
        }
        notify.success(`${app.name} ${WORDS[action].past}`)
      } catch (err) {
        notify.error(
          targets.length > 1 && at
            ? `Could not ${action} ${app.name} #${at.id}`
            : `Could not ${action} ${app.name}`,
          err,
        )
        throw err
      } finally {
        setPending(undefined)
        // Whether or not every worker took it, some may have.
        onChanged?.()
      }
    },
    [onChanged],
  )

  return { pending, act }
}

/**
 * Every verb this operator may use on the application, in the order they are
 * wanted: the one that changes whether it is serving first — it is the page's
 * one command — then the rest, and the one that removes it last.
 */
export function usePM2AppVerbs({
  app,
  confirm,
  act,
  busy,
  onScale,
  onDeleted,
}: {
  app: PM2App
  confirm: ConfirmFn
  act: PM2AppAct
  /** A verb is in flight: a second one would cross it on the same workers. */
  busy?: boolean
  onScale: () => void
  /** The application is gone from PM2: its page has nothing left to show. */
  onDeleted: () => void
}): Verb[] {
  const { can } = useAuth()
  const router = useRouter()

  return useMemo(() => {
    const verbs: Verb[] = []
    const lead = pm2Lead(app)
    const cluster = isCluster(lead)
    const count = app.instances.length
    const anyOnline = app.online > 0
    const restarts = app.instances.reduce((n, p) => n + p.restarts + p.unstableRestarts, 0)
    // "api" for one process, "All 4 workers of api" where the verb reaches several.
    const subject =
      count > 1 ? (
        <>
          All {count} workers of <b>{app.name}</b>
        </>
      ) : (
        <b>{app.name}</b>
      )
    const several = count > 1

    if (can("service.control")) {
      if (app.online < count) {
        verbs.push({
          key: "start",
          disabled: busy,
          progressive: "Starting",
          label: "Start",
          icon: Play,
          inline: true,
          run: () => void act(app, "start").catch(() => undefined),
        })
      }
      if (anyOnline) {
        verbs.push({
          key: "reload",
          disabled: busy,
          progressive: "Reloading",
          label: "Reload",
          icon: RotateClockwise,
          inline: true,
          run: () => void act(app, "reload").catch(() => undefined),
        })
      }
    }

    if (can("destructive")) {
      verbs.push({
        key: "restart",
        disabled: busy,
        progressive: "Restarting",
        label: "Restart",
        icon: RefreshClockwise,
        run: () =>
          confirm({
            title: "Restart application",
            confirmLabel: "Restart",
            description: (
              <p>
                {subject} {several ? "stop and start" : "stops and starts"} again, and{" "}
                {several ? "each" : "it"} briefly stops serving. A reload does the same without the
                gap.
              </p>
            ),
            action: (phrase) => act(app, "restart", phrase),
          }),
      })
      if (anyOnline) {
        verbs.push({
          key: "stop",
          disabled: busy,
          progressive: "Stopping",
          label: "Stop",
          icon: StopCircle,
          run: () =>
            confirm({
              title: "Stop application",
              confirmLabel: "Stop",
              description: (
                <p>
                  {subject} {several ? "stop" : "stops"} serving until started again. Nothing is
                  removed.
                </p>
              ),
              action: (phrase) => act(app, "stop", phrase),
            }),
        })
      }
    }

    if (can("service.control") && cluster) {
      verbs.push({ key: "scale", label: "Scale…", icon: ArrowUpDown, run: onScale })
    }

    if (can("service.control") && restarts > 0) {
      verbs.push({
        key: "reset",
        disabled: busy,
        label: "Reset restart counter",
        icon: Backspace,
        run: () => void act(app, "reset").catch(() => undefined),
      })
    }

    if (can("destructive") && lead.logsAvailable !== false) {
      verbs.push({
        key: "flush",
        disabled: busy,
        label: "Flush logs",
        icon: Trash,
        run: () =>
          confirm({
            title: "Flush logs",
            confirmLabel: "Flush",
            description: (
              <p>
                The log files of <b>{app.name}</b> are truncated to nothing. Rotated copies are
                untouched.
              </p>
            ),
            action: (phrase) => act(app, "flush", phrase),
          }),
      })
    }

    if (lead.cwd) {
      verbs.push({
        key: "cwd",
        label: "Open working directory",
        icon: FolderOpen,
        run: () => router.push(`/files?path=${encodeURIComponent(lead.cwd)}`),
      })
    }
    if (lead.scriptPath) {
      verbs.push({
        key: "copy-script",
        label: "Copy script path",
        icon: Copy,
        run: () => void copyText(lead.scriptPath, "Script path copied"),
      })
    }

    if (can("destructive")) {
      verbs.push({
        key: "delete",
        disabled: busy,
        label: "Delete from PM2",
        icon: Trash,
        danger: true,
        run: () =>
          confirm({
            title: "Delete from PM2",
            confirmLabel: "Delete",
            description: (
              <p>
                Removes <b>{app.name}</b>
                {several ? ` and its ${plural(count, "worker")}` : ""} from PM2 entirely. Files on
                disk are untouched, but the process definition is gone, and so is it from the saved
                startup list once that is saved again.
              </p>
            ),
            action: async (phrase) => {
              await act(app, "delete", phrase)
              onDeleted()
            },
          }),
      })
    }

    return verbs
  }, [app, can, confirm, act, busy, onScale, onDeleted, router])
}
