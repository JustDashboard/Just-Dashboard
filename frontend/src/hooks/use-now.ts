"use client"

import { useEffect, useState } from "react"

/**
 * The time, advancing every `intervalMs`, for a figure that is a distance from now (a countdown, "12
 * s ago", a build's running time). It is state rather than a `Date.now()` in the render because a
 * render has to give the same answer twice; the first value is read once, when the component
 * mounts. `enabled` stops the clock while nothing is in flight, so a page with no countdown on it
 * does not re-render every second.
 */
export function useNow(intervalMs = 1000, enabled = true) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!enabled) return
    const timer = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(timer)
  }, [intervalMs, enabled])
  return now
}
