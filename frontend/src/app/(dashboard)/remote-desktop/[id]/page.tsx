"use client"

import { useEffect } from "react"
import { useParams, useSearchParams } from "next/navigation"
import { useHeldSessions } from "@/components/remote-desktop/held-sessions"
import { sessionMode } from "@/lib/remote-desktop"

/**
 * A computer's session is drawn by the shell (`HeldSessionsHost`), which keeps
 * it connected while other pages are up. This page only names it: the
 * computer, and the mode when the address asks for one; without one, a
 * session already open keeps its own.
 */
export default function RemoteSessionPage() {
  const { id } = useParams<{ id: string }>()
  const asked = useSearchParams().get("mode")
  const { hold } = useHeldSessions()
  useEffect(() => {
    hold(id, asked === null ? null : sessionMode(asked))
  }, [id, asked, hold])
  return null
}
