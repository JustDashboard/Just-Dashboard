"use client"

import { useMemo, useState } from "react"
import { Question, Trash, Warning } from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post } from "@/lib/api"
import { bytes } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { CleanupCategory, CleanupPreview, PruneReport } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"
import { ROW_BLEED } from "@/components/row-list"
import { HoverCard, HoverCardContent, HoverCardTrigger } from "@/components/ui/hover-card"
import { LoadingRows } from "@/components/state"
import type { ConfirmFn } from "@/components/docker/shared"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"

/**
 * Cleanup as a decision rather than a button.
 *
 * "Prune" is one word for five sweeps with five different blast radii, one of
 * which deletes databases. A single button labelled with it is either too timid
 * to help — dangling images only, which on a host that redeploys through
 * compose frees nothing — or too dangerous to press.
 *
 * So each category states what it holds, what removing it gives back, and what
 * that costs, and the operator picks. Volumes are on the list because leaving
 * them out sends people hunting for missing disk in the wrong place, and are
 * never selected by default because they are the one category that is the data.
 */
export function CleanupPanel({
  confirm,
  onDone,
  className,
}: {
  confirm: ConfirmFn
  onDone?: () => void
  className?: string
}) {
  const { data, loading, refresh } = usePoll<CleanupPreview>(
    (signal) => get<CleanupPreview>("/docker/cleanup/preview", undefined, signal),
    120_000,
  )
  // `null` means "nothing has been chosen yet", which is not the same as "the
  // empty selection" — the default below is derived from the preview rather
  // than written into state by an effect, so it appears with the data instead
  // of one render after it.
  const [picked, setPicked] = useState<Set<string> | null>(null)
  const [busy, setBusy] = useState(false)

  const selected = useMemo(() => {
    if (picked) return picked
    // The recommended set. Never volumes: that is the one category that is the
    // data, and it is opt-in every single time.
    return new Set(
      (data?.categories ?? []).filter((c) => c.recommended && c.items > 0).map((c) => c.key),
    )
  }, [picked, data])

  const chosen = useMemo(
    () => (data?.categories ?? []).filter((c) => selected.has(c.key)),
    [data, selected],
  )
  const total = chosen.reduce((sum, c) => sum + c.reclaimable, 0)
  const destroys = chosen.some((c) => c.destroys)

  const toggle = (key: string) => {
    const next = new Set(selected)
    if (next.has(key)) next.delete(key)
    else next.add(key)
    setPicked(next)
  }

  const run = () =>
    confirm({
      title: destroys ? "Remove volumes and reclaim disk" : `Reclaim ${bytes(total)}`,
      confirmLabel: "Remove",
      // The typed phrase is required by the route whenever volumes are in the
      // selection; asking for it here keeps the dialog and the server agreed
      // on when it is needed rather than the client guessing.
      phrase: destroys ? "delete volumes" : undefined,
      description: (
        <>
          <ul className="space-y-1.5 text-body">
            {chosen.map((c) => (
              <li key={c.key} className={cn(c.destroys && "text-destructive")}>
                <b>{c.label}</b> — {c.items} {c.items === 1 ? "item" : "items"}
                {c.reclaimable > 0 && `, ${bytes(c.reclaimable)}`}
                <span className="block text-muted-foreground">{c.cost}</span>
              </li>
            ))}
          </ul>
          {!destroys && (
            <p>
              No volume is touched. Everything here comes back from a registry, a rebuild, or the
              next deploy.
            </p>
          )}
        </>
      ),
      action: async (phrase) => {
        setBusy(true)
        try {
          const res = await post<{ reports: PruneReport[]; reclaimed: number }>(
            "/docker/cleanup",
            { categories: [...selected] },
            { confirm: phrase },
          )
          const failed = res.reports.filter((r) => r.error)
          if (failed.length && res.reclaimed === 0) {
            notify.error(`Nothing was removed — ${failed.map((f) => f.error).join("; ")}`)
          } else {
            notify.success(
              res.reclaimed > 0
                ? `Reclaimed ${bytes(res.reclaimed)}`
                : "Removed what was selected; it was occupying no measurable space",
            )
          }
          refresh()
          onDone?.()
        } finally {
          setBusy(false)
        }
      },
    })

  return (
    // Plain: a decision laid out as rows on the page, with the figure it adds
    // up to on a footer rule. The frame it used to carry made the one panel on
    // the overview that asks for a decision look like the ones that report.
    <Panel plain className={cn("animate-rise", className)}>
      <PanelHeader title="Docker cleanup" />
      <PanelBody flush>
        {loading && !data ? (
          <LoadingRows rows={4} className="py-3" />
        ) : (
          <ul className="divide-y divide-hairline">
            {(data?.categories ?? []).map((category) => (
              <CategoryRow
                key={category.key}
                category={category}
                selected={selected.has(category.key)}
                onToggle={() => toggle(category.key)}
              />
            ))}
          </ul>
        )}
      </PanelBody>
      <PanelFooter>
        <span className="text-body">
          {chosen.length === 0 ? (
            <span className="text-muted-foreground">Nothing selected.</span>
          ) : (
            <>
              <b className="numeric">{bytes(total)}</b>
              <span className="text-muted-foreground">
                {" "}
                from {chosen.length} {chosen.length === 1 ? "category" : "categories"}
              </span>
            </>
          )}
        </span>
        <Button
          size="sm"
          variant={destroys ? "destructive" : "outline"}
          className="ml-auto"
          disabled={busy || chosen.length === 0}
          onClick={run}
        >
          <Trash className="size-3.5" />
          {chosen.length === 0
            ? "Reclaim"
            : destroys
              ? `Remove · Reclaim ${bytes(total)}`
              : `Reclaim ${bytes(total)}`}
        </Button>
      </PanelFooter>
    </Panel>
  )
}

