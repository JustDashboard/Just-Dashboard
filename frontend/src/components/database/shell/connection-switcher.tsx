"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { usePathname, useRouter } from "next/navigation"
import { Check, ChevronDown, Layers, Plus } from "@/components/icons"
import { get } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useSessionState } from "@/lib/view-state"
import type { DbConnection, DbFleet } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { FormNote } from "@/components/form"
import { StatusDot } from "@/components/status-dot"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { sectionHref, type Engine } from "@/components/database/engine"
import { EngineMark } from "@/components/database/kit/engine-mark"
import { EnvironmentTag } from "@/components/database/kit/environment-tag"
import { useDatabase } from "@/components/database/shell/database-context"
import { useDatabases } from "@/components/database/shell/databases-context"
import {
  KNOWN_DATABASES_KEY,
  knownDatabase,
  type KnownDatabase,
} from "@/components/database/shell/nav-groups"
import { DATABASES_HREF, databasePlace } from "@/components/database/shell/routes"
import { fleetStatus, statusLabel, statusTone } from "@/components/database/shell/status"

/** A row at the foot of the list, drawn as the rows above it are. */
const FOOT =
  "flex h-8 items-center gap-2 rounded-sm px-2 text-sm focus-ring-inset hover:bg-menu-hover"

/**
 * A plain substring match. cmdk's own scoring is fuzzy, which among a dozen
 * names means "shop" also finds "staging-postgres".
 */
function contains(value: string, search: string) {
  return value.toLowerCase().includes(search.trim().toLowerCase()) ? 1 : 0
}

/**
 * When a database was last chosen here. Going to another database mounts its
 * pages afresh, this control with them, and the keyboard's place — which was
 * on this control — would be left on nothing. The one that mounts next takes
 * it back, if it mounts soon enough to be the result of that choice.
 */
const handover = { at: 0 }
const HANDOVER_MS = 15_000

function leaveFocus() {
  handover.at = Date.now()
}

/** Whether the control that just mounted is the one a choice led to. Asked once. */
function takeFocus() {
  const chosen = Date.now() - handover.at < HANDOVER_MS
  handover.at = 0
  return chosen
}

/**
 * The database's name, as the control that goes to another one.
 *
 * The rail's panel says which database this is and cannot change it; this is
 * the gesture a repository switcher makes. It is searched, because past a
 * handful of connections a menu is a list to read, and grouped by engine,
 * because "the other Postgres" is how the next one is usually thought of.
 * Each row is drawn as its engine with a dot for whether it answers — read
 * once, from the fleet, when the list opens.
 *
 * Choosing one opens the page being looked at on that database, or its home
 * where its engine has no such page: a Redis server is never opened on a
 * schema browser because the last database had one. The selection does not
 * travel; a table of this database is not one of that one's. Choosing the
 * one already open closes the list and changes nothing — it is where the
 * reader is, table and all.
 */
