/**
 * What the settings forms of this section hold in their boxes, and what that
 * means to the host. A box is text; the host wants whole numbers, and spells
 * "no rule" as -1. An empty box that was sent as `Number("")` is 0, which for a
 * password's shortest length or a group's number is an answer nobody gave, so
 * a form holds its Save while a box says something the host would not take.
 */

/** A number of days as its box shows it: nothing for "no rule". */
export function daysToBox(value: number | undefined) {
  return value === undefined || value < 0 ? "" : String(value)
}

/** The box back to what the host stores: an empty one is -1, its "no rule". */
export function boxToDays(value: string) {
  return value.trim() === "" ? -1 : Number(value)
}

/** Whole, and inside what the host accepts. */
export function validWhole(value: string, min: number, max: number) {
  if (!/^\d+$/.test(value.trim())) return false
  const number = Number(value)
  return number >= min && number <= max
}

/** A box of days: empty, or a whole number of them up to what `chage` takes. */
export function validDays(value: string, min = 0) {
  return value.trim() === "" || validWhole(value, min, 99999)
}

/**
 * An optional limit: empty or 0 for none (the host reads 0 as "no limit"), or
 * a number from `min` up, whole unless the box takes halves.
 */
export function validLimit(value: string, min: number, whole = true) {
  const text = value.trim()
  if (text === "") return true
  if (!(whole ? /^\d+$/ : /^\d+(\.\d+)?$/).test(text)) return false
  return Number(text) === 0 || Number(text) >= min
}

/** How many of a form's boxes differ from what was read, for the save bar's count. */
export function countChanged(pairs: [unknown, unknown][]) {
  return pairs.filter(([now, before]) => now !== before).length
}
