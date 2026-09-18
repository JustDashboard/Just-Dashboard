"use client"

import { useCallback, useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import {
  ArrowUpDown,
  Backspace,
  Copy,
  FloppyDisk,
  FolderOpen,
  Logs,
  Play,
  RefreshClockwise,
  RotateClockwise,
  StopCircle,
  Trash,
} from "@/components/icons"
import { del, post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { notify } from "@/lib/toast"
import type { PM2Daemon, PM2Process } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import type { Verb } from "@/components/verbs"

export type ConfirmFn = (request: ConfirmRequest) => void
export type PendingMap = Record<string, string>

/** Names and numeric ids are per daemon; the pair is the identity. */
export function pm2Key(process: Pick<PM2Process, "daemonId" | "id">): string {
  return `${process.daemonId}:${process.id}`
}

const PAST: Record<string, string> = {
  start: "started",
  stop: "stopped",
  restart: "restarted",
  reload: "reloaded",
  reset: "restart counter reset",
  flush: "logs flushed",
}

/**
 * The lifecycle calls, with the row's own "this is happening" state attached.
 * A reload of a cluster takes as long as the slowest worker's start-up, and
 * `pm2 jlist` reports the old state until it is done.
 */
export function usePM2Control(onChanged?: () => void) {
  const [pending, setPending] = useState<PendingMap>({})

  const act = useCallback(
    async (process: PM2Process, action: string, progressive: string, confirmText?: string) => {
      const key = pm2Key(process)
      setPending((p) => ({ ...p, [key]: progressive }))
      try {
        await post(`/pm2/${encodeURIComponent(process.name)}/${action}`, undefined, {
          confirm: confirmText,
          query: { user: process.daemonId, id: process.id },
        })
        notify.success(`${process.name} ${PAST[action] ?? action}`)
        onChanged?.()
      } catch (err) {
        notify.error(`Could not ${action} ${process.name}`, err)
        throw err
      } finally {
        setPending((p) => {
          const next = { ...p }
          delete next[key]
          return next
        })
      }
    },
    [onChanged],
  )

  return { pending, act }
}

/**
 * Every verb this operator may use on this application, in the order they
 * are wanted: the one that changes whether it is serving, the two ways of
 * looking at it, the housekeeping, and the one that removes it.
 */
export function usePM2Verbs({
  process,
  confirm,
  act,
  onOpenTab,
  onScale,
  onChanged,
}: {
  process: PM2Process
  confirm: ConfirmFn
  act: (process: PM2Process, action: string, progressive: string, confirm?: string) => Promise<void>
  /** Opens the detail sheet at a named tab; absent inside the sheet. */
  onOpenTab?: (tab: string) => void
  /** Opens the scale dialog. */
  onScale?: () => void
  onChanged?: () => void
}): Verb[] {
  const { can } = useAuth()
  const router = useRouter()

  return useMemo(() => {
    const verbs: Verb[] = []
    const online = process.status === "online"
    const cluster = process.execMode === "cluster_mode" || process.execMode === "cluster"

    if (can("service.control")) {
      if (!online) {
        verbs.push({
          key: "start",
          progressive: "Starting",
          label: "Start",
          detail: "Runs it again with the settings it was registered with.",
          icon: Play,
          inline: true,
          run: () => void act(process, "start", "Starting").catch(() => undefined),
        })
      } else {
        verbs.push({
          key: "reload",
          progressive: "Reloading",
          label: "Reload",
          detail: cluster
            ? "Replaces the workers one at a time, so nothing stops serving."
            : "Starts the new process before stopping the old one where it can.",
          icon: RotateClockwise,
          inline: true,
          run: () => void act(process, "reload", "Reloading").catch(() => undefined),
        })
      }
    }

    if (can("destructive")) {
      verbs.push({
        key: "restart",
        progressive: "Restarting",
        label: "Restart",
        detail: "Stops it and starts it again. Whatever it serves is interrupted.",
        icon: RefreshClockwise,
        inline: true,
        run: () =>
          confirm({
            title: "Restart application",
            confirmLabel: "Restart",
            description: (
              <p>
                <b>{process.name}</b> stops and starts again, and briefly stops serving. A reload
                does the same without the gap.
              </p>
            ),
            action: (phrase) => act(process, "restart", "Restarting", phrase),
          }),
      })
      if (online) {
        verbs.push({
          key: "stop",
          progressive: "Stopping",
          label: "Stop",
          detail: "Stops it. It stays in PM2's list and can be started again.",
          icon: StopCircle,
          inline: true,
          run: () =>
            confirm({
              title: "Stop application",
              confirmLabel: "Stop",
              description: (
                <p>
                  <b>{process.name}</b> stops serving until it is started again. Nothing is removed.
                </p>
              ),
              action: (phrase) => act(process, "stop", "Stopping", phrase),
            }),
        })
      }
    }

    if (onOpenTab) {
      verbs.push({
        key: "logs",
        label: "Logs",
        detail: "stdout and stderr, merged and live. The first place to look.",
        icon: Logs,
        run: () => onOpenTab("logs"),
      })
    }

    if (can("service.control") && cluster && onScale) {
      verbs.push({
        key: "scale",
        label: "Scale…",
        detail: `${process.instances || 1} ${process.instances === 1 ? "instance" : "instances"} now. Add or remove workers without a restart.`,
        icon: ArrowUpDown,
        run: onScale,
      })
    }

    if (can("service.control") && (process.restarts > 0 || process.unstableRestarts > 0)) {
      verbs.push({
        key: "reset",
        label: "Reset restart counter",
        detail: `Back to zero from ${process.restarts}, so the next crash is not lost in old history.`,
        icon: Backspace,
        run: () => void act(process, "reset", "Resetting").catch(() => undefined),
      })
    }

    if (can("destructive") && process.logsAvailable !== false) {
      verbs.push({
        key: "flush",
        label: "Flush logs",
        detail: "Empties its stdout and stderr files. What was in them is gone.",
        icon: Trash,
        run: () =>
          confirm({
            title: "Flush logs",
            confirmLabel: "Flush",
            description: (
              <p>
                The log files of <b>{process.name}</b> are truncated to nothing. Rotated copies are
                untouched.
              </p>
            ),
            action: (phrase) => act(process, "flush", "Flushing", phrase),
          }),
      })
    }

    if (process.cwd) {
      verbs.push({
        key: "cwd",
        label: "Open working directory",
        detail: process.cwd,
        icon: FolderOpen,
        run: () => router.push(`/files?path=${encodeURIComponent(process.cwd)}`),
      })
    }
    if (process.scriptPath) {
      verbs.push({
        key: "copy-script",
        label: "Copy script path",
        detail: process.scriptPath,
        icon: Copy,
        run: () => void copyText(process.scriptPath, "Script path copied"),
      })
    }

    if (can("destructive")) {
      verbs.push({
        key: "delete",
        label: "Delete from PM2",
        detail: "Stops it and forgets it. Files on disk are untouched.",
        icon: Trash,
        danger: true,
        run: () =>
          confirm({
            title: "Delete from PM2",
            confirmLabel: "Delete",
            description: (
              <p>
                Removes <b>{process.name}</b> from PM2 entirely. Files on disk are untouched, but
                the process definition is gone — and so is it from the saved startup list once that
                is saved again.
              </p>
            ),
            action: async (phrase) => {
              await del(`/pm2/${encodeURIComponent(process.name)}`, {
                confirm: phrase,
                query: { user: process.daemonId, id: process.id },
              })
              notify.success(`${process.name} deleted from PM2`)
              onChanged?.()
            },
          }),
      })
    }

    return verbs
  }, [process, can, confirm, act, onOpenTab, onScale, onChanged, router])
}

/**
 * The verbs that apply to a whole daemon rather than one application: the
 * startup list and the all-at-once lifecycle. One account at a time, because
 * "restart everything" across accounts is a sentence nobody means literally.
 */
export function useDaemonVerbs({
  daemons,
  confirm,
  onChanged,
}: {
  daemons: PM2Daemon[]
  confirm: ConfirmFn
  onChanged?: () => void
}): Verb[] {
  const { can } = useAuth()
  return useMemo(() => {
    const verbs: Verb[] = []
    const several = daemons.length > 1
    const suffix = (d: PM2Daemon) => (several ? ` (${d.account})` : "")

    if (can("service.control")) {
      verbs.push({
        key: "save",
        label: "Save startup list",
        detail: "Writes what runs now as what comes back after a reboot, for every account.",
        icon: FloppyDisk,
        run: () =>
          void post("/pm2/save")
            .then(() => {
              notify.success("PM2 startup list saved")
              onChanged?.()
            })
            .catch((err) => notify.error("Could not save the startup list", err)),
      })
      for (const daemon of daemons) {
        verbs.push({
          key: `reload-all:${daemon.account}`,
          label: `Reload all${suffix(daemon)}`,
          detail: "Every application, workers replaced one at a time where the mode allows.",
          icon: RotateClockwise,
          run: () =>
            void post(`/pm2/daemons/${encodeURIComponent(daemon.account)}/reload`)
              .then(() => {
                notify.success(`Reloaded every application of ${daemon.account}`)
                onChanged?.()
              })
              .catch((err) => notify.error("Could not reload", err)),
        })
      }
    }
    if (can("destructive")) {
      for (const daemon of daemons) {
        verbs.push({
          key: `restart-all:${daemon.account}`,
          label: `Restart all${suffix(daemon)}`,
          detail: "Every application stops and starts again. Everything is interrupted.",
          icon: RefreshClockwise,
          run: () =>
            confirm({
              title: "Restart every application",
              confirmLabel: "Restart all",
              description: (
                <p>
                  Every application under <b>{daemon.account}</b>&apos;s PM2 restarts, and all of
                  them briefly stop serving.
                </p>
              ),
              action: async (phrase) => {
                await post(
                  `/pm2/daemons/${encodeURIComponent(daemon.account)}/restart`,
                  undefined,
                  {
                    confirm: phrase,
                  },
                )
                notify.success(`Restarted every application of ${daemon.account}`)
                onChanged?.()
              },
            }),
        })
      }
      for (const daemon of daemons) {
        verbs.push({
          key: `stop-all:${daemon.account}`,
          label: `Stop all${suffix(daemon)}`,
          detail: "Every application stops. They stay in the list and can be started again.",
          icon: StopCircle,
          danger: true,
          run: () =>
            confirm({
              title: "Stop every application",
              confirmLabel: "Stop all",
              description: (
                <p>
                  Every application under <b>{daemon.account}</b>&apos;s PM2 stops serving until
                  started again.
                </p>
              ),
              action: async (phrase) => {
                await post(`/pm2/daemons/${encodeURIComponent(daemon.account)}/stop`, undefined, {
                  confirm: phrase,
                })
                notify.success(`Stopped every application of ${daemon.account}`)
                onChanged?.()
              },
            }),
        })
      }
    }
    return verbs
  }, [daemons, can, confirm, onChanged])
}
