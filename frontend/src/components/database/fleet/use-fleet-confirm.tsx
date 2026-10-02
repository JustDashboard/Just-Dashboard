"use client"

import { useCallback, useState, useSyncExternalStore } from "react"
import { ConfirmDialog, type ConfirmRequest } from "@/components/confirm-dialog"
import { returnFocus } from "@/components/database/connect/focus"

/**
 * The confirmation dialog of the control center: the product's own
 * `ConfirmDialog`, driven the way `useConfirm` drives it, with the one thing
 * these pages need of it besides — where the keyboard goes when it closes.
 *
 * Every confirmation here is opened from a card's menu, and a menu's item is
 * gone by the time the dialog closes, so the dialog has nothing to return
 * focus to and a keyboard user was put back at the top of the page after
 * every cancelled Stop or Forget. The caller names the control that stands
 * for the database (its menu, by accessible name) and focus goes there.
 */
export function useFleetConfirm() {
  const [asked, setAsked] = useState<{ request: ConfirmRequest; returnTo?: string } | null>(null)
  const confirm = useCallback(
    (request: ConfirmRequest, returnTo?: string) => setAsked({ request, returnTo }),
    [],
  )
  const dialog = (
    <ConfirmDialog
      request={asked?.request ?? null}
      onOpenChange={(open) => {
        if (open) return
        returnFocus(asked?.returnTo)
        setAsked(null)
      }}
    />
  )
  return { confirm, dialog }
}

export type FleetConfirm = ReturnType<typeof useFleetConfirm>["confirm"]

/**
 * What the server said when it turned a confirmed action down, held where the
 * open dialog can read it. The dialog's body is handed over once, when the
 * question is asked; this is how the answer reaches it afterwards, so the
 * refusal is read beside the question rather than only in a toast that
 * leaves.
 */
export function createRefusal() {
  let said: string | undefined
  const listeners = new Set<() => void>()
  return {
    read: () => said,
    say(message: string | undefined) {
      said = message
      listeners.forEach((listener) => listener())
    },
    subscribe(listener: () => void) {
      listeners.add(listener)
      return () => {
        listeners.delete(listener)
      }
    },
  }
}

export type Refusal = ReturnType<typeof createRefusal>

export function useRefusal(refusal: Refusal) {
  return useSyncExternalStore(refusal.subscribe, refusal.read, () => undefined)
}
