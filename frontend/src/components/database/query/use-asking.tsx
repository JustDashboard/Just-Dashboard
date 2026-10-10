"use client"

import { useCallback, useState } from "react"
import { ConfirmDialog, type ConfirmRequest } from "@/components/confirm-dialog"

/**
 * The product's confirmation dialog, with one thing `useConfirm` does not
 * tell its caller: that the dialog has gone, whichever way the reader
 * answered. The editor needs to know — a statement the reader declined to
 * run leaves its tab free to run another, and the keyboard goes back to
 * where it was rather than to nowhere.
 */
export function useAsking() {
  const [held, setHeld] = useState<{ request: ConfirmRequest; closed?: () => void } | null>(null)
  const confirm = useCallback(
    (request: ConfirmRequest, closed?: () => void) => setHeld({ request, closed }),
    [],
  )
  const dialog = (
    <ConfirmDialog
      request={held?.request ?? null}
      onOpenChange={(open) => {
        if (open) return
        setHeld(null)
        held?.closed?.()
      }}
    />
  )
  return { confirm, dialog }
}
