"use client"

import { Check, ChevronDown, Database, Linked, Plus } from "@/components/icons"
import { ProductLogo } from "@/components/product-logo"
import { cn } from "@/lib/utils"
import type { DbConnection } from "@/lib/types"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

/**
 * The connection, as the workbench's location control.
 *
 * The section used to open with a select box beside a status dot, and no
 * title at all: the one thing every page under it is about had the visual
 * weight of a filter. The name stays beside its engine mark in the workbench
 * strip, and pressing it lists the others — the same gesture a repository
 * switcher makes.
 */
export function ConnectionSwitcher({
  connections,
  current,
  onSelect,
  onNew,
  onConnect,
  className,
}: {
  connections: DbConnection[]
  current: DbConnection
  onSelect: (id: number) => void
  /** Offered only to an administrator, who is the only one who may add. */
  onNew?: () => void
  onConnect?: () => void
  className?: string
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label={`Connection: ${current.name}. Switch connection`}
          className={cn(
            "group flex max-w-full min-w-0 items-center gap-2 rounded-md text-left text-sm font-medium focus-ring",
            className,
          )}
        >
          {/* The engine's own logo in front of the name: which of Postgres,
              Redis or Mongo this is decides what every page under it can do,
              and it is recognised before "shop" is read. */}
          <ProductLogo id={current.driver} size="sm" fallback={Database} />
          <span className="truncate">{current.name}</span>
          <ChevronDown className="size-3.5 shrink-0 text-muted-foreground transition-colors group-hover:text-foreground group-data-[state=open]:text-foreground" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-72">
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
            <span className="min-w-0 flex-1 truncate">{c.name}</span>
            {(c.host || c.database) && (
              <span className="max-w-[50%] shrink truncate font-mono text-hint text-muted-foreground">
                {[c.host ? `${c.host}${c.port ? `:${c.port}` : ""}` : null, c.database]
                  .filter(Boolean)
                  .join(" · ")}
              </span>
            )}
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
