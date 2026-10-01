"use client"

import { useState } from "react"
import { ChevronLeft, ChevronRight } from "@/components/icons"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import type { ChangeCounts, ChangeProblem } from "@/components/database/grid"
import { changeSummary } from "@/components/database/data/rows"
import type { BrowsePage } from "@/components/database/data/types"
import { PAGE_SIZES, grouped, pageFacts, readableDuration } from "@/components/database/data/view"

/**
 * The foot of the grid while nothing is staged: how long the page took, which
 * rows it is and of how many, and the way to another page.
 *
 * "Of how many" is said honestly. The engine's own estimate is printed with
 * its tilde and only for the whole table — it knows nothing of the filters —
 * and the exact figure is counted when the reader asks, because on a large
 * table counting is a scan. "Next" follows the server's word that a page
 * exists rather than a guess from how full this one is.
 */
export function Pager({
  page,
  filtered,
  exact,
  counting,
  countError,
  onCount,
  pageNumber,
  pageSize,
  onPage,
  onPageSize,
}: {
  page: BrowsePage
  /** Conditions are applied, so the table's estimate is not these rows'. */
  filtered: boolean
  exact: number | null
  counting: boolean
  countError?: Error
  onCount: () => void
  pageNumber: number
  pageSize: number
  onPage: (page: number) => void
  onPageSize: (size: number) => void
}) {
  const estimate =
    !filtered && page.estimatedRows !== null && page.estimatedRows >= 0 ? page.estimatedRows : null
  const facts = pageFacts(page, exact)
  return (
    <>
      <span className="numeric text-hint whitespace-nowrap text-muted-foreground max-lg:hidden">
        {readableDuration(page.duration)}
      </span>
      {page.sort.length === 0 && page.rowCount > 0 && (
        // No key and no sort chosen: the engine promises no order, and pages
        // of an unordered table can repeat a row or skip one. Said, not hidden.
        <span
          className="text-hint whitespace-nowrap text-muted-foreground"
          title="Nothing orders these rows: the table has no key and no sort is chosen, so two pages may show the same row or miss one. Sort by a column to page reliably."
        >
          in no order
        </span>
      )}
      <span className="numeric text-hint whitespace-nowrap text-muted-foreground">
        {facts.from > 0 ? `${grouped(facts.from)}–${grouped(facts.to)}` : "0"}
        {exact !== null ? (
          <> of {grouped(exact)}</>
        ) : (
          estimate !== null && (
            <span title="The engine's own estimate"> of ~{grouped(estimate)}</span>
          )
        )}
      </span>
      {exact === null && (
        <Button
          type="button"
          size="xs"
          variant="ghost"
          className="text-muted-foreground max-sm:hidden"
          pending={counting}
          title={countError?.message}
          onClick={onCount}
        >
          {countError ? "Count again" : "Count exactly"}
        </Button>
      )}
      <Select value={String(pageSize)} onValueChange={(next) => onPageSize(Number(next))}>
        <SelectTrigger
          size="sm"
          aria-label="Rows a page"
          className="h-6 gap-1 px-1.5 text-hint max-sm:hidden sm:data-[size=sm]:h-6"
        >
          <SelectValue />
        </SelectTrigger>
        <SelectContent align="end">
          {PAGE_SIZES.map((size) => (
            <SelectItem key={size} value={String(size)} className="text-xs">
              {size} a page
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <div className="flex items-center gap-0.5">
        <IconAction
          label="Previous page"
          className="size-6"
          disabled={!facts.hasPrevious}
          onClick={() => onPage(pageNumber - 1)}
        >
          <ChevronLeft />
        </IconAction>
        <PageField page={pageNumber} last={facts.lastPage} onPage={onPage} />
        <IconAction
          label="Next page"
          className="size-6"
          disabled={!facts.hasNext}
          onClick={() => onPage(pageNumber + 1)}
        >
          <ChevronRight />
        </IconAction>
      </div>
    </>
  )
}

/** The page number as a field: type one and press Enter to go there. */
function PageField({
  page,
  last,
  onPage,
}: {
  page: number
  last: number | null
  onPage: (page: number) => void
}) {
  const [draft, setDraft] = useState<string | null>(null)
  const go = () => {
    const wanted = Number(draft)
    setDraft(null)
    if (!Number.isInteger(wanted) || wanted < 1) return
    // With a total in hand a page past the end is the last one; without one
    // the page says where it is and how to get back.
    const target = last === null ? wanted : Math.min(wanted, last)
    if (target !== page) onPage(target)
  }
  return (
    <span className="numeric flex items-center gap-1 text-hint text-muted-foreground">
      <Input
        value={draft ?? String(page)}
        inputMode="numeric"
        aria-label="Page"
        className="h-6 w-11 px-1 text-center text-xs sm:h-6 sm:text-xs"
        onChange={(event) => setDraft(event.target.value.replace(/\D/g, ""))}
        onBlur={() => draft !== null && go()}
        onKeyDown={(event) => {
          if (event.key === "Enter") {
            event.preventDefault()
            go()
          } else if (event.key === "Escape") {
            setDraft(null)
          }
        }}
      />
      {last !== null && <span className="whitespace-nowrap">of {grouped(last)}</span>}
    </span>
  )
}

/**
 * The foot of the grid while edits are staged: what the set is made of, and
 * the three things to do with it. Apply is the surface's one command.
 *
 * A set that cannot be sent as it stands — a row whose key was only partly
 * loaded, more rows than one request takes — says why in place of its
 * summary, and offers neither Review nor Apply: there is no request to show.
 */
export function ChangeBar({
  counts,
  problems,
  applying,
  reviewing,
  confirms = false,
  onReview,
  onDiscard,
  onApply,
}: {
  counts: ChangeCounts
  problems: readonly ChangeProblem[]
  applying: boolean
  reviewing: boolean
  /**
   * Apply reads the statements back before it writes: its word carries the
   * ellipsis of a verb that asks first.
   */
  confirms?: boolean
  onReview: () => void
  onDiscard: () => void
  onApply: () => void
}) {
  const blocked = problems.length > 0
  return (
    <div data-slot="change-bar" className="flex min-w-0 flex-wrap items-center justify-end gap-2">
      {blocked ? (
        <span
          role="alert"
          className="min-w-0 truncate text-hint text-destructive"
          title={problems[0].reason}
        >
          {problems[0].reason}
        </span>
      ) : (
        <span className="numeric min-w-0 truncate text-hint font-medium">
          {changeSummary(counts)}
        </span>
      )}
      <Button type="button" size="xs" variant="outline" disabled={applying} onClick={onDiscard}>
        Discard
      </Button>
      <Button
        type="button"
        size="xs"
        variant="outline"
        disabled={blocked || applying}
        pending={reviewing}
        onClick={onReview}
      >
        Review
      </Button>
      <Button
        type="button"
        size="xs"
        disabled={blocked || reviewing}
        pending={applying}
        onClick={onApply}
      >
        {confirms ? "Apply…" : "Apply"}
      </Button>
    </div>
  )
}
