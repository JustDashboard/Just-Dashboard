"use client"

import { ChevronDown } from "@/components/icons"
import { grouped } from "@/lib/format"
import { ChipCount, ChipStrip, FilterChip } from "@/components/chips"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { NameMark } from "@/components/database/kit/marks"

/** A schema as a chip offers it: its name, and how many tables it holds where that is known. */
export type ChipSchema = { name: string; tables: number }

/**
 * The schemas a page reads, as chips: the chosen ones first, the others
 * while there is room, and every one past that in a menu — so a server that
 * names thirty has thirty to choose from, and what is chosen is always in
 * sight.
 *
 * Search drew it to pick the one schema it reads, Generate to tick the
 * several it writes for: the same strip with the same overflow menu, which
 * is what `mode` says.
 *
 * - `one`: a choice of one. `value` is the schema (or empty, with `every`
 *   chosen); a chip picks it. `every` adds "Every schema" at the end, a
 *   choice of its own (`onEvery`).
 * - `many`: ticks. `value` is the names ticked; a chip ticks or unticks one.
 */
export function SchemaChips({
  schemas,
  mode,
  value,
  onChange,
  every,
  noun,
  label,
  visible = 8,
}: {
  schemas: readonly ChipSchema[]
  /** A label for the strip: "Which schema is read". */
  label: string
  /** The engine's word for one, for "Every schema" and "3 more schemas". */
  noun: { one: string; many: string }
  visible?: number
} & (
  | {
      mode: "one"
      value: string
      onChange: (name: string) => void
      /** "Every schema" as a choice of its own: whether it is the one chosen, and what it does. */
      every?: { chosen: boolean; onPick: () => void }
    }
  | {
      mode: "many"
      value: readonly string[]
      onChange: (names: string[]) => void
      every?: never
    }
)) {
  const chosen = (name: string) =>
    mode === "one" ? !every?.chosen && name === value : value.includes(name)
  const on = schemas.filter((schema) => chosen(schema.name))
  const off = schemas.filter((schema) => !chosen(schema.name))
  const chips = [...on, ...off.slice(0, Math.max(0, visible - on.length))]
  // In the order the server gave them, so a chip does not jump when chosen.
  const shown = schemas.filter((schema) => chips.includes(schema))
  const rest = off.slice(Math.max(0, visible - on.length))
  const press = (name: string) => {
    if (mode === "one") onChange(name)
    else onChange(value.includes(name) ? value.filter((entry) => entry !== name) : [...value, name])
  }
  const row = (schema: ChipSchema) => (
    <>
      <NameMark name={schema.name} />
      <span className="min-w-0 flex-1 truncate font-mono text-xs">{schema.name}</span>
      {schema.tables >= 0 && (
        <span className="numeric shrink-0 text-hint text-muted-foreground">
          {grouped(schema.tables)}
        </span>
      )}
    </>
  )
  return (
    <ChipStrip role="group" aria-label={label} data-slot="schema-chips">
      {(mode === "one" ? shown : chips).map((schema) => (
        <FilterChip
          key={schema.name}
          selected={chosen(schema.name)}
          onClick={() => press(schema.name)}
        >
          <NameMark name={schema.name} />
          <span className="font-mono">{schema.name}</span>
          {schema.tables >= 0 && <ChipCount>{schema.tables}</ChipCount>}
        </FilterChip>
      ))}
      {rest.length > 0 && (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <FilterChip aria-label={`${rest.length} more ${noun.many}`}>
              {rest.length} more
              <ChevronDown className="size-3" />
            </FilterChip>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="max-h-72 w-72 overflow-y-auto">
            {rest.map((schema) =>
              mode === "many" ? (
                <DropdownMenuCheckboxItem
                  key={schema.name}
                  checked={false}
                  onCheckedChange={() => press(schema.name)}
                >
                  {row(schema)}
                </DropdownMenuCheckboxItem>
              ) : (
                <DropdownMenuItem key={schema.name} onSelect={() => press(schema.name)}>
                  {row(schema)}
                </DropdownMenuItem>
              ),
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      )}
      {mode === "one" && every && (
        <FilterChip selected={every.chosen} onClick={every.onPick}>
          Every {noun.one}
        </FilterChip>
      )}
    </ChipStrip>
  )
}
