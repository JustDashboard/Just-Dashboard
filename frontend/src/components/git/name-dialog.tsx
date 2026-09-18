"use client"

import { useState } from "react"
import { errorMessage } from "@/lib/api"
import { Modal } from "@/components/modal"
import { Field, FormFacts, FormFact } from "@/components/form"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"

/**
 * The one small form git keeps asking for: a name, and sometimes a message.
 *
 * A branch from a commit, a tag on a commit, a rename, a stash with a label —
 * four places wanted a title, one field and a button, and one dialog that
 * takes those as props keeps them from arriving at four spellings of the same
 * box. The optional message field is what turns a tag into an annotated one,
 * and is offered only where that distinction exists.
 */
type NameDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  label: string
  hint?: React.ReactNode
  placeholder?: string
  initial?: string
  /** What the form operates on — the commit, the branch — as data under the title. */
  facts?: { label: string; value: string; mono?: boolean }[]
  /** Offer a message field, labelled and explained this way. */
  message?: { label: string; hint: React.ReactNode; placeholder?: string }
  confirmLabel: string
  onSubmit: (name: string, message: string) => Promise<void>
}

export function NameDialog(props: NameDialogProps) {
  // A fresh body per opening, keyed the way ConfirmDialog keys its own: a
  // dialog reopened for a different commit must not carry the previous name
  // in, and remounting is how that happens without an effect that writes
  // state.
  return <NameDialogBody key={`${props.open}:${props.initial ?? ""}`} {...props} />
}

function NameDialogBody({
  open,
  onOpenChange,
  title,
  label,
  hint,
  placeholder,
  initial = "",
  facts,
  message,
  confirmLabel,
  onSubmit,
}: NameDialogProps) {
  const [name, setName] = useState(initial)
  const [text, setText] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  const submit = async () => {
    const value = name.trim()
    if (!value) return
    setBusy(true)
    setError(undefined)
    try {
      await onSubmit(value, text.trim())
      onOpenChange(false)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={(next) => !busy && onOpenChange(next)}
      title={title}
      size="sm"
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={busy || !name.trim()} pending={busy}>
            {confirmLabel}
          </Button>
        </>
      }
    >
      <form
        className="space-y-3"
        onSubmit={(e) => {
          e.preventDefault()
          void submit()
        }}
      >
        {facts && facts.length > 0 && (
          <FormFacts>
            {facts.map((f) => (
              <FormFact key={f.label} label={f.label} mono={f.mono}>
                {f.value}
              </FormFact>
            ))}
          </FormFacts>
        )}
        {error && (
          <Notice title="git refused" tone="danger">
            <span className="break-words whitespace-pre-wrap">{error}</span>
          </Notice>
        )}
        <Field label={label} htmlFor="git-name" hint={hint}>
          <Input
            id="git-name"
            autoFocus
            autoComplete="off"
            spellCheck={false}
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder={placeholder}
            className="font-mono"
          />
        </Field>
        {message && (
          <Field label={message.label} htmlFor="git-message" hint={message.hint}>
            <Textarea
              id="git-message"
              rows={3}
              value={text}
              onChange={(e) => setText(e.target.value)}
              placeholder={message.placeholder}
              className="resize-none"
            />
          </Field>
        )}
      </form>
    </Modal>
  )
}
