"use client"

import { useState } from "react"
import { PaperAirplane } from "@/components/icons"
import { errorMessage, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { Field } from "@/components/form"
import { Modal } from "@/components/modal"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"

/**
 * A plain comment on a pull request or an issue, as the checkout's owner.
 *
 * One dialog for both because GitHub keeps one conversation per number —
 * `issues/{n}/comments` takes a pull request's number as readily as an
 * issue's — and the route behind it does the same. It is not the review
 * form: that one pins a verdict to a commit, and this one says something.
 */
export function CommentDialog({
  open,
  onOpenChange,
  repoPath,
  number,
  title,
  onCommented,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  repoPath: string
  number: number
  /** What the comment is on, for the title strip. */
  title?: string
  onCommented?: () => void
}) {
  const [body, setBody] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  const change = (next: boolean) => {
    if (busy) return
    if (!next) setError(undefined)
    onOpenChange(next)
  }

  const send = async () => {
    setBusy(true)
    setError(undefined)
    try {
      await post(
        `/git/github/pulls/${number}/comment`,
        { body: body.trim() },
        { query: { path: repoPath } },
      )
      notify.success(`Commented on #${number}`)
      setBody("")
      onCommented?.()
      setBusy(false)
      change(false)
    } catch (err) {
      setError(errorMessage(err))
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={change}
      title={`Comment on #${number}`}
      description={
        title ? `${title}. Posted as your GitHub account.` : "Posted as your GitHub account."
      }
      size="sm"
      footer={
        <>
          <Button variant="ghost" onClick={() => change(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={send} disabled={busy || !body.trim()} pending={busy}>
            <PaperAirplane className="size-4" />
            Comment
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        {error && (
          <Notice title="GitHub refused the comment" tone="danger">
            <span className="break-words whitespace-pre-wrap">{error}</span>
          </Notice>
        )}
        <Field label="Comment" htmlFor="gh-comment" hint="Markdown works.">
          <Textarea
            id="gh-comment"
            rows={5}
            value={body}
            onChange={(e) => setBody(e.target.value)}
            className="resize-none text-body"
          />
        </Field>
      </div>
    </Modal>
  )
}
