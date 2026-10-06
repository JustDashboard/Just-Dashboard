"use client"

import { useApple } from "@/lib/platform"

/**
 * The editor's keys as the reader's keyboard prints them. The bindings are
 * Ctrl on a PC and Command on a Mac; a hint that says "Ctrl+Enter" on a Mac
 * names a key that does something else there.
 */
export function useKeyNames() {
  return useApple()
    ? { run: "⌘Enter", runAll: "⇧⌘Enter", save: "⌘S", format: "⇧⌥F" }
    : { run: "Ctrl+Enter", runAll: "Ctrl+Shift+Enter", save: "Ctrl+S", format: "Shift+Alt+F" }
}
