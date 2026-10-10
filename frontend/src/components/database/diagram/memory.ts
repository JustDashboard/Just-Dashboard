"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { API_BASE, del, get, mutationHeaders, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import { useViewState } from "@/lib/view-state"
import {
  DEFAULT_DOCUMENT,
  chooseDocument,
  decodeDocument,
  type DiagramDocument,
  type Mirror,
} from "@/components/database/diagram/document"
import type { DbDiagramLayoutResponse } from "@/components/database/diagram/types"

/**
 * Where the diagram's document is kept, and how it gets there.
 *
 * It is saved on the server beside the connection's saved queries, so an
 * arrangement comes back on the next visit, in the next browser and for the
 * next operator. And it is mirrored in this browser, so a role that may read
 * the schema but not write to the dashboard still keeps its own arrangement,
 * and a server that is briefly away does not lose a drag.
 *
 * The mirror is the page's furniture and is kept where the rest of it is
 * (`useViewState`, under the section's own key), with the moment of any change
 * the server has not confirmed. That moment is what decides, on the next
 * visit, whether the browser's copy or the server's is the newer.
 *
 * Saving is debounced and reported, because a diagram that silently forgets
 * is worse than one that never remembered: the toolbar says "Saved", "Saving…"
 * or "Kept in this browser", and a failed save says so once. A change still
 * waiting for its debounce when the page goes is sent on the way out, on a
 * request the browser keeps alive past the page.
 */
export type MemoryStatus = "loading" | "saved" | "saving" | "unsaved" | "failed" | "local"

type Held = DiagramDocument & { legacy?: true }

const SAVE_DELAY = 900

/** The key the diagram wrote straight to localStorage before it kept to the view store. */
const legacyKey = (connId: number, schema: string) => `jd.db.diagram.${connId}.${schema}`

function takeLegacy(key: string): Mirror | null {
  try {
    const raw = window.localStorage.getItem(key)
    if (!raw) return null
    window.localStorage.removeItem(key)
    return { doc: JSON.parse(raw) }
  } catch {
    return null
  }
}

