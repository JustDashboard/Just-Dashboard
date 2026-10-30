"use client"

import { useEffect } from "react"
import { RenderFailure } from "@/components/standalone-state"

/**
 * The floor under `(dashboard)/error.tsx`, which cannot catch its own layout,
 * and under the sign-in and welcome pages, which it does not cover. There is
 * no shell left to keep, so this takes the window.
 */
export default function RootError({
  error,
  reset,
}: {
  error: Error & { digest?: string }
  reset: () => void
}) {
  useEffect(() => {
    console.error("page render failed", error)
  }, [error])

  return <RenderFailure error={error} reset={reset} />
}
