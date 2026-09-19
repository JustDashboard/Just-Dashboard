"use client"

import { useCallback, useEffect, useState, useSyncExternalStore } from "react"

/**
 * What a page remembers about itself, kept in the browser.
 *
 * Every page in this app is unmounted the moment you navigate away from it, so
 * anything held in `useState` is gone by the time you come back: the file
 * panel you closed on the terminal page is open again, the folder you
 * collapsed is expanded, the tab you were on is the default one, the filter
 * you typed is blank and the project you were half-way through setting up is
 * a blank form. That is indistinguishable from the dashboard ignoring you,
 * and it is worst on exactly the screens somebody leaves and returns to all
 * day.
 *
 * Three stores, drawn by how long the thing should live:
 *
 * - `useViewState` is **how the page is arranged**: a hidden panel, a
 *   collapsed group, a chosen tab, a sort order, a "show system accounts"
 *   switch. Decisions about the furniture, kept in localStorage so a reload
 *   and a restart of the backend under the tab keep them too.
 * - `useSessionState` is **what you were doing**: the filter in the box, the
 *   chip you narrowed to, the page of results you were on, the row whose
 *   detail is open, the form you had half filled in. Kept in sessionStorage,
 *   so it survives moving between pages and an accidental reload, and is
 *   gone when the tab is closed — a tab opened next week starts with nothing
 *   hidden by a filter typed last Tuesday, which is the failure the old
 *   "never remember a search box" rule existed to avoid.
 * - `useMemoryState` is the same thing for a value that must never be written
 *   to disk by the browser: a secret typed into a form before it is saved. It
 *   lives as long as the page's JavaScript does — across navigation, not
 *   across a reload — and nothing about it reaches Web Storage or the URL.
 *
 * On the screen and not on the account, for the same reason the theme and the
 * terminal's font are: whether the file tree is worth a fifth of the window is
 * a property of the window.
 *
 * Sibling of `panel-size.ts`, which does the same for a dragged width and
 * stays separate: a width is a number with its own clamping rules and its own
 * "reset to normal", and folding it in here would make both stores worse.
 */

type Listener = () => void

type Store = {
  read: (key: string) => unknown
  write: (key: string, value: unknown) => void
  forget: (prefix: string) => void
  subscribe: (listener: Listener) => () => void
}

/**
 * One JSON document under one key, read once and then kept in memory.
 *
 * `area` is called rather than captured: the server has no Web Storage, a
 * private window may refuse it, and asking each time is what lets the same
 * code serve the memory-only store by answering `null`.
 */
export function createStore(area: () => Storage | null, storageKey: string): Store {
  let state: Record<string, unknown> | null = null
  const listeners = new Set<Listener>()

  const load = (): Record<string, unknown> => {
    if (state) return state
    state = {}
    const storage = area()
    if (!storage) return state
    try {
      const raw = storage.getItem(storageKey)
      if (raw) {
        const parsed = JSON.parse(raw) as unknown
        if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
          state = { ...(parsed as Record<string, unknown>) }
        }
      }
    } catch {
      // A corrupt store just means the defaults, which are a working layout.
    }
    return state
  }

  const persist = () => {
    const storage = area()
    if (!storage) return
    try {
      storage.setItem(storageKey, JSON.stringify(state ?? {}))
    } catch {
      // Private browsing or a full quota. The choice still applies for this
      // session, which beats refusing to close the panel.
    }
  }

  const notify = () => {
    for (const listener of listeners) listener()
  }

  return {
    read: (key) => load()[key],
    write: (key, value) => {
      state = { ...load(), [key]: value }
      persist()
      notify()
    },
    forget: (prefix) => {
      const current = load()
      const next: Record<string, unknown> = {}
      let dropped = false
      for (const [key, value] of Object.entries(current)) {
        if (key.startsWith(prefix)) dropped = true
        else next[key] = value
      }
      if (!dropped) return
      state = next
      persist()
      notify()
    },
    subscribe: (listener) => {
      listeners.add(listener)
      return () => {
        listeners.delete(listener)
      }
    },
  }
}

function webStorage(name: "localStorage" | "sessionStorage") {
  return () => {
    if (typeof window === "undefined") return null
    try {
      return window[name]
    } catch {
      // Some privacy settings throw on the accessor itself.
      return null
    }
  }
}

const view = createStore(webStorage("localStorage"), "jd.view.state")
const session = createStore(webStorage("sessionStorage"), "jd.session.state")
const memory = createStore(() => null, "jd.memory.state")

