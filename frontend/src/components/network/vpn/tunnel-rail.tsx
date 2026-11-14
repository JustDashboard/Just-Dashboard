"use client"

import { cn } from "@/lib/utils"
import { ProductLogo } from "@/components/product-logo"
import { LoadingRows } from "@/components/state"
import { StatusDot, type DotTone } from "@/components/status-dot"

export type RailTunnel = { name: string; line: string; tone: DotTone; archived: boolean }

/**
 * The tunnels, beside the one that is open: the live ones with who is online,
 * then the removed ones whose record is still kept, so the next tunnel is one
 * press away and Alt+↑ and Alt+↓ walk the list. The VPN page's list has no
 * filter, so neither has this.
 */
export function TunnelRail({
  tunnels,
  current,
  loading,
  onOpen,
  className,
}: {
  tunnels: RailTunnel[]
  current: string
  loading: boolean
  onOpen: (name: string) => void
  className?: string
}) {
  return (
    <nav
      aria-label="Tunnels"
      data-slot="tunnel-rail"
      className={cn("min-h-0 flex-col border-r border-hairline pr-4", className)}
    >
      <p className="flex shrink-0 items-center justify-between gap-2 pb-3 text-hint text-muted-foreground">
        <span>Tunnels</span>
        <span className="numeric">{tunnels.length || ""}</span>
      </p>
      <ol
        aria-label="Tunnels to open"
        className="-mx-2 min-h-0 flex-1 [scrollbar-width:thin] space-y-0.5 overflow-y-auto px-2 pb-2"
      >
        {tunnels.map((tunnel) => {
          const open = tunnel.name === current
          return (
            <li key={tunnel.name} className="min-w-0">
              <button
                type="button"
                data-workspace-item={tunnel.name}
                data-workspace-name={tunnel.name}
                aria-current={open ? "page" : undefined}
                aria-label={tunnel.name}
                onClick={() => onOpen(tunnel.name)}
                className={cn(
                  "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left focus-ring-inset transition-colors",
                  open ? "bg-accent" : "hover:bg-row-hover",
                )}
              >
                <span className="relative flex shrink-0">
                  <ProductLogo
                    id="wireguard"
                    size="sm"
                    className={cn(
                      "size-7 rounded-md [&_img]:size-4",
                      tunnel.archived && "opacity-60",
                    )}
                  />
                  <span
                    aria-hidden
                    className="absolute -right-0.5 -bottom-0.5 flex size-2.5 items-center justify-center rounded-full bg-background"
                  >
                    <StatusDot tone={tunnel.tone} className="size-1.5" />
                  </span>
                </span>
                <span className="min-w-0 flex-1">
                  <span
                    className={cn(
                      "block truncate font-mono text-body",
                      open ? "font-semibold" : "font-medium",
                    )}
                  >
                    {tunnel.name}
                  </span>
                  <span className="numeric block truncate text-hint text-muted-foreground">
                    {tunnel.line}
                  </span>
                </span>
              </button>
            </li>
          )
        })}
        {loading && tunnels.length === 0 && <LoadingRows rows={3} />}
      </ol>
    </nav>
  )
}
