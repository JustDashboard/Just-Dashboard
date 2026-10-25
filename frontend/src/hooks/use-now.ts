"use client"

import { useEffect, useState } from "react"

/**
 * The time, advancing every `intervalMs`, for a figure that is a distance from now (a countdown, "12
 * s ago"). It is state rather than a `Date.now()` in the render because a render has to give the
 * same answer twice; the first value is read once, when the component mounts.
 */
export function useNow(intervalMs: number) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(timer)
  }, [intervalMs])
  return now
}
