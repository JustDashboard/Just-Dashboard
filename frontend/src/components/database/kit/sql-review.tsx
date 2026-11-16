"use client"

import { Modal } from "@/components/modal"
import { FormNote, Statement } from "@/components/form"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"

/**
 * One statement of a review: its SQL, or an item with its own label, the
 * server's refusal to plan it and the notes the server gave about it.
 */
export type ReviewStatement =
  | string
  | {
      sql: string
      label?: React.ReactNode
      /** The server could not plan it: said under it, in the danger ink. */
      error?: string
      notes?: string[]
      /** What the empty statement says, where the server runs none for it. */
      placeholder?: string
    }

/**
 * What is about to run, shown before it runs.
 *
 * A set of staged edits, a column being altered, a role being granted: each
 * ends in statements, and the reader is asked to read them rather than to
 * trust a form. The statements are the server's own rendering, each through
 * the one `Statement` every schema form shows its SQL in, and under them the
 * dialog has exactly two ways out — Cancel, and the command, named for what
 * it does.
 *
 * A statement may be an item (`ReviewStatement`) with its own label, the
 * server's error and its notes; `loading` says the statements are still being
 * read from the server, and `failure` what went wrong once the command ran.
 * The grants review was a second copy of this dialog for those three.
 */
export function SqlReview({
  open,
  onOpenChange,
  title,
  statements,
  command,
  onRun,
  pending,
  danger,
  note,
  description = "The statements about to run",
  loading,
  failure,
  disabled,
  children,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** What is being done: "Apply 3 changes to orders". */
  title: React.ReactNode
  /** The statements, in the order they run. */
  statements: ReviewStatement[]
  /** The dialog's description, for a screen reader: what the statements are. */
  description?: string
  /** The statements are still being read from the server. */
  loading?: boolean
  /** What went wrong once the command ran, under the statements. */
  failure?: string
  /** The command cannot run yet: a statement could not be planned. */
  disabled?: boolean
  /** The command's own word: "Apply", "Drop column". */
  command: string
  onRun: () => void
  /** The command has been sent and has not come back. */
  pending?: boolean
  /** The statements destroy something that cannot be put back. */
  danger?: boolean
  /** One line beside the buttons on what the command will do. */
  note?: React.ReactNode
  /** What the statements act on, above them: the table, the row count. */
  children?: React.ReactNode
}) {
  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      size="lg"
      title={title}
      description={description}
      initialFocus="body"
      footer={
        <>
          {note && <p className="mr-auto min-w-0 text-hint text-muted-foreground">{note}</p>}
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={pending}>
            Cancel
          </Button>
          <Button
            variant={danger ? "destructive" : "default"}
            onClick={onRun}
            pending={pending}
            disabled={disabled || loading || statements.length === 0}
          >
            {command}
          </Button>
        </>
      }
    >
      <div className="grid gap-4">
        {children}
        {loading ? (
          <p className="flex text-body" role="status">
            <TextShimmer>Reading the statements from the server…</TextShimmer>
          </p>
        ) : statements.length === 0 ? (
          <Statement sql="" placeholder="Nothing to run." />
        ) : (
          statements.map((statement, index) => {
            const one = typeof statement === "string" ? { sql: statement } : statement
            return (
              <div key={index} className="min-w-0 space-y-1.5">
                <Statement
                  label={
                    one.label ??
                    (statements.length > 1
                      ? `Statement ${index + 1} of ${statements.length}`
                      : "Statement")
                  }
                  sql={one.sql}
                  placeholder={one.placeholder ?? ""}
                />
                {one.error && (
                  <FormNote tone="danger" role="alert" className="break-words">
                    {one.error}
                  </FormNote>
                )}
                {one.notes?.map((note) => (
                  <FormNote key={note}>{note}</FormNote>
                ))}
              </div>
            )
          })
        )}
        {failure && (
          <FormNote tone="danger" role="alert" className="break-words">
            {failure}
          </FormNote>
        )}
      </div>
    </Modal>
  )
}
