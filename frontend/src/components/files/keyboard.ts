export function filesOwnKeyboard(event: KeyboardEvent): boolean {
  if (event.defaultPrevented || event.isComposing) return false
  const target = event.target instanceof HTMLElement ? event.target : null
  if (target && target !== document.body && !target.closest("[data-files-workspace]")) return false
  return !document.querySelector(
    "[role='dialog']:not([data-state='closed']), [role='menu']:not([data-state='closed']), [role='listbox']:not([data-state='closed'])",
  )
}

export function isTypingTarget(target: EventTarget | null): boolean {
  return (
    target instanceof HTMLElement &&
    !!target.closest("input, textarea, select, [contenteditable]:not([contenteditable='false'])")
  )
}
