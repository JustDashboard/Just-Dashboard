"use client"

import { ChevronDown } from "@/components/icons"
import { cn } from "@/lib/utils"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group"
import { useDatabases } from "@/components/database/shell/databases-context"
import { useDatabase } from "@/components/database/shell/database-context"

/** The column types the server lists for this connection's engine, to choose from. */
export function useColumnTypes(): readonly string[] {
  const { drivers } = useDatabases()
  const { engine } = useDatabase()
  return drivers?.find((driver) => driver.id === engine.driver)?.columnTypes ?? NO_TYPES
}

const NO_TYPES: readonly string[] = []

/** What the routes take as a default, said once under every field that asks for one. */
export const DEFAULT_HINT =
  "A number, a 'quoted string', NULL, TRUE, FALSE, CURRENT_TIMESTAMP, or a call with no arguments such as now()."

/**
 * A column's type: typed, or taken from the engine's own list.
 *
 * It is a field first, because a type is more than the list can hold —
 * `numeric(12,4)`, `varchar(64)`, an enum's name — and the list is the
 * engine's common ones a press away, in the field's own box.
 */
export function TypeField({
  id,
  value,
  onChange,
  label,
  invalid,
  dense,
  autoFocus,
}: {
  id?: string
  value: string
  onChange: (value: string) => void
  /** The field's name where no visible label says it: "Type of column 2". */
  label?: string
  invalid?: boolean
  /** A row of a column editor rather than a field of a form. */
  dense?: boolean
  autoFocus?: boolean
}) {
  const types = useColumnTypes()
  return (
    <InputGroup className={cn(dense && "h-8 sm:h-8")}>
      <InputGroupInput
        id={id}
        value={value}
        aria-label={label}
        aria-invalid={invalid || undefined}
        autoFocus={autoFocus}
        autoComplete="off"
        spellCheck={false}
        placeholder={types[0] ?? "type"}
        className="font-mono text-xs"
        onChange={(event) => onChange(event.target.value)}
      />
      {types.length > 0 && (
        <InputGroupAddon align="inline-end" className="gap-0 p-0">
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <InputGroupButton
                aria-label={
                  label ? `${label}: choose from the list` : "Choose a type from the list"
                }
                className="px-2"
              >
                <ChevronDown className="size-3.5" />
              </InputGroupButton>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="max-h-72 w-52 overflow-y-auto">
              {types.map((type) => (
                <DropdownMenuItem
                  key={type}
                  className={cn("font-mono text-xs", type === value && "bg-accent")}
                  onSelect={() => onChange(type)}
                >
                  {type}
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        </InputGroupAddon>
      )}
    </InputGroup>
  )
}

/**
 * Columns chosen in an order: an index's key, a unique constraint's columns.
 *
 * Every column of the table is a chip; a pressed one carries its place in the
 * order, because `(status, placed_at)` and `(placed_at, status)` are two
 * different indexes. Pressing it again takes it out and closes the gap.
 */
export function ColumnChips({
  label,
  columns,
  chosen,
  onChange,
}: {
  /** The group's name: "Columns of the index". */
  label: string
  columns: readonly string[]
  chosen: readonly string[]
  onChange: (chosen: string[]) => void
}) {
  return (
    <ChipStrip role="group" aria-label={label} className="max-sm:mx-0 max-sm:px-0">
      {columns.map((column) => {
        const at = chosen.indexOf(column)
        return (
          <FilterChip
            key={column}
            selected={at >= 0}
            className="font-mono"
            onClick={() =>
              onChange(at >= 0 ? chosen.filter((name) => name !== column) : [...chosen, column])
            }
          >
            {at >= 0 && chosen.length > 1 && <ChipCount>{at + 1}</ChipCount>}
            {column}
          </FilterChip>
        )
      })}
    </ChipStrip>
  )
}
