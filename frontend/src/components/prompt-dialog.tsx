"use client"

import { useId, useState } from "react"
import { errorMessage } from "@/lib/api"
import { cn } from "@/lib/utils"
import { Field, FormFact, FormFacts } from "@/components/form"
import { Modal } from "@/components/modal"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"

/** A fact under the title: what the name is for — the commit, the folder. */
export type PromptFact = { label: string; value: string; mono?: boolean }

export type PromptOptions = {
  title: string
  /** The field's label. */
  label: string
  /**
   * One line under the field: what goes here, or — as a function of what is
   * typed — what it will make, such as the path a new folder lands at.
   */
  hint?: React.ReactNode | ((value: string) => React.ReactNode)
  placeholder?: string
  initial?: string
  /** The one word on the button: Rename, Create, Tag. */
  verb: string
  /** What the name is given to, as data under the title. */
  facts?: PromptFact[]
  /** A second field for a message, which turns a tag into an annotated one. */
  message?: { label: string; hint?: React.ReactNode; placeholder?: string }
  /** A string refuses the name, said under the field; undefined accepts it. */
  validate?: (value: string) => string | undefined
  /**
   * Runs the verb. A throw keeps the dialog open and says the reason in it,
   * under `refusal` as its title.
   */
  onSubmit: (value: string, message: string) => Promise<void> | void
  /** The title of a refusal; "{verb} failed" if left out. */
  refusal?: string
  /** Select the name without its extension, the way a rename usually wants. */
  selectBasename?: boolean
  /** The name is an identifier — a branch, a path — and is typed in mono. */
  mono?: boolean
}

/**
 * The one small form the product keeps asking for: a name, and sometimes a
 * message, and one verb.
 *
 * Files renames, makes a folder or a file; Git branches from a commit, tags
 * one, renames a branch, labels a stash. Both had a dialog of their own, made
 * so that their four uses would not drift, and the two drifted from each
 * other: one with a bare label, a hand-written error line, a failure toasted
 * behind the dialog and a button that said OK; the other with a `Field`, the
 * refusal in a `Notice` in the dialog and a verb on the button. This is the
 * second one, for both.
 *
 * The verb is required and is one word (§D14). A refusal from the server
 * stays in the dialog as a `Notice`, beside the name that caused it, so the
 * name can be changed and tried again without opening the dialog again.
 */
export function PromptDialog({
  open,
  onOpenChange,
  ...options
}: PromptOptions & { open: boolean; onOpenChange: (open: boolean) => void }) {
  if (!open) return null
  // A fresh body per opening, keyed the way ConfirmDialog keys its own: a
  // dialog reopened for another subject must not carry the last name in, and
  // remounting is how that happens without an effect that writes state.
  return (
    <PromptBody
      key={`${options.title}:${options.initial ?? ""}`}
      onOpenChange={onOpenChange}
      {...options}
    />
  )
}

/** Drives one PromptDialog from anywhere in a page, as `useConfirm` drives its dialog. */
export function usePrompt() {
  const [request, setRequest] = useState<PromptOptions | null>(null)
  const dialog = request ? (
    <PromptDialog {...request} open onOpenChange={(open) => !open && setRequest(null)} />
  ) : null
  return { prompt: setRequest, dialog }
}

function PromptBody({
  onOpenChange,
  title,
  label,
  hint,
  placeholder,
  initial = "",
  verb,
  facts,
  message,
  validate,
  onSubmit,
  refusal,
  selectBasename,
  mono,
}: PromptOptions & { onOpenChange: (open: boolean) => void }) {
  const [value, setValue] = useState(initial)
  const [text, setText] = useState("")
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState<string>()
  const form = useId()
  const name = value.trim()
  const invalid = validate?.(value)

  const submit = async () => {
    if (busy || !name || invalid) return
    setBusy(true)
    setRefused(undefined)
    try {
      await onSubmit(name, text.trim())
      onOpenChange(false)
    } catch (err) {
      setRefused(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  // A rename usually means changing the stem, not the extension, so select
  // just that on focus when asked.
  const onFocus = (event: React.FocusEvent<HTMLInputElement>) => {
    const dot = selectBasename ? event.target.value.lastIndexOf(".") : -1
    if (dot > 0) event.target.setSelectionRange(0, dot)
    else event.target.select()
  }

  return (
    <Modal
      open
      onOpenChange={(next) => !busy && onOpenChange(next)}
      title={title}
      size="sm"
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button type="submit" form={form} disabled={busy || !name || !!invalid} pending={busy}>
            {verb}
          </Button>
        </>
      }
    >
      <form
        id={form}
        className="space-y-3"
        onSubmit={(event) => {
          event.preventDefault()
          void submit()
        }}
      >
        {facts && facts.length > 0 && (
          <FormFacts>
            {facts.map((fact) => (
              <FormFact key={fact.label} label={fact.label} mono={fact.mono}>
                {fact.value}
              </FormFact>
            ))}
          </FormFacts>
        )}
        {refused && (
          <Notice title={refusal ?? `${verb} failed`} tone="danger">
            <span className="break-words whitespace-pre-wrap">{refused}</span>
          </Notice>
        )}
        <Field label={label} hint={typeof hint === "function" ? hint(name) : hint} error={invalid}>
          <Input
            autoFocus
            autoComplete="off"
            spellCheck={false}
            value={value}
            onFocus={onFocus}
            onChange={(event) => setValue(event.target.value)}
            placeholder={placeholder}
            className={cn(mono && "font-mono")}
          />
        </Field>
        {message && (
          <Field label={message.label} hint={message.hint}>
            <Textarea
              rows={3}
              value={text}
              onChange={(event) => setText(event.target.value)}
              placeholder={message.placeholder}
              className="resize-none"
            />
          </Field>
        )}
      </form>
    </Modal>
  )
}
