import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

/**
 * Padding for a scrolling container that holds focusable controls.
 *
 * A focus ring is drawn *outside* the control's border box — a 2px outline at a
 * 2px offset in this design system — and any container whose overflow is not
 * `visible` clips it. Setting one axis to `auto` makes the other `auto` too, so an
 * `overflow-y-auto` list clips the ring on all four sides rather than only the
 * two it scrolls on. The symptom is a highlight that looks bitten off along
 * whichever edge the control is nearest, which in a dense form is most of them.
 *
 * Four pixels clears the outline, its offset and the border it sits outside.
 * Scroll containers with focusable children should carry this instead of a
 * one-sided `pr-1`, which only ever gave the scrollbar room.
 */
export const ringSafeScroll = "p-1"
