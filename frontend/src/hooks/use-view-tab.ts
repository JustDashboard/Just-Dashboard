"use client"

import { useEffect, useState } from "react"
import { useSearchParams } from "next/navigation"
import { useViewState } from "@/lib/view-state"

/**
 * Which view of a page is on screen, kept in the address.
 *
 * The detail pages each wrote this for themselves — `useAddressTab` in the
 * accounts pages, a `?tab=` reader in services, containers and sites, a
 * `?view=` one in the database performance pages — and they disagreed about
 * the edges: what a link to a view that no longer exists opens on, whether the
 * first view writes its name into the address, whether Back steps through the
 * views. One hook answers them once, for `ViewTabs`.
 *
 * - The first view is the bare address. Choosing it takes the parameter out,
 *   so the page's plain link and its first view are one address.
 * - A view is replaced in the address, never pushed: Back leaves the page
 *   rather than walking the tabs you looked at.
 * - A value the page does not have (an old link, a typo) opens the first
 *   view rather than nothing.
 * - An address that changes under the page (a link to another view of the
 *   same thing) is followed.
 * - With `remember`, a bare address opens on the view last chosen on this
 *   screen, and a link that names a view is remembered in its turn. Without
 *   it the bare address is always the first view.
 */
export type ViewTabOptions = {
  /** The query parameter: `tab` for an item's pages, `view` for a workbench's views. */
  param?: "tab" | "view"
  /** A `useViewState` key (`databases.performance`) to reopen on the last view chosen. */
  remember?: string
}

type ViewKey<T extends string> = T | { key: T }

const keyOf = <T extends string>(view: ViewKey<T>): T =>
  typeof view === "string" ? view : view.key

/**
 * The view to show: what the address asked for when the page has it, then
 * what was remembered, then the first. Pure, so the order is held by a test.
 */
export function pickView<T extends string>(
  keys: readonly T[],
  requested: string | null | undefined,
  remembered?: string | null,
): T {
  const has = (key: string | null | undefined): key is T =>
    key != null && (keys as readonly string[]).includes(key)
  if (has(requested)) return requested
  if (has(remembered)) return remembered
  return keys[0]
}

/**
 * The query string with `param` set to `next`, or without it when `next` is
 * the first view. Every other parameter is kept as it was.
 */
export function viewSearch(search: string, param: string, next: string, first: string): string {
  const params = new URLSearchParams(search)
  if (next === first) params.delete(param)
  else params.set(param, next)
  const text = params.toString()
  return text ? `?${text}` : ""
}

export function useViewTab<T extends string>(
  views: readonly ViewKey<T>[],
  { param = "tab", remember }: ViewTabOptions = {},
): [T, (next: string) => void] {
  const keys = views.map(keyOf)
  const first = keys[0]
  const search = useSearchParams()
  const requested = search?.get(param) ?? null

  // The hook is called whether or not the page remembers; a page that does
  // not is given a key nothing writes to.
  const [remembered, setRemembered] = useViewState<string | null>(
    remember ? `view-tab.${remember}` : "view-tab.",
    null,
  )

  // The address is followed when it changes under the page; between changes
  // the last choice stands, so a click shows at once rather than after the
  // router has read the address back.
  const [chosen, setChosen] = useState<string | null>(requested)
  const [seen, setSeen] = useState(requested)
  if (requested !== seen) {
    setSeen(requested)
    setChosen(requested)
  }

  const view = pickView(keys, chosen, remember ? remembered : null)

  // A link that names a view is the view the next bare visit opens on.
  const arrived = pickView(keys, requested, null)
  useEffect(() => {
    if (remember && requested !== null && arrived === requested) setRemembered(requested)
  }, [remember, requested, arrived, setRemembered])

  const setView = (next: string) => {
    setChosen(next)
    if (remember) setRemembered(next)
    const url = new URL(window.location.href)
    const query = viewSearch(url.search, param, next, first)
    window.history.replaceState(null, "", `${url.pathname}${query}${url.hash}`)
  }

  return [view, setView]
}
