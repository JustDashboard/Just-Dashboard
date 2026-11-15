"use client"

import { useEffect, useRef } from "react"
import Link from "next/link"
import { dnsEngineName, type DNSConnection } from "@/lib/network-dns-services"
import { cn } from "@/lib/utils"
import { Globe } from "@/components/icons"
import { ProductLogo } from "@/components/product-logo"
import { LoadingRows } from "@/components/state"
import { StatusDot } from "@/components/status-dot"
import { engineHref, type EngineTab } from "./engine-views"
import { ENGINE_PRODUCT } from "./services"

/**
 * The connected engines, beside the one that is open, from `xl`: each as the
 * product it is, with its name and whether changes may be reviewed through
 * it, the one on screen filled. The Services view's list has no filter, so
 * neither has this; the view on screen goes with you to the next engine.
 */
export function EngineRail({
  engines,
  current,
  tab,
  loading,
  failed,
  className,
}: {
  engines: DNSConnection[]
  current: string
  tab: EngineTab
  loading: boolean
  /** The list could not be read: the column says so instead of counting nothing. */
  failed?: boolean
  className?: string
}) {
  const list = useRef<HTMLOListElement>(null)
  useEffect(() => {
    list.current
      ?.querySelector<HTMLElement>("[aria-current='page']")
      ?.scrollIntoView({ block: "nearest" })
  }, [current])
  return (
    <nav
      aria-label="DNS engines"
      data-slot="engine-rail"
      className={cn("min-h-0 flex-col border-r border-hairline pr-4", className)}
    >
      <p className="flex shrink-0 items-center justify-between gap-2 pb-3 text-hint text-muted-foreground">
        <span>{failed ? "Not read" : "Connected engines"}</span>
        <span className="numeric">{engines.length || ""}</span>
      </p>
      <ol
        ref={list}
        aria-label="Engines to open"
        className="-mx-2 min-h-0 flex-1 [scrollbar-width:thin] space-y-0.5 overflow-y-auto px-2 pb-2"
      >
        {engines.map((engine) => {
          const here = engine.id === current
          return (
            <li key={engine.id} className="min-w-0">
              <Link
                href={engineHref(engine.id, tab)}
                aria-current={here ? "page" : undefined}
                aria-label={engine.name}
                data-workspace-item={engine.id}
                data-workspace-name={engine.name}
                className={cn(
                  "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left focus-ring-inset transition-colors",
                  here ? "bg-accent" : "hover:bg-row-hover",
                )}
              >
                <span className="relative flex shrink-0">
                  <ProductLogo
                    id={ENGINE_PRODUCT[engine.engine]}
                    fallback={Globe}
                    size="sm"
                    className="size-7 rounded-md [&_img]:size-4 [&_svg]:size-3.5"
                  />
                  <span
                    aria-hidden
                    className="absolute -right-0.5 -bottom-0.5 flex size-2.5 items-center justify-center rounded-full bg-background"
                  >
                    <StatusDot
                      tone={engine.management ? "notice" : "stopped"}
                      className="size-1.5"
                    />
                  </span>
                </span>
                <span className="min-w-0 flex-1">
                  <span
                    className={cn(
                      "block truncate text-body",
                      here ? "font-semibold" : "font-medium",
                    )}
                  >
                    {engine.name}
                  </span>
                  <span className="block truncate text-hint text-muted-foreground">
                    {dnsEngineName(engine.engine)} ·{" "}
                    {engine.management ? "reviewed changes" : "read-only"}
                  </span>
                </span>
              </Link>
            </li>
          )
        })}
        {loading && engines.length === 0 && <LoadingRows rows={3} />}
      </ol>
    </nav>
  )
}
