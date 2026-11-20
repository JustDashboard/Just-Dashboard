"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { get } from "@/lib/api"
import type { FileEntry, FileListing } from "@/lib/types"
import {
  applyPatches,
  foldChange,
  patchesAfter,
  type FolderChange,
  type HeldPatches,
} from "./live-patches"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import type { PollState } from "@/hooks/use-poll"

/** Whether the tab is in front: a hidden tab follows nothing. */
export function usePageVisible() {
  const [visible, setVisible] = useState(
    () => typeof document === "undefined" || document.visibilityState !== "hidden",
  )
  useEffect(() => {
    const update = () => setVisible(document.visibilityState !== "hidden")
    document.addEventListener("visibilitychange", update)
    return () => document.removeEventListener("visibilitychange", update)
  }, [])
  return visible
}

/** How a folder is being followed: by the kernel, by a cheap polled check, or not yet. */
export type FollowMode = "watch" | "check" | "off"

const CHECK_MS = 2_000

/**
 * The folder on screen, following the disk.
 *
 * The server watches the folder (`GET /files/watch`, inotify on the server)
 * and sends the rows whose names changed, a few times a second at most; they
 * are laid over the last listing, so nothing else on the page moves. Where the
 * folder cannot be watched the socket says `fallback`, and the page asks
 * `GET /files/changes` every two seconds for a fingerprint, reading the
 * listing again only when it differs. A hidden tab follows nothing, and the
 * socket closes with the page, which releases the server's watch.
 *
 * Called before the listing's poll, which it tells how often to ask: never
 * again on a timer while the folder is followed.
 */
export function useFolderFollow(path: string | null, hidden: boolean) {
  const visible = usePageVisible()
  const key = `${path}\u0000${hidden}`
  const [fallback, setFallback] = useState<string | null>(null)
  const [state, setState] = useState<FollowState>({ key, patches: new Map(), resync: 0, seq: 0 })
  const current: FollowState =
    state.key === key ? state : { key, patches: new Map(), resync: 0, seq: 0 }
  const opened = useRef<string | null>(null)
  const [watching, setWatching] = useState<string | null>(null)

  const resync = useCallback(
    () =>
      setState((previous) => {
        const base =
          previous.key === key ? previous : { key, patches: new Map(), resync: 0, seq: 0 }
        return { ...base, resync: base.resync + 1 }
      }),
    [key],
  )

  const socket = useSocket("/files/watch", {
    enabled: path !== null && visible && fallback !== key,
    query: { path: path ?? "", hidden },
    onMessage: (envelope: Envelope) => {
      switch (envelope.type) {
        case "watching":
          setWatching(key)
          // A socket that opens again missed what happened while it was
          // down; the first one for a folder opens with its listing.
          if (opened.current === key) resync()
          opened.current = key
          break
        case "changes": {
          const change = envelope.data as FolderChange
          setState((previous) => {
            const base =
              previous.key === key ? previous : { key, patches: new Map(), resync: 0, seq: 0 }
            const seq = base.seq + 1
            return { ...base, seq, patches: foldChange(base.patches, change, seq) }
          })
          break
        }
        case "resync":
        case "gone":
          resync()
          break
        case "fallback":
          setFallback(key)
          break
      }
    },
    onClose: () => setWatching(null),
  })

  // The fallback: a fingerprint, asked while the tab is in front.
  const checking = path !== null && visible && fallback === key
  const lastPrint = useRef<{ key: string; print: string } | null>(null)
  useEffect(() => {
    if (!checking || path === null) return
    let stopped = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const controller = new AbortController()
    const check = async () => {
      try {
        const answer = await get<{ fingerprint: string }>(
          "/files/changes",
          { path, hidden },
          controller.signal,
        )
        if (stopped) return
        const before = lastPrint.current
        lastPrint.current = { key, print: answer.fingerprint }
        if (before?.key === key && before.print !== answer.fingerprint) resync()
      } catch {
        // A failed check is asked again; the listing's own error says why.
      }
      if (!stopped) timer = setTimeout(check, CHECK_MS)
    }
    void check()
    return () => {
      stopped = true
      controller.abort()
      if (timer) clearTimeout(timer)
    }
  }, [checking, path, hidden, key, resync])

  const mode: FollowMode =
    socket.state === "open" && watching === key ? "watch" : checking ? "check" : "off"
  return { mode, patches: current.patches, resync: current.resync, seq: current.seq, key }
}