export function useDiagramMemory({
  connId,
  schema,
  canSave,
}: {
  connId: number
  /** "" is the picture of every schema, which has an arrangement of its own. */
  schema: string
  /** Whether this role may write dashboard state. Without it the browser keeps the copy. */
  canSave: boolean
}) {
  const key = `${connId}:${schema}`
  const [mirror, setMirror] = useViewState<Mirror | null>(
    `databases.${connId}.diagram.${schema || "*"}`,
    null,
  )
  const mirrored = useRef(mirror)
  useEffect(() => {
    mirrored.current = mirror
  })

  // The state carries the key it was loaded for, so a switch of connection
  // or schema reads as "loading" until its own fetch lands rather than as the
  // previous diagram's arrangement — the same bargain `usePoll` makes.
  const [state, setState] = useState<{
    key: string
    document: Held | null
    status: MemoryStatus
    updatedAt?: string
  }>({ key, document: null, status: "loading" })
  const current =
    state.key === key ? state : { key, document: null, status: "loading" as MemoryStatus }
  const pending = useRef<DiagramDocument | null>(null)
  // The document a save now on its way is carrying: if the page goes before
  // the server answers, that request goes with it and this is what is owed.
  const flying = useRef<DiagramDocument | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const failed = useRef(false)

  useEffect(() => {
    let cancelled = false
    const local = mirrored.current ?? takeLegacy(legacyKey(connId, schema))
    get<DbDiagramLayoutResponse>(`/databases/${connId}/diagram`, { schema })
      .then((res) => {
        if (cancelled) return
        const chosen = chooseDocument(local, res ?? { layout: null })
        if (chosen.saved) {
          // Mirrored only where there is an arrangement to mirror: a visit
          // to a diagram nobody has arranged leaves nothing in the browser.
          // A document still keyed by bare names is mirrored once the canvas
          // has re-keyed it — written back as it is, it would read as one
          // that needs no re-keying.
          if (res?.layout && !chosen.document.legacy) setMirror({ doc: chosen.document })
          setState({ key, document: chosen.document, status: "saved", updatedAt: res?.updatedAt })
          return
        }
        // This browser's copy is the newer one, or the only one: it is owed
        // to the server and goes up now, where this role can save. One still
        // keyed by bare names goes up once the canvas has re-keyed it.
        if (canSave && !chosen.document.legacy) pending.current = chosen.document
        setState({ key, document: chosen.document, status: canSave ? "unsaved" : "local" })
      })
      .catch(() => {
        if (cancelled) return
        const mine = local ? decodeDocument(local.doc) : null
        setState({ key, document: mine ?? DEFAULT_DOCUMENT, status: "local" })
      })
    return () => {
      cancelled = true
    }
  }, [connId, schema, key, canSave, setMirror])

  const flush = useCallback(async () => {
    const doc = pending.current
    if (!doc || !canSave) return
    pending.current = null
    flying.current = doc
    setState((s) => (s.key === key ? { ...s, status: "saving" } : s))
    try {
      const res = await put<DbDiagramLayoutResponse>(
        `/databases/${connId}/diagram`,
        { layout: doc },
        { query: { schema } },
      )
      flying.current = null
      failed.current = false
      // A change that landed while this one was in flight is still pending
      // and keeps the status — and the mirror's unconfirmed mark — honest.
      if (!pending.current) setMirror({ doc })
      setState((s) =>
        s.key === key
          ? { ...s, status: pending.current ? "unsaved" : "saved", updatedAt: res.updatedAt }
          : s,
      )
    } catch (err) {
      flying.current = null
      setState((s) => (s.key === key ? { ...s, status: "failed" } : s))
      if (!failed.current) notify.error("The diagram's layout could not be saved", err)
      failed.current = true
    }
  }, [connId, schema, key, canSave, setMirror])

  const update = useCallback(
    (next: Partial<DiagramDocument> | ((doc: DiagramDocument) => DiagramDocument)) => {
      setState((s) => {
        const base: DiagramDocument = (s.key === key ? s.document : null) ?? DEFAULT_DOCUMENT
        const resolved: DiagramDocument =
          typeof next === "function" ? next(base) : { ...base, ...next }
        pending.current = canSave ? resolved : null
        return {
          key,
          document: resolved,
          status: canSave ? "unsaved" : "local",
          updatedAt: s.key === key ? s.updatedAt : undefined,
        }
      })
    },
    [key, canSave],
  )

  // The mirror and the debounce follow the document rather than being written
  // inside the state update: an updater may run twice, and a write to another
  // store from inside one would be made twice with it.
  const document = current.document
  const status = current.status
  useEffect(() => {
    if (!document || document.legacy || (status !== "unsaved" && status !== "local")) return
    setMirror({ doc: document, dirtyAt: new Date().toISOString() })
    if (!canSave || !pending.current) return
    clearTimeout(timer.current)
    timer.current = setTimeout(() => void flush(), SAVE_DELAY)
  }, [document, status, canSave, flush, setMirror])

  // A drag followed by a closed tab must not be the one change that is
  // forgotten. A request made as the page goes is cancelled with it unless
  // the browser is told to keep it alive, so the last save is sent that way.
  useEffect(() => {
    const leave = () => {
      clearTimeout(timer.current)
      const doc = pending.current ?? flying.current
      if (!doc || !canSave) return
      pending.current = null
      void fetch(`${API_BASE}/databases/${connId}/diagram?schema=${encodeURIComponent(schema)}`, {
        method: "PUT",
        keepalive: true,
        credentials: "include",
        headers: { ...mutationHeaders(), "Content-Type": "application/json" },
        body: JSON.stringify({ layout: doc }),
      }).catch(() => undefined)
    }
    window.addEventListener("pagehide", leave)
    return () => {
      window.removeEventListener("pagehide", leave)
      // Leaving the page inside the dashboard: the ordinary save, now.
      clearTimeout(timer.current)
      if (pending.current) void flush()
    }
  }, [connId, schema, canSave, flush])

  const reset = useCallback(async () => {
    clearTimeout(timer.current)
    pending.current = null
    setMirror(null)
    setState({ key, document: DEFAULT_DOCUMENT, status: canSave ? "saved" : "local" })
    if (!canSave) return
    try {
      await del(`/databases/${connId}/diagram`, { query: { schema } })
    } catch (err) {
      setState((s) => (s.key === key ? { ...s, status: "failed" } : s))
      notify.error("Could not reset the saved layout", err)
    }
  }, [connId, schema, key, canSave, setMirror])

  return {
    document: current.document,
    loading: current.document === null,
    status: current.status,
    updatedAt: current.updatedAt,
    update,
    reset,
  }
}
