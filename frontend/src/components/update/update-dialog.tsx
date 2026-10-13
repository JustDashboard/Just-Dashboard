"use client"

import { useState } from "react"
import { ArrowCircleUp } from "@/components/icons"
import { notify } from "@/lib/toast"
import { useSelfUpdate } from "@/hooks/use-self-update"
import { LogoGlyph } from "@/components/logo"
import { UpdateWire } from "@/components/update/update-art"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"

/**
 * "Update to 0.7.5?" — yes or not now, and nothing else to read.
 *
 * This was the shared `ConfirmDialog`, and that dialog exists for acts that
 * are awkward to undo — most of them deletions — so its command is always
 * destructive red. An update is the opposite kind of act: the operator is
 * taking something, and the dialog asking them to confirm it wore the same
 * red as "Delete database". So it has its own, with the brand face (§3: the
 * one action on the surface).
 *
 * It asks and does not explain. A dialog that also listed the releases, the
 * breaking notes and what the updater does was too much to read for a yes or
 * a no; all of that is on the Version page, where the notice's *View patch
 * notes* goes. What is left is the picture: the installed version wired to
 * the newest on arrival's light (`.update-art`, design system §16), the way
 * the sidebar notice draws it.
 *
 * No phrase to type: the route is destructive, but installing an update is
 * not one of the unrecoverable acts that require one (security invariant 3).
 */
export function UpdateDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { report, install } = useSelfUpdate()
  const [busy, setBusy] = useState(false)
  if (!report?.latest) return null

  const target = report.latest

  const start = async () => {
    setBusy(true)
    try {
      await install("")
      onOpenChange(false)
    } catch (err) {
      notify.error(`Could not start the update to ${target}`, err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={(next) => !busy && onOpenChange(next)}
      size="sm"
      title={`Update to ${target}`}
      description={`Installs ${target} over ${report.version} and restarts the dashboard.`}
      bodyClassName="p-0"
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Not now
          </Button>
          <Button onClick={start} pending={busy}>
            <ArrowCircleUp />
            Update
          </Button>
        </>
      }
    >
      <div
        aria-hidden
        className="update-art grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2 px-5 py-6"
      >
        <VersionMark />
        <UpdateWire />
        <VersionMark next />
        <VersionLabel label="Installed" version={report.version} />
        <span />
        <VersionLabel label="Latest" version={target} next />
      </div>
    </Modal>
  )
}

/**
 * The dashboard's mark at either end of the wire: the installed version a
 * tile of plain ground with the J in grey, the newest one ringed and lit in
 * the brand's tint the way the rack is drawn for the server the reader is on.
 */
function VersionMark({ next = false }: { next?: boolean }) {
  return next ? (
    <span aria-hidden className="flex shrink-0 rounded-[0.875rem] border border-rule-brand p-[2px]">
      <span className="flex size-[2.375rem] items-center justify-center rounded-[0.625rem] bg-wash-brand text-brand">
        <LogoGlyph className="h-5 w-auto" />
      </span>
    </span>
  ) : (
    <span
      aria-hidden
      className="flex size-11 shrink-0 items-center justify-center rounded-xl border border-hairline bg-background text-muted-foreground"
    >
      <LogoGlyph className="h-5 w-auto" />
    </span>
  )
}

function VersionLabel({
  label,
  version,
  next = false,
}: {
  label: string
  version: string
  next?: boolean
}) {
  return (
    <span className={next ? "grid justify-items-end" : "grid justify-items-start"}>
      <span className="eyebrow">{label}</span>
      <span
        className={
          next
            ? "numeric text-base leading-tight font-semibold"
            : "numeric text-sm leading-tight text-muted-foreground"
        }
      >
        {version}
      </span>
    </span>
  )
}

/** Drives one UpdateDialog: the sidebar notice and the Version page each hold one. */
export function useUpdateDialog() {
  const [open, setOpen] = useState(false)
  const dialog = <UpdateDialog open={open} onOpenChange={setOpen} />
  return { openUpdate: () => setOpen(true), dialog }
}
