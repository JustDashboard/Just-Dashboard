"use client"

import { useMemo, useState } from "react"
import { Check, ChevronDown, Cross, EyeOff } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { LogFacet, LogLine } from "@/lib/types"
import type { LogFields, LogFilterState } from "@/components/logs/types"
import {
  clearField,
  describePredicate,
  exactValue,
  fieldsEqual,
  fieldsOf,
  hideField,
  lineValue,
  matchFields,
  toggleField,
  type LogLevel,
} from "@/lib/log-filter"
import { fieldOf, isFilterable } from "@/lib/log-fields"
import type { LensView, LogLens } from "@/lib/log-lenses"
import { FieldValue } from "@/components/logs/field-value"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { IconAction } from "@/components/icon-action"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command"

/** How many of a key's values the popover lists. */
const TOP = 8

const NO_VIEWS: LensView[] = []

function sameLevels(a: readonly string[], b: readonly string[]) {
  return a.length === b.length && a.every((level) => b.includes(level))
}

function viewSelected(view: LensView, filter: LogFilterState) {
  return (
    fieldsEqual(fieldsOf(filter), view.fields ?? {}) &&
    sameLevels(filter.levels, view.levels ?? []) &&
    (view.q === undefined || filter.q === view.q)
  )
}

/**
 * Applying a view replaces the question's fields and levels — it is a
 * question of its own, not a narrowing of the last one — and its search only
 * if it has one. Pressing the chosen view again lets all of that go.
 */
function applyView(filter: LogFilterState, view: LensView): LogFilterState {
  if (viewSelected(view, filter)) {
    return { ...filter, fields: {}, levels: [], ...(view.q !== undefined ? { q: "" } : {}) }
  }
  return {
    ...filter,
    fields: view.fields ?? {},
    levels: view.levels ?? [],
    ...(view.q !== undefined ? { q: view.q } : {}),
  }
}

function levelHolds(line: LogLine, levels: readonly LogLevel[] | undefined) {
  return !levels?.length || levels.includes((line.level || "unknown") as LogLevel)
}

/**
 * A view's count out of History's facets, where it can be read off them: a
 * view on one key's plain values is the sum of those values' counts, one on
 * levels alone the sum of theirs. Anything else would need its own search,
 * and a chip that says nothing is better than one that guesses.
 */
function countFromFacets(view: LensView, facets: Record<string, LogFacet>): number | undefined {
  const entries = Object.entries(view.fields ?? {})
  const sum = (key: string, values: readonly string[]) => {
    const facet = facets[key]
    if (!facet) return undefined
    const wanted = new Set(values.map((v) => v.toLowerCase()))
    return facet.values.reduce((n, v) => (wanted.has(v.value.toLowerCase()) ? n + v.count : n), 0)
  }
  if (entries.length === 0 && view.levels?.length) return sum("level", view.levels)
  if (entries.length === 1 && !view.levels?.length) {
    const [key, values] = entries[0]
    // Plain values only: a negation or a comparison is not a sum of rows.
    if (values.every((v) => exactValue(v) === v && v !== "*")) return sum(key, values)
  }
  return undefined
}

type Tally = { value: string; count: number; errors: number }

/** A key's top values over lines already on screen: the live pane's facets. */
function tally(lines: LogLine[], key: string): { values: Tally[]; missing: number } {
  const counts = new Map<string, Tally>()
  let missing = 0
  for (const line of lines) {
    if (line.cont) continue
    const value = lineValue(line, key)
    if (value === undefined) {
      missing++
      continue
    }
    const entry = counts.get(value) ?? { value, count: 0, errors: 0 }
    entry.count++
    if (line.level === "error" || line.level === "critical") entry.errors++
    counts.set(value, entry)
  }
  return { values: [...counts.values()].sort((a, b) => b.count - a.count), missing }
}

