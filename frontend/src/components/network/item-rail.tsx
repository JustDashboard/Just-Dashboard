"use client"

import { Rail, RailLine, RailName, RailRow } from "@/components/rail"

export type RailItem = {
  id: string
  href: string
  /** The row's accessible name. */
  label: string
  mark: React.ReactNode
  title: React.ReactNode
  line: React.ReactNode
  /** The reading at the row's end: a state, a time. */
  end?: React.ReactNode
}

/**
 * The list a capture or a saved run was opened from, folded into a column
 * beside it: the list's own search and chips, the item on screen filled, each
 * row a link carrying the view that is open. The column is the shared `Rail`;
 * a page hands it its rows already worded.
 */
export function ItemRail({
  label,
  items,
  current,
  loading,
  error,
  onRetry,
  query,
  onQuery,
  placeholder,
  caption,
  filters,
  className,
}: {
  label: string
  items: RailItem[]
  current: string
  loading: boolean
  /** The list could not be read: said in place of the rows. */
  error?: Error
  onRetry?: () => void
  query: string
  onQuery: (value: string) => void
  placeholder: string
  caption: React.ReactNode
  /** The list's chips, where it has them. */
  filters?: React.ReactNode
  className?: string
}) {
  return (
    <Rail
      slot="item-rail"
      label={label}
      search={{ placeholder, query, onQuery }}
      filters={filters}
      caption={caption}
      rows={items}
      keyOf={(item) => item.id}
      current={(item) => item.id === current}
      row={(item, on) => (
        <RailRow
          item={item.id}
          name={item.label}
          current={on}
          href={item.href}
          mark={item.mark}
          end={item.end && <span className="shrink-0">{item.end}</span>}
        >
          <RailName current={on}>{item.title}</RailName>
          <RailLine className="font-mono">{item.line}</RailLine>
        </RailRow>
      )}
      loading={loading}
      error={error}
      onRetry={onRetry}
      empty="Nothing matches."
      className={className}
    />
  )
}
