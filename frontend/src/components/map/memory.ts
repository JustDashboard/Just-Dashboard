"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { fetchResponse, get, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import {
  DEFAULT_DOCUMENT,
  boardLayout,
  composeLayout,
  decodeDocument,
  type MapDocument,
} from "@/lib/server-map"
import type { MapLayoutResponse } from "@/lib/types"

/**
 * Where the map's arrangement is kept: on the server, against the account.
 *
 * The ER diagram's arrangement is the connection's and shared, because a
 * schema has one shape everybody learns. A server map is somebody's own
 * desk — one operator keeps Jobs folded and their three projects pulled out
 * beside the server, another wants every container — so each account keeps
 * its own, on every browser it signs in from, and no role is too small to
 * keep one: arranging your view of the server changes nothing on it.
 *
 * Saving is debounced and speaks only when it fails, with a toast; a change
 * still waiting when the page goes is sent on the way out on a request the
 * browser keeps alive.
 *
 * The databases map keeps its own arrangement in the same document, under
 * `boards.databases` (`composeLayout`): each canvas writes the whole document
 * with only its own part changed. So a page reads it only after the save the
 * page before it sent on its way out has landed, or it would put back what
 * that save had just moved.
 */
type MemoryStatus = "loading" | "saved" | "saving" | "unsaved" | "failed"

const SAVE_DELAY = 900
const PATH = "/map/layout"

/** The save on its way, which the next canvas to open reads after. */
let saving: Promise<unknown> = Promise.resolve()

/** `board` names a canvas other than the server map's: `"databases"`. */
export function useMapMemory(board?: string) {
  const [state, setState] = useState<{
    document: MapDocument | null
    status: MemoryStatus
  }>({ document: null, status: "loading" })
  const pending = useRef<MapDocument | null>(null)
  const flying = useRef<MapDocument | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const failed = useRef(false)
  // The whole document as last read or written, which a save changes only
  // this board's part of; `undefined` until it has been read.
  const whole = useRef<unknown>(undefined)

  useEffect(() => {
    let cancelled = false
    saving
      .then(
        () => undefined,
        () => undefined,
      )
      .then(() => get<MapLayoutResponse>(PATH))
      .then((res) => {
        if (cancelled) return
        whole.current = res?.layout ?? null
        setState({ document: decodeDocument(boardLayout(whole.current, board)), status: "saved" })
      })
      .catch(() => {
        // A map that cannot read its arrangement still draws: laid out fresh,
        // and the first change tries to save again.
        if (!cancelled) setState({ document: DEFAULT_DOCUMENT, status: "failed" })
      })
    return () => {
      cancelled = true
    }
  }, [board])

  const flush = useCallback(async () => {
    const doc = pending.current
    if (!doc) return
    pending.current = null
    flying.current = doc
    setState((s) => ({ ...s, status: "saving" }))
    try {
      // Never read — the first read failed: read it now, so the other
      // canvases' arrangements are kept rather than written over.
      if (whole.current === undefined) {
        whole.current = (await get<MapLayoutResponse>(PATH))?.layout ?? null
      }
      const layout = composeLayout(whole.current, board, doc)
      const sent = put<MapLayoutResponse>(PATH, { layout })
      saving = sent
      await sent
      whole.current = layout
      flying.current = null
      failed.current = false
      setState((s) => ({ ...s, status: pending.current ? "unsaved" : "saved" }))
    } catch (err) {
      flying.current = null
      setState((s) => ({ ...s, status: "failed" }))
      if (!failed.current) notify.error("The map's layout could not be saved", err)
      failed.current = true
    }
  }, [board])

  const update = useCallback((next: Partial<MapDocument> | ((doc: MapDocument) => MapDocument)) => {
    setState((s) => {
      const base = s.document ?? DEFAULT_DOCUMENT
      const resolved = typeof next === "function" ? next(base) : { ...base, ...next }
      pending.current = resolved
      return { ...s, document: resolved, status: "unsaved" }
    })
  }, [])

  // The debounce follows the document rather than being started inside the
  // state update: an updater may run twice.
  const document = state.document
  const status = state.status
  useEffect(() => {
    if (!document || status !== "unsaved" || !pending.current) return
    clearTimeout(timer.current)
    timer.current = setTimeout(() => void flush(), SAVE_DELAY)
  }, [document, status, flush])

  useEffect(() => {
    const leave = () => {
      clearTimeout(timer.current)
      const doc = pending.current ?? flying.current
      // Unread, the rest of the document is not known, and this would erase it.
      if (!doc || whole.current === undefined) return
      pending.current = null
      void fetchResponse(PATH, {
        method: "PUT",
        keepalive: true,
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ layout: composeLayout(whole.current, board, doc) }),
      }).catch(() => undefined)
    }
    window.addEventListener("pagehide", leave)
    return () => {
      window.removeEventListener("pagehide", leave)
      clearTimeout(timer.current)
      if (pending.current) void flush()
    }
  }, [flush, board])

  return { document, update }
}
