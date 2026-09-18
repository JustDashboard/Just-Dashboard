"use client"

import { createContext, useContext, useEffect, useMemo, useRef, useState } from "react"
import type { Capability } from "@/lib/types"

/**
 * A panel of pages the rail cannot know about from the route alone.
 *
 * Two sections need one. The databases section hides the SQL-only pages when
 * the connection is Redis or Mongo, and every one of its pages carries the
 * chosen connection in the query string — neither fact exists outside the
 * section's own layout. A deployment is a place the rail has never heard of
 * until the project's read lands: its name, whether it is a game server, and
 * whether it has changes waiting.
 *
 * So the section registers its own panel here and the rail draws it. The
 * alternative was the rail fetching a project and a driver catalogue of its
 * own, which is a second poll of the same two endpoints for every page in the
 * product, to draw a list the page beneath it already has in hand.
 */

export type NavScopeEntry = {
  title: string
  href: string
  icon: React.ComponentType<{ className?: string }>
  /** Hidden unless the signed-in role holds this capability. */
  capability?: Capability
  /** A small mark on the row: something in here is waiting to be dealt with. */
  pending?: boolean
}

export type NavScope = {
  /** The route prefix this panel owns. The rail drops it once you leave. */
  path: string
  /** What the panel is of — a connection's name, a project's name. */
  title: string
  /** One line of fact under it: the engine, the address, the state. */
  caption?: string
  icon?: React.ComponentType<{ className?: string }>
  /**
   * Whether this stands *in place of* the section panel the route would
   * otherwise open (databases) or a level below it (one deployment inside
   * Deployments). It decides what the back control goes back to.
   */
  replaces?: boolean
  groups: {
    label?: string
    /** The same small mark a row takes, for a group whose pages hold it. */
    pending?: boolean
    items: NavScopeEntry[]
  }[]
}

type ScopeState = {
  scope: NavScope | null
  setScope: (next: NavScope | null | ((current: NavScope | null) => NavScope | null)) => void
}

const NavScopeContext = createContext<ScopeState | null>(null)

export function NavScopeProvider({ children }: { children: React.ReactNode }) {
  const [scope, setScope] = useState<NavScope | null>(null)
  const value = useMemo(() => ({ scope, setScope }), [scope])
  return <NavScopeContext.Provider value={value}>{children}</NavScopeContext.Provider>
}

/** What the rail should draw, if a section below it has registered something. */
export function useNavScopeValue() {
  return useContext(NavScopeContext)?.scope ?? null
}

/**
 * Hand the rail this section's own panel for as long as this component is
 * mounted.
 *
 * The scope is rebuilt on every render of its owner, so the effect keys off
 * what the rail would actually draw rather than the object's identity — a poll
 * settling twice with the same answer must not re-publish the panel and
 * re-run its entry animation.
 */
export function useNavScope(scope: NavScope | null) {
  const context = useContext(NavScopeContext)
  const setScope = context?.setScope
  const latest = useRef(scope)
  // Written in an effect rather than during render, and declared before the
  // effect that reads it so the order within a commit is ref-then-publish.
  useEffect(() => {
    latest.current = scope
  })

  const signature = scope
    ? JSON.stringify([
        scope.path,
        scope.title,
        scope.caption,
        scope.replaces,
        scope.groups.map((group) => [
          group.label,
          group.pending,
          group.items.map((item) => [item.title, item.href, item.capability, item.pending]),
        ]),
      ])
    : ""

  useEffect(() => {
    const published = latest.current
    if (!setScope || !published) return
    setScope(published)
    return () => setScope((current) => (current?.path === published.path ? null : current))
  }, [setScope, signature])
}
