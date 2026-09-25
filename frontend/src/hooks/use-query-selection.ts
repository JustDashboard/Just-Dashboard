"use client"

import { useCallback, useEffect, useRef } from "react"
import { usePathname, useSearchParams } from "next/navigation"
import { useSessionState } from "@/lib/view-state"

/**
 * A detail panel's selection: in the address bar, so it is shareable and the
 * browser's back button closes it, and remembered for this tab, so leaving
 * the page and coming back finds the same row open.
 *
 * The rail's links are bare paths (`/docker/containers`, never a container),
 * which is what used to close every panel on the way out. Arriving with
 * nothing in the URL now puts the remembered selection back with
 * `replaceState` — no history entry, since nothing was navigated — and the
 * page opens as it was left. Closing the panel, or walking to the bare path
 * while it is open, forgets it: the URL going from a selection to none is
 * the reader's own act, whichever button did it.
 */
export function useQuerySelection(key: string) {
  const search = useSearchParams()
  const pathname = usePathname()
  const current = search.get(key) || null
  const [remembered, remember] = useSessionState<string | null>(
    `selection.${pathname}.${key}`,
    null,
  )

  const select = useCallback(
    (value: string | null) => {
      const url = new URL(window.location.href)
      if (value) url.searchParams.set(key, value)
      else url.searchParams.delete(key)
      if (url.href !== window.location.href) {
        window.history.pushState(null, "", `${url.pathname}${url.search}${url.hash}`)
      }
    },
    [key],
  )

  // Undefined until the first look, so a mount with nothing in the URL is
  // told apart from a selection that was just cleared.
  const previous = useRef<string | null | undefined>(undefined)
  const restored = useRef(false)
  useEffect(() => {
    const before = previous.current
    previous.current = current
    if (current) {
      if (remembered !== current) remember(current)
      return
    }
    if (before) {
      remember(null)
      return
    }
    if (remembered && !restored.current) {
      restored.current = true
      const url = new URL(window.location.href)
      url.searchParams.set(key, remembered)
      window.history.replaceState(null, "", `${url.pathname}${url.search}${url.hash}`)
    }
  }, [current, remembered, remember, key])

  return [current, select] as const
}

/** The workspace's tabs, as the address bar may name one. */
const WORKSPACE_TABS = new Set(["changes", "history", "branches", "github"])

/**
 * Opens a checkout's workspace on its GitHub tab — on one of its pull
 * requests, when a number is given — in one history entry.
 *
 * Not through `useQuerySelection`: that hook remembers its value per
 * pathname, and a pull request number remembered for `/git` would reopen on
 * the next checkout entered. And not as two `pushState`s, one for `repo` and
 * one for `pull`: the browser's back button would then leave the pull
 * request and stay in the workspace, which is not where the reader came from.
 */
export function openRepoPull(path: string, pull?: number) {
  const url = new URL(window.location.href)
  url.searchParams.set("repo", path)
  url.searchParams.delete("pull")
  url.searchParams.delete("tab")
  if (pull) url.searchParams.set("pull", String(pull))
  else url.searchParams.set("tab", "github")
  window.history.pushState(null, "", `${url.pathname}${url.search}${url.hash}`)
}

/**
 * Leaves the workspace. The checkout and whatever was opened inside it go
 * together: a pull request number left behind in the address would open the
 * next checkout entered on a pull request it does not have.
 */
export function closeRepo() {
  const url = new URL(window.location.href)
  for (const key of ["repo", "pull", "tab"]) url.searchParams.delete(key)
  window.history.pushState(null, "", `${url.pathname}${url.search}${url.hash}`)
}

/** The tab the address bar asks the workspace to open on, when it names one it has. */
export function workspaceTab(value: string | null) {
  return value && WORKSPACE_TABS.has(value)
    ? (value as "changes" | "history" | "branches" | "github")
    : undefined
}
