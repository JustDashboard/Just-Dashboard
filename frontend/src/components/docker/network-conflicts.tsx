"use client"

import { useState } from "react"
import { errorMessage } from "@/lib/api"
import { notify } from "@/lib/toast"
import {
  isChangePreview,
  orderedConflicts,
  type NetworkChangePreview,
  type NetworkConflict,
} from "@/lib/docker-networks"
import { usePoll } from "@/hooks/use-poll"
import { LoadingRows } from "@/components/state"
import { StatusDot, type DotTone } from "@/components/status-dot"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { Hint } from "./explain"

const LEVEL_TONE: Record<NetworkConflict["level"], DotTone> = {
  block: "danger",
  warn: "warning",
  info: "notice",
}

const LEVEL_WORD: Record<NetworkConflict["level"], string> = {
  block: "Refused",
  warn: "Confirm",
  info: "Note",
}

/**
 * What a change would run into, one line each, refusals first. A refusal is
 * the backend's as well as this list's, so the button below it is disabled
 * for the same reason the request would fail.
 */
export function ConflictList({
  conflicts,
  emptyLabel,
}: {
  conflicts: NetworkConflict[]
  emptyLabel: string
}) {
  if (conflicts.length === 0) return <Hint>{emptyLabel}</Hint>
  return (
    <ul aria-label="What this change runs into" className="space-y-2">
      {orderedConflicts(conflicts).map((conflict, index) => (
        <li key={`${conflict.code}:${index}`} className="flex items-start gap-2 text-hint">
          <StatusDot tone={LEVEL_TONE[conflict.level]} className="mt-1.5" />
          <span className="min-w-0 leading-relaxed">
            <span className="sr-only">{LEVEL_WORD[conflict.level]}: </span>
            {conflict.message}
          </span>
        </li>
      ))}
    </ul>
  )
}

/**
 * A network change read before it is made. The preview is fetched afresh
 * each time the dialog opens, for the target `target` names; nothing changes
 * until the confirmation, and a refused or unread preview leaves the
 * confirmation disabled.
 */
export function NetworkChangeDialog({
  open,
  onOpenChange,
  target,
  title,
  description,
  intro,
  confirmLabel,
  emptyLabel,
  load,
  onConfirm,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** What the preview is of; a different target never shows another's preview. */
  target: string
  title: string
  description: string
  intro?: React.ReactNode
  confirmLabel: string
  emptyLabel: string
  load: (signal: AbortSignal) => Promise<unknown>
  onConfirm: (preview: NetworkChangePreview) => Promise<void>
}) {
  const reading = usePoll<unknown>(load, 0, [open, target], { enabled: open })
  const [busy, setBusy] = useState(false)
  const preview = isChangePreview(reading.data) ? reading.data : undefined
  const malformed = reading.data !== undefined && !preview

  const confirm = async () => {
    if (!preview || preview.blocked) return
    setBusy(true)
    try {
      await onConfirm(preview)
      onOpenChange(false)
    } catch (err) {
      notify.error(`Could not ${confirmLabel.toLowerCase()}`, err)
      reading.refresh()
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={(next) => !busy && onOpenChange(next)}
      size="sm"
      title={title}
      description={description}
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button
            variant="destructive"
            onClick={confirm}
            disabled={
              busy || reading.loading || Boolean(reading.error) || !preview || preview.blocked
            }
            pending={busy}
          >
            {confirmLabel}
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        {intro}
        {reading.loading && <LoadingRows rows={2} />}
        {(reading.error || malformed) && (
          <div role="alert" className="space-y-2 text-hint">
            <p className="text-destructive">
              {reading.error
                ? errorMessage(reading.error)
                : "The preview came back in a shape this page cannot read."}
            </p>
            <p className="text-muted-foreground">
              Nothing was changed. What it would disturb is unread.
            </p>
            <Button size="xs" variant="outline" onClick={reading.refresh}>
              Try again
            </Button>
          </div>
        )}
        {preview && !reading.error && (
          <>
            <ConflictList conflicts={preview.conflicts} emptyLabel={emptyLabel} />
            <p className="text-micro text-muted-foreground">
              Read {new Date(preview.checkedAt).toLocaleTimeString()}
              {preview.blocked && " · refused until what is marked first changes"}
            </p>
          </>
        )}
      </div>
    </Modal>
  )
}
