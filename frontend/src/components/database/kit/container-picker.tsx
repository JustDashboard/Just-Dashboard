"use client"

import { useState } from "react"
import Link from "next/link"
import { ChevronDown } from "@/components/icons"
import { cn } from "@/lib/utils"
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
import { NameMark } from "@/components/database/kit/marks"
import { pickerGroups, type PickerItem } from "@/components/database/kit/container-picker-model"

export type { PickerItem } from "@/components/database/kit/container-picker-model"

/**
 * Which container a workbench is looking at — a schema of a SQL database, a
 * MongoDB database — and the way to another.
 *
 * The rails of Data, Query and Schema, the MongoDB rail and the Diagram's
 * toolbar each drew this dropdown: the mark, the name in mono and a chevron;
 * the containers somebody made, then the engine's own under their own word;
 * a box to find one past a dozen; and "No schema is called that". They
 * drifted in what was accident — the count grouped in two, raw in another,
 * the menu 256 or 288 wide, the list 288 or 320 tall, one with no find box —
 * and differed in three ways that are real, which are its props:
 *
 * - `hrefOf` makes each choice a link (a container is a place: Back returns
 *   to the one before, a new tab opens it), `onPick` a choice that stays on
 *   the page (Query's tree, which keeps its tabs).
 * - `every` is a first choice that is all of them, the Diagram's picture of
 *   every schema at once; with no `current` the trigger says its label.
 * - `blank` is what the trigger says with nothing chosen: "default" for a
 *   SQL connection's own schema, "Choose one" for MongoDB.
 *
 * The trigger keeps the name every spec presses it by: "<noun>: <current>".
 * A container is one line in the menu, as a rail's rows are: its `figure`
 * (how many tables, its size) at the end of the line, never under the name.
 */
export function ContainerPicker({
  items,
  current,
  noun,
  nouns,
  hrefOf,
  onPick,
  every,
  blank = "default",
  label,
  findFrom = 12,
  state,
  error,
  fill = true,
  className,
}: {
  /** Absent while the list is being read, or where it could not be. */
  items: readonly PickerItem[] | undefined
  current: string
  /** The engine's word for one: "schema", "database". */
  noun: string
  /** And for several. */
  nouns: string
  hrefOf?: (name: string) => string
  onPick?: (name: string) => void
  every?: { label: string; href: string }
  /** The trigger's words with nothing chosen. */
  blank?: string
  /** The trigger's accessible name with nothing chosen, where it says more than `blank`. */
  label?: string
  /** Past this many the menu grows a box to find one in. */
  findFrom?: number
  /** The list is on its way, or could not be read (`error` says why). */
  state?: "reading" | "failed"
  error?: string
  /** The trigger takes the head's room (a rail); off, it is as wide as its words (a toolbar). */
  fill?: boolean
  className?: string
}) {
  const [query, setQuery] = useState("")
  const all = items ?? []
  const { own, system } = pickerGroups(all, query)
  const shown = current || every?.label || blank
  const named = current || every?.label || label || blank

  const item = (entry: PickerItem) => {
    const body = (
      <>
        <NameMark name={entry.name} />
        <span className="min-w-0 flex-1 truncate font-mono text-xs">{entry.name}</span>
        {entry.figure && (
          <span className="numeric max-w-[50%] shrink-0 truncate text-hint text-muted-foreground">
            {entry.figure}
          </span>
        )}
      </>
    )
    const chosen = entry.name === current
    return hrefOf ? (
      <DropdownMenuItem
        key={entry.name}
        asChild
        title={entry.title}
        className={cn(chosen && "bg-accent")}
      >
        <Link href={hrefOf(entry.name)}>{body}</Link>
      </DropdownMenuItem>
    ) : (
      <DropdownMenuItem
        key={entry.name}
        title={entry.title}
        className={cn(chosen && "bg-accent")}
        onSelect={() => onPick?.(entry.name)}
      >
        {body}
      </DropdownMenuItem>
    )
  }

  return (
    <DropdownMenu onOpenChange={(open) => !open && setQuery("")}>
      <DropdownMenuTrigger asChild>
        <Button
          size="sm"
          variant="ghost"
          aria-label={`${noun}: ${named}`}
          data-slot="container-picker"
          className={cn(
            "h-7 min-w-0 justify-start gap-1.5 px-1.5",
            fill ? "flex-1" : "max-w-56",
            className,
          )}
        >
          {current || !every ? <NameMark name={current} /> : null}
          <span className={cn("min-w-0 truncate text-xs", (current || !every) && "font-mono")}>
            {shown}
          </span>
          <ChevronDown className={cn("size-3 shrink-0 text-muted-foreground", fill && "ml-auto")} />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-72">
        {all.length > findFrom && (
          <div className="p-1 pb-2">
            <Input
              value={query}
              placeholder={`Find a ${noun}`}
              aria-label={`Find a ${noun}`}
              className="h-7 sm:h-7"
              onChange={(event) => setQuery(event.target.value)}
              // The menu reads typed letters as type-ahead; in the box they are text.
              onKeyDown={(event) => event.key !== "Escape" && event.stopPropagation()}
            />
          </div>
        )}
        <div className="max-h-72 overflow-y-auto">
          {every && (
            <>
              {/* Said in so many words: an address with the container left
                  out would be completed from where the reader last was. */}
              <DropdownMenuItem asChild className={cn(current === "" && "bg-accent")}>
                <Link href={every.href}>{every.label}</Link>
              </DropdownMenuItem>
              {own.length + system.length > 0 && <DropdownMenuSeparator />}
            </>
          )}
          {state === "failed" && !items && (
            <p className="px-2 py-2 text-hint text-muted-foreground">
              The {nouns} could not be listed{error ? `: ${error}` : "."}
            </p>
          )}
          {own.map(item)}
          {system.length > 0 && (
            <>
              {own.length > 0 && <DropdownMenuSeparator />}
              <DropdownMenuLabel>The engine&rsquo;s own</DropdownMenuLabel>
              {system.map(item)}
            </>
          )}
          {own.length + system.length === 0 && state !== "failed" && (
            <p className="px-2 py-3 text-center text-hint text-muted-foreground">
              {!items || state === "reading"
                ? `Reading the ${nouns}…`
                : all.length === 0
                  ? `The ${nouns} have not been read`
                  : `No ${noun} is called that`}
            </p>
          )}
        </div>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
