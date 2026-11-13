"use client"

import { useCallback, useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import {
  Backspace,
  Copy,
  Cpu,
  FileText,
  Lightning,
  LockClosed,
  LockOpen,
  Logs,
  Pause,
  Play,
  RefreshClockwise,
  RotateClockwise,
  Slash,
  StopCircle,
} from "@/components/icons"
import { post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { notify } from "@/lib/toast"
import type { SystemdUnit } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import type { Verb } from "@/components/verbs"

export type ConfirmFn = (request: ConfirmRequest) => void
export type PendingMap = Record<string, string>

const PAST: Record<string, string> = {
  start: "started",
  stop: "stopped",
  restart: "restarted",
  reload: "reloaded",
  enable: "enabled on boot",
  disable: "disabled on boot",
  "reset-failed": "failed state cleared",
  mask: "masked",
  unmask: "unmasked",
  freeze: "frozen",
  thaw: "thawed",
}

/**
 * The unit calls, with the row's own "this is happening" state attached. A
 * stop waits for the service's own shutdown, up to systemd's timeout, and
 * `list-units` reports `deactivating` only once it gets round to it.
 */
export function useUnitControl(onChanged?: () => void) {
  const [pending, setPending] = useState<PendingMap>({})

  const act = useCallback(
    async (unit: SystemdUnit, action: string, progressive: string, confirmText?: string) => {
      setPending((p) => ({ ...p, [unit.name]: progressive }))
      try {
        await post(`/systemd/${encodeURIComponent(unit.name)}/${action}`, undefined, {
          confirm: confirmText,
        })
        notify.success(`${unit.name} ${PAST[action] ?? action}`)
        onChanged?.()
      } catch (err) {
        notify.error(`Could not ${action} ${unit.name}`, err)
        throw err
      } finally {
        setPending((p) => {
          const next = { ...p }
          delete next[unit.name]
          return next
        })
      }
    },
    [onChanged],
  )

  return { pending, act }
}

/**
 * Every verb this operator may use on this unit. `canReload` is only known
 * from `systemctl show`, so the list row leaves that verb out and the sheet
 * offers it; a reload sent to a unit without one fails with systemd's own
 * words, which is worse than not offering it.
 */
export function useUnitVerbs({
  unit,
  confirm,
  act,
  canReload,
  onOpenTab,
  properties,
  onDaemonReload,
}: {
  unit: SystemdUnit
  confirm: ConfirmFn
  act: (unit: SystemdUnit, action: string, progressive: string, confirm?: string) => Promise<void>
  canReload?: boolean
  /** Opens the unit's page at a named view. */
  onOpenTab?: (tab: string) => void
  /**
   * Its `systemctl show` properties, which the unit's own page has: with
   * them the verbs only that read can offer join the list — freezing (when
   * its cgroup can be frozen), masking, re-reading a changed unit file and
   * the way to its main process.
   */
  properties?: Record<string, string>
  /** Re-reads every unit file (`daemon-reload`), offered when this one changed on disk. */
  onDaemonReload?: () => void
}): Verb[] {
  const { can } = useAuth()
  const router = useRouter()

  return useMemo(() => {
    const verbs: Verb[] = []
    const active = unit.activeState === "active"
    const failed = unit.activeState === "failed"
    const startup = unit.unitFileState

    if (can("service.control") && !active) {
      verbs.push({
        key: "start",
        progressive: "Starting",
        label: "Start",
        icon: Play,
        inline: true,
        run: () => void act(unit, "start", "Starting").catch(() => undefined),
      })
    }
    if (can("destructive")) {
      verbs.push({
        key: "restart",
        progressive: "Restarting",
        label: "Restart",
        icon: RotateClockwise,
        inline: true,
        run: () =>
          confirm({
            title: "Restart unit",
            confirmLabel: "Restart",
            description: (
              <p>
                <b>{unit.name}</b> restarts, interrupting whatever it serves for as long as it takes
                to come back.
              </p>
            ),
            action: (phrase) => act(unit, "restart", "Restarting", phrase),
          }),
      })
      if (active) {
        verbs.push({
          key: "stop",
          progressive: "Stopping",
          label: "Stop",
          icon: StopCircle,
          inline: true,
          run: () =>
            confirm({
              title: "Stop unit",
              confirmLabel: "Stop",
              description: (
                <p>
                  <b>{unit.name}</b> stops until started again. Its startup setting is unchanged, so
                  it still comes back after a reboot if it is enabled.
                </p>
              ),
              action: (phrase) => act(unit, "stop", "Stopping", phrase),
            }),
        })
      }
    }

    if (onOpenTab) {
      verbs.push({
        key: "journal",
        label: "Journal",
        icon: Logs,
        run: () => onOpenTab("journal"),
      })
    }

    if (can("service.control") && active && canReload) {
      verbs.push({
        key: "reload",
        progressive: "Reloading",
        label: "Reload configuration",
        icon: RefreshClockwise,
        run: () => void act(unit, "reload", "Reloading").catch(() => undefined),
      })
    }

    if (can("service.control") && failed) {
      verbs.push({
        key: "reset-failed",
        label: "Clear failed state",
        icon: Backspace,
        run: () => void act(unit, "reset-failed", "Clearing").catch(() => undefined),
      })
    }

    if (properties) {
      const freezer = properties.FreezerState
      if (can("service.control") && (freezer === "frozen" || freezer === "freezing")) {
        verbs.push({
          key: "thaw",
          progressive: "Thawing",
          label: "Thaw",
          icon: Play,
          group: "Pause",
          run: () => void act(unit, "thaw", "Thawing").catch(() => undefined),
        })
      } else if (
        can("destructive") &&
        active &&
        properties.CanFreeze === "yes" &&
        freezer === "running"
      ) {
        verbs.push({
          key: "freeze",
          progressive: "Freezing",
          label: "Freeze",
          icon: Pause,
          group: "Pause",
          run: () =>
            confirm({
              title: "Freeze unit",
              confirmLabel: "Freeze",
              description: (
                <p>
                  Every process in <b>{unit.name}</b> is paused by its cgroup until it is thawed. It
                  keeps its memory and its connections, and answers nothing in the meantime.
                </p>
              ),
              action: (phrase) => act(unit, "freeze", "Freezing", phrase),
            }),
        })
      }
    }

    if (can("system.admin") && ["enabled", "enabled-runtime", "disabled"].includes(startup)) {
      verbs.push(
        unit.enabled
          ? {
              key: "disable",
              group: properties ? "Startup" : undefined,
              label: "Disable on boot",
              icon: Slash,
              run: () => void act(unit, "disable", "Disabling").catch(() => undefined),
            }
          : {
              key: "enable",
              group: properties ? "Startup" : undefined,
              label: "Enable on boot",
              icon: Lightning,
              run: () => void act(unit, "enable", "Enabling").catch(() => undefined),
            },
      )
    }

    if (properties) {
      // A transient or generated unit has no file of its own to link away.
      const maskable = ["enabled", "enabled-runtime", "disabled", "static", "indirect"]
      if (can("system.admin") && (startup === "masked" || startup === "masked-runtime")) {
        verbs.push({
          key: "unmask",
          label: "Unmask",
          icon: LockOpen,
          group: "Startup",
          run: () => void act(unit, "unmask", "Unmasking").catch(() => undefined),
        })
      } else if (can("system.admin") && can("destructive") && maskable.includes(startup)) {
        verbs.push({
          key: "mask",
          label: "Mask",
          icon: LockClosed,
          group: "Startup",
          run: () =>
            confirm({
              title: "Mask unit",
              confirmLabel: "Mask",
              description: (
                <p>
                  <b>{unit.name}</b> is linked to /dev/null, so nothing can start it — not by hand,
                  not another unit, not a boot — until it is unmasked.
                  {active ? " It keeps running until it is stopped." : ""}
                </p>
              ),
              action: (phrase) => act(unit, "mask", "Masking", phrase),
            }),
        })
      }
      if (onDaemonReload && can("system.admin") && properties.NeedDaemonReload === "yes") {
        verbs.push({
          key: "daemon-reload",
          label: "Reload unit files",
          icon: RefreshClockwise,
          run: onDaemonReload,
        })
      }
      if (unit.mainPid) {
        const pid = unit.mainPid
        verbs.push({
          key: "main",
          label: `Open its main process, ${pid}`,
          icon: Cpu,
          run: () => router.push(`/processes/${pid}`),
        })
      }
    }

    if (unit.fragmentPath) {
      verbs.push({
        key: "file",
        label: "Open unit file",
        icon: FileText,
        run: () => router.push(`/files?path=${encodeURIComponent(unit.fragmentPath ?? "/")}`),
      })
    }
    verbs.push({
      key: "copy",
      label: "Copy unit name",
      icon: Copy,
      run: () => void copyText(unit.name, "Unit name copied"),
    })
    return verbs
  }, [unit, can, confirm, act, canReload, onOpenTab, properties, onDaemonReload, router])
}
