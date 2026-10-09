"use client"

import { useState } from "react"
import { del, post } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { ProtectionBlocklist, ProtectionException } from "@/lib/types"
import { Trash } from "@/components/icons"
import { useConfirm } from "@/components/confirm-dialog"
import { Field, FieldRow } from "@/components/form"
import { DimActions, IconAction } from "@/components/icon-action"
import { Modal } from "@/components/modal"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Cidr } from "@/components/network/address"
import { compact } from "@/components/network/gateway/reading"
import { EXPIRY_CHOICES, expiryFrom } from "@/components/network/protection/reading"
import { useAuth } from "@/hooks/use-auth"

/**
 * Networks let past the drops on purpose: for one list, or for every list and
 * limit, each with the reason it was made and, if it was given one, the time
 * it ends. The end is written into the rule itself, so the kernel stops
 * honouring it then whether or not the dashboard is running; the dashboard
 * only tidies it away afterwards. What each has let through is counted.
 *
 * Making one gives part of a list away and removing one can refuse someone
 * who relies on it, so both ask for the destructive capability.
 */
export function ExceptionList({
  exceptions,
  onChanged,
}: {
  exceptions: ProtectionException[]
  onChanged: () => void
}) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const remove = (e: ProtectionException) =>
    confirm({
      title: `Remove the exception for ${e.address}`,
      confirmLabel: "Remove",
      description: (
        <p>
          New connections from {e.address} are judged by {e.scopeName} again. Connections already
          open continue.
        </p>
      ),
      action: async () => {
        await del(`/network/protection/exceptions/${e.id}`)
        notify.success(`The exception for ${e.address} is removed`)
        onChanged()
      },
    })
  if (exceptions.length === 0) {
    return <EmptyNote>No network is excepted from the drops.</EmptyNote>
  }
  return (
    <>
      <RowList>
        {exceptions.map((e) => (
          <Row
            key={e.id}
            title={
              <span className="inline-flex min-w-0 flex-wrap items-center gap-2">
                <Cidr cidr={e.address} />
                {e.expired && <Tag tone="warning">expired</Tag>}
              </span>
            }
            subtitle={[
              e.reason,
              e.scope === "all" ? "every list and limit" : e.scopeName,
              e.expiresAt
                ? e.expired
                  ? `ended ${relativeTime(e.expiresAt)}`
                  : `ends ${relativeTime(e.expiresAt)}`
                : "until removed",
              e.createdBy ? `made by ${e.createdBy}` : undefined,
            ]
              .filter(Boolean)
              .join(" · ")}
            trailing={
              <>
                <span className="numeric font-mono text-micro text-muted-foreground">
                  {compact(e.packets)} let through
                </span>
                {can("system.admin") && can("destructive") ? (
                  <DimActions>
                    <IconAction
                      label={`Remove the exception for ${e.address}`}
                      onClick={() => remove(e)}
                    >
                      <Trash aria-hidden />
                    </IconAction>
                  </DimActions>
                ) : (
                  <span aria-hidden className="size-8 shrink-0" />
                )}
              </>
            }
          />
        ))}
      </RowList>
      {dialog}
    </>
  )
}

const ALL = "all"

/** The editor for a new exception: which network, where it applies, why, and until when. */
export function ExceptionModal({
  lists,
  scope: initialScope = ALL,
  onOpenChange,
  onSaved,
}: {
  lists: ProtectionBlocklist[]
  scope?: string
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const { can } = useAuth()
  const [address, setAddress] = useState("")
  const [scope, setScope] = useState(initialScope)
  const [reason, setReason] = useState("")
  const [expiry, setExpiry] = useState<string>("24h")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const ready = address.trim() && reason.trim()
  const submit = async () => {
    setBusy(true)
    setError(undefined)
    try {
      await post("/network/protection/exceptions", {
        address: address.trim(),
        scope,
        reason: reason.trim(),
        expiresAt: expiryFrom(expiry),
      })
      notify.success(`${address.trim()} is excepted`)
      onOpenChange(false)
      onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      open
      onOpenChange={(next) => !busy && onOpenChange(next)}
      title="New exception"
      description="Let a network past the drops for a reason, until a time"
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button
            type="submit"
            form="exception-form"
            disabled={!can("destructive") || !ready || busy}
            pending={busy}
          >
            Make exception
          </Button>
        </>
      }
    >
      <form
        id="exception-form"
        className="flex min-w-0 flex-col gap-5"
        onSubmit={(event) => {
          event.preventDefault()
          if (ready && !busy) void submit()
        }}
      >
        <FieldRow>
          <Field
            label="Address or network"
            htmlFor="exception-address"
            hint="198.51.100.7 or a /24"
          >
            <Input
              id="exception-address"
              value={address}
              placeholder="198.51.100.7"
              onChange={(event) => setAddress(event.target.value)}
              className="font-mono"
              autoComplete="off"
            />
          </Field>
          <Field label="Applies to" htmlFor="exception-scope">
            <Select value={scope} onValueChange={setScope}>
              <SelectTrigger id="exception-scope" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={ALL}>Every list and limit</SelectItem>
                {lists.map((l) => (
                  <SelectItem key={l.id} value={`blocklist:${l.id}`}>
                    {l.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        </FieldRow>
        <FieldRow>
          <Field
            label="Why"
            htmlFor="exception-reason"
            hint="Kept with it: who needs it and for what"
          >
            <Input
              id="exception-reason"
              value={reason}
              placeholder="Partner's monitoring"
              onChange={(event) => setReason(event.target.value)}
              autoComplete="off"
            />
          </Field>
          <Field label="Ends" htmlFor="exception-expiry" hint="The kernel stops honouring it then">
            <Select value={expiry} onValueChange={setExpiry}>
              <SelectTrigger id="exception-expiry" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {EXPIRY_CHOICES.map((c) => (
                  <SelectItem key={c.value || "never"} value={c.value || "never"}>
                    {c.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        </FieldRow>
        {error && (
          <p role="alert" className="animate-rise text-body text-destructive">
            {error}
          </p>
        )}
      </form>
    </Modal>
  )
}
