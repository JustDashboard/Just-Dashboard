"use client"

import { useCallback, useState } from "react"
import { notify } from "@/lib/toast"
import type { GitResult } from "@/lib/types"

export type GitRun = (label: string, fn: () => Promise<GitResult>) => Promise<GitResult>

/**
 * One way of running a git operation from a page.
 *
 * Every verb here follows the same shape — say what is happening, run it,
 * show git's first lines when it worked, show the failure when it did not,
 * refresh whatever the caller reads — and the Git page and the terminal's git
 * tab had each written that shape out for themselves. `busy` is the label of
 * the operation in flight, so a button can say "Pulling…" rather than going
 * inert and saying nothing.
 */
export function useGitRun(onChanged: () => void): { busy?: string; run: GitRun } {
  const [busy, setBusy] = useState<string>()
  const run = useCallback<GitRun>(
    async (label, fn) => {
      setBusy(label)
      try {
        const res = await fn()
        notify.success(label, { description: res.output?.split("\n").slice(0, 3).join("\n") })
        onChanged()
        return res
      } catch (err) {
        notify.error(`${label} failed`, err)
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
  return { busy, run }
}
