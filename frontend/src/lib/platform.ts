"use client"

import { useSyncExternalStore } from "react"

const never = () => () => {}
const onApple = () => /Mac|iPhone|iPad|iPod/.test(navigator.platform)

/**
 * Whether the reader's keyboard prints ⌘ where a PC's prints Ctrl. A hint that
 * says "⌘K" on Linux names a key the keyboard does not have. The server, and
 * the first client paint that has to match it, say Ctrl.
 */
export function useApple() {
  return useSyncExternalStore(never, onApple, () => false)
}
