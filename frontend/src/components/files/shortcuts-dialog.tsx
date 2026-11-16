"use client"

import { Modal } from "@/components/modal"
import { ShortcutList, type ShortcutGroup } from "@/components/keycaps"

/** Written once for both keyboards: `Keycaps` draws Ctrl or ⌘ for the reader's. */
const groups: ShortcutGroup[] = [
  {
    title: "Move around",
    items: [
      { does: "Back / Forward", keys: "Alt+← / Alt+→ · ⌘+[ / ⌘+]" },
      { does: "Parent folder", keys: "Backspace · Alt+↑" },
      { does: "Type a path", keys: "Ctrl/⌘+L" },
      { does: "Move between items", keys: "↑ / ↓ / ← / → · Home / End" },
      { does: "Jump to a name", keys: "Type its first letters" },
      { does: "Open", keys: "Enter" },
      { does: "Quick look", keys: "Space" },
    ],
  },
  {
    title: "Find and arrange",
    items: [
      { does: "Find a file", keys: "Ctrl/⌘+F · Ctrl/⌘+P" },
      { does: "Search inside files", keys: "Ctrl/⌘+Shift+F" },
      { does: "Refresh this folder", keys: "F5 · Ctrl/⌘+R" },
      { does: "Show hidden files", keys: "Ctrl/⌘+Shift+." },
      { does: "Extend selection", keys: "Shift+↑ / Shift+↓ · Shift+click" },
      { does: "Toggle item selection", keys: "Ctrl/⌘+Space" },
      { does: "Select all", keys: "Ctrl/⌘+A" },
    ],
  },
  {
    title: "Work with files",
    items: [
      { does: "Copy / Cut / Paste", keys: "Ctrl/⌘+C / X / V" },
      { does: "Paste files or text from your computer", keys: "Ctrl/⌘+V" },
      { does: "New folder", keys: "Ctrl/⌘+Shift+N" },
      { does: "Rename", keys: "F2" },
      { does: "Delete with confirmation", keys: "Delete" },
      { does: "Item menu", keys: "Shift+F10" },
      { does: "Clear selection, then cancel clipboard", keys: "Escape" },
      { does: "Show these shortcuts", keys: "?" },
    ],
  },
]

export function FilesShortcuts(props: { open: boolean; onOpenChange: (open: boolean) => void }) {
  return (
    <Modal
      {...props}
      title="Files shortcuts"
      description="Keyboard commands for the Files workspace."
      size="lg"
      initialFocus="body"
    >
      <ShortcutList groups={groups} />
    </Modal>
  )
}