/**
 * The lens's row: the questions a reader of this log asks first, one press
 * each, and the values its lines carry, ranked.
 *
 * One row that scrolls sideways rather than wrapping, like the level chips
 * under it — a third row of chrome is already one more than the lines want.
 * The quick views are chips with their counts; the fields are one chip
 * opening a popover of each facet's top values, because a chip per facet was
 * a row of ten buttons; and what is narrowed shows as chips that say so and
 * go on a press, including a lens's own defaults, which hide its noise in the
 * open rather than behind the reader's back.
 *
 * Counts in History come from the search's facets; in Live they are tallied
 * here over the lines on screen — the popover's only while it is open.
 */
export function LensBar({
  lens,
  lensId,
  filter,
  onFilterChange,
  lines,
  facets,
}: {
  lens: LogLens | undefined
  /** The lens the lines were read through, which names event values. */
  lensId?: string
  filter: LogFilterState
  onFilterChange: (filter: LogFilterState) => void
  /** The live buffer, for tallies; absent in History. */
  lines?: LogLine[]
  /** History's facets over every match; absent in Live. */
  facets?: Record<string, LogFacet>
}) {
  const fields = fieldsOf(filter)
  const views = lens?.views ?? NO_VIEWS

  const counts = useMemo(() => {
    const out = new Map<string, number>()
    if (facets) {
      for (const view of views) {
        const n = countFromFacets(view, facets)
        if (n !== undefined) out.set(view.id, n)
      }
    } else if (lines) {
      for (const view of views) {
        let n = 0
        for (const line of lines) {
          if (!line.cont && levelHolds(line, view.levels) && matchFields(line, view.fields ?? {}))
            n++
        }
        out.set(view.id, n)
      }
    }
    return out
  }, [views, facets, lines])

  const defaults = lens?.defaults
  const onDefaults =
    defaults !== undefined &&
    Object.keys(fields).length > 0 &&
    fieldsEqual(fields, defaults.fields ?? {}) &&
    (!defaults.levels || sameLevels(filter.levels, defaults.levels))
  const selectedView = views.find((view) => viewSelected(view, filter))
  const predicateKeys = selectedView || onDefaults ? [] : Object.keys(fields)

  if (views.length === 0 && !lens?.facets.length && Object.keys(fields).length === 0) return null

  const setFields = (next: LogFields) => onFilterChange({ ...filter, fields: next })

  return (
    <ChipStrip className="scroll-affordance shrink-0 border-b border-hairline px-2 py-1 max-sm:mx-0 max-sm:my-0 max-sm:px-2 max-sm:py-1 sm:flex-nowrap sm:overflow-x-auto">
      {views.map((view) => {
        const count = counts.get(view.id)
        return (
          <FilterChip
            key={view.id}
            selected={selectedView === view}
            onClick={() => onFilterChange(applyView(filter, view))}
            title={view.requires ? `Logged only with ${view.requires} set` : undefined}
          >
            {view.label}
            {count !== undefined && count > 0 && <ChipCount>{count.toLocaleString()}</ChipCount>}
          </FilterChip>
        )
      })}

      {lens && lens.facets.length > 0 && (
        <FieldsPopover
          lens={lens}
          lensId={lensId}
          fields={fields}
          onFieldsChange={setFields}
          lines={lines}
          facets={facets}
        />
      )}

      {onDefaults && (
        <FilterChip
          selected
          aria-label={`Clear the ${defaults.label.toLowerCase()} filter`}
          title="The lens hides these by default — press to show them"
          onClick={() =>
            onFilterChange({ ...filter, fields: {}, levels: defaults.levels ? [] : filter.levels })
          }
        >
          {defaults.label}
          <Cross aria-hidden className="size-3 text-muted-foreground" />
        </FilterChip>
      )}

      {predicateKeys.map((key) => {
        const label = fieldOf(key).label
        const values = fields[key]
        return (
          <FilterChip
            key={key}
            selected
            aria-label={`Clear the ${label} filter`}
            title={`${label}: ${values.map(describePredicate).join(" or ")} — press to clear`}
            onClick={() => setFields(clearField(fields, key))}
          >
            <span className="text-muted-foreground">{label}</span>
            <span className="max-w-48 truncate font-mono">
              {values.map(describePredicate).join(", ")}
            </span>
            <Cross aria-hidden className="size-3 text-muted-foreground" />
          </FilterChip>
        )
      })}
    </ChipStrip>
  )
}

