"use client"

import { useEffect, useRef } from "react"
import Link from "next/link"
import { bytes } from "@/lib/format"
import type { InstalledPackage } from "@/lib/types"
import { cn } from "@/lib/utils"
import { SearchInput } from "@/components/page"
import { LoadingRows } from "@/components/state"
import { StatusDot } from "@/components/status-dot"
import { PackageMark } from "@/components/packages/marks"

/** Rows drawn at once: a host carries two thousand packages, and a rail is searched, not read. */
const MAX_ROWS = 300

/** What the rail draws of a package: an installed row, or the one on screen that is not. */
export type RailPackage = Pick<InstalledPackage, "name"> & Partial<InstalledPackage>

/**
 * The installed list, folded into a column beside the package it opened: its
 * search, its scope and its order, so the next package is one press away.
 * Each row is the package as the software it is, its name over its version,
 * and its size at the edge; one that is behind carries a dot in the logo's
 * corner, amber for a security fix. The one on screen is filled, and is drawn
 * first when the filter does not hold it: a dependency opened from a list of
 * what was installed by hand, or a package that is not installed at all.
 */
export function PackageRail({
  packages,
  current,
  currentPackage,
  loading,
  query,
  onQuery,
  filtered,
  onClearFilters,
  caption,
  hrefFor,
  className,
}: {
  packages: InstalledPackage[]
  current: string
  /** The package on screen, drawn first when the list does not hold it. */
  currentPackage?: RailPackage
  loading: boolean
  query: string
  onQuery: (value: string) => void
  filtered: boolean
  onClearFilters: () => void
  caption: string
  hrefFor: (name: string) => string
  className?: string
}) {
  const at = packages.findIndex((p) => p.name === current)
  const shown = packages.slice(0, MAX_ROWS)
  // Past the cap the one on screen is still on its own list, in the first place.
  const pinned = at < 0 || at >= MAX_ROWS ? (packages[at] ?? currentPackage) : undefined
  const list = useRef<HTMLOListElement>(null)
  // Opened from a link or walked to past the column's edge, the package on
  // screen would otherwise be off its own list.
  useEffect(() => {
    const row = list.current?.querySelector<HTMLElement>("[aria-current='page']")
    row?.scrollIntoView({ block: "nearest" })
  }, [current, shown.length])

  return (
    <nav
      aria-label="Packages"
      data-slot="package-rail"
      className={cn("min-h-0 flex-col border-r border-hairline pr-4", className)}
    >
      <div className="flex shrink-0 flex-col gap-2 pb-3">
        <SearchInput
          value={query}
          onChange={(event) => onQuery(event.target.value)}
          placeholder="Name, description or section"
          aria-label="Search the installed packages"
          containerClassName="sm:w-full"
        />
        <p className="flex min-w-0 items-center justify-between gap-2 text-hint text-muted-foreground">
          <span className="truncate">{caption}</span>
          {filtered && (
            <button
              type="button"
              onClick={onClearFilters}
              className="shrink-0 rounded-sm font-medium focus-ring hover:text-foreground"
            >
              Clear filters
            </button>
          )}
        </p>
      </div>
      <ol
        ref={list}
        aria-label="Packages to open"
        className="-mx-2 min-h-0 flex-1 [scrollbar-width:thin] space-y-0.5 overflow-y-auto px-2 pb-2"
      >
        {pinned && <RailRow pkg={pinned} current href={hrefFor(pinned.name)} />}
        {pinned && shown.length > 0 && (
          <li aria-hidden className="mx-3 my-1.5 border-t border-hairline" />
        )}
        {shown.map((pkg) => (
          <RailRow
            key={pkg.name}
            pkg={pkg}
            current={pkg.name === current}
            href={hrefFor(pkg.name)}
          />
        ))}
        {loading && packages.length === 0 && <LoadingRows rows={8} />}
        {!loading && packages.length === 0 && !pinned && (
          <li className="px-3 py-4 text-hint text-muted-foreground">No package matches.</li>
        )}
        {packages.length > MAX_ROWS && (
          <li className="px-3 py-3 text-hint text-muted-foreground">
            The first {MAX_ROWS} of {packages.length.toLocaleString()}. Search to reach the rest.
          </li>
        )}
      </ol>
    </nav>
  )
}

function RailRow({ pkg, current, href }: { pkg: RailPackage; current: boolean; href: string }) {
  return (
    <li className="min-w-0">
      <Link
        href={href}
        data-workspace-item={pkg.name}
        data-workspace-name={pkg.name}
        aria-current={current ? "page" : undefined}
        aria-label={pkg.name}
        title={pkg.summary}
        className={cn(
          "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left focus-ring-inset transition-colors",
          current ? "bg-accent" : "hover:bg-row-hover",
        )}
      >
        <span className="relative flex shrink-0">
          <PackageMark
            name={pkg.name}
            section={pkg.section}
            className="size-7 rounded-md [&_img]:size-4"
          />
          {pkg.upgradable && (
            <span
              aria-hidden
              className="absolute -right-0.5 -bottom-0.5 flex size-2.5 items-center justify-center rounded-full bg-background"
            >
              <StatusDot tone={pkg.security ? "warning" : "notice"} className="size-1.5" />
            </span>
          )}
        </span>
        <span className="min-w-0 flex-1">
          <span
            className={cn(
              "block truncate font-mono text-xs",
              current ? "font-semibold" : "font-medium",
            )}
          >
            {pkg.name}
          </span>
          <span className="block truncate font-mono text-hint text-muted-foreground">
            {pkg.version ?? "not installed"}
          </span>
        </span>
        {pkg.size ? (
          <span className="numeric shrink-0 text-hint text-muted-foreground">
            {bytes(pkg.size)}
          </span>
        ) : null}
      </Link>
    </li>
  )
}
