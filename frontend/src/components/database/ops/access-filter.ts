"use client"

import { useEffect } from "react"
import { useSessionState } from "@/lib/view-state"

/**
 * The Access list's filter, kept for the tab so the rail beside an account
 * holds the same accounts the list did.
 *
 * On the list the address is what says the chip (`?show=`), as it always
 * has: a link to the administrators opens on them. The list hands what the
 * address says to this store, and an account's page — whose address is the
 * account's — reads it from here and gives it back to the list in its way
 * back. The search is the reader's typing and lives only here.
 */
export function useAccessFilter(id: number) {
  const [show, setShow] = useSessionState(`databases.${id}.access.show`, "")
  const [search, setSearch] = useSessionState(`databases.${id}.access.search`, "")
  return { show, setShow, search, setSearch }
}

/** The list's side of it: what the address says is what the store holds. */
export function useAccessFilterFrom(id: number, asked: string) {
  const filter = useAccessFilter(id)
  const { show, setShow } = filter
  useEffect(() => {
    if (show !== asked) setShow(asked)
  }, [show, asked, setShow])
  return filter
}
