"use client"

import { useEffect, useState } from "react"

/**
 * `value` once it has held still for `delay` milliseconds: a search field's
 * text asks the server once the typing stops, not on every key.
 *
 * An object is compared by what it holds, not by identity: a filter rebuilt
 * on every keystroke is a new object each render, and keyed on identity the
 * timer would restart forever. The first value is the settled one, so nothing
 * waits on mount.
 */
export function useDebounced<T>(value: T, delay: number): T {
  const [settled, setSettled] = useState(value)
  const key = typeof value === "object" && value !== null ? JSON.stringify(value) : value
  useEffect(() => {
    const timer = window.setTimeout(() => setSettled(value), delay)
    return () => window.clearTimeout(timer)
    // `key` stands for `value`: its content, which is what changed.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, delay])
  return settled
}
