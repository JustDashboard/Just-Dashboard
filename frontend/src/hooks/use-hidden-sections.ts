"use client"

import { useCallback, useMemo } from "react"
import { put } from "@/lib/api"
import { useAuth } from "@/hooks/use-auth"

/**
 * The sections the signed-in account keeps out of its sidebar, and the one
 * way to change them.
 *
 * Held on the account rather than in the browser: what somebody uses the
 * server for is true of them on every machine they sign in from, and the
 * session already carries it, so every reader of this is answered without a
 * request of its own. Saving reads the session back, which is what makes the
 * rail, the Overview and the palette follow at once.
 */
export function useHiddenSections() {
  const { status, refresh } = useAuth()
  const list = status?.user?.hiddenSections
  const hidden: ReadonlySet<string> = useMemo(() => new Set(list), [list])

  const save = useCallback(
    async (sections: readonly string[]) => {
      await put("/account/workspace", { hiddenSections: sections })
      await refresh()
    },
    [refresh],
  )

  return { hidden, save }
}