type FollowState = { key: string; patches: HeldPatches; resync: number; seq: number }

/**
 * The listing with the followed changes over it. A resync reads the listing
 * again; the patches it replaces are dropped once that read has landed.
 */
export function useFollowedListing(
  polled: PollState<FileListing>,
  follow: ReturnType<typeof useFolderFollow>,
): PollState<FileListing> {
  const { refresh } = polled
  const asked = useRef({ key: follow.key, resync: 0, at: 0, seq: 0 })
  useEffect(() => {
    if (follow.resync === 0) return
    if (asked.current.key === follow.key && asked.current.resync === follow.resync) return
    asked.current = { key: follow.key, resync: follow.resync, at: Date.now(), seq: follow.seq }
    refresh()
  }, [follow.key, follow.resync, follow.seq, refresh])

  // Patches that arrived before a resync was asked for are in the listing
  // that answered it, once that listing has landed.
  const [floor, setFloor] = useState({ key: follow.key, seq: 0 })
  const lastSuccess = polled.lastSuccess ?? 0
  useEffect(() => {
    const ask = asked.current
    if (ask.at === 0 || lastSuccess < ask.at || ask.key !== follow.key) return
    asked.current = { ...ask, at: 0 }
    setFloor({ key: follow.key, seq: ask.seq })
  }, [lastSuccess, follow.key])

  const data = polled.data
  const held = follow.patches
  const below = floor.key === follow.key ? floor.seq : 0
  const merged = useMemo(
    () => (data ? applyPatches(data, patchesAfter(held, below)) : data),
    [data, held, below],
  )
  return useMemo(() => ({ ...polled, data: merged }), [polled, merged])
}

/**
 * One file followed for an editor: `onChange` runs when it changes on disk,
 * at most a few times a second. Where the folder cannot be watched, its row
 * is read every few seconds and compared instead.
 */
export function useFileFollow(path: string | null, onChange: () => void) {
  const visible = usePageVisible()
  const dir = path ? path.slice(0, Math.max(path.lastIndexOf("/"), 1)) : null
  const name = path ? path.slice(path.lastIndexOf("/") + 1) : ""
  const [fallback, setFallback] = useState<string | null>(null)
  const changed = useRef(onChange)
  useEffect(() => {
    changed.current = onChange
  })
  useSocket("/files/watch", {
    enabled: path !== null && visible && fallback !== path,
    query: { path: dir ?? "", hidden: true, name },
    onMessage: (envelope: Envelope) => {
      if (envelope.type === "changes" || envelope.type === "resync" || envelope.type === "gone")
        changed.current()
      else if (envelope.type === "fallback") setFallback(path)
    },
  })
  const checking = path !== null && visible && fallback === path
  useEffect(() => {
    if (!checking || path === null) return
    let stopped = false
    let last: string | null = null
    let timer: ReturnType<typeof setTimeout> | undefined
    const check = async () => {
      try {
        const entry = await get<FileEntry>("/files/stat", { path })
        const print = `${entry.size}:${entry.modified}`
        if (last !== null && print !== last && !stopped) changed.current()
        last = print
      } catch {
        // Gone or unreadable: the next save says so in its own words.
      }
      if (!stopped) timer = setTimeout(check, CHECK_MS * 2)
    }
    void check()
    return () => {
      stopped = true
      if (timer) clearTimeout(timer)
    }
  }, [checking, path])
}
