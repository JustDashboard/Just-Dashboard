"use client"

import { useRef, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { ChevronDown } from "@/components/icons"
import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import {
  Command,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from "@/components/ui/command"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
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
 *
 * Past `findFrom` it is a combobox rather than a menu: the find box is where
 * the keyboard lands when it opens, typing narrows the list, and the arrow
 * keys walk what is left from the box. A menu kept the box outside the items
 * its arrows walk and took Tab for itself, so the box was out of a
 * keyboard's reach.
 */
export function ContainerPicker(props: PickerProps) {
  return (props.items?.length ?? 0) > (props.findFrom ?? 12) ? (
    <FindPicker {...props} />
  ) : (
    <MenuPicker {...props} />
  )
}

type PickerProps = {
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
  /** Past this many it is a combobox: a box to find one in, over the list. */
  findFrom?: number
  /** The list is on its way, or could not be read (`error` says why). */
  state?: "reading" | "failed"
  error?: string
  /** The trigger takes the head's room (a rail); off, it is as wide as its words (a toolbar). */
  fill?: boolean
  className?: string
}

/** The trigger both forms open from, named "<noun>: <current>". */
function trigger({
  current,
  noun,
  every,
  blank = "default",
  label,
  fill = true,
  className,
}: PickerProps) {
  const shown = current || every?.label || blank
  const named = current || every?.label || label || blank
  return (
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
  )
}

/** A container's one line: its mark, its name, its figure at the end. */
function ItemBody({ entry }: { entry: PickerItem }) {
  return (
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
}

/** What the list says when it holds nothing to choose, or nothing that matched. */
function nothingWords(
  { items, noun, nouns, state }: PickerProps,
  all: readonly PickerItem[],
): string {
  return !items || state === "reading"
    ? `Reading the ${nouns}…`
    : all.length === 0
      ? `The ${nouns} have not been read`
      : `No ${noun} is called that`
}

/** A list short enough to read: a menu of its containers. */
function MenuPicker(props: PickerProps) {
  const { items, current, nouns, hrefOf, onPick, every, state, error } = props
  const all = items ?? []
  const { own, system } = pickerGroups(all, "")

  const item = (entry: PickerItem) => {
    const body = <ItemBody entry={entry} />
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
    <DropdownMenu>
      <DropdownMenuTrigger asChild>{trigger(props)}</DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-72">
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
              {nothingWords(props, all)}
            </p>
          )}
        </div>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/**
 * A list long enough to search: a combobox. The find box has the keyboard
 * from the moment it opens, what is typed narrows the list by name, and the
 * arrow keys and Enter choose from the box. A container that is a place is
 * still a link, so a new tab opens it.
 */
function FindPicker(props: PickerProps) {
  const { items, current, noun, nouns, hrefOf, onPick, every, state, error } = props
  const router = useRouter()
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState("")
  const all = items ?? []
  const { own, system } = pickerGroups(all, query)
  // A press on an item's link is the link's own: the item's select, which
  // follows it, would go there a second time.
  const clicked = useRef(false)

  const close = (next: boolean) => {
    setOpen(next)
    if (!next) setQuery("")
  }
  const choice = (
    value: string,
    href: string | undefined,
    pick: () => void,
    chosen: boolean,
    body: React.ReactNode,
    title?: string,
  ) => (
    <CommandItem
      key={value}
      value={value}
      title={title}
      className={cn("gap-2", chosen && "bg-accent")}
      onSelect={() => {
        if (clicked.current) {
          clicked.current = false
          return
        }
        close(false)
        if (href) router.push(href)
        else pick()
      }}
    >
      {href ? (
        <Link
          href={href}
          tabIndex={-1}
          className="-mx-2 -my-1.5 flex min-w-0 flex-1 items-center gap-2 self-stretch px-2 py-1.5"
          onClick={() => {
            clicked.current = true
            close(false)
          }}
        >
          {body}
        </Link>
      ) : (
        body
      )}
    </CommandItem>
  )
  const item = (entry: PickerItem) =>
    choice(
      `container:${entry.name}`,
      hrefOf?.(entry.name),
      () => onPick?.(entry.name),
      entry.name === current,
      <ItemBody entry={entry} />,
      entry.title,
    )

  return (
    <Popover open={open} onOpenChange={close}>
      <PopoverTrigger asChild>{trigger(props)}</PopoverTrigger>
      <PopoverContent align="start" className="w-72 p-0">
        {/* cmdk names its own input and list from these; an aria-label on
            either is overwritten. The list is narrowed here, by name, as
            the menu's was; cmdk's own scoring is fuzzy. */}
        <Command
          label={`Find a ${noun}`}
          shouldFilter={false}
          loop
          defaultValue={current ? `container:${current}` : every ? "every" : undefined}
        >
          <CommandInput placeholder={`Find a ${noun}`} value={query} onValueChange={setQuery} />
          <CommandList label={nouns} className="max-h-72 p-1">
            {every &&
              choice(
                "every",
                every.href,
                () => {},
                current === "",
                <span className="min-w-0 flex-1 truncate text-xs">{every.label}</span>,
              )}
            {every && own.length + system.length > 0 && <CommandSeparator className="my-1" />}
            {state === "failed" && !items && (
              <p className="px-2 py-2 text-hint text-muted-foreground">
                The {nouns} could not be listed{error ? `: ${error}` : "."}
              </p>
            )}
            {own.map(item)}
            {system.length > 0 && (
              <>
                {own.length > 0 && <CommandSeparator className="my-1" />}
                <CommandGroup heading="The engine’s own" className="p-0">
                  {system.map(item)}
                </CommandGroup>
              </>
            )}
            {own.length + system.length === 0 && state !== "failed" && (
              <p className="px-2 py-3 text-center text-hint text-muted-foreground">
                {nothingWords(props, all)}
              </p>
            )}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}
