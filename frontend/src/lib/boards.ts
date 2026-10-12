/**
 * A drawing's thumbnail, derived by the server from the scene on every save
 * (`boardPreviewOf`) so the list never reads a whole scene — which can be
 * megabytes of embedded images — to draw it. The colours are the scene's own,
 * in Excalidraw's light palette, and checked to be hex before they are stored.
 */
export type DrawingElement = {
  type:
    | "rectangle"
    | "ellipse"
    | "diamond"
    | "line"
    | "arrow"
    | "freedraw"
    | "text"
    | "image"
    | "frame"
    | "magicframe"
    | "embeddable"
    | "iframe"
  x: number
  y: number
  w: number
  h: number
  angle?: number
  stroke?: string
  fill?: string
  fillStyle?: "hachure" | "cross-hatch" | "zigzag"
  strokeWidth?: number
  strokeStyle?: "dashed" | "dotted"
  round?: boolean
  opacity?: number
  /** Relative to `x`/`y`, as Excalidraw stores a linear element's points. */
  points?: [number, number][]
  text?: string
  fontSize?: number
  fontFamily?: number
  textAlign?: "center" | "right"
  startArrow?: boolean
  endArrow?: boolean
}

/** A card the editor placed for something on this server (`insertCard`). */
export type BoardResource = { kind: "server" | "project" | "database"; id?: number }

export type BoardPreview = {
  background: string
  /** Every element on the board; `elements` keeps the largest 400. */
  count: number
  elements: DrawingElement[]
  resources: BoardResource[]
}

export type BoardSummary = {
  id: number
  name: string
  revision: number
  /** On the list only. */
  preview?: BoardPreview
  createdAt: string
  updatedAt: string
}

export type BoardScene = {
  elements: unknown[]
  appState: Record<string, unknown>
  files: Record<string, unknown>
  [key: string]: unknown
}

export type Board = BoardSummary & { scene: BoardScene }
