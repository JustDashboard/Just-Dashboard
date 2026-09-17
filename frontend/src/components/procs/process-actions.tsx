"use client"

import { useCallback, useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import {
  ArrowUpRight,
  Copy,
  FolderOpen,
  Inspect,
  Pause,
  Play,
  RefreshClockwise,
  Stop,
  StopCircle,
} from "@/components/icons"
import { post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { notify } from "@/lib/toast"
import type { ProcessRow } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import type { Verb } from "@/components/verbs"
import { managerHref, managerName, processKey } from "@/components/procs/shared"

export type ConfirmFn = (request: ConfirmRequest) => void

/** Which process is mid-signal, and what it is doing. */
export type PendingMap = Record<string, string>

/**
 * The signal calls, with the row's own "this is happening" state attached.
 *
 * A SIGTERM takes a process as long as it takes to shut down, and the table
 * keeps reporting it as sleeping until the next poll. Without this the row
 * answers a press by sitting still, which is why anybody presses Kill next.
 */
export function useProcessControl(onChanged?: () => void) {
  const [pending, setPending] = useState<PendingMap>({})

  const signal = useCallback(
    async (process: ProcessRow, sig: string, progressive: string, confirmText?: string) => {
      const key = processKey(process)
      setPending((p) => ({ ...p, [key]: progressive }))
      try {
        await post(
          `/processes/${process.pid}/signal`,
          { signal: sig, startedAt: process.createTime },
          { confirm: confirmText },
        )
        notify.success(`${sig} sent to ${process.name} (${process.pid})`)
        onChanged?.()
      } catch (err) {
        notify.error(`Could not signal ${process.name}`, err)
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

  return { pending, signal }
}

/**
 * Every verb this operator may use on this process, in the order they are
 * wanted: look at it, ask it to stop, make it stop, freeze it, then the ways
 * of carrying its identity somewhere else.
 *
 * Nothing here is inline in a table row. A process table is not a list of
 * things one restarts every morning — the daily verb is *reading* it — and a
 * red stop glyph beside every one of four hundred rows is four hundred
 * invitations to end something by mis-click. The sheet draws the first two as
 * named buttons, where the width exists for the word.
 */
export function useProcessVerbs({
  process,
  confirm,
  signal,
  onInspect,
}: {
  process: ProcessRow
  confirm: ConfirmFn
  signal: (process: ProcessRow, sig: string, progressive: string, confirm?: string) => Promise<void>
  /** Opens the detail sheet; absent inside the sheet itself. */
  onInspect?: () => void
}): Verb[] {
  const { can } = useAuth()
  const router = useRouter()

  return useMemo(() => {
    const verbs: Verb[] = []
    // Inline only in the sheet, where the width exists for the word; a row
    // in the table keeps every verb behind its menu.
    const inline = !onInspect
    const stopped = process.state === "stopped"
    const label = `${process.name} (${process.pid})`
    const supervisor = process.managerName
      ? `${managerName(process.manager)} — ${process.managerName}`
      : null

    if (onInspect) {
      verbs.push({
        key: "inspect",
        label: "Inspect",
        detail: "Resources, open ports, the parent chain and its children.",
        icon: Inspect,
        run: onInspect,
      })
    }

    if (can("destructive")) {
      verbs.push({
        key: "term",
        progressive: "Terminating",
        label: "Terminate",
        detail: "Asks it to exit cleanly (SIGTERM). A supervised process comes straight back.",
        icon: StopCircle,
        inline,
        run: () =>
          confirm({
            title: "Terminate process",
            confirmLabel: "Terminate",
            description: (
              <>
                <p>
                  <b>{label}</b> is asked to shut down. Most programs finish what they are doing and
                  exit; one that ignores the request is still running afterwards, and Kill is the
                  next step.
                </p>
                {supervisor && (
                  <p>
                    It is supervised by {supervisor}, which will start it again. To stop it for
                    good, stop it there.
                  </p>
                )}
              </>
            ),
            action: (phrase) => signal(process, "SIGTERM", "Terminating", phrase),
          }),
      })
      verbs.push({
        key: "kill",
        progressive: "Killing",
        label: "Kill",
        detail: "Ends it this instant with no chance to clean up (SIGKILL).",
        icon: Stop,
        inline,
        danger: true,
        run: () =>
          confirm({
            title: "Kill process",
            confirmLabel: "Kill",
            description: (
              <p>
                <b>{label}</b> is ended by the kernel immediately. Anything it had not written is
                lost, and files it held may be left half-written. Use it for a process that did not
                answer Terminate.
              </p>
            ),
            action: (phrase) => signal(process, "SIGKILL", "Killing", phrase),
          }),
      })
      if (stopped) {
        verbs.push({
          key: "cont",
          progressive: "Resuming",
          label: "Resume",
          detail: "Lets a paused process carry on where it was (SIGCONT).",
          icon: Play,
          run: () => void signal(process, "SIGCONT", "Resuming").catch(() => undefined),
        })
      } else {
        verbs.push({
          key: "stop",
          progressive: "Pausing",
          label: "Pause",
          detail: "Freezes it in place without ending it (SIGSTOP). Resume undoes it.",
          icon: Pause,
          run: () =>
            confirm({
              title: "Pause process",
              confirmLabel: "Pause",
              description: (
                <p>
                  <b>{label}</b> stops being scheduled until it is resumed. Anything waiting on it —
                  a request, a client, a parent — waits too.
                </p>
              ),
              action: (phrase) => signal(process, "SIGSTOP", "Pausing", phrase),
            }),
        })
      }
      verbs.push({
        key: "hup",
        progressive: "Reloading",
        label: "Hang up",
        detail: "SIGHUP: many daemons re-read their configuration; a shell job exits.",
        icon: RefreshClockwise,
        run: () =>
          confirm({
            title: "Send SIGHUP",
            confirmLabel: "Send",
            description: (
              <p>
                <b>{label}</b> receives a hang-up. nginx, sshd and most daemons reload their
                configuration on it; a program that does not handle it exits.
              </p>
            ),
            action: (phrase) => signal(process, "SIGHUP", "Reloading", phrase),
          }),
      })
    }

    const owner = managerHref(process)
    if (owner) {
      verbs.push({
        key: "owner",
        label: `Open ${process.managerName}`,
        detail:
          process.manager === "container"
            ? "The container this runs in, where stopping and restarting live."
            : `The ${managerName(process.manager)} entry that supervises it.`,
        icon: ArrowUpRight,
        run: () => router.push(owner),
      })
    }
    if (process.cwd) {
      verbs.push({
        key: "cwd",
        label: "Open working directory",
        detail: process.cwd,
        icon: FolderOpen,
        run: () => router.push(`/files?path=${encodeURIComponent(process.cwd ?? "/")}`),
      })
    }
    verbs.push({
      key: "copy-pid",
      label: "Copy PID",
      detail: `${process.pid} — for kill, strace, or a search on the Security page.`,
      icon: Copy,
      run: () => void copyText(String(process.pid), "PID copied"),
    })
    if (process.cmdline) {
      verbs.push({
        key: "copy-cmd",
        label: "Copy command line",
        detail: "Exactly how it was started, arguments included.",
        icon: Copy,
        run: () => void copyText(process.cmdline, "Command line copied"),
      })
    }
    return verbs
  }, [process, can, confirm, signal, onInspect, router])
}
