"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { ApiError, put } from "@/lib/api"
import type { StickyBoard, StickyDocument, StickySummary } from "@/lib/stickyboard"
import { notify } from "@/lib/toast"

export type SaveState = "saved" | "pending" | "saving" | "error" | "conflict" | "missing"

/** How a change enters the history. */
export type ChangeMode =
  /** Its own step: a press of a colour, a delete, a paste. */
  | "step"
  /** Part of a gesture that took a `checkpoint` when it began: a drag, a resize, typing. */
  | "live"
  /** Joins the step before it when that step had the same key a moment ago: a run of arrow nudges. */
  | { merge: string }

const HISTORY = 200
const MERGE_WINDOW = 800
const SAVE_DELAY = 700

/**
 * One board's document, its undo history, and the save that keeps the server
 * up to date with it.
 *
 * Every change makes a new document object and history holds the objects
 * themselves, so "is there anything to save" is a comparison with the object
 * last saved: undoing back to it leaves nothing to save, and the board never
 * writes a revision that changes nothing. A save carries the revision it read
 * and is refused if another tab saved first — then saving stops and the board
 * says so, as Boards does, rather than silently overwriting the other tab.
 */
export function useBoardDocument(board: StickyBoard, canWrite: boolean) {
  const [doc, setDoc] = useState(board.document)
  const [saveState, setSaveState] = useState<SaveState>("saved")
  const [history, setHistory] = useState({ undo: 0, redo: 0 })
  const docRef = useRef(board.document)
  const savedRef = useRef(board.document)
  const revisionRef = useRef(board.revision)
  const past = useRef<StickyDocument[]>([])
  const future = useRef<StickyDocument[]>([])
  const lastMerge = useRef<{ key: string; at: number } | null>(null)
  const stoppedRef = useRef<"conflict" | "missing" | null>(null)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const savingRef = useRef<Promise<boolean> | null>(null)
  const errorToast = useRef<string | number | undefined>(undefined)
  // The save and its scheduling call each other; the save reaches the
  // scheduler through this so neither has to be declared first.
  const scheduleRef = useRef<() => void>(() => {})

  const syncHistory = useCallback(
    () => setHistory({ undo: past.current.length, redo: future.current.length }),
    [],
  )

  const isDirty = useCallback(() => docRef.current !== savedRef.current, [])

  const flush = useCallback(async (): Promise<boolean> => {
    if (timerRef.current) clearTimeout(timerRef.current)
    // A second caller waits out the save in flight and whatever is queued
    // behind it, rather than reporting success while the latest change is unsent.
    while (savingRef.current) await savingRef.current
    if (stoppedRef.current || !canWrite) return false
    if (!isDirty()) return true
    const snapshot = docRef.current
    setSaveState("saving")
    const task = put<StickySummary>(`/stickyboards/${board.id}`, {
      document: snapshot,
      revision: revisionRef.current,
    })
      .then((saved) => {
        revisionRef.current = saved.revision
        savedRef.current = snapshot
        if (errorToast.current !== undefined) notify.dismiss(errorToast.current)
        errorToast.current = undefined
        setSaveState(isDirty() ? "pending" : "saved")
        return true
      })
      .catch((cause) => {
        if (cause instanceof ApiError && cause.code === "stickyboard_conflict") {
          stoppedRef.current = "conflict"
          setSaveState("conflict")
        } else if (cause instanceof ApiError && cause.code === "stickyboard_not_found") {
          stoppedRef.current = "missing"
          setSaveState("missing")
        } else {
          setSaveState("error")
          if (errorToast.current !== undefined) notify.dismiss(errorToast.current)
          errorToast.current = notify.error("Board was not saved", cause)
        }
        return false
      })
    savingRef.current = task
    const saved = await task
    if (savingRef.current === task) savingRef.current = null
    // Changes made while this save was out go in the next one.
    if (saved && isDirty()) scheduleRef.current()
    return saved
  }, [board.id, canWrite, isDirty])

  const schedule = useCallback(() => {
    if (timerRef.current) clearTimeout(timerRef.current)
    if (stoppedRef.current || !canWrite) return
    if (!isDirty()) {
      if (!savingRef.current) setSaveState("saved")
      return
    }
    setSaveState((state) => (state === "saving" ? state : "pending"))
    timerRef.current = setTimeout(() => void flush(), SAVE_DELAY)
  }, [canWrite, flush, isDirty])
  useEffect(() => {
    scheduleRef.current = schedule
  }, [schedule])

  const write = useCallback(
    (next: StickyDocument) => {
      docRef.current = next
      setDoc(next)
      schedule()
    },
    [schedule],
  )

  const remember = useCallback((state: StickyDocument) => {
    past.current.push(state)
    if (past.current.length > HISTORY) past.current.shift()
    future.current = []
  }, [])

  const change = useCallback(
    (update: (doc: StickyDocument) => StickyDocument, mode: ChangeMode = "step") => {
      if (!canWrite) return
      const current = docRef.current
      const next = update(current)
      if (next === current) return
      if (mode === "step") {
        remember(current)
        lastMerge.current = null
      } else if (typeof mode === "object") {
        const now = Date.now()
        const last = lastMerge.current
        if (!last || last.key !== mode.merge || now - last.at > MERGE_WINDOW) remember(current)
        lastMerge.current = { key: mode.merge, at: now }
      }
      write(next)
      if (mode !== "live") syncHistory()
    },
    [canWrite, remember, syncHistory, write],
  )

  const checkpoint = useCallback(() => {
    remember(docRef.current)
    lastMerge.current = null
    syncHistory()
  }, [remember, syncHistory])

  const settle = useCallback(() => {
    if (past.current[past.current.length - 1] === docRef.current) past.current.pop()
    syncHistory()
  }, [syncHistory])

  const undo = useCallback(() => {
    const previous = past.current.pop()
    if (!previous || !canWrite) return
    future.current.push(docRef.current)
    lastMerge.current = null
    write(previous)
    syncHistory()
  }, [canWrite, syncHistory, write])

  const redo = useCallback(() => {
    const next = future.current.pop()
    if (!next || !canWrite) return
    past.current.push(docRef.current)
    write(next)
    syncHistory()
  }, [canWrite, syncHistory, write])

  useEffect(() => {
    const warn = (event: BeforeUnloadEvent) => {
      if (stoppedRef.current || (!isDirty() && !savingRef.current)) return
      event.preventDefault()
    }
    window.addEventListener("beforeunload", warn)
    return () => {
      window.removeEventListener("beforeunload", warn)
      if (timerRef.current) clearTimeout(timerRef.current)
      if (isDirty()) void flush()
    }
  }, [flush, isDirty])

  return {
    doc,
    docRef,
    saveState,
    canUndo: history.undo > 0,
    canRedo: history.redo > 0,
    change,
    checkpoint,
    settle,
    undo,
    redo,
    flush,
    isDirty,
  }
}
