"use client"

import { useCallback, useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import {
  Backspace,
  Copy,
  FileText,
  Logs,
  Play,
  RefreshClockwise,
  RotateClockwise,
  StopCircle,
  Lightning,
  Slash,
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
}: {
  unit: SystemdUnit
  confirm: ConfirmFn
  act: (unit: SystemdUnit, action: string, progressive: string, confirm?: string) => Promise<void>
  canReload?: boolean
  /** Opens the detail sheet at a named tab; absent inside the sheet. */
  onOpenTab?: (tab: string) => void
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
        detail: "Runs it now. Whether it also runs after a reboot is the startup setting.",
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
        detail: "Stops it and starts it again. Whatever it serves is interrupted.",
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
          detail: "Shuts it down. It stays installed and can be started again.",
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
        detail: "What it has logged, live. The first place to look when it fails.",
        icon: Logs,
        run: () => onOpenTab("journal"),
      })
    }

    if (can("service.control") && active && canReload) {
      verbs.push({
        key: "reload",
        progressive: "Reloading",
        label: "Reload configuration",
        detail: "Asks it to re-read its configuration without stopping.",
        icon: RefreshClockwise,
        run: () => void act(unit, "reload", "Reloading").catch(() => undefined),
      })
    }

    if (can("service.control") && failed) {
      verbs.push({
        key: "reset-failed",
        label: "Clear failed state",
        detail: "Marks it inactive rather than failed, without starting it.",
        icon: Backspace,
        run: () => void act(unit, "reset-failed", "Clearing").catch(() => undefined),
      })
    }

    if (can("system.admin") && ["enabled", "enabled-runtime", "disabled"].includes(startup)) {
      verbs.push(
        unit.enabled
          ? {
              key: "disable",
              label: "Disable on boot",
              detail: "Stays as it is now, but does not start after the next reboot.",
              icon: Slash,
              run: () => void act(unit, "disable", "Disabling").catch(() => undefined),
            }
          : {
              key: "enable",
              label: "Enable on boot",
              detail: "Starts after every reboot. Does not start it now.",
              icon: Lightning,
              run: () => void act(unit, "enable", "Enabling").catch(() => undefined),
            },
      )
    }

    if (unit.fragmentPath) {
      verbs.push({
        key: "file",
        label: "Open unit file",
        detail: unit.fragmentPath,
        icon: FileText,
        run: () => router.push(`/files?path=${encodeURIComponent(unit.fragmentPath ?? "/")}`),
      })
    }
    verbs.push({
      key: "copy",
      label: "Copy unit name",
      detail: `${unit.name} — for systemctl and journalctl.`,
      icon: Copy,
      run: () => void copyText(unit.name, "Unit name copied"),
    })
    return verbs
  }, [unit, can, confirm, act, canReload, onOpenTab, router])
}
