"use client"

import { Check, ChevronDown, Database, Linked, Plus } from "@/components/icons"
import { ProductLogo } from "@/components/product-logo"
import { cn } from "@/lib/utils"
import type { DbConnection, DbDriverInfo } from "@/lib/types"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

/**
 * The connection, as the page's title.
 *
 * The section used to open with a select box beside a status dot, and no
 * title at all: the one thing every page under it is about had the visual
 * weight of a filter. The name is the title now, at the page's own size, and
 * pressing it lists the others — the same gesture a repository switcher
 * makes, and the reason a chevron sits after the name rather than a box
 * around it.
 */
export function ConnectionSwitcher({
  connections,
  drivers,
  current,
  onSelect,
  onNew,
  onConnect,
  className,
}: {
  connections: DbConnection[]
  drivers: DbDriverInfo[]
  current: DbConnection
  onSelect: (id: number) => void
  /** Offered only to an administrator, who is the only one who may add. */
  onNew?: () => void
  onConnect?: () => void
  className?: string
}) {
  const label = (c: DbConnection) => drivers.find((d) => d.id === c.driver)?.label ?? c.driver
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label={`Connection: ${current.name}. Switch connection`}
          className={cn(
            "group flex max-w-full min-w-0 items-center gap-2.5 rounded-md text-left text-2xl leading-tight font-semibold tracking-tight focus-ring",
            className,
          )}
        >
          {/* The engine's own logo in front of the name: which of Postgres,
              Redis or Mongo this is decides what every page under it can do,
              and it is recognised before "shop" is read. */}
          <ProductLogo id={current.driver} fallback={Database} />
          <span className="truncate">{current.name}</span>
          <ChevronDown className="size-5 shrink-0 text-muted-foreground transition-colors group-hover:text-foreground group-data-[state=open]:text-foreground" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-80">
        <DropdownMenuLabel className="text-hint font-medium text-muted-foreground">
          Connections
        </DropdownMenuLabel>
        {connections.map((c) => (
          <DropdownMenuItem
            key={c.id}
            onClick={() => c.id !== current.id && onSelect(c.id)}
            className="items-center gap-2.5"
          >
            <ProductLogo id={c.driver} size="sm" fallback={Database} />
            <span className="flex min-w-0 flex-1 flex-col">
              <span className="truncate text-body font-medium">{c.name}</span>
              <span className="truncate text-hint text-muted-foreground">
                {label(c)}
                {c.host ? ` · ${c.host}${c.port ? `:${c.port}` : ""}` : ""}
                {c.database ? ` · ${c.database}` : ""}
              </span>
            </span>
            {c.id === current.id && <Check className="size-3.5 text-foreground" />}
          </DropdownMenuItem>
        ))}
        {(onNew || onConnect) && (
          <>
            <DropdownMenuSeparator />
            {onNew && (
              <DropdownMenuItem onClick={onNew}>
                <Plus />
                New database…
              </DropdownMenuItem>
            )}
            {onConnect && (
              <DropdownMenuItem onClick={onConnect}>
                <Linked />
                Connect one somewhere else…
              </DropdownMenuItem>
            )}
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
