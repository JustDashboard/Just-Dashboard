"use client"

import { useState } from "react"
import { ApiError } from "@/lib/api"
import { notify } from "@/lib/toast"
import { useConfirm } from "@/components/confirm-dialog"
import { FormFacts, FormNote, Statement } from "@/components/form"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { EngineMark } from "@/components/database/kit"
import { useDatabase } from "@/components/database/shell/database-context"
import type { DdlRequest } from "@/components/database/schema/changes"
import type { DdlAnswer } from "@/components/database/schema/types"
import { runChange, usePreview, type Preview } from "@/components/database/schema/use-ddl"

export type Subject = {
  /** What the change acts on: `public.orders`, a column of it. */
  name: React.ReactNode
  /** What tells it from its neighbours, as `FormFact`s. */
  facts?: React.ReactNode
}

/** The words a statement's refusal is said in, where the form is. */
export function refusal(error: Error): string {
  if (error instanceof ApiError && error.status === 429) {
    return "Too many changes were asked for in a minute. Wait a moment and the statement is read again."
  }
  return error.message
}

/**
 * The statement a form would run, as the server planned it, under the form.
 *
 * It is the server's own text or nothing: while the form is ahead of the
 * server the place says what it is waiting for, and a request the server
 * cannot plan says why in the server's words. A function the statement calls
 * that the server does not vouch for is named, because the engine runs it for
 * every row it checks or fills.
 */
export function PlannedStatement({
  request,
  preview,
  waiting,
  onRetry,
}: {
  request: DdlRequest | null
  preview: Preview
  /** Said in the statement's place while the form is not filled in far enough. */
  waiting: string
  onRetry: () => void
}) {
  const answer = preview.answer
  const refused = preview.error instanceof ApiError && preview.error.status < 500
  return (
    <div className="space-y-2">
      <Statement
        sql={answer?.statement ?? ""}
        placeholder={
          !request
            ? waiting
            : preview.error
              ? "No statement: the change cannot be planned as it stands."
              : "Reading the statement from the server…"
        }
      />
      {/* A refusal is the reader's to act on; it is said once, politely: a
          statement that changes with every key is news, not an alarm. */}
      <div aria-live="polite" className="space-y-2">
        {preview.error && (
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
            <FormNote tone="danger" className="min-w-0 flex-1 break-words">
              {refusal(preview.error)}
            </FormNote>
            {!refused && (
              <Button size="xs" variant="outline" onClick={onRetry}>
                Try again
              </Button>
            )}
          </div>
        )}
        {answer?.calls && answer.calls.length > 0 && (
          <FormNote tone="warning">
            This statement also runs {answer.calls.map((name) => `${name}()`).join(", ")} for every
            row it checks or fills. The server does not vouch for what that does.
          </FormNote>
        )}
      </div>
    </div>
  )
}

/**
 * One structure change as a dialog: the thing it acts on, the form, the
 * statement the server will run for it, and the one command.
 *
 * The command stays off until the server has planned exactly what the form
 * says, so what is read is what runs. A change that can lose or refuse rows
 * (`confirm`) is asked about once more by name before it does.
 *
 * Typed work is not lost to a slip. Escape and a press outside the dialog ask
 * before they discard anything that was typed, and neither closes it while
 * the statement is running; Cancel is the reader saying so and closes at once.
 * A statement the engine refuses leaves the form as it was, with the engine's
 * words under it.
 */
export function ChangeDialog({
  onClose,
  title,
  subject,
  request,
  waiting,
  command,
  done,
  dirty,
  confirm: risk,
  size = "md",
  onDone,
  children,
}: {
  onClose: () => void
  title: string
  subject: Subject
  /** The change as the form states it now; null while it is not filled in far enough. */
  request: DdlRequest | null
  waiting: string
  /** The command's own words: "Add column". */
  command: string
  /** What the toast says once it has run: "Added total to orders". */
  done: string
  /** Something was typed that closing would lose. */
  dirty: boolean
  /** Set for a change the engine may refuse half-way or that rewrites every row. */
  confirm?: { title: string; description: React.ReactNode } | null
  size?: "md" | "lg"
  onDone: (answer: DdlAnswer) => void
  children: React.ReactNode
}) {
  const { id, engine } = useDatabase()
  const [attempt, setAttempt] = useState(0)
  const preview = usePreview(id, request, attempt)
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<{ key: string; error: Error }>()
  const [asking, setAsking] = useState(false)
  const { confirm, dialog } = useConfirm()
  // The confirmation is up: `useConfirm` says so only through what it renders.
  const confirming = (dialog.props as { request: unknown }).request !== null

  const planned = preview.answer
  const key = JSON.stringify(request)
  const failed = failure?.key === key ? failure.error : undefined

  const run = async () => {
    if (!request || !planned || busy) return
    setBusy(true)
    setFailure(undefined)
    try {
      const answer = await runChange(id, request)
      notify.success(done, { description: answer.statement })
      onDone(answer)
      onClose()
    } catch (err) {
      setFailure({ key, error: err instanceof Error ? err : new Error(String(err)) })
    } finally {
      setBusy(false)
    }
  }

  const press = () => {
    if (!request || !planned) return
    if (!risk) return void run()
    confirm({
      title: risk.title,
      confirmLabel: command,
      subject: { mark: <EngineMark engine={engine} size="sm" />, ...subject },
      description: (
        <>
          {risk.description}
          <Statement sql={planned.statement} placeholder="" />
        </>
      ),
      action: async () => {
        const answer = await runChange(id, request)
        notify.success(done, { description: answer.statement })
        onDone(answer)
        onClose()
        return "reported"
      },
    })
  }

  // Escape and a press outside: never while the statement runs, and not past
  // typed work without asking. The confirmation over this dialog is outside
  // it too, and a press there is not a wish to close this one.
  const dismiss = (open: boolean) => {
    if (open || busy || confirming) return
    if (dirty) setAsking(true)
    else onClose()
  }

  return (
    <>
      <Modal
        open
        onOpenChange={dismiss}
        size={size}
        title={title}
        description={`${title}: the statement is shown before it runs`}
        footer={
          asking ? (
            <>
              <p role="alert" className="mr-auto min-w-0 text-body">
                Close and lose what you typed?
              </p>
              <Button variant="outline" autoFocus onClick={() => setAsking(false)}>
                Keep editing
              </Button>
              <Button variant="destructive" onClick={onClose}>
                Discard
              </Button>
            </>
          ) : (
            <>
              <Button variant="outline" onClick={onClose} disabled={busy}>
                Cancel
              </Button>
              <Button
                variant={risk ? "destructive" : "default"}
                onClick={press}
                disabled={!planned}
                pending={busy}
              >
                {risk ? `${command}…` : command}
              </Button>
            </>
          )
        }
      >
        <div className="space-y-4">
          <div className="flex min-w-0 items-center gap-3">
            <EngineMark engine={engine} size="sm" />
            <div className="min-w-0 space-y-0.5">
              <p className="truncate font-mono text-body font-medium">{subject.name}</p>
              {subject.facts && <FormFacts>{subject.facts}</FormFacts>}
            </div>
          </div>
          {children}
          <PlannedStatement
            request={request}
            preview={preview}
            waiting={waiting}
            onRetry={() => setAttempt((n) => n + 1)}
          />
          {failed && (
            <FormNote role="alert" tone="danger" className="break-words">
              Nothing was changed. {refusal(failed)}
            </FormNote>
          )}
        </div>
      </Modal>
      {dialog}
    </>
  )
}
