"use client"

import { useState } from "react"
import { errorMessage, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { GitIdentity } from "@/lib/types"
import { Modal } from "@/components/modal"
import { Field, FormNote } from "@/components/form"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

/**
 * Who commits made in this repository are recorded as.
 *
 * git refuses to commit without a name and an address, and it says so only
 * after the message has been written — from a terminal that is a one-line
 * fix, from a web page it was a dead end. The values go into this
 * repository's own configuration, so an account-wide identity set elsewhere
 * is not overruled for every other checkout at once.
 */
type IdentityDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  repoPath: string
  identity?: GitIdentity
  onSaved: () => void
}

export function IdentityDialog(props: IdentityDialogProps) {
  // A fresh body per opening, so the fields start from what git currently
  // records rather than from whatever was typed last time.
  const key = `${props.open}:${props.identity?.name ?? ""}:${props.identity?.email ?? ""}`
  return <IdentityDialogBody key={key} {...props} />
}

function IdentityDialogBody({ open, onOpenChange, repoPath, identity, onSaved }: IdentityDialogProps) {
  const [name, setName] = useState(identity?.name ?? "")
  const [email, setEmail] = useState(identity?.email ?? "")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  const save = async () => {
    setBusy(true)
    setError(undefined)
    try {
      await post("/git/identity", { name: name.trim(), email: email.trim() }, { query: { path: repoPath } })
      notify.success("Commits here are recorded as " + name.trim())
      onSaved()
      onOpenChange(false)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const valid = name.trim().length > 0 && email.includes("@")

  return (
    <Modal
      open={open}
      onOpenChange={(next) => !busy && onOpenChange(next)}
      title="Who commits here"
      size="sm"
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={save} disabled={busy || !valid} pending={busy}>
            Save
          </Button>
        </>
      }
    >
      <form
        className="space-y-3"
        onSubmit={(e) => {
          e.preventDefault()
          if (valid) void save()
        }}
      >
        {error && (
          <Notice title="git refused" tone="danger">
            {error}
          </Notice>
        )}
        <Field label="Name" htmlFor="git-id-name" hint="As it appears on every commit.">
          <Input
            id="git-id-name"
            autoFocus
            autoComplete="name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Ada Lovelace"
          />
        </Field>
        <Field
          label="Email"
          htmlFor="git-id-email"
          hint="GitHub links commits to an account by this address; its no-reply form keeps a personal one private."
        >
          <Input
            id="git-id-email"
            type="email"
            autoComplete="email"
            spellCheck={false}
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder="12345+ada@users.noreply.github.com"
            className="font-mono"
          />
        </Field>
        <FormNote>Written into this repository only. Other checkouts keep their own.</FormNote>
      </form>
    </Modal>
  )
}
