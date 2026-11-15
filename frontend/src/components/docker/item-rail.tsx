"use client"

import { Rail, RailRow } from "@/components/rail"

/**
 * The column the five Docker item pages shared before there was one rail for
 * every detail page. Each Docker rail is on `Rail` now; these names stay
 * until the wave that deletes the retired ones.
 *
 * @deprecated use `Rail` from `@/components/rail`.
 */
export function DockerRail<T>({
  search,
  query,
  onQuery,
  ...props
}: Omit<React.ComponentProps<typeof Rail<T>>, "search"> & {
  search: { label: string; placeholder: string }
  query: string
  onQuery: (value: string) => void
}) {
  return <Rail<T> {...props} search={{ ...search, query, onQuery }} />
}

/** @deprecated use `RailRow` from `@/components/rail`. */
export const DockerRailRow = RailRow
