"use client"

import { useMemo } from "react"
import { useSessionState, useViewState } from "@/lib/view-state"
import {
  PACKAGE_FILTER_KEYS,
  SCOPE_WORDS,
  filterPackages,
  packageScope,
  softwareName,
  type PackageScope,
} from "@/components/packages/inventory"
import type { PackageInventory } from "@/lib/types"

/**
 * The installed list's filter, read where the list is drawn and again in the
 * rail beside a package: the search is kept for the tab, the scope and the
 * order for good, so the rail shows the list as it was left and narrowing one
 * narrows the other.
 */
export function usePackageFilter(inventory: PackageInventory | undefined) {
  const [query, setQuery] = useSessionState(PACKAGE_FILTER_KEYS.query, "")
  const [remembered, setScope] = useViewState<PackageScope>(PACKAGE_FILTER_KEYS.scope, "explicit")
  const [bySize, setBySize] = useViewState(PACKAGE_FILTER_KEYS.bySize, false)
  const [software, setSoftware] = useSessionState(PACKAGE_FILTER_KEYS.software, "")
  const knowsExplicit = (inventory?.explicitCount ?? 0) > 0
  const scope = packageScope(remembered, inventory?.explicitCount ?? 0)
  const rows = useMemo(
    () => filterPackages(inventory?.packages ?? [], { query, scope, software, bySize }),
    [inventory, query, scope, software, bySize],
  )
  const total = inventory?.packages.length ?? 0
  const narrowed = [software ? softwareName(software) : "", SCOPE_WORDS[scope]]
    .filter(Boolean)
    .join(", ")
  return {
    rows,
    total,
    query,
    setQuery,
    scope,
    setScope,
    knowsExplicit,
    bySize,
    setBySize,
    software,
    setSoftware,
    /** What narrows the list besides the search, in words: "Docker, behind". */
    narrowed,
  }
}