/**
 * One category, on one line.
 *
 * It used to be a block: a label, the cost sentence wrapped to however many
 * lines it happened to need, and — for the two categories that have them — a
 * line of example names. Three row heights in one list, with the size and the
 * item count top-aligned against the tallest of them, so the numbers the panel
 * exists to compare never lined up with each other. Four of the six rows were
 * "Nothing in this category. — 0 items" at half opacity, and they were the
 * tallest thing on screen saying the least.
 *
 * The row is now the comparison — name, size, count, in fixed columns — and
 * the reasoning is behind the `?`, which is where the rest of this feature
 * puts a paragraph. Nothing was dropped: the cost sentence and the examples
 * are both in the hover card, and the examples are no longer truncated to fit
 * a line.
 */
function CategoryRow({
  category,
  selected,
  onToggle,
}: {
  category: CleanupCategory
  selected: boolean
  onToggle: () => void
}) {
  const empty = category.items === 0
  return (
    <li
      className={cn(
        "flex min-w-0 items-center gap-3 px-4 py-2.5",
        ROW_BLEED,
        empty && "opacity-50",
        category.destroys && !empty && "bg-wash-danger",
      )}
    >
      <Checkbox
        id={`cleanup-${category.key}`}
        checked={selected}
        disabled={empty}
        onCheckedChange={onToggle}
      />
      {/* The `?` sits beside the name rather than out at the numeric columns:
          it explains the category, and a column of detached question marks
          reads as a column of its own. Outside the `<label>` on purpose — a
          click inside one toggles the checkbox it is for. */}
      <div className="flex min-w-0 flex-1 items-center gap-2">
        <label
          htmlFor={`cleanup-${category.key}`}
          className="flex min-w-0 items-center gap-2 text-body font-medium"
        >
          <span className="truncate">{category.label}</span>
          {category.destroys && (
            <Tag tone="danger" icon={Warning}>
              destroys data
            </Tag>
          )}
        </label>
        <CategoryExplain category={category} empty={empty} />
      </div>
      {/* Fixed columns rather than an auto-sized pair, so eight gigabytes and a
          dash sit on the same right edge and the list can be read down. */}
      <span className="numeric w-20 shrink-0 text-right text-body font-medium">
        {category.reclaimable > 0 ? bytes(category.reclaimable) : "—"}
      </span>
      <span className="numeric w-16 shrink-0 text-right text-hint text-muted-foreground">
        {category.items} {category.items === 1 ? "item" : "items"}
      </span>
    </li>
  )
}

/**
 * What removing this category costs, and what is in it.
 *
 * The cost sentence is the whole decision — "a rollback would have to pull
 * again" is the reason somebody leaves images alone — so it is one gesture
 * away rather than gone. The examples come with it because "which images?" is
 * the immediate next question and the row has no room to answer it.
 */
function CategoryExplain({ category, empty }: { category: CleanupCategory; empty: boolean }) {
  return (
    <HoverCard openDelay={150}>
      <HoverCardTrigger asChild>
        <button
          type="button"
          aria-label={`What removing ${category.label} costs`}
          className="shrink-0 cursor-help text-muted-foreground focus-ring transition-colors hover:text-foreground"
        >
          <Question className="size-3.5" />
        </button>
      </HoverCardTrigger>
      <HoverCardContent className="w-80 space-y-1.5 text-xs leading-relaxed">
        <p className="text-body font-medium">{category.label}</p>
        <p className="text-muted-foreground">
          {empty ? "Nothing on this server is in this category right now." : category.cost}
        </p>
        {category.examples.length > 0 && (
          <p className="font-mono text-hint break-all text-muted-foreground">
            {category.examples.join(", ")}
            {category.items > category.examples.length &&
              ` and ${category.items - category.examples.length} more`}
          </p>
        )}
      </HoverCardContent>
    </HoverCard>
  )
}
