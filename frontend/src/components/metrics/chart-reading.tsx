"use client"

import { Meter, utilisationTone } from "@/components/meter"

/**
 * A chart's reading now, at the end of its head: the figure beside the key of
 * the series it is, what it is a share of, and — where there is a ceiling — a
 * short meter of how close it is. The words are read once, as a sentence; the
 * figure, the key and the meter are what the eye reads.
 *
 * A project's Runtime heads each container chart with one, and a database's
 * home each activity chart: the number and its shape are read in one place,
 * where a row of tiles over the charts said the same figure twice.
 */
export function ChartReading({
  label,
  color,
  value,
  note,
  meter,
}: {
  label: string
  color: string
  value: string
  note?: string
  meter?: number
}) {
  return (
    <span className="flex min-w-0 items-baseline gap-1.5">
      <span className="sr-only">
        {label}: {value}
        {note && ` ${note}`}
      </span>
      <span
        aria-hidden
        className="h-2.5 w-0.5 shrink-0 self-center rounded-full"
        style={{ background: color }}
      />
      <span aria-hidden className="numeric text-sm font-semibold tracking-tight">
        {value}
      </span>
      {note && (
        <span aria-hidden className="truncate text-hint text-muted-foreground">
          {note}
        </span>
      )}
      {meter !== undefined && (
        <Meter
          value={meter}
          tone={utilisationTone(meter)}
          size="thin"
          label={label}
          className="w-14 self-center"
        />
      )}
    </span>
  )
}
