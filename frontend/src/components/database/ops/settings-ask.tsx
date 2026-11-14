"use client"

import { useCallback, useRef, useState } from "react"
import { ConfirmDialog, type ConfirmRequest } from "@/components/confirm-dialog"

type Asking = { request: ConfirmRequest; settle: (done: boolean) => void }

/**
 * A confirmation a save can wait on.
 *
 * A change that replaces a server's container is asked for before it is sent,
 * and on these pages it is sent by the page's Save, which has to know how it
 * ended: the bar says *Saved* only when the change went through, and keeps the
 * edit when the reader said no. The section's shared `confirm` only begins an
 * action; this one answers.
 *
 * `ask` resolves false when the dialog is dismissed. Confirmed, the dialog
 * closes at once (a recreate can take a minute, and a dialog held open that
 * long is a page the reader cannot use) and `ask` resolves with what `run`
 * came to, so the caller's own surface says it is in flight.
 */
export function useAsk() {
  const [asking, setAsking] = useState<Asking>()
  // Whether the open confirmation was taken: closing it otherwise is "no".
  const taken = useRef(false)
  const ask = useCallback(
    (request: Omit<ConfirmRequest, "action">, run: () => Promise<boolean>) =>
      new Promise<boolean>((settle) => {
        taken.current = false
        setAsking({
          settle,
          request: {
            ...request,
            action: async () => {
              taken.current = true
              void run().then(settle, () => settle(false))
              return "reported"
            },
          },
        })
      }),
    [],
  )
  const dialog = (
    <ConfirmDialog
      request={asking?.request ?? null}
      onOpenChange={(open) => {
        if (open || !asking) return
        setAsking(undefined)
        if (!taken.current) asking.settle(false)
      }}
    />
  )
  return { ask, dialog, asking: Boolean(asking) }
}
