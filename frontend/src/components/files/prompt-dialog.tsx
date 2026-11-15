"use client"

import { usePrompt as useSharedPrompt, type PromptOptions } from "@/components/prompt-dialog"

/**
 * Files' first shape of a prompt request, kept while its callers move to
 * `PromptOptions` (`components/prompt-dialog.tsx`).
 *
 * @deprecated use `usePrompt` from `@/components/prompt-dialog`.
 */
export type PromptRequest = {
  title: string
  label?: string
  placeholder?: string
  initial?: string
  confirmLabel?: string
  /** A live line under the input — the resulting path, say. */
  hint?: (value: string) => React.ReactNode
  /** Return a string to reject with a message; return undefined to accept. */
  validate?: (value: string) => string | undefined
  /** Runs on submit; a throw keeps the dialog open with the reason in it. */
  submit: (value: string) => Promise<void> | void
  /** Pre-select the name without its extension, the way a rename usually wants. */
  selectBasename?: boolean
}

/** What Files' requests become in the shared prompt. */
export function fromFilesRequest(request: PromptRequest): PromptOptions {
  const hint = request.hint
  return {
    title: request.title,
    label: request.label ?? "Name",
    placeholder: request.placeholder,
    initial: request.initial,
    verb: request.confirmLabel ?? "Save",
    // A path, which is read character by character and may be long.
    hint: hint && ((value) => <span className="font-mono break-all">{hint(value)}</span>),
    validate: request.validate,
    onSubmit: (value) => request.submit(value),
    selectBasename: request.selectBasename,
  }
}

/**
 * A single-input dialog, driven from anywhere like useConfirm: Files' rename,
 * new file, new folder and new symlink. The dialog is the shared one.
 *
 * @deprecated use `usePrompt` from `@/components/prompt-dialog`.
 */
export function usePrompt() {
  const shared = useSharedPrompt()
  return {
    prompt: (request: PromptRequest | null) =>
      shared.prompt(request ? fromFilesRequest(request) : null),
    dialog: shared.dialog,
  }
}
