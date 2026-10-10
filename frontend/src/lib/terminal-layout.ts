export type SplitDirection = "left" | "right" | "up" | "down"
export type TerminalLayout =
  | { window: string }
  | {
      id: string
      axis: "x" | "y"
      ratio: number
      /** The standalone window that owns this group's tab. */
      tab?: string
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
export const TERMINAL_WINDOW_DRAG = "application/x-jd-terminal-window"
const MIN = { width: 160, height: 120 }

export function leaves(tree: TerminalLayout): string[] {
  return "window" in tree ? [tree.window] : [...leaves(tree.first), ...leaves(tree.second)]
}

export function layoutTab(tree: TerminalLayout): string {
  return "window" in tree ? tree.window : (tree.tab ?? leaves(tree)[0])
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
    if (!first || !second) {
      const surviving = first ?? second
      return surviving &&
        !("window" in surviving) &&
        typeof node.tab === "string" &&
        leaves(surviving).includes(node.tab)
        ? { ...surviving, tab: node.tab }
        : surviving
    }
    const members = [...leaves(first), ...leaves(second)]
    return {
      id: node.id,
      axis: node.axis,
      // Older layouts listed every pane as a tab. Retain the oldest window
      // as their owner when migrating, even when the first split was left/up.
      tab:
        typeof node.tab === "string" && members.includes(node.tab)
          ? node.tab
          : windows.find((window) => members.includes(window)),
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
  created: string | TerminalLayout,
  direction: SplitDirection,
  id: string,
): TerminalLayout {
  if ("window" in tree) {
    if (tree.window !== window) return tree
    const before = direction === "left" || direction === "up"
    const added = typeof created === "string" ? { window: created } : created
    return {
      id,
      axis: direction === "left" || direction === "right" ? "x" : "y",
      ratio: 0.5,
      tab: tree.window,
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

export function detachWindow(groups: TerminalLayout[], window: string): TerminalLayout[] {
  const group = groups.find((tree) => leaves(tree).includes(window))
  if (!group || "window" in group) return groups
  return [
    ...reconcileLayouts(
      groups,
      groups.flatMap(leaves).filter((id) => id !== window),
    ),
    { window },
  ]
}

export function dockLayout(
  groups: TerminalLayout[],
  source: string,
  target: string,
  direction: SplitDirection,
  id: string,
): TerminalLayout[] {
  const added = groups.find((tree) => layoutTab(tree) === source)
  const destination = groups.find((tree) => leaves(tree).includes(target))
  if (!added || !destination || added === destination) return groups
  return groups
    .filter((tree) => tree !== added)
    .map((tree) => splitLayout(tree, target, added, direction, id))
}

/** Nearest normalized edge, so wide/short panes still offer all four sides. */
export function dropDirection(rect: Rect, x: number, y: number): SplitDirection | undefined {
  if (rect.width <= 0 || rect.height <= 0) return
  const horizontal = (x - rect.x) / rect.width
  const vertical = (y - rect.y) / rect.height
  if (horizontal < 0 || horizontal > 1 || vertical < 0 || vertical > 1) return
  const edges: [SplitDirection, number][] = [
    ["left", horizontal],
    ["right", 1 - horizontal],
    ["up", vertical],
    ["down", 1 - vertical],
  ]
  return edges.sort((a, b) => a[1] - b[1])[0][0]
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

export function canSplit(
  rect: Rect | undefined,
  direction: SplitDirection,
  added?: TerminalLayout,
): boolean {
  if (!rect) return false
  const size = added ? minimum(added) : MIN
  return direction === "left" || direction === "right"
    ? rect.width >= MIN.width + size.width + SPLIT_GAP && (!added || rect.height >= size.height)
    : rect.height >= MIN.height + size.height + SPLIT_GAP && (!added || rect.width >= size.width)
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
