"use client"

import { useState } from "react"
import { get, post, errorMessage } from "@/lib/api"
import type { GitResult } from "@/lib/types"
import type { PreviewContext } from "@/components/git/preview-panel"
import { PreviewHeader } from "@/components/git/preview-header"
import { Field } from "@/components/form"
import { ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

type Item = { sha: string; action: string; message: string }
type Plan = { base: string; head: string; items: Item[] }

export function RebasePreview({ ctx, onClose }: { ctx: PreviewContext; onClose: () => void }) {
  const [base, setBase] = useState("")
  const [plan, setPlan] = useState<Plan>()
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<Error>()
  const load = async () => {
    setLoading(true)
    setError(undefined)
    try {
      setPlan(await get<Plan>("/git/rebase/plan", { path: ctx.repoPath, base }))
    } catch (e) {
      setError(new Error(errorMessage(e)))
      setPlan(undefined)
    } finally {
      setLoading(false)
    }
  }
  const update = (index: number, change: Partial<Item>) =>
    setPlan(
      (p) =>
        p && {
          ...p,
          items: p.items.map((item, i) => (i === index ? { ...item, ...change } : item)),
        },
    )
  const move = (index: number, direction: number) =>
    setPlan((p) => {
      if (!p) return p
      const items = [...p.items]
      ;[items[index], items[index + direction]] = [items[index + direction], items[index]]
      return { ...p, items }
    })
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PreviewHeader
        title="Edit local history"
        mono={false}
        subtitle={ctx.branch}
        onClose={onClose}
      />
      <div className="min-h-0 flex-1 space-y-3 overflow-auto p-3">
        <p className="text-hint text-muted-foreground">
          Choose the commit to keep as the base. Commits after it appear oldest first. Only linear
          history absent from the fetched remote branches can be edited; fetch first if the remote
          may have changed.
        </p>
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            void load()
          }}
        >
          <Field label="Base branch or commit">
            <Input
              aria-label="Rebase base"
              value={base}
              onChange={(e) => {
                setBase(e.target.value)
                setPlan(undefined)
              }}
              placeholder="origin/main"
            />
          </Field>
          <Button size="sm" variant="outline" disabled={loading || !base.trim() || !!ctx.busy}>
            {loading ? "Loading…" : "Load commits"}
          </Button>
        </form>
        {error && <ErrorState error={error} />}
        {plan && (
          <>
            <ol className="divide-y divide-hairline border-y border-hairline">
              {plan.items.map((item, i) => (
                <li key={item.sha} className="space-y-2 py-3">
                  <div className="flex items-center gap-2">
                    <span className="numeric font-mono text-hint text-muted-foreground">
                      {i + 1} · {item.sha.slice(0, 7)}
                    </span>
                    <span className="min-w-0 flex-1 truncate text-xs">
                      {item.message.split("\n")[0]}
                    </span>
                  </div>
                  <div className="flex gap-2">
                    <Select value={item.action} onValueChange={(action) => update(i, { action })}>
                      <SelectTrigger
                        aria-label={`Action for commit ${i + 1}`}
                        className="min-w-0 flex-1"
                      >
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="pick">Keep</SelectItem>
                        <SelectItem value="reword">Edit message</SelectItem>
                        <SelectItem value="squash">Squash into previous</SelectItem>
                        <SelectItem value="drop">Drop</SelectItem>
                      </SelectContent>
                    </Select>
                    <Button
                      size="xs"
                      variant="ghost"
                      disabled={i === 0}
                      aria-label={`Move commit ${i + 1} earlier`}
                      onClick={() => move(i, -1)}
                    >
                      Earlier
                    </Button>
                    <Button
                      size="xs"
                      variant="ghost"
                      disabled={i === plan.items.length - 1}
                      aria-label={`Move commit ${i + 1} later`}
                      onClick={() => move(i, 1)}
                    >
                      Later
                    </Button>
                  </div>
                  {item.action === "reword" && (
                    <Textarea
                      aria-label={`Message for commit ${i + 1}`}
                      value={item.message}
                      onChange={(e) => update(i, { message: e.target.value })}
                      rows={3}
                      maxLength={60000}
                    />
                  )}
                </li>
              ))}
            </ol>
            <p className="text-hint text-muted-foreground">
              A recovery branch keeps the original commits. If a conflict occurs, resolve it in
              Changes, then continue or abort.
            </p>
            {ctx.canControl && (
              <Button
                size="sm"
                variant="outline"
                disabled={!!ctx.busy}
                onClick={() =>
                  ctx.confirm({
                    title: "Rewrite these local commits?",
                    description: `Apply this order and these messages to ${ctx.branch}. The original history will remain on a recovery branch.`,
                    confirmLabel: "Apply rebase",
                    action: async () => {
                      await ctx.run("Local history updated", () =>
                        post<GitResult>("/git/rebase", plan, { query: { path: ctx.repoPath } }),
                      )
                      setPlan(undefined)
                    },
                  })
                }
              >
                Apply rebase
              </Button>
            )}
          </>
        )}
      </div>
    </div>
  )
}
