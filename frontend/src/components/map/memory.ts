"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { del, fetchResponse, get, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import { DEFAULT_DOCUMENT, decodeDocument, type MapDocument } from "@/lib/server-map"
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
 * Saving is debounced and reported, as the diagram's is: the toolbar says
 * "Saved", "Saving…" or that it could not, and a change still waiting when
 * the page goes is sent on the way out on a request the browser keeps alive.
 */
export type MemoryStatus = "loading" | "saved" | "saving" | "unsaved" | "failed"

const SAVE_DELAY = 900
const PATH = "/map/layout"

export function useMapMemory() {
  const [state, setState] = useState<{
    document: MapDocument | null
    status: MemoryStatus
    updatedAt?: string
  }>({ document: null, status: "loading" })
  const pending = useRef<MapDocument | null>(null)
  const flying = useRef<MapDocument | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const failed = useRef(false)

  useEffect(() => {
    let cancelled = false
    get<MapLayoutResponse>(PATH)
      .then((res) => {
        if (cancelled) return
        setState({
          document: decodeDocument(res?.layout),
          status: "saved",
          updatedAt: res?.updatedAt,
        })
      })
      .catch(() => {
        // A map that cannot read its arrangement still draws: laid out fresh,
        // and the first change tries to save again.
        if (!cancelled) setState({ document: DEFAULT_DOCUMENT, status: "failed" })
      })
    return () => {
      cancelled = true
    }
  }, [])

  const flush = useCallback(async () => {
    const doc = pending.current
    if (!doc) return
    pending.current = null
    flying.current = doc
    setState((s) => ({ ...s, status: "saving" }))
    try {
      const res = await put<MapLayoutResponse>(PATH, { layout: doc })
      flying.current = null
      failed.current = false
      setState((s) => ({
        ...s,
        status: pending.current ? "unsaved" : "saved",
        updatedAt: res.updatedAt,
      }))
    } catch (err) {
      flying.current = null
      setState((s) => ({ ...s, status: "failed" }))
      if (!failed.current) notify.error("The map's layout could not be saved", err)
      failed.current = true
    }
  }, [])

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
      if (!doc) return
      pending.current = null
      void fetchResponse(PATH, {
        method: "PUT",
        keepalive: true,
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ layout: doc }),
      }).catch(() => undefined)
    }
    window.addEventListener("pagehide", leave)
    return () => {
      window.removeEventListener("pagehide", leave)
      clearTimeout(timer.current)
      if (pending.current) void flush()
    }
  }, [flush])

  const reset = useCallback(async () => {
    clearTimeout(timer.current)
    pending.current = null
    setState({ document: DEFAULT_DOCUMENT, status: "saved" })
    try {
      await del(PATH)
    } catch (err) {
      setState((s) => ({ ...s, status: "failed" }))
      notify.error("Could not forget the saved layout", err)
    }
  }, [])

  return { document, status, updatedAt: state.updatedAt, update, reset }
}
