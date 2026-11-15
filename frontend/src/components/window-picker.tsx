"use client"

import { Segments } from "@/components/segments"

/**
 * How far back a chart reaches: a few preset windows, one of them chosen.
 *
 * Five pages drew this five ways — Metrics' range picker, the traffic
 * charts', the VPN record's, the container traffic page's and the log
 * console's — each with its own height, its own label size and its own name
 * ("How far back the trend reaches", "…the charts reach"). It is one
 * `Segments`, so every chart says its window the same way and stands the same
 * height beside the section title it sits under.
 *
 * What a page adds around it stays the page's: Metrics' pan and zoom-out
 * once a span is dragged, the traffic page's typed range. A window that is
 * not one of the presets (a dragged span) is `value=""`: nothing is chosen,
 * and pressing a preset goes back to it. A window that is a form — the log
 * console's `TimeWindow` — is a popover, not this.
 */
export function WindowPicker<W extends string>({
  windows,
  value,
  onChange,
  onReselect,
  label = "How far back the charts reach",
  className,
}: {
  windows: { value: W; label: React.ReactNode }[]
  value: W | ""
  onChange: (next: W) => void
  /** The chosen window was pressed again: a typed range reopens its form. */
  onReselect?: (value: W) => void
  /** The group's name, when a page has one chart: "How far back the trend reaches". */
  label?: string
  className?: string
}) {
  return (
    <Segments
      label={label}
      size="sm"
      value={value}
      options={windows}
      onChange={onChange}
      onReselect={onReselect}
      className={className}
    />
  )
}
