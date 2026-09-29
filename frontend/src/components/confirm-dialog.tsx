"use client"

import { useState } from "react"
import { Check } from "@/components/icons"
import { notify } from "@/lib/toast"
import { ApiError } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"

import { Field, FormFacts } from "@/components/form"
import { Modal } from "@/components/modal"

export type ConfirmRequest = {
  /** Short title, e.g. "Remove container". */
  title: string
  /** What will happen, in plain language. */
  description: React.ReactNode
  /**
   * The thing this acts on, drawn as itself above the sentence: its mark (a
   * `ProductLogo` or `ProjectMark` at `size="sm"`), its name, and the facts
   * that tell it from its neighbours in `FormFact`s. "Remove credential" over
   * GitHub's logo and "GitHub PAT · github.com" is recognised before it is
   * read, which is the moment a wrong one gets caught (§14).
   */
  subject?: {
    mark: React.ReactNode
    name: React.ReactNode
    facts?: React.ReactNode
  }
  /**
   * The exact phrase the server expects echoed in X-Confirm.
   *
   * Optional. Only permanent deployment project deletion, deleting an entire
   * database, and Docker stack removal use it. The server checks these phrases.
   *
   * Whatever is set here, the server re-decides. This is a guard against a
   * slip, never the enforcement point.
   */
  phrase?: string
  confirmLabel?: string
  /**
   * Runs the action; the typed phrase is passed through to the API call. An
   * action whose outcome is not simply "done" — deleted, but not reloaded —
   * announces it itself and resolves to "reported", and the dialog then adds
   * no "completed" of its own.
   */
  action: (confirm: string) => Promise<void | "reported">
  onDone?: () => void
}

/**
 * The confirmation dialog, in its two forms: a plain "are you sure" for the
 * ordinary destructive act, and the same dialog with a phrase to type for the
 * three resource kinds named in ConfirmRequest.phrase.
 *
 * Either way the server re-decides, so this is a usability guard against a
 * mis-click rather than the enforcement point.
 */
export function ConfirmDialog({
  request,
  onOpenChange,
}: {
  request: ConfirmRequest | null
  onOpenChange: (open: boolean) => void
}) {
  if (!request) return null
  // Keyed on the phrase so a second dialog never opens pre-filled with what
  // was typed into the previous one — which would defeat the whole point.
  return (
    <ConfirmBody
      key={request.phrase ?? request.title}
      request={request}
      onOpenChange={onOpenChange}
    />
  )
}

function ConfirmBody({
  request,
  onOpenChange,
}: {
  request: ConfirmRequest
  onOpenChange: (open: boolean) => void
}) {
  const [typed, setTyped] = useState("")
  const [busy, setBusy] = useState(false)
  const matches = !request.phrase || typed === request.phrase

  const run = async () => {
    if (!matches || busy) return
    setBusy(true)
    try {
      if ((await request.action(typed)) !== "reported") notify.success(`${request.title} completed`)
      onOpenChange(false)
      request.onDone?.()
    } catch (err) {
      const message = err instanceof ApiError ? err.message : String(err)
      notify.error(`${request.title} failed`, message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open
      onOpenChange={(open) => !busy && onOpenChange(open)}
      title={request.title}
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          {/*
            Always destructive, not only when a phrase is typed for. This
            dialog exists at all because the action behind it changes state in
            a way that is awkward to undo — most of them delete something — and
            once most deletions stopped asking for a phrase, keying the red
            button to the phrase meant "Delete row" and "Stop container" came
            up wearing the same blue as a Save. The typed ones stay louder by
            the phrase the reader has to type above, which is the difference
            that should carry.
          */}
          <Button variant="destructive" onClick={run} disabled={!matches || busy} pending={busy}>
            {request.confirmLabel ?? request.title}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        {request.subject && (
          <div className="flex min-w-0 items-center gap-3">
            {request.subject.mark}
            <div className="min-w-0 space-y-0.5">
              <p className="truncate text-body font-medium">{request.subject.name}</p>
              {request.subject.facts && <FormFacts>{request.subject.facts}</FormFacts>}
            </div>
          </div>
        )}

        <div className="space-y-2 text-body leading-relaxed">{request.description}</div>

        {request.phrase && (
          // A field like every other in the product (§7), where it had been the
          // last form still built from a bare label at a fourth size. The mark
          // at its end answers the typing — the phrase is right, the button
          // below is live — before the reader looks down to find out.
          <Field
            htmlFor="confirm-phrase"
            label={
              <>
                Type <code className="font-mono text-foreground">{request.phrase}</code> to confirm
              </>
            }
          >
            <InputGroup>
              <InputGroupInput
                id="confirm-phrase"
                autoFocus
                autoComplete="off"
                spellCheck={false}
                value={typed}
                onChange={(e) => setTyped(e.target.value)}
                onKeyDown={(e) => e.key === "Enter" && run()}
                className="font-mono"
                placeholder="Type the phrase above"
              />
              {matches && (
                <InputGroupAddon align="inline-end">
                  <Check aria-hidden className="text-success" />
                </InputGroupAddon>
              )}
            </InputGroup>
          </Field>
        )}
      </div>
    </Modal>
  )
}

/** Drives a single ConfirmDialog from anywhere in a page. */
export function useConfirm() {
  const [request, setRequest] = useState<ConfirmRequest | null>(null)
  const dialog = <ConfirmDialog request={request} onOpenChange={(o) => !o && setRequest(null)} />
  return { confirm: setRequest, dialog }
}
