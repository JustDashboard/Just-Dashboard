"use client"

import { MenuItemText } from "@/components/ui/menu-item-text"

import { useId, useState } from "react"
import { ArrowDown, ArrowUp, Cross, Filter as FilterIcon, Plus } from "@/components/icons"
import { cn } from "@/lib/utils"
import { Segments } from "@/components/deploy/settings/segments"
import { Field } from "@/components/form"
import { FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import type { GridColumn } from "@/components/database/grid"
import {
  MAX_FILTERS,
  OPERATORS,
  buildFilter,
  filterLabel,
  filterValues,
  operatorsFor,
  parseList,
} from "@/components/database/data/filters"
import type { Filter, FilterOp, SortKey } from "@/components/database/data/types"

/**
 * The conditions and the order of the rows, drawn as what they are: one chip
 * a condition, one chip a sort key, each removable where it stands.
 *
 * A condition is written in a small form and applied when the reader says so
 * — Enter, or Apply. The old filter row wrote each keystroke straight into the
 * request, so typing "acme" was four scans of the table and four blanked
 * grids. And an empty value is a value here: `name = ""` asks for the empty
 * string, which is not the same rows as `name is NULL`.
 *
 * Which operators exist is the engine's to say (`operators`, from the server's
 * driver catalogue); which of them a column is offered follows what it holds.
 */
export function FilterBar({
  columns,
  filters,
  match,
  sort,
  operators,
  onFiltersChange,
  onMatchChange,
  onSortChange,
}: {
  columns: readonly GridColumn[]
  filters: readonly Filter[]
  match: "all" | "any"
  sort: readonly SortKey[]
  /** The operators this engine's browse takes. */
  operators: readonly string[]
  onFiltersChange: (filters: Filter[]) => void
  onMatchChange: (match: "all" | "any") => void
  onSortChange: (sort: SortKey[]) => void
}) {
  const byName = new Map(columns.map((column) => [column.name, column]))
  const unsorted = columns.filter((column) => !sort.some((key) => key.column === column.name))
  const canFilter = columns.length > 0 && operators.length > 0

  return (
    <>
      {filters.map((filter, index) => (
        <FilterEntry
          key={`${index}:${filter.column}:${filter.op}`}
          filter={filter}
          columns={columns}
          operators={operators}
          label={filterLabel(filter, byName.get(filter.column)?.kind)}
          onChange={(next) => onFiltersChange(filters.map((f, i) => (i === index ? next : f)))}
          onRemove={() => onFiltersChange(filters.filter((_, i) => i !== index))}
        />
      ))}
      {canFilter && filters.length < MAX_FILTERS && (
        <FilterPopover
          columns={columns}
          operators={operators}
          onApply={(filter) => onFiltersChange([...filters, filter])}
        >
          <Button type="button" size="xs" variant="outline" className="shrink-0 max-sm:h-8">
            <FilterIcon />
            {filters.length === 0 ? "Filter" : "Add filter"}
          </Button>
        </FilterPopover>
      )}
      {filters.length > 1 && (
        <div role="group" aria-label="Rows match" className="flex shrink-0 items-center gap-0.5">
          <FilterChip
            selected={match === "all"}
            className="h-8 px-2 sm:h-6"
            onClick={() => onMatchChange("all")}
          >
            All
          </FilterChip>
          <FilterChip
            selected={match === "any"}
            className="h-8 px-2 sm:h-6"
            onClick={() => onMatchChange("any")}
          >
            Any
          </FilterChip>
        </div>
      )}

      {/* The order travels as one run: where the strip wraps, its keys stay
          together on their own line rather than trail the conditions singly. */}
      <div
        role="group"
        aria-label="Order of the rows"
        className="flex min-w-0 shrink-0 items-center gap-1.5 sm:shrink sm:flex-wrap"
      >
        {sort.map((key, index) => (
          <span
            key={key.column}
            role="group"
            aria-label={`Sorted by ${key.column}, ${key.desc ? "descending" : "ascending"}`}
            className="inline-flex h-8 min-w-0 shrink-0 items-center rounded-md border border-hairline bg-accent sm:h-6"
          >
            <button
              type="button"
              aria-label={`Sort ${key.column} ${key.desc ? "ascending" : "descending"} instead`}
              className="flex h-full min-w-0 items-center gap-1 rounded-l-md pr-1 pl-1.5 text-hint font-medium focus-ring-inset"
              onClick={() =>
                onSortChange(sort.map((k, i) => (i === index ? { ...k, desc: !k.desc } : k)))
              }
            >
              {key.desc ? <ArrowDown className="size-3" /> : <ArrowUp className="size-3" />}
              <span className="max-w-40 truncate font-mono">{key.column}</span>
              {sort.length > 1 && <span className="numeric opacity-60">{index + 1}</span>}
            </button>
            <button
              type="button"
              aria-label={`Stop sorting by ${key.column}`}
              className="flex h-full w-6 items-center justify-center rounded-r-md text-muted-foreground focus-ring-inset transition-colors hover:text-foreground max-sm:w-8"
              onClick={() => onSortChange(sort.filter((_, i) => i !== index))}
            >
              <Cross className="size-3" />
            </button>
          </span>
        ))}
        {unsorted.length > 0 && (
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              {sort.length === 0 ? (
                <Button
                  type="button"
                  size="xs"
                  variant="ghost"
                  className="shrink-0 text-muted-foreground max-sm:h-8"
                >
                  <ArrowUp />
                  Sort
                </Button>
              ) : (
                <Button
                  type="button"
                  size="icon-xs"
                  variant="ghost"
                  aria-label="Then sort by another column"
                  className="shrink-0 text-muted-foreground max-sm:size-8"
                >
                  <Plus />
                </Button>
              )}
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start" className="max-h-72 w-56">
              {unsorted.map((column) => (
                <DropdownMenuItem
                  key={column.key}
                  onSelect={() => onSortChange([...sort, { column: column.name, desc: false }])}
                >
                  <MenuItemText
                    hint={
                      <span className="text-hint text-muted-foreground">{column.typeName}</span>
                    }
                  >
                    <span className="min-w-0 flex-1 truncate font-mono text-xs">{column.name}</span>
                  </MenuItemText>
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        )}
      </div>
    </>
  )
}

/** One applied condition: its sentence, which opens the form again, and the way to drop it. */
function FilterEntry({
  filter,
  label,
  columns,
  operators,
  onChange,
  onRemove,
}: {
  filter: Filter
  label: string
  columns: readonly GridColumn[]
  operators: readonly string[]
  onChange: (filter: Filter) => void
  onRemove: () => void
}) {
  return (
    <span
      role="group"
      aria-label={`Filter: ${label}`}
      className="inline-flex h-8 max-w-full min-w-0 shrink-0 items-center rounded-md border border-hairline bg-accent sm:h-6"
    >
      <FilterPopover
        columns={columns}
        operators={operators}
        initial={filter}
        onApply={onChange}
        onRemove={onRemove}
      >
        <button
          type="button"
          className="flex h-full min-w-0 items-center rounded-l-md pr-1 pl-2 text-hint font-medium focus-ring-inset"
        >
          <span className="max-w-64 truncate font-mono">{label}</span>
        </button>
      </FilterPopover>
      <button
        type="button"
        aria-label={`Remove the filter ${label}`}
        className="flex h-full w-6 shrink-0 items-center justify-center rounded-r-md text-muted-foreground focus-ring-inset transition-colors hover:text-foreground max-sm:w-8"
        onClick={onRemove}
      >
        <Cross className="size-3" />
      </button>
    </span>
  )
}

function FilterPopover({
  columns,
  operators,
  initial,
  onApply,
  onRemove,
  children,
}: {
  columns: readonly GridColumn[]
  operators: readonly string[]
  initial?: Filter
  onApply: (filter: Filter) => void
  onRemove?: () => void
  children: React.ReactNode
}) {
  const [open, setOpen] = useState(false)
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>{children}</PopoverTrigger>
      <PopoverContent align="start" className="w-80 p-3">
        {/* Mounted with the popover, so the form starts from the filter as it stands each time. */}
        <FilterForm
          columns={columns}
          operators={operators}
          initial={initial}
          onApply={(filter) => {
            onApply(filter)
            setOpen(false)
          }}
          onRemove={
            onRemove &&
            (() => {
              onRemove()
              setOpen(false)
            })
          }
        />
      </PopoverContent>
    </Popover>
  )
}

/** What a typed value is expected to look like, by what the column holds. */
function placeholderFor(column: GridColumn | undefined): string {
  switch (column?.kind) {
    case "number":
      return "A number"
    case "date":
      return "YYYY-MM-DD"
    case "datetime":
      return "YYYY-MM-DD HH:MM:SS"
    case "time":
      return "HH:MM:SS"
    case "uuid":
      return "A UUID"
    default:
      return "Empty means the empty string"
  }
}

function FilterForm({
  columns,
  operators,
  initial,
  onApply,
  onRemove,
}: {
  columns: readonly GridColumn[]
  operators: readonly string[]
  initial?: Filter
  onApply: (filter: Filter) => void
  onRemove?: () => void
}) {
  const ids = useId()
  const [columnName, setColumnName] = useState(initial?.column ?? columns[0]?.name ?? "")
  const column = columns.find((entry) => entry.name === columnName)
  const offered = column ? operatorsFor(column, operators) : []
  const [chosen, setChosen] = useState<FilterOp | undefined>(initial?.op)
  // An operator the newly chosen column is not offered falls back to its first.
  const op = chosen && offered.includes(chosen) ? chosen : offered[0]
  const before = initial ? filterValues(initial) : []
  const [one, setOne] = useState(before[0] ?? "")
  const [two, setTwo] = useState(before[1] ?? "")
  const [list, setList] = useState(before.join("\n"))
  const [error, setError] = useState<string>()

  if (!column || !op) {
    return <p className="text-hint text-muted-foreground">This table has no column to filter on.</p>
  }
  const arity = OPERATORS[op].arity
  const boolean = column.kind === "boolean"
  const labels = column.kind === "enum" ? (column.enumValues ?? []) : []

  const submit = () => {
    const values =
      arity === "many"
        ? parseList(list)
        : arity === "two"
          ? [one.trim(), two.trim()]
          : arity === "one"
            ? [boolean && one !== "0" ? "1" : one]
            : []
    const built = buildFilter(column.name, op, values)
    if (built.error !== undefined) {
      setError(built.error)
      return
    }
    onApply(built.filter)
  }
  const onEnter = (event: React.KeyboardEvent) => {
    if (event.key !== "Enter") return
    event.preventDefault()
    submit()
  }

  return (
    <form
      className="grid gap-3"
      onSubmit={(event) => {
        event.preventDefault()
        submit()
      }}
    >
      <Field label="Column" htmlFor={`${ids}-column`}>
        <Select
          value={column.name}
          onValueChange={(next) => {
            setColumnName(next)
            setError(undefined)
          }}
        >
          <SelectTrigger id={`${ids}-column`} size="sm" className="w-full font-mono text-xs">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {columns.map((entry) => (
              <SelectItem key={entry.key} value={entry.name} className="font-mono text-xs">
                {entry.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      <Field label="Condition" htmlFor={`${ids}-op`}>
        <Select
          value={op}
          onValueChange={(next) => {
            setChosen(next as FilterOp)
            setError(undefined)
          }}
        >
          <SelectTrigger id={`${ids}-op`} size="sm" className="w-full text-xs">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {offered.map((entry) => (
              <SelectItem key={entry} value={entry} className="text-xs">
                {OPERATORS[entry].label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>

      {arity === "one" &&
        (boolean ? (
          <Field label="Value">
            <Segments
              label="Value"
              fill
              value={one === "0" ? "0" : "1"}
              options={[
                { value: "1", label: "true" },
                { value: "0", label: "false" },
              ]}
              onChange={setOne}
            />
          </Field>
        ) : labels.length > 0 ? (
          <Field label="Value" htmlFor={`${ids}-value`}>
            <Select value={labels.includes(one) ? one : undefined} onValueChange={setOne}>
              <SelectTrigger id={`${ids}-value`} size="sm" className="w-full font-mono text-xs">
                <SelectValue placeholder="Pick a label" />
              </SelectTrigger>
              <SelectContent>
                {labels.map((label) => (
                  <SelectItem key={label} value={label} className="font-mono text-xs">
                    {label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        ) : (
          <Field label="Value" htmlFor={`${ids}-value`}>
            <Input
              id={`${ids}-value`}
              autoFocus
              value={one}
              placeholder={placeholderFor(column)}
              spellCheck={false}
              autoComplete="off"
              className="font-mono sm:h-8 sm:text-xs"
              onChange={(event) => setOne(event.target.value)}
              onKeyDown={onEnter}
            />
          </Field>
        ))}
      {arity === "two" && (
        <Field label="From and to" htmlFor={`${ids}-from`} error={error}>
          <div className="flex items-center gap-2">
            <Input
              id={`${ids}-from`}
              autoFocus
              value={one}
              aria-label="From"
              placeholder="From"
              spellCheck={false}
              autoComplete="off"
              className="font-mono sm:h-8 sm:text-xs"
              onChange={(event) => setOne(event.target.value)}
              onKeyDown={onEnter}
            />
            <Input
              value={two}
              aria-label="To"
              placeholder="To"
              spellCheck={false}
              autoComplete="off"
              className="font-mono sm:h-8 sm:text-xs"
              onChange={(event) => setTwo(event.target.value)}
              onKeyDown={onEnter}
            />
          </div>
        </Field>
      )}
      {arity === "many" && (
        <Field
          label="Values"
          htmlFor={`${ids}-list`}
          hint="One a line, or separated by commas."
          error={error}
        >
          <Textarea
            id={`${ids}-list`}
            autoFocus
            rows={3}
            value={list}
            spellCheck={false}
            className="max-h-40 font-mono sm:text-xs"
            onChange={(event) => setList(event.target.value)}
          />
        </Field>
      )}

      <div className={cn("flex items-center gap-2", onRemove ? "justify-between" : "justify-end")}>
        {onRemove && (
          <Button type="button" size="sm" variant="ghost" onClick={onRemove}>
            Remove
          </Button>
        )}
        <Button type="submit" size="sm">
          Apply
        </Button>
      </div>
    </form>
  )
}
