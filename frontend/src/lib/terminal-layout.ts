export type SplitDirection = "left" | "right" | "up" | "down"
export type TerminalLayout =
  | { window: string }
  | {
      id: string
      axis: "x" | "y"
      ratio: number
      first: TerminalLayout
      second: TerminalLayout
    }

export type Rect = { x: number; y: number; width: number; height: number }
export type Divider = Rect & {
  id: string
  axis: "x" | "y"
  ratio: number
  parent: Rect
  min: number
  max: number
}
export const SPLIT_GAP = 6
const MIN = { width: 160, height: 120 }

export function leaves(tree: TerminalLayout): string[] {
  return "window" in tree ? [tree.window] : [...leaves(tree.first), ...leaves(tree.second)]
}

// Stored layouts outlive their PTYs, and another browser may create or close a
// window. Normalize at the boundary, including corrupt or duplicate entries.
export function reconcileLayouts(value: unknown, windows: string[]): TerminalLayout[] {
  const remaining = new Set(windows)
  const ids = new Set<string>()
  const read = (value: unknown, depth = 0): TerminalLayout | null => {
    if (!value || typeof value !== "object" || depth > 32) return null
    const node = value as Record<string, unknown>
    if (typeof node.window === "string") {
      if (!remaining.delete(node.window)) return null
      return { window: node.window }
    }
    if (typeof node.id !== "string" || ids.has(node.id) || (node.axis !== "x" && node.axis !== "y"))
      return null
    ids.add(node.id)
    const first = read(node.first, depth + 1)
    const second = read(node.second, depth + 1)
    if (!first || !second) return first ?? second
    return {
      id: node.id,
      axis: node.axis,
      ratio:
        typeof node.ratio === "number" && Number.isFinite(node.ratio)
          ? Math.max(0.05, Math.min(0.95, node.ratio))
          : 0.5,
      first,
      second,
    }
  }
  const groups = (Array.isArray(value) ? value : []).flatMap((item) => {
    const tree = read(item)
    return tree ? [tree] : []
  })
  return [...groups, ...Array.from(remaining, (window) => ({ window }))]
}

export function splitLayout(
  tree: TerminalLayout,
  window: string,
  created: string,
  direction: SplitDirection,
  id: string,
): TerminalLayout {
  if ("window" in tree) {
    if (tree.window !== window) return tree
    const before = direction === "left" || direction === "up"
    const added = { window: created }
    return {
      id,
      axis: direction === "left" || direction === "right" ? "x" : "y",
      ratio: 0.5,
      first: before ? added : tree,
      second: before ? tree : added,
    }
  }
  return {
    ...tree,
    first: splitLayout(tree.first, window, created, direction, id),
    second: splitLayout(tree.second, window, created, direction, id),
  }
}

export function resizeLayout(tree: TerminalLayout, id: string, ratio: number): TerminalLayout {
  if ("window" in tree) return tree
  if (tree.id === id) return { ...tree, ratio: Math.max(0.05, Math.min(0.95, ratio)) }
  return {
    ...tree,
    first: resizeLayout(tree.first, id, ratio),
    second: resizeLayout(tree.second, id, ratio),
  }
}

function minimum(tree: TerminalLayout): { width: number; height: number } {
  if ("window" in tree) return MIN
  const a = minimum(tree.first)
  const b = minimum(tree.second)
  return tree.axis === "x"
    ? { width: a.width + b.width + SPLIT_GAP, height: Math.max(a.height, b.height) }
    : { width: Math.max(a.width, b.width), height: a.height + b.height + SPLIT_GAP }
}

export function layoutGeometry(tree: TerminalLayout, width: number, height: number) {
  const panes: Record<string, Rect> = {}
  const dividers: Divider[] = []
  const place = (node: TerminalLayout, rect: Rect) => {
    if ("window" in node) {
      panes[node.window] = rect
      return
    }
    const horizontal = node.axis === "x"
    const size = horizontal ? "width" : "height"
    const position = horizontal ? "x" : "y"
    const gap = Math.min(SPLIT_GAP, rect[size])
    const available = Math.max(0, rect[size] - gap)
    const a = minimum(node.first)[size]
    const b = minimum(node.second)[size]
    // On a smaller viewport, shrink both subtrees proportionally rather than
    // overflow the workspace or force a negative/zero-sized neighbour.
    const scale = Math.min(1, available / (a + b))
    const min = available ? (a * scale) / available : 0.5
    const max = available ? 1 - (b * scale) / available : 0.5
    const ratio = Math.max(min, Math.min(max, node.ratio))
    const first = available * ratio
    place(node.first, { ...rect, [size]: first })
    dividers.push({
      ...rect,
      [position]: rect[position] + first,
      [size]: gap,
      id: node.id,
      axis: node.axis,
      ratio,
      parent: rect,
      min,
      max,
    })
    place(node.second, {
      ...rect,
      [position]: rect[position] + first + gap,
      [size]: available - first,
    })
  }
  place(tree, { x: 0, y: 0, width: Math.max(0, width), height: Math.max(0, height) })
  return { panes, dividers }
}

export function canSplit(rect: Rect | undefined, direction: SplitDirection): boolean {
  if (!rect) return false
  return direction === "left" || direction === "right"
    ? rect.width >= MIN.width * 2 + SPLIT_GAP
    : rect.height >= MIN.height * 2 + SPLIT_GAP
}

export function neighbour(
  panes: Record<string, Rect>,
  focused: string,
  direction: SplitDirection,
): string | undefined {
  const origin = panes[focused]
  if (!origin) return
  const horizontal = direction === "left" || direction === "right"
  const sign = direction === "left" || direction === "up" ? -1 : 1
  const center = (rect: Rect) => [rect.x + rect.width / 2, rect.y + rect.height / 2]
  const a = center(origin)
  return Object.entries(panes)
    .filter(
      ([id, rect]) =>
        id !== focused && (center(rect)[horizontal ? 0 : 1] - a[horizontal ? 0 : 1]) * sign > 0,
    )
    .map(([id, rect]) => {
      const b = center(rect)
      const cross = horizontal ? 1 : 0
      const aligned = horizontal
        ? rect.y < origin.y + origin.height && rect.y + rect.height > origin.y
        : rect.x < origin.x + origin.width && rect.x + rect.width > origin.x
      return {
        id,
        score:
          Math.abs(b[horizontal ? 0 : 1] - a[horizontal ? 0 : 1]) +
          Math.abs(b[cross] - a[cross]) * 2 +
          (aligned ? 0 : 10000),
      }
    })
    .sort((a, b) => a.score - b.score)[0]?.id
}
