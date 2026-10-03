"use client"

import { Modal } from "@/components/modal"

const groups = [
  [
    "Move around",
    [
      ["Back / Forward", "Alt+← / Alt+→ · ⌘[ / ⌘]"],
      ["Parent folder", "Backspace · Alt+↑"],
      ["Type a path", "Ctrl/⌘+L"],
      ["Move between items", "Arrow keys · Home / End"],
      ["Jump to a name", "Type its first letters"],
      ["Open", "Enter"],
      ["Quick look", "Space"],
    ],
  ],
  [
    "Find and arrange",
    [
      ["Find a file", "Ctrl/⌘+F · Ctrl/⌘+P"],
      ["Search inside files", "Ctrl/⌘+Shift+F"],
      ["Refresh this folder", "F5 · Ctrl/⌘+R"],
      ["Show hidden files", "Ctrl/⌘+Shift+."],
      ["Extend selection", "Shift+Arrow · Shift+click"],
      ["Toggle item selection", "Ctrl/⌘+Space"],
      ["Select all", "Ctrl/⌘+A"],
    ],
  ],
  [
    "Work with files",
    [
      ["Copy / Cut / Paste", "Ctrl/⌘+C / X / V"],
      ["New folder", "Ctrl/⌘+Shift+N"],
      ["Rename", "F2"],
      ["Delete with confirmation", "Delete"],
      ["Item menu", "Shift+F10"],
      ["Clear selection, then cancel clipboard", "Escape"],
      ["Show these shortcuts", "?"],
    ],
  ],
] as const

export function FilesShortcuts(props: { open: boolean; onOpenChange: (open: boolean) => void }) {
  return (
    <Modal
      {...props}
      title="Files shortcuts"
      description="Keyboard commands for the Files workspace."
      size="lg"
      initialFocus="body"
    >
      <div className="space-y-5">
        {groups.map(([title, shortcuts]) => (
          <section key={title}>
            <h3 className="mb-2 text-body font-medium">{title}</h3>
            <dl className="divide-y divide-hairline">
              {shortcuts.map(([label, keys]) => (
                <div
                  key={label}
                  className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 py-1.5 text-hint"
                >
                  <dt>{label}</dt>
                  <dd className="text-muted-foreground">
                    <kbd>{keys}</kbd>
                  </dd>
                </div>
              ))}
            </dl>
          </section>
        ))}
      </div>
    </Modal>
  )
}
