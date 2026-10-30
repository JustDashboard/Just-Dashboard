"use client"

import { useState } from "react"
import { Checkbox } from "@/components/ui/checkbox"
import type { GitMatchBranch, GitMatchPlan } from "@/lib/types"

/** Whether the plan would change anything, so a checkout that already matches skips the dialog. */
export function matchPlanIsEmpty(plan: GitMatchPlan): boolean {
  return (
    !plan.switch &&
    plan.delete.length === 0 &&
    plan.update.length === 0 &&
    plan.unique.length === 0 &&
    plan.staleRefs.length === 0
  )
}

function plural(n: number, noun: string): string {
  return `${n.toLocaleString()} ${noun}${n === 1 ? "" : noun.endsWith("ch") ? "es" : "s"}`
}

/**
 * The confirmation's body: every effect of matching, largest first, with the
 * one choice that can lose work left off until it is ticked. A list can run to
 * hundreds of branches, so each scrolls in place rather than pushing the
 * buttons off the screen.
 */
export function MatchOriginBody({
  plan,
  onDiscardChange,
}: {
  plan: GitMatchPlan
  onDiscardChange: (discard: boolean) => void
}) {
  const [discard, setDiscard] = useState(false)
  const { remote } = plan
  return (
    <>
      {plan.switch && plan.branch && (
        <p>
          Switches this checkout to <span className="font-mono">{plan.branch}</span>.
        </p>
      )}
      {plan.stash > 0 && (
        <p>
          {plural(plan.stash, "uncommitted file")} {plan.stash === 1 ? "goes" : "go"} into a stash
          first. Nothing in them is lost.
        </p>
      )}
      <MatchList
        title={`Update ${plural(plan.update.length, "branch")} to match ${remote}`}
        rows={plan.update}
        fact={(row) => (row.behind ? `${row.behind.toLocaleString()} behind` : "")}
      />
      <MatchList
        title={`Delete ${plural(plan.delete.length, "branch")} ${remote} no longer has`}
        hint={`Nothing is lost: every commit is still on ${remote}.`}
        rows={plan.delete}
      />
      {plan.staleRefs.length > 0 && (
        <p>
          Removes {plural(plan.staleRefs.length, "leftover ref")} under{" "}
          <span className="font-mono">{plan.staleRemotes.join(", ")}</span>, which is no longer a
          remote here.
        </p>
      )}
      {plan.unique.length > 0 && (
        <div className="space-y-1.5">
          <label className="flex items-start gap-2 text-destructive">
            <Checkbox
              className="mt-0.5"
              checked={discard}
              onCheckedChange={(checked) => {
                setDiscard(checked === true)
                onDiscardChange(checked === true)
              }}
            />
            <span>
              Also discard {plural(plan.unique.length, "branch")} with commits {remote} does not
              have. Those commits exist nowhere else. Left unticked, they stay as they are.
            </span>
          </label>
          <MatchRows
            rows={plan.unique}
            fact={(row) => `${plural(row.commits ?? 0, "commit")} only here`}
          />
        </div>
      )}
      <MatchList
        title={`Leave ${plural(plan.inUse.length, "branch")} alone`}
        hint="Another checkout is using them."
        rows={plan.inUse}
      />
      <p className="text-hint text-muted-foreground">Tags and existing stashes stay as they are.</p>
    </>
  )
}

function MatchList({
  title,
  hint,
  rows,
  fact,
}: {
  title: string
  hint?: string
  rows: GitMatchBranch[]
  fact?: (row: GitMatchBranch) => string
}) {
  if (rows.length === 0) return null
  return (
    <div className="space-y-1.5">
      <p>
        {title}
        {hint ? `. ${hint}` : ""}
      </p>
      <MatchRows rows={rows} fact={fact} />
    </div>
  )
}

function MatchRows({
  rows,
  fact,
}: {
  rows: GitMatchBranch[]
  fact?: (row: GitMatchBranch) => string
}) {
  return (
    <ul className="max-h-32 divide-y divide-hairline overflow-auto rounded-md border border-hairline text-hint">
      {rows.map((row) => (
        <li key={row.name} className="flex min-w-0 items-center gap-2 px-2 py-1">
          <span className="min-w-0 flex-1 truncate font-mono">{row.name}</span>
          {fact && <span className="shrink-0 text-muted-foreground">{fact(row)}</span>}
        </li>
      ))}
    </ul>
  )
}
