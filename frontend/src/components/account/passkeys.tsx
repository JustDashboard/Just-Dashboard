"use client"

import { useState } from "react"
import { Fingerprint, Pencil, Plus, Trash } from "@/components/icons"
import { ApiError, del, get, patch } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { Passkey } from "@/lib/types"
import { registerPasskey, suggestedPasskeyName, useHostname, usePasskeyPlace } from "@/lib/passkeys"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { Field, FormNote, FormSection } from "@/components/form"
import { Modal } from "@/components/modal"
import { Row, RowList } from "@/components/row-list"
import { ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { useConfirm } from "@/components/confirm-dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { VerbActions, type Verb } from "@/components/verbs"

/**
 * Passkeys: what can sign this account in instead of its password. Each is a
 * row with when it was added and last signed in, whether a password manager
 * keeps it synced, and — for one made at another name of this dashboard —
 * where it works, since a browser offers a passkey only at the name it was
 * made at. Adding one asks for the password and the code again; a passkey
 * never stands in for the code.
 */
export function PasskeysSection({ className }: { className?: string }) {
  const { status } = useAuth()
  const keys = usePoll((signal) => get<Passkey[]>("/account/passkeys", undefined, signal), 30000)
  const place = usePasskeyPlace()
  const hostname = useHostname()
  const [adding, setAdding] = useState(false)
  const [renaming, setRenaming] = useState<Passkey>()
  const { confirm, dialog } = useConfirm()
  const list = keys.data ?? []
  const totp = Boolean(status?.user?.totpEnabled)

  const remove = (key: Passkey) =>
    confirm({
      title: `Remove ${key.name}`,
      confirmLabel: "Remove passkey",
      description: (
        <p>
          It no longer signs in here. The credential stays on the device or in the password manager
          until it is deleted there too.
        </p>
      ),
      action: async () => {
        await del(`/account/passkeys/${key.id}`)
      },
      onDone: () => keys.refresh(),
    })

  const verbs = (key: Passkey): Verb[] => [
    { key: "rename", label: "Rename", icon: Pencil, run: () => setRenaming(key) },
    { key: "remove", label: "Remove", icon: Trash, danger: true, run: () => remove(key) },
  ]

  return (
    <FormSection
      aside
      className={className}
      title="Passkeys"
      hint={
        <span className="flex flex-col items-start gap-1.5">
          {/* "none" is an answer, so it waits for one: an unread list may hold
              the only way this account signs in without a password. */}
          {keys.data ? (
            <Status
              verdict={list.length > 0 ? "ok" : "notice"}
              label={list.length > 0 ? plural(list.length, "passkey") : "none"}
            />
          ) : (
            <Status tone="unknown" label={keys.error ? "could not read" : "reading"} />
          )}
          <span>
            {totp
              ? "In place of the password. The code is still asked for."
              : "In place of the password."}
          </span>
        </span>
      }
    >
      {keys.error && !keys.data && (
        <ErrorState error={keys.error} onRetry={keys.refresh} retryable />
      )}
      {keys.loading && !keys.data && <LoadingRows rows={1} />}
      {list.length > 0 && (
        <RowList aria-label="Passkeys">
          {list.map((key) => (
            <Row
              key={key.id}
              leading={<Fingerprint aria-hidden className="size-4 text-muted-foreground" />}
              title={key.name}
              subtitle={passkeyLine(key, hostname)}
              trailing={
                <VerbActions dim verbs={verbs(key)} menuLabel={`Actions for ${key.name}`} />
              }
            />
          ))}
        </RowList>
      )}
      {place && !place.usable ? (
        <FormNote>{place.reason}</FormNote>
      ) : (
        <div>
          <Button size="sm" variant="outline" disabled={!place} onClick={() => setAdding(true)}>
            <Plus className="size-3.5" />
            Add a passkey
          </Button>
        </div>
      )}
      {/* Mounted only while open, so each opening starts from a clean form. */}
      {adding && (
        <AddPasskey
          totp={totp}
          onOpenChange={setAdding}
          onAdded={() => {
            setAdding(false)
            keys.refresh()
          }}
        />
      )}
      {renaming && (
        <RenamePasskey
          passkey={renaming}
          onClose={() => setRenaming(undefined)}
          onRenamed={() => {
            setRenaming(undefined)
            keys.refresh()
          }}
        />
      )}
      {dialog}
    </FormSection>
  )
}

/** One passkey's line: added, last used, synced, and where it works if not here. */
export function passkeyLine(key: Passkey, hostname: string) {
  const parts = [`added ${relativeTime(key.createdAt)}`]
  const used = Date.parse(key.lastUsedAt)
  parts.push(used > 0 ? `last signed in ${relativeTime(key.lastUsedAt)}` : "never used yet")
  if (key.synced) parts.push("synced")
  if (hostname && key.rpId !== hostname) parts.push(`signs in at ${key.rpId}`)
  return parts.join(" · ")
}

function AddPasskey({
  totp,
  onOpenChange,
  onAdded,
}: {
  totp: boolean
  onOpenChange: (open: boolean) => void
  onAdded: () => void
}) {
  const [name, setName] = useState(() => suggestedPasskeyName(navigator.userAgent))
  const [password, setPassword] = useState("")
  const [code, setCode] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    setBusy(true)
    setError(undefined)
    try {
      const key = await registerPasskey({ password, code, name: name.trim() })
      notify.success(`${key.name} added`, {
        description: totp
          ? "Sign in with it in place of your password; the code is still asked for."
          : "Sign in with it in place of your password.",
      })
      onAdded()
    } catch (err) {
      if (err instanceof DOMException && err.name === "NotAllowedError") {
        setError("The browser's prompt was closed, or it timed out. Nothing was saved.")
      } else if (err instanceof DOMException && err.name === "InvalidStateError") {
        setError("This device already holds a passkey for this account.")
      } else {
        setError(err instanceof ApiError || err instanceof Error ? err.message : String(err))
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open
      onOpenChange={onOpenChange}
      title="Add a passkey"
      description="Confirm it is you, then follow the browser's prompt."
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button
            type="submit"
            form="add-passkey"
            pending={busy}
            disabled={!name.trim() || !password || (totp && code.length < 6)}
          >
            Continue
          </Button>
        </>
      }
    >
      <form id="add-passkey" onSubmit={submit} className="flex flex-col gap-4">
        <Field label="Name" htmlFor="passkey-name" hint="How it is listed here.">
          <Input
            id="passkey-name"
            value={name}
            maxLength={64}
            onChange={(event) => setName(event.target.value)}
          />
        </Field>
        <Field label="Current password" htmlFor="passkey-password">
          <Input
            id="passkey-password"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
          />
        </Field>
        {totp && (
          <Field label="Code from your authenticator" htmlFor="passkey-code">
            <Input
              id="passkey-code"
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={6}
              placeholder="000000"
              className="max-w-48 text-center font-mono tracking-[0.4em]"
              value={code}
              onChange={(event) => setCode(event.target.value)}
            />
          </Field>
        )}
        {error && (
          <FormNote tone="danger" role="alert">
            {error}
          </FormNote>
        )}
      </form>
    </Modal>
  )
}

function RenamePasskey({
  passkey,
  onClose,
  onRenamed,
}: {
  passkey: Passkey
  onClose: () => void
  onRenamed: () => void
}) {
  const [name, setName] = useState(passkey.name)
  const [busy, setBusy] = useState(false)
  const save = async (event: React.FormEvent) => {
    event.preventDefault()
    setBusy(true)
    try {
      await patch(`/account/passkeys/${passkey.id}`, { name: name.trim() })
      onRenamed()
    } catch (err) {
      notify.error("Could not rename it", err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      open
      onOpenChange={(next) => !next && onClose()}
      title="Rename passkey"
      size="sm"
      footer={
        <>
          <Button variant="outline" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button type="submit" form="rename-passkey" pending={busy} disabled={!name.trim()}>
            Save
          </Button>
        </>
      }
    >
      <form id="rename-passkey" onSubmit={save}>
        <Field label="Name" htmlFor="passkey-rename">
          <Input
            id="passkey-rename"
            value={name}
            maxLength={64}
            onChange={(event) => setName(event.target.value)}
          />
        </Field>
      </form>
    </Modal>
  )
}
