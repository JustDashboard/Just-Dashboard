import * as React from "react"

const MOBILE_BREAKPOINT = 768
const QUERY = `(max-width: ${MOBILE_BREAKPOINT - 1}px)`

/**
 * Tracks a media query.
 *
 * useSyncExternalStore is what React provides for reading a browser API that
 * changes outside of React — it avoids the extra render an effect-plus-state
 * version costs, and gives the server render an explicit answer instead of
 * undefined.
 */
export function useMediaQuery(query: string) {
  const subscribe = React.useCallback(
    (onChange: () => void) => {
      const mql = window.matchMedia(query)
      mql.addEventListener("change", onChange)
      return () => mql.removeEventListener("change", onChange)
    },
    [query],
  )
  return React.useSyncExternalStore(
    subscribe,
    () => window.matchMedia(query).matches,
    () => false,
  )
}

/** Tracks the mobile breakpoint. */
export function useIsMobile() {
  return useMediaQuery(QUERY)
}