export function ConnectionSwitcher({
  inset,
  className,
}: {
  /**
   * Draw the focus ring inside the control. For a place that clips what
   * leaves its box — the title of an identity line truncates, and a ring
   * drawn outside the name was cut on three sides.
   */
  inset?: boolean
  className?: string
}) {
  const router = useRouter()
  const pathname = usePathname()
  const { conn } = useDatabase()
  const { connections, error, engineFor, admin, newHref } = useDatabases()
  const [known] = useSessionState<Record<string, KnownDatabase>>(KNOWN_DATABASES_KEY, {})
  const [open, setOpen] = useState(false)

  const fleet = usePoll((signal) => get<DbFleet>("/databases/fleet", undefined, signal), 0, [], {
    enabled: open,
  })
  const answering = useMemo(
    () => new Map(fleet.data?.connections.map((entry) => [entry.id, entry])),
    [fleet.data],
  )

  const groups = useMemo(() => {
    const byEngine = new Map<string, { engine: Engine; rows: DbConnection[] }>()
    for (const row of connections) {
      // The saved row says only how a server is dialled. What answered is
      // known for the one that is open, for every one the fleet has dialled,
      // and for those opened earlier in this tab — which is what keeps a row
      // under the same heading before and after the fleet lands.
      const flavor =
        row.flavor ??
        (row.id === conn.id ? conn.flavor : undefined) ??
        answering.get(row.id)?.flavor ??
        knownDatabase(known, row.id)?.flavor
      const engine = engineFor({ ...row, flavor })
      const group = byEngine.get(engine.label) ?? { engine, rows: [] }
      group.rows.push(row)
      byEngine.set(engine.label, group)
    }
    return [...byEngine.values()].sort((a, b) => a.engine.label.localeCompare(b.engine.label))
  }, [connections, engineFor, answering, known, conn.id, conn.flavor])

  const trigger = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    if (takeFocus()) trigger.current?.focus()
  }, [])

  const section = databasePlace(pathname)?.section ?? "home"
  const go = (href: string) => {
    leaveFocus()
    setOpen(false)
    router.push(href)
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          ref={trigger}
          type="button"
          aria-label={`Database: ${conn.name}. Switch database`}
          className={cn(
            "group flex max-w-full min-w-0 items-center gap-1.5 rounded-md text-left text-sm font-medium",
            inset ? "focus-ring-inset" : "focus-ring",
            className,
          )}
        >
          <span className="truncate">{conn.name}</span>
          <ChevronDown className="size-3.5 shrink-0 text-muted-foreground transition-colors group-hover:text-foreground group-data-[state=open]:text-foreground" />
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-80 max-w-[calc(100vw-1.5rem)] p-0">
        {/* cmdk names its own input and list from these; an aria-label on
            either is overwritten. */}
        <Command label="Find a database" filter={contains}>
          <CommandInput placeholder="Find a database" />
          <CommandList label="Databases" className="max-h-[min(24rem,60svh)]">
            <CommandEmpty className="px-3 py-6 text-center text-hint text-muted-foreground">
              No database matches.
            </CommandEmpty>
            {groups.map(({ engine, rows }) => (
              <CommandGroup key={engine.label} heading={engine.label}>
                {rows.map((row) => {
                  const entry = answering.get(row.id)
                  const reading = entry && fleetStatus(entry)
                  return (
                    <CommandItem
                      key={row.id}
                      // What the filter reads, and unique: two connections
                      // may share a name, never an id.
                      value={`${row.name} ${engine.label} ${row.host}:${row.port} ${row.database} #${row.id}`}
                      onSelect={() =>
                        row.id === conn.id
                          ? setOpen(false)
                          : go(sectionHref(row.id, engine.has(section) ? section : "home"))
                      }
                      className="gap-2.5"
                    >
                      <EngineMark engine={engine} size="sm" />
                      <span className="flex min-w-0 flex-1 flex-col">
                        <span className="flex min-w-0 items-center gap-2">
                          <span className="truncate text-body">{row.name}</span>
                          <EnvironmentTag environment={row.environment} />
                        </span>
                        <span className="truncate font-mono text-micro text-muted-foreground">
                          {row.port ? `${row.host}:${row.port}` : row.database}
                        </span>
                      </span>
                      {reading && (
                        <span
                          role="img"
                          aria-label={statusLabel(reading.state)}
                          title={reading.error}
                          className="flex shrink-0"
                        >
                          <StatusDot tone={statusTone(reading.state)} />
                        </span>
                      )}
                      <Check
                        aria-hidden
                        className={cn(
                          "size-3.5 shrink-0 text-brand",
                          row.id === conn.id ? "opacity-100" : "opacity-0",
                        )}
                      />
                    </CommandItem>
                  )
                })}
              </CommandGroup>
            ))}
          </CommandList>
        </Command>
        {error && (
          <FormNote tone="warning" className="border-t border-hairline px-3 py-2">
            The list could not be read again just now. This is what it held before.
          </FormNote>
        )}
        {/* The two ways out of the list stay put under it, whatever was
            typed and however far it has scrolled: they are where a search
            that found nothing goes next. */}
        <div className="flex flex-col border-t border-hairline p-1">
          <Link href={DATABASES_HREF} onClick={() => setOpen(false)} className={FOOT}>
            <Layers className="size-4 text-muted-foreground" />
            All databases
          </Link>
          {admin && (
            <Link href={newHref()} onClick={() => setOpen(false)} className={FOOT}>
              <Plus className="size-4 text-muted-foreground" />
              Add a database
            </Link>
          )}
        </div>
      </PopoverContent>
    </Popover>
  )
}
