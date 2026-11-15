"use client"

import { useEffect, useRef } from "react"
import Link from "next/link"
import type { EgressGroup } from "@/lib/network-egress"
import { cn } from "@/lib/utils"
import { LoadingRows } from "@/components/state"
import { EgressMark, TONE_TEXT } from "@/components/network/egress/marks"
import { egressCarrying, egressHref } from "@/components/network/egress/reading"

/**
 * The list beside the group it opened, from `xl`: each group as its mark,
 * its name and the member carrying it in that state's colour, the one on
 * screen filled. There are at most six groups and the list has no filter, so
 * the rail has none either; the view on screen goes with you to the next.
 */
export function EgressRail({
  groups,
  current,
  tab,
  loading,
  failed,
  className,
}: {
  groups: EgressGroup[]
  current?: number
  tab: string
  loading: boolean
  /** The first read failed: the column says so instead of counting nothing. */
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
      aria-label="Egress groups"
      data-slot="egress-rail"
      className={cn("min-h-0 flex-col border-r border-hairline pr-4", className)}
    >
      <p className="shrink-0 pb-3 text-hint text-muted-foreground">
        {failed
          ? "Not read"
          : loading && groups.length === 0
            ? "Reading the groups…"
            : `${groups.length} ${groups.length === 1 ? "group" : "groups"}`}
      </p>
      <ol
        ref={list}
        aria-label="Groups to open"
        className="-mx-2 min-h-0 flex-1 [scrollbar-width:thin] space-y-0.5 overflow-y-auto px-2 pb-2"
      >
        {groups.map((group) => {
          const carrying = egressCarrying(group)
          const here = group.id === current
          return (
            <li key={group.id} className="min-w-0">
              <Link
                href={egressHref(group.name, tab)}
                aria-current={here ? "page" : undefined}
                aria-label={group.name}
                data-workspace-item={String(group.id)}
                data-workspace-name={group.name}
                className={cn(
                  "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left focus-ring-inset transition-colors",
                  here ? "bg-accent" : "hover:bg-row-hover",
                )}
              >
                <EgressMark group={group} size="sm" />
                <span className="min-w-0 flex-1">
                  <span
                    className={cn(
                      "block truncate text-body",
                      here ? "font-semibold" : "font-medium",
                    )}
                  >
                    {group.name}
                  </span>
                  <span className={cn("block truncate text-hint", TONE_TEXT[carrying.tone])}>
                    {carrying.label}
                  </span>
                </span>
              </Link>
            </li>
          )
        })}
        {loading && groups.length === 0 && <LoadingRows rows={3} />}
      </ol>
    </nav>
  )
}
