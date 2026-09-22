"use client"

import { useCallback, useEffect, useRef } from "react"
import { discardAbandoned, type ConfigureFlow } from "./draft"

/** A source left behind must not reopen itself when its inspection finishes. */
export function useSourceInspection(onInspected: (flow: ConfigureFlow) => void) {
  const generation = useRef(0)
  const cancel = useCallback(() => {
    generation.current += 1
  }, [])
  useEffect(() => cancel, [cancel])

  const inspect = async (prepare: () => Promise<ConfigureFlow>) => {
    const own = ++generation.current
    try {
      const flow = await prepare()
      if (generation.current !== own) {
        await discardAbandoned(flow.draft.id)
        return false
      }
      onInspected(flow)
      return true
    } catch (error) {
      if (generation.current === own) throw error
      return false
    }
  }
  return { inspect, cancel }
}
