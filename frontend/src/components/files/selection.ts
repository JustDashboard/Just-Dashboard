export type Point = { x: number; y: number }
export type SelectionRect = { left: number; top: number; right: number; bottom: number }

export function selectionRect(start: Point, end: Point): SelectionRect {
  return {
    left: Math.min(start.x, end.x),
    top: Math.min(start.y, end.y),
    right: Math.max(start.x, end.x),
    bottom: Math.max(start.y, end.y),
  }
}

export function intersectingPaths(
  box: SelectionRect,
  entries: { path: string; rect: SelectionRect }[],
  initial: Iterable<string> = [],
): Set<string> {
  const paths = new Set(initial)
  for (const { path, rect } of entries) {
    if (
      rect.left < box.right &&
      rect.right > box.left &&
      rect.top < box.bottom &&
      rect.bottom > box.top
    ) {
      paths.add(path)
    }
  }
  return paths
}

export function rangePaths(paths: string[], anchor: string, target: string): string[] {
  const a = paths.indexOf(anchor)
  const b = paths.indexOf(target)
  if (a < 0 || b < 0) return [target]
  return paths.slice(Math.min(a, b), Math.max(a, b) + 1)
}
