import { flowCounter } from "@/lib/network-flows"

const FULL = new Intl.DateTimeFormat("en-GB", {
  timeZone: "UTC",
  day: "numeric",
  month: "short",
  year: "numeric",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
})
const DAY = new Intl.DateTimeFormat("en-GB", {
  timeZone: "UTC",
  day: "numeric",
  month: "short",
  hour: "2-digit",
  minute: "2-digit",
})
const DATE = new Intl.DateTimeFormat("en-GB", { timeZone: "UTC", day: "numeric", month: "short" })
const HOUR = new Intl.DateTimeFormat("en-GB", {
  timeZone: "UTC",
  hour: "2-digit",
  minute: "2-digit",
})
const CLOCK = new Intl.DateTimeFormat("en-GB", {
  timeZone: "UTC",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
})

/**
 * A moment of the record, always in UTC and always saying so: the recorder
 * keeps UTC hours, and a local time beside them would name a different hour.
 * `short` drops the year and seconds, `date` the time, `clock` the date and
 * `hour` the seconds too; the whole moment is the element's title either way. A moment that was never recorded reads
 * "Unknown".
 */
export function UTCTime({
  value,
  form = "full",
}: {
  value?: string | null
  form?: "full" | "short" | "date" | "clock" | "hour"
}) {
  const at = value ? new Date(value) : undefined
  if (!value || !at || Number.isNaN(at.getTime())) return <span>Unknown</span>
  const whole = `${FULL.format(at)} UTC`
  return (
    <time dateTime={value} title={whole}>
      {form === "full"
        ? whole
        : form === "short"
          ? `${DAY.format(at)} UTC`
          : form === "date"
            ? DATE.format(at)
            : form === "hour"
              ? HOUR.format(at)
              : CLOCK.format(at)}
    </time>
  )
}

/** A counter the server may not have measured: its exact value, or "Unknown". */
export const counted = (raw: string | null | undefined) => flowCounter(raw)?.toString() ?? "Unknown"

/** A calendar day in words, for a UTC day picked as `2026-10-07`. */
export function dayWords(day: string) {
  const at = new Date(`${day}T00:00:00Z`)
  if (Number.isNaN(at.getTime())) return day
  return new Intl.DateTimeFormat("en-GB", {
    timeZone: "UTC",
    day: "numeric",
    month: "short",
    year: "numeric",
  }).format(at)
}
