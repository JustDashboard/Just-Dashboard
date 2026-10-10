"use client"

import { useCallback, useEffect, useLayoutEffect, useSyncExternalStore } from "react"
import { useSearchParams } from "next/navigation"
import { useSessionState } from "@/lib/view-state"
import { cleanPath } from "./media"
import { folderHref, preserveFolderMarker, visitFolder, type FolderHistory } from "./navigation"

const subscribe = () => () => {}
const client = () => true
const server = () => false
const subscribePosition = (listener: () => void) => {
  window.addEventListener("popstate", listener)
  return () => window.removeEventListener("popstate", listener)
}
const readPosition = () => {
  const marker = window.history.state?.jdFiles
  return marker ? `${marker.id}:${marker.index}` : ""
}
const noPosition = () => ""

export function useFolderNavigation(home?: string) {
  useLayoutEffect(() => {
    const original = window.history.replaceState
    let active = true
    const replace: History["replaceState"] = (state, unused, url) => {
      // Next can rewrite the current entry after a native traversal and drop
      // Files' position. Retain only our marker, not an older router state.
      const sameEntry =
        active &&
        window.location.pathname === "/files" &&
        new URL(url ?? window.location.href, window.location.href).href === window.location.href
      original.call(
        window.history,
        preserveFolderMarker(state, window.history.state?.jdFiles, sameEntry),
        unused,
        url,
      )
    }
    window.history.replaceState = replace
    return () => {
      // A later router wrapper may have captured ours; it must become inert too.
      active = false
      if (window.history.replaceState === replace) window.history.replaceState = original
    }
  }, [])

  const hydrated = useSyncExternalStore(subscribe, client, server)
  const urlPath = useSearchParams().get("path")
  const [remembered, setRemembered] = useSessionState<string | null>("files.path", null)
  const [trail, setTrail] = useSessionState<FolderHistory | null>("files.history", null)
  const position = useSyncExternalStore(subscribePosition, readPosition, noPosition)
  const [id, cursor] = position.split(":")
  const index = id === trail?.id ? Number(cursor) : 0
  const path = urlPath ? cleanPath(urlPath) : (remembered ?? home ?? null)

  useEffect(() => {
    if (!hydrated || !path || window.location.pathname !== "/files") return
    const addressPath = new URL(window.location.href).searchParams.get("path")
    // Native traversal updates the address before Next commits its render.
    // A synchronous selection snapshot must not rewrite that destination.
    if (addressPath && cleanPath(addressPath) !== path) return
    const marker = window.history.state?.jdFiles as { id: string; index: number } | undefined
    if (marker && trail && marker.id === trail.id && trail.paths[marker.index] === path) {
      if (trail.index !== marker.index) setTrail({ ...trail, index: marker.index })
    } else {
      const next = { id: crypto.randomUUID(), paths: [path], index: 0 }
      setTrail(next)
      // Pass only our state: Next copies its router state and synchronizes
      // useSearchParams. Passing its internal __NA flag skips that integration.
      const url = new URL(window.location.href)
      url.searchParams.set("path", path)
      window.history.replaceState({ jdFiles: { id: next.id, index: 0 } }, "", url)
    }
    if (remembered !== path) setRemembered(path)
  }, [hydrated, path, remembered, trail, setRemembered, setTrail])

  const navigate = useCallback(
    (destination: string) => {
      const nextPath = cleanPath(destination)
      if (!path || nextPath === path) return
      const current = trail
        ? { ...trail, index }
        : { id: crypto.randomUUID(), paths: [path], index: 0 }
      const next = visitFolder(current, nextPath)
      window.history.pushState(
        { jdFiles: { id: next.id, index: next.index } },
        "",
        folderHref(nextPath, window.location.href),
      )
      setTrail(next)
      setRemembered(nextPath)
    },
    [path, trail, index, setTrail, setRemembered],
  )

  const canBack = !!trail && index > 0
  const canForward = !!trail && index < trail.paths.length - 1
  const back = useCallback(() => {
    if (canBack) window.history.back()
  }, [canBack])
  const forward = useCallback(() => {
    if (canForward) window.history.forward()
  }, [canForward])

  return { path, navigate, back, forward, canBack, canForward }
}
