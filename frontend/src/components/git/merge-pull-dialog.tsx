"use client"

import { useState } from "react"

import { errorMessage, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { SourceMerge } from "@/components/git/glyphs"
import type { GitPullRequest } from "@/lib/types"
import { Modal } from "@/components/modal"
import { Field, FormFacts, FormFact, OptionList, OptionRow } from "@/components/form"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

const METHODS = [
  {
    value: "merge",
    label: "Merge commit",
    hint: "Keeps every commit on the branch and adds one that joins them.",
  },
  {
    value: "squash",
    label: "Squash and merge",
    hint: "Folds the branch into one commit on the base — a tidy history, no per-commit detail.",
  },
  {
    value: "rebase",
    label: "Rebase and merge",
    hint: "Replays the branch's commits onto the base one by one, with no merge commit.",
  },
]

/** What the deploy page's merge route answers with; the Git page's route says nothing. */
export type MergeResult = {
  merged?: boolean
  previewRunId?: number
  production?: { automatic: boolean; awaitingFirstDeployment?: boolean }
}

/**
 * Merging a pull request on GitHub, with the two choices its own button
 * offers: how, and whether the branch is deleted afterwards. It is a dialog
 * rather than a confirm because the method is a decision, not a yes.
 *
 * Two pages open it. The Git page merges as the checkout's owner through
 * `/git/github/pulls/{n}/merge?path=`; a project's Overview has no checkout
 * and merges as the dashboard's own account through its deploy route, which
 * also says whether production redeploys by itself. The head sha pins the
 * merge to the commit the reader looked at: GitHub refuses if the branch
 * moved on in between, which is the right answer to a review of the wrong
 * code.
 */
export function MergePullDialog({
  open,
  onOpenChange,
  repoPath,
  endpoint,
  headSha,
  pull,
  onMerged,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** The checkout whose owner merges; left out where the dashboard's own account does. */
  repoPath?: string
  /** The route to post to, where it is not the Git page's. */
  endpoint?: string
  /** The commit the merge is pinned to. */
  headSha?: string
  pull: GitPullRequest
  onMerged: (result?: MergeResult) => void
}) {
  const [method, setMethod] = useState("merge")
  const [deleteBranch, setDeleteBranch] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  const merge = async () => {
    setBusy(true)
    setError(undefined)
    try {
      const result = await post<MergeResult | undefined>(
        endpoint ?? `/git/github/pulls/${pull.number}/merge`,
        { method, deleteBranch, headSha },
        { query: repoPath ? { path: repoPath } : undefined },
      )
      notify.success(`Merged #${pull.number}`, { description: pull.title })
      onMerged(result ?? undefined)
      onOpenChange(false)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const chosen = METHODS.find((m) => m.value === method) ?? METHODS[0]

  return (
    <Modal
      open={open}
      onOpenChange={(next) => !busy && onOpenChange(next)}
      title={`Merge #${pull.number}`}
      size="sm"
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={merge} disabled={busy} pending={busy}>
            <SourceMerge className="size-4" />
            Merge on GitHub
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        <FormFacts>
          <FormFact label="from" mono>
            {pull.head}
          </FormFact>
          <FormFact label="into" mono>
            {pull.base}
          </FormFact>
        </FormFacts>
        {error && (
          <Notice title="GitHub refused the merge" tone="danger">
            <span className="break-words whitespace-pre-wrap">{error}</span>
          </Notice>
        )}
        {pull.checks === "failure" && (
          <Notice title="Checks failed on this branch" tone="warning">
            GitHub may refuse the merge, or the base may end up broken. Its page says which check.
          </Notice>
        )}
        <Field label="How" hint={chosen.hint}>
          <Select value={method} onValueChange={setMethod}>
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {METHODS.map((m) => (
                <SelectItem key={m.value} value={m.value}>
                  {m.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <OptionList>
          <OptionRow
            title="Delete the branch afterwards"
            hint={`Removes ${pull.head} from GitHub once it is merged. The commits live on in ${pull.base}.`}
            checked={deleteBranch}
            onCheckedChange={setDeleteBranch}
          />
        </OptionList>
      </div>
    </Modal>
  )
}
