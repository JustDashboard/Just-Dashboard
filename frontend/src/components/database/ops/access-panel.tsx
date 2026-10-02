"use client"

import { useCallback } from "react"
import { SidePanel } from "@/components/side-panel"
import { DiscardQuestion, useDiscardGuard } from "@/components/database/ops/settings-dialog"
import { useAddressStep } from "@/components/database/ops/settings-step"

const ACCOUNT_KEYS = ["account", "host"] as const
const KEPT = ["show", "view"]

/**
 * Which account is open, as the address says it (`?account=`, `?host=`).
 * Opening one from the list is a step: Back closes the panel.
 */
export function useOpenAccount() {
  const step = useAddressStep("access", ACCOUNT_KEYS, KEPT)
  const stepOpen = step.open
  const open = useCallback(
    (account: string, host?: string) => stepOpen({ account, host }),
    [stepOpen],
  )
  return { open, close: step.close }
}

/**
 * One account, user or role beside its list: the sheet every engine's Access
 * page opens it in.
 *
 * It holds the largest unsaved work of the page — a set of staged grants, a
 * rule being rewritten — so it closes the way a task's dialog does: Escape,
 * the close button and a press outside ask first when something would be
 * lost, in the sheet's own footer, and do nothing while a request is running.
 */
export function AccountSheet({
  title,
  description,
  actions,
  width = "lg",
  lose,
  busy = false,
  onClose,
  children,
}: {
  title: React.ReactNode
  /** Read to a screen reader, never drawn. */
  description: string
  actions?: React.ReactNode
  width?: "md" | "lg"
  /** What closing now would lose, as the question asks it; empty when nothing would be. */
  lose?: string
  /** A save is in flight: the sheet stays until it has answered. */
  busy?: boolean
  onClose: () => void
  children: React.ReactNode
}) {
  const { asking, dismiss, keep, stay } = useDiscardGuard({ dirty: Boolean(lose), busy, onClose })
  return (
    <SidePanel
      open
      onOpenChange={dismiss}
      width={width}
      initialFocus="body"
      title={title}
      description={description}
      actions={actions}
      footer={
        asking ? (
          <DiscardQuestion
            question={`Close and lose ${lose}?`}
            // The body has its own Discard, which empties a form and stays.
            discardLabel="Close anyway"
            keep={keep}
            onStay={stay}
            onDiscard={onClose}
          />
        ) : undefined
      }
    >
      {children}
    </SidePanel>
  )
}
