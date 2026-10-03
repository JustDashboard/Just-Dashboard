export function rangeSelection(names: string[], selected: string[], anchor: string, end: string) {
  const from = names.indexOf(anchor)
  const to = names.indexOf(end)
  if (from < 0 || to < 0) return selected
  return [...new Set([...selected, ...names.slice(Math.min(from, to), Math.max(from, to) + 1)])]
}
