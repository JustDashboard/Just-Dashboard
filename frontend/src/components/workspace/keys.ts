export function typing(target: EventTarget | null) {
  return (
    target instanceof HTMLElement &&
    !!target.closest(
      "input, textarea, select, [contenteditable]:not([contenteditable='false']), .monaco-editor, .xterm",
    )
  )
}

export function overlayOpen() {
  return Array.from(
    document.querySelectorAll<HTMLElement>("[role='dialog'], [role='menu'], [role='listbox']"),
  ).some((node) => node.getClientRects().length > 0)
}

export function nameIndex(names: string[], prefix: string, start: number) {
  for (let offset = 1; offset <= names.length; offset++) {
    const index = (Math.max(-1, start) + offset) % names.length
    if (names[index].toLocaleLowerCase().startsWith(prefix.toLocaleLowerCase())) return index
  }
  return -1
}

export function nextIndex(length: number, current: number, key: string) {
  if (!length) return -1
  if (key === "Home") return 0
  if (key === "End") return length - 1
  if (key === "ArrowDown") return Math.min(length - 1, current + 1)
  if (key === "ArrowUp") return current < 0 ? length - 1 : Math.max(0, current - 1)
  return -1
}
