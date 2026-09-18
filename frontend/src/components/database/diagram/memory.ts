"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { del, get, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { DbDiagramLayoutResponse } from "@/lib/types"

/**
 * What the diagram remembers, and where.
 *
 * Every decision the operator makes about the picture — where a table sits,
 * which are hidden, a note, a colour, how much of each table is drawn — is one
 * document, kept per connection and schema. It is saved on the server beside
 * the connection's saved queries, so it comes back on the next visit, in the
 * next browser and for the next operator; and it is mirrored in this browser's
 * storage, so a role that may read the schema but not write to the dashboard
 * still keeps its own arrangement, and a server that is briefly away does not
 * lose a drag.
 *
 * Saving is debounced and reported, because a diagram that silently forgets
 * is worse than one that never remembered: the toolbar says "Saved", "Saving…"
 * or "Kept in this browser", and a failed save says so once.
 */

export type DiagramDirection = "LR" | "TB"
/** How much of each table is drawn: every column, only the keys, or the name alone. */
export type DiagramDetail = "all" | "keys" | "names"
export type DiagramSpacing = "compact" | "comfortable"
export const DIAGRAM_COLORS = [
  "slate",
  "red",
  "amber",
  "green",
  "cyan",
  "blue",
  "violet",
  "pink",
] as const
export type DiagramColor = (typeof DIAGRAM_COLORS)[number]

export type DiagramDocument = {
  version: 1
  direction: DiagramDirection
  detail: DiagramDetail
  spacing: DiagramSpacing
  /** Only the tables the operator placed (or a Tidy placed for them). */
  positions: Record<string, { x: number; y: number }>
  hidden: string[]
  notes: Record<string, string>
  colors: Record<string, DiagramColor>
  viewport?: { x: number; y: number; zoom: number }
  grid: boolean
  snap: boolean
  minimap: boolean
  /** Cardinality labels on every edge, rather than only on the focused ones. */
  labels: boolean
  /** Tables cannot be dragged — for a diagram that is finished. */
  locked: boolean
}

export const DEFAULT_DOCUMENT: DiagramDocument = {
  version: 1,
  direction: "LR",
  detail: "all",
  spacing: "comfortable",
  positions: {},
  hidden: [],
  notes: {},
  colors: {},
  grid: true,
  snap: false,
  minimap: true,
  labels: false,
  locked: false,
}

const isRecord = (v: unknown): v is Record<string, unknown> =>
  typeof v === "object" && v !== null && !Array.isArray(v)
const finite = (v: unknown): v is number => typeof v === "number" && Number.isFinite(v)

/**
 * A stored document, checked field by field and filled in with the defaults.
 * Keys outlive the code that wrote them, and a value of the wrong shape would
 * otherwise reach the canvas as if it were fine — a position of `null` is a
 * table that never renders.
 */
export function decodeDocument(raw: unknown): DiagramDocument | null {
  if (!isRecord(raw)) return null
  const doc: DiagramDocument = {
    ...DEFAULT_DOCUMENT,
    positions: {},
    hidden: [],
    notes: {},
    colors: {},
  }
  if (raw.direction === "LR" || raw.direction === "TB") doc.direction = raw.direction
  if (raw.detail === "all" || raw.detail === "keys" || raw.detail === "names")
    doc.detail = raw.detail
  if (raw.spacing === "compact" || raw.spacing === "comfortable") doc.spacing = raw.spacing
  if (isRecord(raw.positions)) {
    for (const [name, p] of Object.entries(raw.positions)) {
      if (isRecord(p) && finite(p.x) && finite(p.y)) doc.positions[name] = { x: p.x, y: p.y }
    }
  }
  if (Array.isArray(raw.hidden))
    doc.hidden = raw.hidden.filter((h): h is string => typeof h === "string")
  if (isRecord(raw.notes)) {
    for (const [name, n] of Object.entries(raw.notes))
      if (typeof n === "string" && n) doc.notes[name] = n
  }
  if (isRecord(raw.colors)) {
    for (const [name, c] of Object.entries(raw.colors)) {
      if (typeof c === "string" && (DIAGRAM_COLORS as readonly string[]).includes(c)) {
        doc.colors[name] = c as DiagramColor
      }
    }
  }
  if (
    isRecord(raw.viewport) &&
    finite(raw.viewport.x) &&
    finite(raw.viewport.y) &&
    finite(raw.viewport.zoom)
  ) {
    doc.viewport = { x: raw.viewport.x, y: raw.viewport.y, zoom: raw.viewport.zoom }
  }
  for (const flag of ["grid", "snap", "minimap", "labels", "locked"] as const) {
    if (typeof raw[flag] === "boolean") doc[flag] = raw[flag]
  }
  return doc
}

/** Whether the operator has changed anything from a fresh diagram. */
export function isArranged(doc: DiagramDocument): boolean {
  return (
    Object.keys(doc.positions).length > 0 ||
    doc.hidden.length > 0 ||
    Object.keys(doc.notes).length > 0 ||
    Object.keys(doc.colors).length > 0 ||
    doc.direction !== DEFAULT_DOCUMENT.direction ||
    doc.detail !== DEFAULT_DOCUMENT.detail ||
    doc.spacing !== DEFAULT_DOCUMENT.spacing ||
    doc.locked
  )
}

