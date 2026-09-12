"use client"

import { useState } from "react"
import { notify } from "@/lib/toast"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"

import { Modal } from "@/components/modal"

export type PromptRequest = {
  title: string
  label?: string
  placeholder?: string
  initial?: string
  confirmLabel?: string
  /** A live line under the input — the resulting path, say. */
  hint?: (value: string) => React.ReactNode
  /** Return a string to reject with a message; return undefined to accept. */
  validate?: (value: string) => string | undefined
  /** Runs on submit; throwing keeps the dialog open with a toast. */
  submit: (value: string) => Promise<void> | void
  /** Pre-select the name without its extension, the way a rename usually wants. */
  selectBasename?: boolean
}

/**
 * A single-input dialog, driven from anywhere like useConfirm.
 *
 * Rename, new file, new folder and new symlink are all "type one name and go",
 * and each one being its own bespoke dialog is how four of them ended up with
 * four slightly different behaviours. One shape, so they cannot drift.
 */
export function usePrompt() {
  const [request, setRequest] = useState<PromptRequest | null>(null)
  const dialog = <PromptDialog request={request} onOpenChange={(o) => !o && setRequest(null)} />
  return { prompt: setRequest, dialog }
}

function PromptDialog({
  request,
  onOpenChange,
}: {
  request: PromptRequest | null
  onOpenChange: (open: boolean) => void
}) {
  if (!request) return null
  return (
    <PromptBody
      key={request.title + (request.initial ?? "")}
      request={request}
      onOpenChange={onOpenChange}
    />
  )
}

function PromptBody({
  request,
  onOpenChange,
}: {
  request: PromptRequest
  onOpenChange: (open: boolean) => void
}) {
  const [value, setValue] = useState(request.initial ?? "")
  const [busy, setBusy] = useState(false)
  const error = request.validate?.(value)

  const run = async () => {
    if (busy || !value.trim() || error) return
    setBusy(true)
    try {
      await request.submit(value.trim())
      onOpenChange(false)
    } catch (err) {
      notify.error(`${request.title} failed`, err)
      setBusy(false)
    }
  }

  // A rename usually means changing the stem, not the extension, so select just
  // that on focus when asked.
  const onFocus = (e: React.FocusEvent<HTMLInputElement>) => {
    if (!request.selectBasename) {
      e.target.select()
      return
    }
    const dot = value.lastIndexOf(".")
    if (dot > 0) e.target.setSelectionRange(0, dot)
    else e.target.select()
  }

  return (
    <Modal
      open
      onOpenChange={(o) => !busy && onOpenChange(o)}
      size="sm"
      title={request.title}
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={run} disabled={busy || !value.trim() || !!error} pending={busy}>
            {request.confirmLabel ?? "OK"}
          </Button>
        </>
      }
    >
      <div className="space-y-2">
        {request.label && (
          <Label htmlFor="prompt-input" className="text-xs text-muted-foreground">
            {request.label}
          </Label>
        )}
        <Input
          id="prompt-input"
          autoFocus
          autoComplete="off"
          spellCheck={false}
          value={value}
          onFocus={onFocus}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && run()}
          placeholder={request.placeholder}
        />
        {error ? (
          <p className="text-hint text-destructive">{error}</p>
        ) : (
          request.hint && (
            <p className="font-mono text-hint break-all text-muted-foreground">
              {request.hint(value.trim())}
            </p>
          )
        )}
      </div>
    </Modal>
  )
}
