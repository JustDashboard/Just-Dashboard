import type { ShapeDevice } from "@/lib/types"

/**
 * The floor the server holds the uplink and the client path to: below a
 * megabit a limit is nearly always a typo for a unit, and on those two
 * devices it is the road back to the dashboard. Mirrors `minGuardedKbit` in
 * netx, so the form says so before the server has to.
 */
export const GUARDED_MIN_KBIT = 1000

/** A limit as a person says it: "50 Mbit/s", "500 kbit/s", "1.5 Gbit/s"; zero is no limit. */
export function formatKbit(kbit: number): string {
  if (kbit <= 0) return "No limit"
  if (kbit >= 1_000_000) return `${trim(kbit / 1_000_000)} Gbit/s`
  if (kbit >= 1000) return `${trim(kbit / 1000)} Mbit/s`
  return `${kbit} kbit/s`
}

const trim = (value: number) => String(Number(value.toFixed(2)))

/** Mbit/s as typed — blank or zero is no limit — to the kbit the route takes; NaN when it is not a number. */
export function mbitToKbit(text: string): number {
  const trimmed = text.trim().replace(",", ".")
  if (trimmed === "") return 0
  if (!/^\d+(\.\d+)?$/.test(trimmed)) return Number.NaN
  return Math.round(Number(trimmed) * 1000)
}

/** kbit back to the Mbit/s a field shows; no limit is an empty field. */
export function kbitToMbit(kbit: number): string {
  return kbit > 0 ? trim(kbit / 1000) : ""
}

/** Whether the server holds this device to the one-megabit floor. */
export function guarded(device: Pick<ShapeDevice, "uplink" | "clientPath">) {
  return device.uplink || device.clientPath
}

/** Why a typed limit is refused on this device, or undefined where it is fine. */
export function limitProblem(
  text: string,
  device: Pick<ShapeDevice, "uplink" | "clientPath">,
): string | undefined {
  const kbit = mbitToKbit(text)
  if (Number.isNaN(kbit)) return "Type a number of megabits a second, like 50 or 2.5."
  if (kbit > 0 && guarded(device) && kbit < GUARDED_MIN_KBIT) {
    return "Under 1 Mbit/s here could lock you out: this device carries your connection to the dashboard."
  }
  return undefined
}
