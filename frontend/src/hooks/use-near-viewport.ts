"use client"

import { useCallback, useState } from "react"

const listeners = new Map<Element, (visible: boolean) => void>()
let observer: IntersectionObserver | undefined

// One observer for the listing, not one browser observer per file. Text remains
// mounted; only previews and heavier controls need to follow the viewport.
export function useNearViewport<T extends HTMLElement>() {
  const [visible, setVisible] = useState(false)
  const ref = useCallback((element: T | null) => {
    if (!element) return
    if (typeof IntersectionObserver === "undefined") {
      setVisible(true)
      return
    }
    observer ??= new IntersectionObserver(
      (entries) => {
        for (const entry of entries) listeners.get(entry.target)?.(entry.isIntersecting)
      },
      { rootMargin: "600px" },
    )
    listeners.set(element, setVisible)
    observer.observe(element)
    return () => {
      observer?.unobserve(element)
      listeners.delete(element)
      if (listeners.size === 0) {
        observer?.disconnect()
        observer = undefined
      }
    }
  }, [])
  return [ref, visible] as const
}
