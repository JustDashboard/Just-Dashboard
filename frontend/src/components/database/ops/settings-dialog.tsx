"use client"

import { useEffect, useId, useRef, useState } from "react"
import { FormFacts, FormNote } from "@/components/form"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import type { DbConnection } from "@/lib/types"
import type { Engine } from "@/components/database/engine"
import { EngineMark } from "@/components/database/kit"

export type TaskSubject = {
  /** The thing drawn as itself: an engine tile, an account's initials. */
  mark: React.ReactNode
  name: React.ReactNode
  /** What tells it from its neighbours, as `FormFact`s. */
  facts?: React.ReactNode
}

/**
 * One task of the Access, Backups and Settings pages as a dialog: the thing
 * it acts on, the form, why the server turned it down when it did, and the
 * one command.
 *
 * Typed work is not lost to a slip. Escape and a press outside the dialog ask
 * before they discard anything that was typed, in the dialog's own footer,
 * and neither closes it while the request is in flight; Cancel is the reader
 * saying so and closes at once. A refusal leaves the form as it was, with the
 * server's words under it.
 *
 * Enter in a field is the command: the body is a form and the command is its
 * submit.
 */
export function TaskDialog({
  title,
  description,
  size = "md",
  subject,
  dirty,
  busy,
  refusal,
  note,
  secondary,
  command,
  commandIcon: CommandIcon,
  destructive,
  disabled,
  cancelLabel = "Cancel",
  discardQuestion = "Close and lose what you entered?",
  onRun,
  onClose,
  children,
}: {
  title: string
  /** Read to a screen reader, never drawn. */
  description: string
  size?: "sm" | "md" | "lg" | "xl"
  subject?: TaskSubject
  /** Something was typed or chosen that closing would lose. */
  dirty: boolean
  busy: boolean
  /** Why the last attempt was turned down, in the server's words. */
  refusal?: React.ReactNode
  /** One line about what the command does, at the footer's left. */
  note?: React.ReactNode
  /** A second control beside the command: a Test, a Reset. */
  secondary?: React.ReactNode
  /** The command's own words. Left out for a dialog that only shows something. */
  command?: string
  commandIcon?: React.ComponentType<{ className?: string }>
  /** The command destroys or replaces something. */
  destructive?: boolean
  disabled?: boolean
  cancelLabel?: string
  /** What the footer asks before a slip closes a dialog that holds something. */
  discardQuestion?: string
  onRun?: () => void
  onClose: () => void
  children: React.ReactNode
}) {
  const form = useId()
  const [asking, setAsking] = useState(false)
  // The footer's question takes the keyboard, and hands it back to the field
  // it was taken from when the answer is to go on editing.
  const keep = useRef<HTMLButtonElement>(null)
  const typing = useRef<HTMLElement | null>(null)
  useEffect(() => {
    if (!asking) return
    typing.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
    keep.current?.focus()
    return () => {
      if (typing.current?.isConnected) typing.current.focus()
    }
  }, [asking])

  // Escape and a press outside: never while the request runs, and not past
  // typed work without asking. While the footer is asking, either means "no".
  const dismiss = (open: boolean) => {
    if (open || busy) return
    if (asking) setAsking(false)
    else if (dirty) setAsking(true)
    else onClose()
  }

  return (
    <Modal
      open
      onOpenChange={dismiss}
      title={title}
      description={description}
      size={size}
      footer={
        asking ? (
          <>
            <p role="alert" className="mr-auto min-w-0 text-body">
              {discardQuestion}
            </p>
            <Button ref={keep} variant="outline" onClick={() => setAsking(false)}>
              Keep editing
            </Button>
            <Button variant="destructive" onClick={onClose}>
              Discard
            </Button>
          </>
        ) : (
          <>
            {note && <FormNote className="mr-auto min-w-0 max-sm:basis-full">{note}</FormNote>}
            <Button variant="outline" onClick={onClose} disabled={busy}>
              {cancelLabel}
            </Button>
            {secondary}
            {command && (
              <Button
                type="submit"
                form={form}
                variant={destructive ? "destructive" : "default"}
                disabled={disabled}
                pending={busy}
              >
                {CommandIcon && <CommandIcon />}
                {command}
              </Button>
            )}
          </>
        )
      }
    >
      <form
        id={form}
        className="space-y-4"
        onSubmit={(event) => {
          event.preventDefault()
          if (!busy && !disabled) onRun?.()
        }}
      >
        {subject && (
          <div className="flex min-w-0 items-center gap-3">
            {subject.mark}
            <div className="min-w-0 space-y-0.5">
              <p className="truncate text-body font-medium">{subject.name}</p>
              {subject.facts && <FormFacts>{subject.facts}</FormFacts>}
            </div>
          </div>
        )}
        {children}
        {refusal && (
          <FormNote role="alert" tone="danger" className="break-words whitespace-pre-wrap">
            {refusal}
          </FormNote>
        )}
      </form>
    </Modal>
  )
}

/** The database itself as a dialog's subject: its engine, its name, and the facts given. */
export function databaseSubject(
  conn: Pick<DbConnection, "name">,
  engine: Engine,
  facts?: React.ReactNode,
): TaskSubject {
  return { mark: <EngineMark engine={engine} size="sm" />, name: conn.name, facts }
}