/**
 * Each facet's top values, one press from including one and a second
 * control from excluding it — the `UnitPicker`'s shape, because a list of
 * values to search through and pick from is what that is.
 */
function FieldsPopover({
  lens,
  lensId,
  fields,
  onFieldsChange,
  lines,
  facets,
}: {
  lens: LogLens
  lensId?: string
  fields: LogFields
  onFieldsChange: (fields: LogFields) => void
  lines?: LogLine[]
  facets?: Record<string, LogFacet>
}) {
  const [open, setOpen] = useState(false)
  const keys = lens.facets.filter((key) => key !== "level" && isFilterable(key))
  const active = keys.filter((key) => fields[key]?.length).length

  const groups = useMemo(() => {
    if (!open) return []
    return keys.map((key) => {
      if (facets?.[key]) {
        const facet = facets[key]
        return { key, values: facet.values.slice(0, TOP) as Tally[] }
      }
      return { key, values: lines ? tally(lines, key).values.slice(0, TOP) : [] }
    })
    // `keys` is derived from the lens, which the memo already follows.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, lens, lines, facets])

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <FilterChip selected={active > 0} aria-expanded={open}>
          Fields
          {active > 0 && <ChipCount>{active}</ChipCount>}
          <ChevronDown
            aria-hidden
            className={cn("size-3 transition-transform", open && "rotate-180")}
          />
        </FilterChip>
      </PopoverTrigger>
      <PopoverContent className="w-80 p-0" align="start">
        <Command>
          <CommandInput placeholder="Find a value…" />
          <CommandList className="max-h-96">
            <CommandEmpty>
              {lines || facets
                ? "No value by that name."
                : "Search the history to rank its values."}
            </CommandEmpty>
            {groups
              .filter((group) => group.values.length > 0)
              .map((group) => {
                const label = fieldOf(group.key).label
                return (
                  <CommandGroup key={group.key} heading={label}>
                    {group.values.map((entry) => {
                      const included = fields[group.key]?.includes(exactValue(entry.value))
                      const excluded = fields[group.key]?.includes(`!${entry.value}`)
                      const share = entry.count ? entry.errors / entry.count : 0
                      return (
                        <CommandItem
                          key={entry.value}
                          value={`${group.key} ${entry.value}`}
                          className="group"
                          onSelect={() =>
                            onFieldsChange(toggleField(fields, group.key, entry.value))
                          }
                        >
                          <Check
                            className={cn(
                              "size-3.5 shrink-0",
                              included ? "opacity-100" : "opacity-0",
                            )}
                          />
                          <span
                            className={cn(
                              "flex min-w-0 flex-1 items-center text-xs",
                              excluded && "line-through opacity-60",
                            )}
                          >
                            <FieldValue name={group.key} value={entry.value} lens={lensId} />
                          </span>
                          {share > 0 && (
                            <span
                              className="numeric shrink-0 text-hint text-destructive"
                              title={`${entry.errors.toLocaleString()} of them errors`}
                            >
                              {Math.round(share * 100)}%
                            </span>
                          )}
                          <span className="numeric shrink-0 text-hint text-muted-foreground">
                            {entry.count.toLocaleString()}
                          </span>
                          <IconAction
                            reveal
                            label={`Hide lines where ${label} is ${entry.value}`}
                            className="size-6"
                            onClick={(event) => {
                              event.stopPropagation()
                              onFieldsChange(hideField(fields, group.key, entry.value))
                            }}
                          >
                            <EyeOff />
                          </IconAction>
                        </CommandItem>
                      )
                    })}
                  </CommandGroup>
                )
              })}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}
