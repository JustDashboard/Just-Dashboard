"use client"

import { useEffect, useState } from "react"
import { INVENTORY_SOURCES, type InventoryItem, type InventorySource } from "./inventory"

type InventoryRead = {
  source: InventorySource
  items: InventoryItem[]
  loading: boolean
  error?: string
}

function pending(): InventoryRead[] {
  return INVENTORY_SOURCES.map((source) => ({ source, items: [], loading: true }))
}

/** Mounted only in the open dialog: closing aborts reads and discards the index. */
export function useSearchInventory(enabled: boolean) {
  const [reads, setReads] = useState(pending)
  const [revision, setRevision] = useState(0)

  useEffect(() => {
    if (!enabled) return
    const controller = new AbortController()
    let next = 0
    const worker = async () => {
      while (next < INVENTORY_SOURCES.length && !controller.signal.aborted) {
        const index = next++
        const source = INVENTORY_SOURCES[index]
        let result: InventoryRead
        try {
          const signal = AbortSignal.any([controller.signal, AbortSignal.timeout(8000)])
          result = { source, items: await source.read(signal), loading: false }
        } catch {
          result = { source, items: [], loading: false, error: "Could not load" }
        }
        if (!controller.signal.aborted) {
          setReads((previous) => previous.map((read, i) => (i === index ? result : read)))
        }
      }
    }
    // Bound concurrent host reads; a slow subsystem cannot hold up the other results.
    void Promise.all(Array.from({ length: 4 }, worker))
    return () => controller.abort()
  }, [enabled, revision])

  return {
    reads: enabled ? reads : [],
    retry: () => {
      setReads(pending())
      setRevision((value) => value + 1)
    },
  }
}