/**
 * Whether a stored value is still the shape its page expects.
 *
 * Keys outlive the code that wrote them — a toggle becomes a three-way switch,
 * a tab is renamed — and a value of the wrong shape would be handed to a
 * component as if it were fine. The test is deliberately shallow: it catches
 * the kind that changed, which is what a rewrite actually does, and does not
 * try to validate the inside of an object nobody has described to it. A
 * nullable slot (`string | null` with `null` as the default) has no shape to
 * compare, so anything goes.
 */
export function usable(value: unknown, fallback: unknown): boolean {
  if (fallback === null || fallback === undefined || value === null) return true
  if (typeof value !== typeof fallback) return false
  if (typeof value !== "object") return true
  return Array.isArray(value) === Array.isArray(fallback)
}

type Setter<T> = (next: T | ((prev: T) => T)) => void

function useStored<T>(store: Store, key: string, fallback: T, arrival?: T | null): [T, Setter<T>] {
  // The fallback as it was when this key was first rendered, kept because
  // `useSyncExternalStore` compares snapshots by identity: handing back a
  // caller's inline `{ key: "name", dir: "asc" }` would be a new object every
  // render and would spin. Re-captured when the key changes — a form keyed on
  // the revision it was read from starts again from the new revision's values,
  // not from the ones the component happened to mount with.
  const [initial, setInitial] = useState({ key, value: fallback })
  if (initial.key !== key) setInitial({ key, value: fallback })
  const base = initial.key === key ? initial.value : fallback

  const read = useCallback((): T => {
    const stored = store.read(key)
    return (stored !== undefined && usable(stored, base) ? stored : base) as T
  }, [store, key, base])

  // What the address bar said on arrival wins over what this tab remembers,
  // and is remembered in its turn: a link into a page is a complete question,
  // and the answer it opens on is the one the next visit should keep. The
  // override lasts until the store carries the same value, which is the
  // render after the write below lands.
  const [arrived, setArrived] = useState<T | undefined>(arrival ?? undefined)
  useEffect(() => {
    if (arrived !== undefined) store.write(key, arrived)
  }, [store, key, arrived])

  const stored = useSyncExternalStore(store.subscribe, read, () => base)
  if (arrived !== undefined && stored === arrived) setArrived(undefined)
  const value = arrived !== undefined ? arrived : stored

  const set = useCallback<Setter<T>>(
    (next) => {
      const resolved = typeof next === "function" ? (next as (prev: T) => T)(read()) : next
      store.write(key, resolved)
    },
    [store, key, read],
  )

  return [value, set]
}

/**
 * `useState`, for a decision about the page's furniture.
 *
 * Same shape as `useState` — including the updater form — so adopting it on a
 * page is a one-line change and reading it later needs no explanation. `key`
 * is a dotted path naming the page and the thing (`terminal.tools`,
 * `files.sort`); it is stored, so renaming one silently resets it, which is
 * the correct outcome for a control that has changed enough to be renamed.
 *
 * The server has no localStorage, so the first paint is always the fallback
 * and the stored value arrives on hydration. `useSyncExternalStore` is what
 * makes that legal rather than a mismatch: React renders the server snapshot,
 * then re-renders once against the real one.
 */
export function useViewState<T>(key: string, fallback: T): [T, Setter<T>] {
  return useStored(view, key, fallback)
}

/**
 * `useState`, for what you were doing on the page: kept for this tab.
 *
 * `arrival` is the value the address bar handed over (`?source=`, `?sql=`);
 * when it is set it is what the page opens on, remembered from then on, and
 * `null`/`undefined` means the URL said nothing and the remembered value
 * stands.
 */
export function useSessionState<T>(key: string, fallback: T, arrival?: T | null): [T, Setter<T>] {
  return useStored(session, key, fallback, arrival)
}

/**
 * `useState`, for a value that must never be written down: a secret in a form
 * that has not been saved yet. Survives navigation, not a reload.
 */
export function useMemoryState<T>(key: string, fallback: T): [T, Setter<T>] {
  return useStored(memory, key, fallback)
}

/**
 * Drops every remembered session value under a key prefix — a dialog's fields
 * once it is saved or cancelled by hand.
 */
export function forgetSessionState(prefix: string) {
  session.forget(prefix)
}

/** The memory-only twin of `forgetSessionState`. */
export function forgetMemoryState(prefix: string) {
  memory.forget(prefix)
}

/**
 * Everything in progress, gone: called on sign-out, so the next account to
 * sign in on this browser does not open on the last one's half-typed forms
 * and filters. The furniture stays — it belongs to the screen, not the account.
 */
export function forgetWorkingState() {
  session.forget("")
  memory.forget("")
}
