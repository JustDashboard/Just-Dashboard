"use client"

import { PromptDialog, type PromptFact } from "@/components/prompt-dialog"

type NameDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  label: string
  hint?: React.ReactNode
  placeholder?: string
  initial?: string
  /** What the form operates on — the commit, the branch — as data under the title. */
  facts?: PromptFact[]
  /** Offer a message field, labelled and explained this way. */
  message?: { label: string; hint: React.ReactNode; placeholder?: string }
  confirmLabel: string
  onSubmit: (name: string, message: string) => Promise<void>
}

/**
 * Git's name dialog — a branch from a commit, a tag, a rename, a stash with a
 * label — drawn by the shared prompt, whose anatomy it was.
 *
 * @deprecated use `PromptDialog` from `@/components/prompt-dialog`, with `verb`.
 */
export function NameDialog({ confirmLabel, ...props }: NameDialogProps) {
  return <PromptDialog {...props} verb={confirmLabel} refusal="git refused" mono />
}
