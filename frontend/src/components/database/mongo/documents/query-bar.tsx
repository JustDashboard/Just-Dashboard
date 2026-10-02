"use client"

import { ClockRewind, SettingsSliders } from "@/components/icons"
import { relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import { ChipCount } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { CodeField } from "@/components/database/mongo/code-field"
import {
  draftLabel,
  optionCount,
  type HistoryEntry,
  type QueryDraft,
  type QueryField,
} from "@/components/database/mongo/query"

type FieldSpec = {
  field: QueryField
  label: string
  placeholder: string
  /** A number typed on one line, rather than a document. */
  number?: boolean
}

const OPTIONS: FieldSpec[] = [
  // Short on purpose: a field grows with its text, and a placeholder that wraps makes it tall while empty.
  { field: "project", label: "Project", placeholder: "{ name: 1 }" },
  { field: "sort", label: "Sort", placeholder: "{ age: -1 }" },
  { field: "collation", label: "Collation", placeholder: '{ locale: "en" }' },
  { field: "hint", label: "Index hint", placeholder: "email_1" },
  { field: "skip", label: "Skip", placeholder: "0", number: true },
  { field: "limit", label: "Limit", placeholder: "none", number: true },
  { field: "maxTime", label: "Max time (ms)", placeholder: "30000", number: true },
]

/**
 * The query bar: what to find, as the fields MongoDB's own `find` takes.
 *
 * The filter is always there; Project, Sort, Collation, an index hint, Skip,
 * Limit and a time limit fold behind Options, which says how many of them are
 * set. Each takes Extended JSON or the shell's spelling — unquoted keys,
 * `ObjectId("…")`, `ISODate("…")`, a `/pattern/` — and is checked as it is
 * typed for what can be told here (a bracket left open); the server's own
 * refusal, which knows the operators, lands under the field it is about.
 *
 * Enter runs the query from any field; Shift+Enter breaks the line.
 */
export function QueryBar({
  draft,
  onChange,
  problems,
  optionsOpen,
  onOptionsOpen,
  running,
  onFind,
  onReset,
  onExplain,
  history,
  onPick,
  onClearHistory,
}: {
  draft: QueryDraft
  onChange: (draft: QueryDraft) => void
  /** What is wrong with a field: found here as it is typed, or said by the server. */
  problems: Partial<Record<QueryField, string>>
  optionsOpen: boolean
  onOptionsOpen: (open: boolean) => void
  running: boolean
  onFind: () => void
  onReset: () => void
  /** Absent where the engine has no plan to show. */
  onExplain?: () => void
  /** This collection's earlier queries, newest first. */
  history: readonly HistoryEntry[]
  onPick: (draft: QueryDraft) => void
  onClearHistory: () => void
}) {
  const set = (field: QueryField, value: string) => onChange({ ...draft, [field]: value })
  const set_ = optionCount(draft)
  const invalid = Object.keys(problems).length > 0
  // A problem in a folded field would be invisible: the fold opens for it.
  const open = optionsOpen || OPTIONS.some((option) => problems[option.field])

  return (
    <form
      data-slot="mongo-query-bar"
      aria-label="Query"
      // Laid out by the bar's own width: the column it sits in is as narrow as
      // the rail beside it leaves, whatever the window is.
      className="@container shrink-0 space-y-2 border-b border-hairline px-3 py-2"
      onSubmit={(event) => {
        event.preventDefault()
        onFind()
      }}
    >
      <div className="flex min-w-0 flex-wrap items-start gap-x-2 gap-y-1.5">
        <label
          htmlFor="mongo-query-filter"
          className="eyebrow flex h-8 w-11 shrink-0 items-center max-sm:h-auto max-sm:w-full"
        >
          Filter
        </label>
        <div className="min-w-0 flex-1 basis-64">
          <CodeField
            id="mongo-query-filter"
            dense
            value={draft.filter}
            invalid={Boolean(problems.filter)}
            aria-describedby={problems.filter ? "mongo-query-filter-problem" : undefined}
            placeholder={'{ status: "paid", total: { $gt: 100 } }'}
            onChange={(value) => set("filter", value)}
            onSubmit={onFind}
          />
          {problems.filter && (
            <p
              id="mongo-query-filter-problem"
              role="alert"
              className="pt-1 text-hint leading-relaxed text-destructive"
            >
              {problems.filter}
            </p>
          )}
        </div>
        <div className="flex min-w-0 flex-wrap items-center gap-1.5">
          <Button
            type="button"
            size="sm"
            variant="outline"
            aria-expanded={open}
            aria-controls="mongo-query-options"
            className={cn("h-8", open && "bg-accent")}
            onClick={() => onOptionsOpen(!open)}
          >
            <SettingsSliders />
            Options
            {set_ > 0 && <ChipCount>{set_}</ChipCount>}
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                type="button"
                size="icon-sm"
                variant="ghost"
                aria-label="Earlier queries"
                className="[&_svg:not([class*='size-'])]:size-3.5"
              >
                <ClockRewind />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-96 max-w-[calc(100vw-2rem)]">
              <DropdownMenuLabel>Earlier queries of this collection</DropdownMenuLabel>
              {history.length === 0 ? (
                <p className="px-2 py-3 text-hint text-muted-foreground">
                  A query is kept here once it has been run, until the tab is closed.
                </p>
              ) : (
                <>
                  <div className="max-h-72 overflow-y-auto">
                    {history.map((entry) => (
                      <DropdownMenuItem
                        key={entry.at}
                        className="items-baseline gap-3"
                        onSelect={() => onPick(entry.draft)}
                      >
                        <span className="min-w-0 flex-1 truncate font-mono text-xs">
                          {draftLabel(entry.draft)}
                        </span>
                        <span className="shrink-0 text-hint text-muted-foreground">
                          {relativeTime(new Date(entry.at).toISOString())}
                        </span>
                      </DropdownMenuItem>
                    ))}
                  </div>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem onSelect={onClearHistory}>Forget them</DropdownMenuItem>
                </>
              )}
            </DropdownMenuContent>
          </DropdownMenu>
          {onExplain && (
            <Button
              type="button"
              size="sm"
              variant="outline"
              className="h-8"
              disabled={invalid}
              onClick={onExplain}
            >
              Explain
            </Button>
          )}
          <Button type="button" size="sm" variant="ghost" className="h-8" onClick={onReset}>
            Reset
          </Button>
          <Button type="submit" size="sm" className="h-8" pending={running} disabled={invalid}>
            Find
          </Button>
        </div>
      </div>

      {open && (
        <div
          id="mongo-query-options"
          className="grid grid-cols-2 gap-x-3 gap-y-2 @2xl:grid-cols-4 @5xl:grid-cols-[repeat(4,minmax(0,1fr))_repeat(3,6.5rem)]"
        >
          {OPTIONS.map((option) => {
            const id = `mongo-query-${option.field}`
            const problem = problems[option.field]
            return (
              <div key={option.field} className="min-w-0 space-y-1">
                <label htmlFor={id} className="eyebrow block">
                  {option.label}
                </label>
                {option.number ? (
                  <Input
                    id={id}
                    inputMode="numeric"
                    value={draft[option.field]}
                    aria-invalid={Boolean(problem) || undefined}
                    aria-describedby={problem ? `${id}-problem` : undefined}
                    placeholder={option.placeholder}
                    className="numeric h-9 bg-surface-sunken px-2 font-mono shadow-none sm:h-8 sm:text-xs dark:bg-surface-sunken"
                    onChange={(event) => set(option.field, event.target.value)}
                  />
                ) : (
                  <CodeField
                    id={id}
                    dense
                    value={draft[option.field]}
                    invalid={Boolean(problem)}
                    aria-describedby={problem ? `${id}-problem` : undefined}
                    placeholder={option.placeholder}
                    onChange={(value) => set(option.field, value)}
                    onSubmit={onFind}
                  />
                )}
                {problem && (
                  <p
                    id={`${id}-problem`}
                    role="alert"
                    className="text-hint leading-relaxed text-destructive"
                  >
                    {problem}
                  </p>
                )}
              </div>
            )
          })}
        </div>
      )}
    </form>
  )
}