export type MemoryStatus = "loading" | "saved" | "saving" | "unsaved" | "failed" | "local"

const localKey = (connId: number, schema: string) => `jd.db.diagram.${connId}.${schema}`

function readLocal(key: string): DiagramDocument | null {
  try {
    const raw = window.localStorage.getItem(key)
    return raw ? decodeDocument(JSON.parse(raw)) : null
  } catch {
    return null
  }
}

function writeLocal(key: string, doc: DiagramDocument | null) {
  try {
    if (doc) window.localStorage.setItem(key, JSON.stringify(doc))
    else window.localStorage.removeItem(key)
  } catch {
    // Private browsing or a full quota: the server copy still holds it.
  }
}

const SAVE_DELAY = 900

export function useDiagramMemory({
  connId,
  schema,
  canSave,
}: {
  connId: number
  schema: string
  /** Whether this role may write dashboard state. Without it the browser keeps the copy. */
  canSave: boolean
}) {
  const key = localKey(connId, schema)
  // The state carries the key it was loaded for, so a switch of connection
  // or schema reads as "loading" until its own fetch lands rather than as the
  // previous diagram's arrangement — the same bargain `usePoll` makes.
  const [state, setState] = useState<{
    key: string
    document: DiagramDocument | null
    status: MemoryStatus
    updatedAt?: string
  }>({ key, document: null, status: "loading" })
  const current =
    state.key === key ? state : { key, document: null, status: "loading" as MemoryStatus }
  const pending = useRef<DiagramDocument | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const failed = useRef(false)

  useEffect(() => {
    let cancelled = false
    const local = readLocal(key)
    get<DbDiagramLayoutResponse>(`/databases/${connId}/diagram`, { schema })
      .then((res) => {
        if (cancelled) return
        const remote = res.layout ? decodeDocument(res.layout) : null
        if (remote) {
          writeLocal(key, remote)
          setState({ key, document: remote, status: "saved", updatedAt: res.updatedAt })
        } else if (local) {
          // Arranged in this browser before the server kept layouts, or by a
          // role that could not save: adopt it, and push it up on the next
          // change if this role can.
          setState({ key, document: local, status: canSave ? "unsaved" : "local" })
        } else {
          setState({ key, document: DEFAULT_DOCUMENT, status: canSave ? "saved" : "local" })
        }
      })
      .catch(() => {
        if (cancelled) return
        setState({ key, document: local ?? DEFAULT_DOCUMENT, status: "local" })
      })
    return () => {
      cancelled = true
    }
  }, [connId, schema, key, canSave])

  const flush = useCallback(async () => {
    const doc = pending.current
    if (!doc || !canSave) return
    pending.current = null
    setState((s) => (s.key === key ? { ...s, status: "saving" } : s))
    try {
      const res = await put<DbDiagramLayoutResponse>(
        `/databases/${connId}/diagram`,
        { layout: doc },
        { query: { schema } },
      )
      failed.current = false
      // A change that landed while this one was in flight is still pending
      // and keeps the status honest.
      setState((s) =>
        s.key === key
          ? { ...s, status: pending.current ? "unsaved" : "saved", updatedAt: res.updatedAt }
          : s,
      )
    } catch (err) {
      setState((s) => (s.key === key ? { ...s, status: "failed" } : s))
      if (!failed.current) notify.error("The diagram's layout could not be saved", err)
      failed.current = true
    }
  }, [connId, schema, key, canSave])

  const update = useCallback(
    (next: Partial<DiagramDocument> | ((doc: DiagramDocument) => DiagramDocument)) => {
      setState((s) => {
        const base = (s.key === key ? s.document : null) ?? DEFAULT_DOCUMENT
        const resolved = typeof next === "function" ? next(base) : { ...base, ...next }
        writeLocal(key, resolved)
        if (canSave) {
          pending.current = resolved
          clearTimeout(timer.current)
          timer.current = setTimeout(() => void flush(), SAVE_DELAY)
        }
        return {
          key,
          document: resolved,
          status: canSave ? "unsaved" : "local",
          updatedAt: s.key === key ? s.updatedAt : undefined,
        }
      })
    },
    [key, canSave, flush],
  )

  // A drag followed by a click on another tab must not be the one change that
  // is forgotten: whatever is waiting for its debounce goes now.
  useEffect(() => {
    const now = () => {
      clearTimeout(timer.current)
      if (pending.current) void flush()
    }
    window.addEventListener("beforeunload", now)
    return () => {
      window.removeEventListener("beforeunload", now)
      now()
    }
  }, [flush])

  const reset = useCallback(async () => {
    clearTimeout(timer.current)
    pending.current = null
    writeLocal(key, null)
    setState({ key, document: DEFAULT_DOCUMENT, status: canSave ? "saved" : "local" })
    if (!canSave) return
    try {
      await del(`/databases/${connId}/diagram`, { query: { schema } })
    } catch (err) {
      setState((s) => (s.key === key ? { ...s, status: "failed" } : s))
      notify.error("Could not reset the saved layout", err)
    }
  }, [connId, schema, key, canSave])

  return {
    document: current.document,
    loading: current.document === null,
    status: current.status,
    updatedAt: current.updatedAt,
    update,
    reset,
  }
}
