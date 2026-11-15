import * as React from "react"

/**
 * The viewport breakpoints, for script.
 *
 * The same five widths as the `--breakpoint-*` tokens in globals.css, which is
 * what `sm:`, `md:` and the rest compile from; the test beside this file reads
 * them from there. Script had been typing them as literal strings — `(min-width:
 * 1024px)` thirty-one times, 640 twenty-nine — plus off-scale 1100 and 479 that
 * no class could agree with, and `useIsMobile` called a phone "under 768" while
 * every class called it "under 640".
 *
 * A viewport breakpoint is for the page's own shape: the shell, a rail, the
 * bottom bar, whether a pane is drawn. A block that can sit full-width on one
 * page and beside a rail on another asks its container instead.
 */
export const BREAKPOINTS = {
  sm: 640,
  md: 768,
  lg: 1024,
  xl: 1280,
  "2xl": 1536,
} as const

export type Breakpoint = keyof typeof BREAKPOINTS

/**
 * The media query a breakpoint stands for, written the way Tailwind writes it:
 * in rem, so a browser set to a larger default font moves the breakpoint with
 * it exactly as the stylesheet does, and `below` as a strict `<` so the two
 * sides of one width never both match.
 */
export function breakpointQuery(name: Breakpoint, side: "from" | "below" = "from"): string {
  const rem = BREAKPOINTS[name] / 16
  return side === "from" ? `(min-width: ${rem}rem)` : `(width < ${rem}rem)`
}

/**
 * What `useSyncExternalStore` needs to follow one media query. Apart from the
 * hook so the subscription can be tested without a document.
 *
 * The server has no viewport, so it answers `false`; a page that must not
 * re-lay itself out on hydration puts the difference in a class instead.
 */
export function mediaQueryStore(query: string) {
  return {
    subscribe(onChange: () => void) {
      const mql = window.matchMedia(query)
      mql.addEventListener("change", onChange)
      return () => mql.removeEventListener("change", onChange)
    },
    getSnapshot: () => window.matchMedia(query).matches,
    getServerSnapshot: () => false,
  }
}

/**
 * Tracks a media query.
 *
 * useSyncExternalStore is what React provides for reading a browser API that
 * changes outside of React — it avoids the extra render an effect-plus-state
 * version costs, and gives the server render an explicit answer instead of
 * undefined.
 */
export function useMediaQuery(query: string): boolean {
  const store = React.useMemo(() => mediaQueryStore(query), [query])
  return React.useSyncExternalStore(store.subscribe, store.getSnapshot, store.getServerSnapshot)
}

/** Whether the viewport is at least this breakpoint wide: `useBreakpoint("lg")` is `lg:`. */
export function useBreakpoint(name: Breakpoint): boolean {
  return useMediaQuery(breakpointQuery(name))
}

/** Whether the viewport is narrower than this breakpoint: `useBelow("sm")` is `max-sm:`. */
export function useBelow(name: Breakpoint): boolean {
  return useMediaQuery(breakpointQuery(name, "below"))
}
