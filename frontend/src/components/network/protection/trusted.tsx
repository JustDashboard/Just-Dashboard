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
import { DimActions } from "@/components/icon-action"
import { Modal } from "@/components/modal"
import { Row, RowList } from "@/components/row-list"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { VerbMenu, type Verb } from "@/components/verbs"
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
import { ROW_HEIGHT } from "@/components/network/protection/exceptions"
import {
  EXPIRY_CHOICES,
  KEEP_EXPIRY,
  ORIGIN_WORD,
  trustedExpiry,
  trustedExpiryChoice,
  trustedNoteWord,
  trustedState,
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
  const [confirming, setConfirming] = useState<string>()
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
        return "reported"
      },
    })
  const confirmStill = async (entry: ProtectionTrusted) => {
    // One review per press: a second press while the first is out sent a second.
    if (confirming) return
    setConfirming(entry.address)
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
    } finally {
      setConfirming(undefined)
    }
  }
  const verbs = (entry: ProtectionTrusted): Verb[] => [
    {
      key: "confirm",
      label: "Confirm still needed",
      icon: Check,
      disabled: confirming !== undefined,
      run: () => void confirmStill(entry),
    },
    { key: "edit", label: "Edit why and until when", icon: Pencil, run: () => setEditing(entry) },
    ...(can("destructive")
      ? [
          {
            key: "remove",
            label: "Stop trusting",
            icon: Trash,
            danger: true,
            run: () => remove(entry.address, entry.origin === "you"),
          },
        ]
      : []),
  ]
  const stale = view.trusted.filter((t) => t.stale).length
  const admin = can("system.admin")
  return (
    <div className="flex min-w-0 flex-col gap-4">
      {!view.clientTrusted && (
        <Notice tone="warning" icon={Warning} title="Your own address is not trusted">
          <span className="font-mono text-foreground">{view.client}</span> is in no entry below, so
          a blocklist or a limit that matches it would cut you off.
        </Notice>
      )}
      {stale > 0 && (
        <Notice
          tone="warning"
          icon={Warning}
          title={`${stale} kept ${stale === 1 ? "address is" : "addresses are"} stale`}
        >
          Nobody has signed in from {stale === 1 ? "it" : "them"} or confirmed{" "}
          {stale === 1 ? "it" : "them"} in thirty days.
        </Notice>
      )}
      <RowList>
        {view.trusted.map((entry) => {
          const note = entry.removable ? trustedNoteWord(entry, relativeTime) : undefined
          const state = trustedState(entry)
          return (
            <Row
              key={entry.address}
              className={ROW_HEIGHT}
              title={<Cidr cidr={entry.address} />}
              subtitle={note || undefined}
              trailing={
                <>
                  {state && <Status tone={state.tone} label={state.label} />}
                  <span className="w-24 text-right text-hint text-muted-foreground">
                    {ORIGIN_WORD[entry.origin]}
                  </span>
                  {admin &&
                    (entry.removable ? (
                      <DimActions>
                        <VerbMenu verbs={verbs(entry)} label={`${entry.address} actions`} />
                      </DimActions>
                    ) : (
                      // The width of the menu it does not have, so the origin
                      // words of a column of rows line up.
                      <span aria-hidden className="size-7 shrink-0" />
                    ))}
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
  // An address that already ends opens on its own end, so a note saved
  // without touching it leaves the end where it was.
  const [expiry, setExpiry] = useState(() => trustedExpiryChoice(entry))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const submit = async () => {
    setBusy(true)
    setError(undefined)
    try {
      await put("/network/protection/trusted", {
        address: entry.address,
        reason: reason.trim(),
        expiresAt: trustedExpiry(entry, expiry),
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
                {entry.expiresAt && !entry.expired && (
                  <SelectItem value={KEEP_EXPIRY}>
                    As it is: {relativeTime(entry.expiresAt)}
                  </SelectItem>
                )}
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
