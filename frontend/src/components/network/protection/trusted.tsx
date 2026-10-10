"use client"

import { useState } from "react"
import { useAuth } from "@/hooks/use-auth"
import { del, put } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { ProtectionTrusted, ProtectionView } from "@/lib/types"
import { Check, Pencil, Trash, Warning } from "@/components/icons"
import { useConfirm } from "@/components/confirm-dialog"
import { Field, FieldRow } from "@/components/form"
import { DimActions, IconAction } from "@/components/icon-action"
import { Modal } from "@/components/modal"
import { Row, RowList } from "@/components/row-list"
import { Notice } from "@/components/state"
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
import {
  EXPIRY_CHOICES,
  expiryFrom,
  ORIGIN_WORD,
  trustedNoteWord,
} from "@/components/network/protection/reading"

/**
 * The addresses no blocklist and no limit may ever refuse: this machine, the
 * dashboard's own allowlist, the address this browser is reading from, and
 * any the reader has kept. Every drop the gateway makes is preceded by this
 * set, which is why a country list cannot lock the operator out. Each says
 * where it came from, and a removable one can be taken out — the server
 * refuses to forget the reader's own address while nothing else would still
 * cover it.
 *
 * A kept address carries who kept it, why, until when, and when an operator
 * last signed in from it. One nobody has used or confirmed in a month is
 * marked stale, so addresses that accumulate as operators move between
 * networks are reviewed rather than trusted forever: confirm one that is
 * still needed, or stop trusting it.
 *
 * When this browser's address is not in the set a notice says so, because it
 * is then the one address a list or a limit may refuse.
 */
export function TrustedList({
  view,
  onChanged,
}: {
  view: Pick<ProtectionView, "trusted" | "client" | "clientTrusted">
  onChanged: () => void
}) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [editing, setEditing] = useState<ProtectionTrusted>()
  const remove = (address: string, you: boolean) =>
    confirm({
      title: `Stop trusting ${address}`,
      confirmLabel: "Stop trusting",
      description: (
        <p>
          {you
            ? "This is the address you are reading this page from. Blocklists and limits may refuse it from now on."
            : "Blocklists and limits may refuse it from now on."}
        </p>
      ),
      action: async () => {
        await del("/network/protection/trusted", { query: { address } })
        notify.success(`${address} is no longer trusted`)
        onChanged()
      },
    })
  const confirmStill = async (entry: ProtectionTrusted) => {
    try {
      await put("/network/protection/trusted", {
        address: entry.address,
        reason: entry.reason ?? "",
        expiresAt: entry.expiresAt && !entry.expired ? entry.expiresAt : "",
        confirm: true,
      })
      notify.success(`${entry.address} is confirmed as still needed`)
      onChanged()
    } catch (err) {
      notify.error(`${entry.address} was not confirmed`, err)
    }
  }
  const stale = view.trusted.filter((t) => t.stale).length
  return (
    <div className="flex min-w-0 flex-col gap-4">
      {!view.clientTrusted && (
        <Notice tone="warning" icon={Warning} title="Your own address is not trusted">
          This browser reads the dashboard from{" "}
          <span className="font-mono text-foreground">{view.client}</span>, which no entry below
          covers, so a blocklist or a limit that matches it would cut you off.
        </Notice>
      )}
      {stale > 0 && (
        <Notice
          tone="warning"
          icon={Warning}
          title={`${stale} kept ${stale === 1 ? "address is" : "addresses are"} stale`}
        >
          Nobody has signed in from {stale === 1 ? "it" : "them"} or confirmed{" "}
          {stale === 1 ? "it" : "them"} in thirty days. Confirm what is still needed and stop
          trusting the rest.
        </Notice>
      )}
      <RowList>
        {view.trusted.map((entry) => {
          const note = entry.removable ? trustedNoteWord(entry, relativeTime) : undefined
          return (
            <Row
              key={entry.address}
              title={
                <span className="inline-flex min-w-0 flex-wrap items-center gap-2">
                  <Cidr cidr={entry.address} />
                  {entry.stale && <Tag tone="warning">stale</Tag>}
                  {entry.expired && <Tag tone="warning">expired</Tag>}
                </span>
              }
              subtitle={note || undefined}
              trailing={
                <>
                  <Tag>{ORIGIN_WORD[entry.origin]}</Tag>
                  {can("system.admin") && entry.removable ? (
                    <DimActions>
                      <IconAction
                        label={`Confirm ${entry.address} is still needed`}
                        onClick={() => void confirmStill(entry)}
                      >
                        <Check aria-hidden />
                      </IconAction>
                      <IconAction
                        label={`Edit why ${entry.address} is kept`}
                        onClick={() => setEditing(entry)}
                      >
                        <Pencil aria-hidden />
                      </IconAction>
                      {can("destructive") && (
                        <IconAction
                          label={`Stop trusting ${entry.address}`}
                          onClick={() => remove(entry.address, entry.origin === "you")}
                        >
                          <Trash aria-hidden />
                        </IconAction>
                      )}
                    </DimActions>
                  ) : (
                    // The width of the action it does not have, so the origin
                    // words of a column of rows line up.
                    <span aria-hidden className="size-8 shrink-0" />
                  )}
                </>
              }
            />
          )
        })}
      </RowList>
      {editing && (
        <TrustedNoteModal
          entry={editing}
          onOpenChange={(open) => !open && setEditing(undefined)}
          onSaved={onChanged}
        />
      )}
      {dialog}
    </div>
  )
}

/** Why an address is kept, and until when. An expiry asks for the destructive capability. */
function TrustedNoteModal({
  entry,
  onOpenChange,
  onSaved,
}: {
  entry: ProtectionTrusted
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const { can } = useAuth()
  const [reason, setReason] = useState(entry.reason ?? "")
  const [expiry, setExpiry] = useState("never")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const submit = async () => {
    setBusy(true)
    setError(undefined)
    try {
      await put("/network/protection/trusted", {
        address: entry.address,
        reason: reason.trim(),
        expiresAt: expiryFrom(expiry),
        confirm: true,
      })
      notify.success(`${entry.address} is saved`)
      onOpenChange(false)
      onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  const expires = expiry !== "never"
  return (
    <Modal
      open
      onOpenChange={(next) => !busy && onOpenChange(next)}
      title={<span className="font-mono">{entry.address}</span>}
      description="Why this address is trusted, and until when"
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button
            type="submit"
            form="trusted-form"
            disabled={busy || (expires && !can("destructive"))}
            pending={busy}
          >
            Save
          </Button>
        </>
      }
    >
      <form
        id="trusted-form"
        className="flex min-w-0 flex-col gap-5"
        onSubmit={(event) => {
          event.preventDefault()
          if (!busy) void submit()
        }}
      >
        <FieldRow>
          <Field label="Why" htmlFor="trusted-reason" hint="Who uses it: home, office, CI">
            <Input
              id="trusted-reason"
              value={reason}
              placeholder="Office network"
              onChange={(event) => setReason(event.target.value)}
              autoComplete="off"
            />
          </Field>
          <Field
            label="Trust ends"
            htmlFor="trusted-expiry"
            hint="After it, a list or a limit may refuse it"
          >
            <Select value={expiry} onValueChange={setExpiry}>
              <SelectTrigger id="trusted-expiry" className="w-full">
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
        <p className="text-hint text-muted-foreground">
          Saving also records that you reviewed it today.
        </p>
        {error && (
          <p role="alert" className="animate-rise text-body text-destructive">
            {error}
          </p>
        )}
      </form>
    </Modal>
  )
}
