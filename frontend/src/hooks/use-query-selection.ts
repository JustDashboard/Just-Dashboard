"use client"

import { useCallback } from "react"
import { useSearchParams } from "next/navigation"

// Keep a detail panel shareable and let browser history restore its selection.
export function useQuerySelection(key: string) {
  const search = useSearchParams()
  const select = useCallback(
    (value: string | null) => {
      const url = new URL(window.location.href)
      if (value) url.searchParams.set(key, value)
      else url.searchParams.delete(key)
      if (url.href !== window.location.href) {
        window.history.pushState(null, "", `${url.pathname}${url.search}${url.hash}`)
      }
    },
    [key],
  )
  return [search.get(key) || null, select] as const
}
