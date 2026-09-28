export type BoardSummary = {
  id: number
  name: string
  revision: number
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
