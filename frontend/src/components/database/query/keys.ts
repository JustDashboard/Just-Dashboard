"use client"

import { useSyncExternalStore } from "react"

const never = () => () => {}
const onApple = () => /Mac|iPhone|iPad|iPod/.test(navigator.platform)

/**
 * The editor's keys as the reader's keyboard prints them. The bindings are
 * Ctrl on a PC and Command on a Mac; a hint that says "Ctrl+Enter" on a Mac
 * names a key that does something else there.
 */
export function useKeyNames() {
  const apple = useSyncExternalStore(never, onApple, () => false)
  return apple
    ? { run: "⌘Enter", runAll: "⇧⌘Enter", save: "⌘S", format: "⇧⌥F" }
    : { run: "Ctrl+Enter", runAll: "Ctrl+Shift+Enter", save: "Ctrl+S", format: "Shift+Alt+F" }
}
