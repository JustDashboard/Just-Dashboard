"use client"

import { useLayoutEffect, useRef } from "react"
import { useSearchParams } from "next/navigation"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * Lets a write to the address be over once the router has moved on.
 *
 * The section's `select` keeps its last write — "from this address, to that
 * one" — and applies it again whenever the reader comes back to the address
 * it started from. On these pages that undid ordinary presses: the New-table
 * panel closed once would not open a second time from the table editor's
 * link, and a diagram switched to every schema bounced back there when one
 * schema was picked again. Until that is mended where it lives, each arrival
 * is answered with a write of nothing made from the new address, which takes
 * the place of the kept one and changes no key. (The table editor does the
 * same for its own page.)
 */
export function useSettledAddress() {
  const { select } = useDatabase()
  const shown = useSearchParams().toString()
  const latest = useRef(select)
  useLayoutEffect(() => {
    latest.current = select
  })
  useLayoutEffect(() => {
    latest.current({})
  }, [shown])
}
