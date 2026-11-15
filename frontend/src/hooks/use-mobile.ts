import { useBelow } from "@/lib/breakpoints"

/**
 * `useMediaQuery` lives with the breakpoints now (`lib/breakpoints.ts`); it is
 * re-exported here for the call sites that import it from this module.
 */
export { useMediaQuery } from "@/lib/breakpoints"

/**
 * A phone: narrower than `sm`, the same width every `max-sm:` class means.
 * This is the one definition of a phone in script.
 */
export function useIsPhone() {
  return useBelow("sm")
}

/**
 * The sidebar's drawer width: narrower than `md`, where the shell's rail is a
 * sheet rather than a column (`components/ui/sidebar.tsx` and its `md:`
 * classes). Not a phone — a 700px window is not a phone and still gets the
 * drawer. A layout that means "phone" asks `useIsPhone`.
 */
export function useIsMobile() {
  return useBelow("md")
}
