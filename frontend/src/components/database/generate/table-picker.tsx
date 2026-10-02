"use client"

import { useState } from "react"
import { ChevronDown } from "@/components/icons"
import { SearchInput } from "@/components/page"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * Which tables a read covers: all of them, or the ones ticked.
 *
 * "Every table" is an empty list, so a table made tomorrow is covered without
 * anybody ticking it; the first one left out names all the others. The same
 * control scopes the code generator and the search for a value.
 */
export function TablePicker({
  names,
  picked,
  loading,
  onChange,
  disabledWhy,
  what = "written",
}: {
  names: string[]
  /** The ones ticked; none means every one. */
  picked: string[]
  loading: boolean
  onChange: (names: string[]) => void
  /** Why the choice cannot be made here, where it cannot. */
  disabledWhy?: string
  /** What happens to the ticked ones: "written", "read". */
  what?: string
}) {
  const { engine } = useDatabase()
  const [find, setFind] = useState("")
  const needle = find.trim().toLowerCase()
  const listed = needle ? names.filter((name) => name.toLowerCase().includes(needle)) : names
  const every = picked.length === 0
  const toggle = (name: string) => {
    const base = every ? names : picked
    const next = base.includes(name) ? base.filter((entry) => entry !== name) : [...base, name]
    onChange(next.length === names.length ? [] : next)
  }
  return (
    <Popover onOpenChange={(open) => !open && setFind("")}>
      <PopoverTrigger asChild>
        <Button
          size="sm"
          variant="outline"
          title={disabledWhy}
          className="h-7 shrink-0 gap-1.5 px-2.5 text-xs max-sm:h-10"
          disabled={Boolean(disabledWhy) || loading || names.length === 0}
        >
          {disabledWhy
            ? `Every ${engine.nouns.object}`
            : loading
              ? `Reading the ${engine.nouns.objects}…`
              : names.length === 0
                ? `No ${engine.nouns.objects}`
                : every
                  ? `Every ${engine.nouns.object} (${names.length})`
                  : `${picked.length} of ${names.length} ${engine.nouns.objects}`}
          <ChevronDown className="size-3" />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-72 p-0">
        <div className="border-b border-hairline p-1.5">
          <SearchInput
            dense
            value={find}
            placeholder={`Find a ${engine.nouns.object}`}
            aria-label={`Find a ${engine.nouns.object}`}
            containerClassName="sm:w-full"
            onChange={(event) => setFind(event.target.value)}
          />
        </div>
        <ul
          aria-label={`The ${engine.nouns.objects} ${what}`}
          className="max-h-64 overflow-y-auto p-1"
        >
          {listed.map((name) => {
            const on = every || picked.includes(name)
            return (
              <li key={name}>
                <label className="flex h-8 min-w-0 cursor-pointer items-center gap-2 rounded-md px-2 hover:bg-menu-hover">
                  {/* The label names it for a reader; the attribute, for tooling that reads only attributes. */}
                  <Checkbox checked={on} aria-label={name} onCheckedChange={() => toggle(name)} />
                  <span className="min-w-0 truncate font-mono text-xs">{name}</span>
                </label>
              </li>
            )
          })}
          {listed.length === 0 && (
            <li className="px-2 py-3 text-center text-hint text-muted-foreground">
              Nothing here is called that.
            </li>
          )}
        </ul>
        <div className="flex items-center justify-between gap-2 border-t border-hairline p-1.5">
          <span className="px-1 text-hint text-muted-foreground">
            {every ? `All are ${what}` : `${picked.length} ${what}`}
          </span>
          <div className="flex items-center gap-1">
            {/* Narrowed by the box, the ones listed are one press: ticking
                every other one off by hand is a dozen. */}
            {needle !== "" && listed.length > 0 && listed.length < names.length && (
              <Button size="xs" variant="ghost" onClick={() => onChange(listed)}>
                Only {listed.length === 1 ? "this one" : `these ${listed.length}`}
              </Button>
            )}
            <Button size="xs" variant="ghost" disabled={every} onClick={() => onChange([])}>
              Every one
            </Button>
          </div>
        </div>
      </PopoverContent>
    </Popover>
  )
}
