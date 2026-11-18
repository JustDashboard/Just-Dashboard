"use client"

import { bytes } from "@/lib/format"
import type { InstalledPackage } from "@/lib/types"
import { Rail, RailFigure, RailLine, RailName, RailRow } from "@/components/rail"
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
  error,
  onRetry,
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
  /** The list could not be read: said in place of the rows. */
  error?: Error
  onRetry?: () => void
  query: string
  onQuery: (value: string) => void
  filtered: boolean
  onClearFilters: () => void
  caption: string
  hrefFor: (name: string) => string
  className?: string
}) {
  const at = packages.findIndex((p) => p.name === current)
  const shown: RailPackage[] = packages.slice(0, MAX_ROWS)
  // Past the cap the one on screen is still on its own list, in the first place.
  const pinned = at < 0 || at >= MAX_ROWS ? (packages[at] ?? currentPackage) : undefined
  return (
    <Rail
      slot="package-rail"
      label="Packages"
      search={{
        label: "Search the installed packages",
        placeholder: "Name, description or section",
        query,
        onQuery,
      }}
      rows={shown}
      keyOf={(pkg) => pkg.name}
      current={(pkg) => pkg.name === current}
      pinned={pinned}
      row={(pkg, on) => <PackageRow pkg={pkg} current={on} href={hrefFor(pkg.name)} />}
      loading={loading}
      loadingRows={8}
      error={error}
      onRetry={onRetry}
      empty="No package matches."
      filtered={filtered}
      onClearFilters={onClearFilters}
      caption={caption}
      foot={
        packages.length > MAX_ROWS &&
        `The first ${MAX_ROWS} of ${packages.length.toLocaleString()}. Search to reach the rest.`
      }
      className={className}
    />
  )
}

function PackageRow({ pkg, current, href }: { pkg: RailPackage; current: boolean; href: string }) {
  return (
    <RailRow
      item={pkg.name}
      name={pkg.name}
      title={pkg.summary}
      current={current}
      href={href}
      mark={
        <PackageMark
          name={pkg.name}
          section={pkg.section}
          className="size-7 rounded-md [&_img]:size-4"
        />
      }
      dot={
        pkg.upgradable ? (
          <StatusDot tone={pkg.security ? "warning" : "notice"} className="size-1.5" />
        ) : null
      }
      end={pkg.size ? <RailFigure mono={false}>{bytes(pkg.size)}</RailFigure> : null}
    >
      <RailName current={current} mono>
        {pkg.name}
      </RailName>
      <RailLine className="font-mono">{pkg.version ?? "not installed"}</RailLine>
    </RailRow>
  )
}
