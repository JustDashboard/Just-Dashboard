"use client"

import { useEffect, useSyncExternalStore } from "react"
import type { TabName } from "@/components/nav"

/**
 * What the page on screen says its tab is about.
 *
 * The shell names a tab from the address (`tabTitle` in `nav.ts`), and the
 * address of a detail page names only the section: every database page read
 * "Databases", every project page "Deployments", a container "Docker". The
 * item is known to the page, not to the nav, so the page says it here and the
 * shell's `<title>` reads it.
 *
 * Two ways in. A page's heading says it without being asked: `PageContext`
 * draws the `h1` that names the page for a screen reader, and hands the same
 * words to `TabHeading`. A page that wants more than its heading calls
 * `useTabTitle("Deployment #42", "shop")`, which outranks a heading. Kept
 * outside React, like the metrics store, so that naming the tab renders the
 * one `<title>` and not the shell.
 */

type Claim = { parts: readonly string[]; explicit: boolean }

const NOTHING: TabName = { parts: [], explicit: false }
/** In the order they were made: a Map keeps it. */
const claims = new Map<object, Claim>()
const listeners = new Set<() => void>()
let said: TabName = NOTHING

function settle() {
  // What was said on purpose over what a heading says; among equals, the last
  // to arrive, which is the page that just opened.
  let best: Claim | undefined
  for (const claim of claims.values()) {
    if (!best || Number(claim.explicit) >= Number(best.explicit)) best = claim
  }
  const next = best ?? NOTHING
  if (
    next.explicit === said.explicit &&
    next.parts.length === said.parts.length &&
    next.parts.every((part, i) => part === said.parts[i])
  )
    return
  said = next === NOTHING ? NOTHING : { parts: next.parts, explicit: next.explicit }
  for (const listener of listeners) listener()
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

const SEPARATOR = "\u0000"

function useClaim(parts: readonly string[], explicit: boolean) {
  // One string, so the effect follows the words and not the array's identity.
  const words = parts.join(SEPARATOR)
  useEffect(() => {
    if (!words) return
    const claim = {}
    claims.set(claim, { parts: words.split(SEPARATOR), explicit })
    settle()
    return () => {
      claims.delete(claim)
      settle()
    }
  }, [words, explicit])
}

function clean(parts: readonly unknown[]): string[] {
  return parts
    .map((part) => (typeof part === "number" ? String(part) : part))
    .filter((part): part is string => typeof part === "string")
    .map((part) => part.trim())
    .filter(Boolean)
}

/**
 * Names the tab for as long as the caller is mounted, most specific first:
 * `useTabTitle("Deployment #42", "shop")` reads "Deployment #42 · shop ·
 * Deployments · Just Dashboard". Anything that is not text yet (a name still
 * loading) is left out, and the title falls back to the heading and the nav.
 */
export function useTabTitle(...parts: (string | number | null | undefined | false)[]) {
  useClaim(clean(parts), true)
}

/**
 * The page's heading, as the tab's name. Drawn by `PageContext` beside the
 * `h1`; a heading that is not plain text (a shimmer while a name loads) says
 * nothing. The shell uses it only on an address the nav has no row for, so a
 * list page keeps the name its rail entry has.
 */
export function TabHeading({ title }: { title: React.ReactNode }) {
  useClaim(clean([title]), false)
  return null
}

/** What the page on screen has said, for the shell's `<title>`. */
export function useTabName(): TabName {
  return useSyncExternalStore(subscribe, read, readOnServer)
}

function read() {
  return said
}

function readOnServer() {
  return NOTHING
}
