"use client"

import { useCallback, useState } from "react"
import { notify } from "@/lib/toast"
import type { GitResult } from "@/lib/types"
import { pullBlocked } from "@/components/git/pull-blocked"

export type GitRun = (label: string, fn: () => Promise<GitResult>) => Promise<GitResult>

/**
 * One way of running a git operation from a page.
 *
 * Every verb here follows the same shape — say what is happening, run it,
 * show git's first lines when it worked, show the failure when it did not,
 * refresh whatever the caller reads — and the Git page and the terminal's git
 * tab had each written that shape out for themselves. `busy` is the label of
 * the operation in flight, so a button can say "Pulling…" rather than going
 * inert and saying nothing. A pull blocked by what is in the working tree is
 * not toasted: the caller opens `PullBlockedDialog` on it instead.
 */
export function useGitRun(onChanged: () => void) {
  const [busy, setBusy] = useState<string>()
  const [startedAt, setStartedAt] = useState(0)
  const run = useCallback<GitRun>(
    async (label, fn) => {
      setBusy(label)
      setStartedAt(Date.now())
      try {
        const res = await fn()
        notify.success(label, { description: res.output?.split("\n").slice(0, 3).join("\n") })
        onChanged()
        return res
      } catch (err) {
        if (!pullBlocked(err)) notify.error(`${label} failed`, err)
        // Whatever failed may still have moved something — a merge that was
        // abandoned, a push that half landed — so the reads catch up either way.
        onChanged()
        throw err
      } finally {
        setBusy(undefined)
      }
    },
    [onChanged],
  )
  const words: Record<string, string> = {
    Fetched: "Fetching from the remote…",
    Pulled: "Pulling from the remote…",
    Pushed: "Pushing to the remote…",
    "Submodule updated": "Updating submodules…",
    "Git LFS updated": "Updating Git LFS…",
  }
  const progress = busy
    ? {
        label: words[busy] ?? "Running Git operation…",
        detail: words[busy] ? undefined : busy,
        startedAt,
      }
    : undefined
  return { busy, run, progress }
}
