"use client"

import { useEffect, useRef, useState } from "react"
import { Warning } from "@/components/icons"
import { ApiError } from "@/lib/api"
import { notify } from "@/lib/toast"
import { FormFacts, FormNote, Statement } from "@/components/form"
import { Modal } from "@/components/modal"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { EngineMark } from "@/components/database/kit"
import { useDatabase } from "@/components/database/shell/database-context"
import { requestKey, type DdlRequest } from "@/components/database/schema/changes"
import { useFocusReturn } from "@/components/database/schema/focus"
import type { DdlAnswer } from "@/components/database/schema/types"
import { runChange, usePreview, type Preview } from "@/components/database/schema/use-ddl"

export type Subject = {
  /** What the change acts on: `public.orders`, a column of it. */
  name: React.ReactNode
  /** What tells it from its neighbours, as `FormFact`s. */
  facts?: React.ReactNode
}

/** A change the engine may refuse half-way, or that rewrites every row. */
export type Risk = {
  /** What it does to the rows that are there, in a few words: the notice's title. */
  title: string
  /** Why it may be refused, as sentences. */
  description: React.ReactNode
  /** What the footer asks before it runs: "Rewrite total in every row?" */
  question: string
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

/** What the footer is asking, when it is asking: whether to lose typed work, or whether to run a risky change. */
type Asking = { about: "discard" } | { about: "risk"; key: string } | null

/**
 * One structure change as a dialog: the thing it acts on, the form, the
 * statement the server will run for it, and the one command.
 *
 * The command stays off until the server has planned exactly what the form
 * says, so what is read is what runs. A change that can lose or refuse rows
 * (`confirm`) says so under its statement as soon as the form states it, and
 * its command asks once more before it runs — in the dialog's own footer, over
 * the subject and the statement the question is about, rather than in a second
 * dialog that repeats both.
 *
 * Typed work is not lost to a slip. Escape and a press outside the dialog ask
 * before they discard anything that was typed, and neither closes it while
 * the statement is running; Cancel is the reader saying so and closes at once.
 * A statement the engine refuses — confirmed or not — leaves the form as it
 * was, with the engine's words under it.
 *
 * When it closes, the keyboard goes back to the control that opened it.
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
  confirm?: Risk | null
  size?: "md" | "lg"
  onDone: (answer: DdlAnswer) => void
  children: React.ReactNode
}) {
  const { id, engine } = useDatabase()
  const [attempt, setAttempt] = useState(0)
  const preview = usePreview(id, request, attempt)
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<{ key: string; error: Error }>()
  const [asking, setAsking] = useState<Asking>(null)
  useFocusReturn(true)

  const planned = preview.answer
  const key = requestKey(request)
  const failed = failure?.key === key ? failure.error : undefined
  // The question is about one statement: a form changed under it is asked again.
  const question: Asking = asking?.about === "risk" && asking.key !== key ? null : asking

  // The footer's question takes the keyboard, and gives it back to the field
  // it was taken from when the answer is to go on editing.
  const keep = useRef<HTMLButtonElement>(null)
  const typing = useRef<HTMLElement | null>(null)
  const about = question?.about
  useEffect(() => {
    if (!about) return
    typing.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
    keep.current?.focus()
    return () => {
      if (typing.current?.isConnected) typing.current.focus()
    }
  }, [about])

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
      setAsking(null)
    } finally {
      setBusy(false)
    }
  }

  const press = () => {
    if (!request || !planned) return
    if (risk && question?.about !== "risk") setAsking({ about: "risk", key })
    else void run()
  }

  // Escape and a press outside: never while the statement runs, and not past
  // typed work without asking. While the footer is asking, either means "no":
  // the form stays, as it was.
  const dismiss = (open: boolean) => {
    if (open || busy) return
    if (question) setAsking(null)
    else if (dirty) setAsking({ about: "discard" })
    else onClose()
  }

  return (
    <Modal
      open
      onOpenChange={dismiss}
      size={size}
      title={title}
      description={`${title}: the statement is shown before it runs`}
      footer={
        question?.about === "discard" ? (
          <>
            <p role="alert" className="mr-auto min-w-0 text-body">
              Close and lose what you typed?
            </p>
            <Button ref={keep} variant="outline" onClick={() => setAsking(null)}>
              Keep editing
            </Button>
            <Button variant="destructive" onClick={onClose}>
              Discard
            </Button>
          </>
        ) : question?.about === "risk" && risk ? (
          <>
            <p role="alert" className="mr-auto min-w-0 text-body">
              {risk.question}
            </p>
            <Button ref={keep} variant="outline" onClick={() => setAsking(null)} disabled={busy}>
              Keep editing
            </Button>
            <Button variant="destructive" onClick={() => void run()} pending={busy}>
              {command}
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
        {risk && request && (
          <Notice tone="warning" icon={Warning} title={risk.title}>
            {risk.description}
          </Notice>
        )}
        {failed && (
          <FormNote role="alert" tone="danger" className="break-words">
            Nothing was changed. {refusal(failed)}
          </FormNote>
        )}
      </div>
    </Modal>
  )
}
