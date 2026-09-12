"use client"

import { useCallback, useEffect, useRef, useState } from "react"

import { copyText } from "@/lib/clipboard"

/**
 * Copying, with an inline tick instead of a toast.
 *
 * Both feedback models survived the consolidation because they answer different
 * questions. A toast says *what* was copied, which is what you want after "copy
 * 340 rows as SQL" — that is `copyText` in `lib/clipboard.ts`, called directly.
 * The tick says *that* it worked without moving the reader's eye, which is what
 * you want on a chip they are about to paste into a terminal, and it is the only
 * one that works when the thing copied is a set of recovery codes the toast
 * would then be sitting on top of.
 *
 * The timer is cleared on unmount: a sheet closed inside the hold window was
 * setting state on a component that had gone.
 */
export function useCopy(options?: {
  /** Milliseconds the `copied` flag stays true. */
  hold?: number
}) {
  const hold = options?.hold ?? 1600
  const [copied, setCopied] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current)
    },
    [],
  )

  const copy = useCallback(
    async (text: string, announce?: string) => {
      if (!(await copyText(text, announce))) return false
      setCopied(true)
      if (timer.current) clearTimeout(timer.current)
      timer.current = setTimeout(() => setCopied(false), hold)
      return true
    },
    [hold],
  )

  return { copy, copied }
}
