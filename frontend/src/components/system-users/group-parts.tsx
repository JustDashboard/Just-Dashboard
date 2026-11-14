"use client"

import { useState } from "react"
import { ShieldCheck, Users } from "@/components/icons"
import { Field } from "@/components/form"
import { Modal } from "@/components/modal"
import { ProductLogo } from "@/components/product-logo"
import { groupReach, type GroupReach } from "@/components/system-users/groups"
import { groupProduct } from "@/components/system-users/marks"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { usePoll } from "@/hooks/use-poll"
import { get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { SystemGroup } from "@/lib/types"
import { cn } from "@/lib/utils"

/** The host's groups, read the way the list and a group's page both read them. */
export function useGroups() {
  return usePoll(
    (signal) => get<SystemGroup[]>("/system-accounts/groups", undefined, signal),
    30000,
    [],
  )
}

/** The ink a group's reach is said in, wherever it is said. */
export const REACH_INK: Record<GroupReach["tone"], string> = {
  danger: "text-destructive",
  warning: "text-warning",
  default: "text-foreground",
  muted: "text-muted-foreground",
}

/**
 * A group on the tile its list lines up on: docker and lxd as themselves, a
 * group that hands root over behind a shield, the rest as people.
 */
export function GroupMark({
  group,
  size = "md",
  className,
}: {
  group: SystemGroup
  size?: "sm" | "md" | "lg"
  className?: string
}) {
  const box =
    size === "lg"
      ? "size-10 rounded-lg [&_img]:size-6"
      : size === "md"
        ? "size-9"
        : "size-7 rounded-md [&_img]:size-4"
  return (
    <ProductLogo
      id={groupProduct(group.name)}
      size="sm"
      fallback={groupReach(group).tone === "muted" ? Users : ShieldCheck}
      className={cn(box, className)}
    />
  )
}

/** A new group is a name: the host numbers it, and its members are chosen on its page. */
export function NewGroupDialog({
  onClose,
  onCreated,
}: {
  onClose: () => void
  onCreated: (name: string) => void
}) {
  const [name, setName] = useState("")
  const [busy, setBusy] = useState(false)
  const create = async () => {
    if (busy) return
    setBusy(true)
    try {
      await post("/system-accounts/groups", { name })
      notify.success(`Created ${name}`)
      onCreated(name)
      onClose()
    } catch (err) {
      notify.error("The group was not created", err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      open
      onOpenChange={(open) => !open && onClose()}
      title="New group"
      footer={
        <Button onClick={() => void create()} disabled={!name.trim()} pending={busy}>
          Create
        </Button>
      }
    >
      <form
        onSubmit={(event) => {
          event.preventDefault()
          if (name.trim()) void create()
        }}
      >
        <Field label="Name" htmlFor="new-group-name">
          <Input
            id="new-group-name"
            className="font-mono"
            autoComplete="off"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
      </form>
    </Modal>
  )
}

/** Rows of a list that has not arrived, each the height a `ChoiceRow` will be. */
export function ChoiceRowsWaiting({ rows = 4 }: { rows?: number }) {
  return (
    <ul aria-hidden data-slot="choice-rows-waiting" className="min-w-0 space-y-2">
      {Array.from({ length: rows }, (_, index) => (
        <li
          key={index}
          className="flex h-[58px] min-w-0 items-center gap-3 rounded-xl border bg-choice-surface px-3"
        >
          <span className="size-9 shrink-0 animate-pulse rounded-lg bg-muted" />
          <span className="min-w-0 flex-1 space-y-2">
            <span className="block h-2.5 w-32 max-w-full animate-pulse rounded-md bg-muted" />
            <span className="block h-2 w-20 max-w-full animate-pulse rounded-md bg-muted" />
          </span>
        </li>
      ))}
    </ul>
  )
}
