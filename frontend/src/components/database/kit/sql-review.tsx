"use client"

import { Modal } from "@/components/modal"
import { Statement } from "@/components/form"
import { Button } from "@/components/ui/button"

/**
 * What is about to run, shown before it runs.
 *
 * A set of staged edits, a column being altered, a role being granted: each
 * ends in statements, and the reader is asked to read them rather than to
 * trust a form. The statements are the server's own rendering, each through
 * the one `Statement` every schema form shows its SQL in, and under them the
 * dialog has exactly two ways out — Cancel, and the command, named for what
 * it does.
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
  children,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** What is being done: "Apply 3 changes to orders". */
  title: React.ReactNode
  /** The statements, in the order they run. */
  statements: string[]
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
      description="The statements about to run"
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
            disabled={statements.length === 0}
          >
            {command}
          </Button>
        </>
      }
    >
      <div className="grid gap-4">
        {children}
        {statements.length === 0 ? (
          <Statement sql="" placeholder="Nothing to run." />
        ) : (
          statements.map((sql, index) => (
            <Statement
              key={index}
              label={
                statements.length > 1
                  ? `Statement ${index + 1} of ${statements.length}`
                  : "Statement"
              }
              sql={sql}
              placeholder=""
            />
          ))
        )}
      </div>
    </Modal>
  )
}
