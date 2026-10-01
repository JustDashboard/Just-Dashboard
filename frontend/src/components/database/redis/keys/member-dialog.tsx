"use client"

import { FormFact, FormFacts, FormNote } from "@/components/form"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { bytesLabel } from "@/components/database/redis/bytes"
import { KindMark, kindLabel } from "@/components/database/redis/kinds"
import type { RedisBytes } from "@/components/database/redis/types"

/**
 * The dialog a member of a key is added or edited in.
 *
 * It opens on the key it acts on — its type's mark, its name, its database —
 * and ends on the one command. What is typed in it belongs to the caller, so
 * a stray Escape or a click outside closes the dialog and loses nothing: it
 * opens again on the same words. Only Cancel, and a write that landed, clear
 * them. While the write is in flight it does not close at all.
 */
export function MemberDialog({
  open,
  onOpenChange,
  onCancel,
  title,
  name,
  type,
  db,
  command,
  onSubmit,
  busy,
  error,
  disabled,
  size = "md",
  children,
}: {
  open: boolean
  /** A dismissal: the dialog closes and the draft is kept. */
  onOpenChange: (open: boolean) => void
  /** Cancel: the dialog closes and the draft is thrown away. */
  onCancel: () => void
  title: string
  /** The key being changed; absent while a new key is still being named. */
  name?: RedisBytes
  type: string
  db: number | undefined
  /** The word on the command. */
  command: string
  onSubmit: () => void
  busy: boolean
  /** Why the server refused, in its own words. */
  error?: string
  disabled?: boolean
  size?: "md" | "lg"
  children: React.ReactNode
}) {
  return (
    <Modal
      open={open}
      onOpenChange={(next) => !busy && onOpenChange(next)}
      title={title}
      size={size}
      footer={
        <>
          <Button variant="outline" onClick={onCancel} disabled={busy}>
            Cancel
          </Button>
          <Button type="submit" form="redis-member-form" pending={busy} disabled={disabled}>
            {command}
          </Button>
        </>
      }
    >
      <form
        id="redis-member-form"
        className="space-y-4"
        onSubmit={(event) => {
          event.preventDefault()
          if (!busy && !disabled) onSubmit()
        }}
      >
        {name !== undefined && (
          <div className="flex min-w-0 items-center gap-2.5">
            <KindMark type={type} className="size-4" />
            <div className="min-w-0 space-y-0.5">
              <p className="truncate font-mono text-xs font-medium">{bytesLabel(name)}</p>
              <FormFacts>
                <FormFact label="Type">{kindLabel(type)}</FormFact>
                {db !== undefined && <FormFact label="In">db{db}</FormFact>}
              </FormFacts>
            </div>
          </div>
        )}
        {children}
        {error && (
          <FormNote tone="danger" role="alert">
            {error}
          </FormNote>
        )}
      </form>
    </Modal>
  )
}
