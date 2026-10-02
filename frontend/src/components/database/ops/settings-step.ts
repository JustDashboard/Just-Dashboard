"use client"

import { useCallback, useRef } from "react"
import { useRouter } from "next/navigation"
import type { SectionId, SectionParams } from "@/components/database/engine"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * A thing opened over a page of the area and named in its address — an
 * account's panel (`?account=`), a transfer's output (`?job=`) — as a step
 * the reader took.
 *
 * Opening it from the page makes a history entry, so Back closes it instead
 * of leaving the page, which is what a reader on a phone presses. Closing one
 * opened that way goes back to the entry it came from; one opened by a link,
 * or put in the address by the page itself, has no such entry and is taken
 * out of the address in place.
 *
 * `keep` names the other keys of this page's address that an opened thing
 * leaves as they are (the tile that narrows the list behind a panel).
 */
export function useAddressStep(section: SectionId, keys: readonly string[], keep: string[] = []) {
  const { href, select, param } = useDatabase()
  const router = useRouter()
  const stepped = useRef(false)
  const kept = Object.fromEntries(keep.map((key) => [key, param(key)]))
  const signature = JSON.stringify(kept)
  const open = useCallback(
    (params: SectionParams) => {
      stepped.current = true
      // The shell holds the last address it was asked to write, and applies
      // it again whenever the address comes back to where that write began —
      // which undid opening a thing that had just been closed. An empty write
      // makes the address as it stands the last one written.
      select({})
      // Not scrolled: the page stays where the reader left it behind what opened.
      router.push(href(section, { ...(JSON.parse(signature) as SectionParams), ...params }), {
        scroll: false,
      })
    },
    [router, href, select, section, signature],
  )
  const names = keys.join(" ")
  const close = useCallback(() => {
    if (stepped.current) {
      stepped.current = false
      window.history.back()
    } else select(Object.fromEntries(names.split(" ").map((key) => [key, null])))
  }, [select, names])
  return { open, close, stepped }
}
