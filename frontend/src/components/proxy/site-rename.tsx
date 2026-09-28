"use client"

import { useState } from "react"
import { errorMessage } from "@/lib/api"
import type { VHost } from "@/lib/types"
import { Modal } from "@/components/modal"
import { Field, FormFact, FormFacts } from "@/components/form"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

const SITE_NAME = /^[a-z0-9][a-z0-9._-]{0,63}$/

/**
 * Renames a site: a new name, and one change on the host behind nginx's
 * test. A refusal stays in the dialog with nginx's reason, since the name
 * typed is what the operator would change to try again.
 */
export function SiteRenameDialog({
  vhost,
  onOpenChange,
  onRename,
}: {
  vhost: VHost | null
  onOpenChange: (open: boolean) => void
  onRename: (vhost: VHost, to: string) => Promise<void>
}) {
  // Keyed per site, so reopening for another one starts from its name.
  return (
    <SiteRenameBody
      key={vhost?.name ?? ""}
      vhost={vhost}
      onOpenChange={onOpenChange}
      onRename={onRename}
    />
  )
}

function SiteRenameBody({
  vhost,
  onOpenChange,
  onRename,
}: {
  vhost: VHost | null
  onOpenChange: (open: boolean) => void
  onRename: (vhost: VHost, to: string) => Promise<void>
}) {
  const confd = vhost?.layout === "conf.d"
  const [name, setName] = useState(vhost ? current(vhost) : "")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const value = name.trim()
  const invalid = value !== "" && !SITE_NAME.test(value)
  const unchanged = vhost !== null && value === current(vhost)

  const submit = async () => {
    if (!vhost || !value || invalid || unchanged) return
    setBusy(true)
    setError(undefined)
    try {
      await onRename(vhost, value)
      onOpenChange(false)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={vhost !== null}
      onOpenChange={(next) => !busy && onOpenChange(next)}
      title={`Rename ${vhost?.name ?? "site"}`}
      size="sm"
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={busy || !value || invalid || unchanged} pending={busy}>
            Rename and reload
          </Button>
        </>
      }
    >
      <form
        className="space-y-3"
        onSubmit={(e) => {
          e.preventDefault()
          void submit()
        }}
      >
        {vhost && (
          <FormFacts>
            <FormFact label="File" mono>
              {vhost.path}
            </FormFact>
          </FormFacts>
        )}
        {error && (
          <Notice title="Not renamed" tone="danger">
            <span className="break-words whitespace-pre-wrap">{error}</span>
          </Notice>
        )}
        <Field
          label="New name"
          htmlFor="site-rename"
          error={
            invalid
              ? "Lower-case letters, digits, dots, dashes and underscores, up to 64."
              : undefined
          }
          hint={
            confd
              ? "The file keeps its .conf ending, which is what makes nginx read it."
              : "The file and its link in sites-enabled move together; a form-written file's log paths follow the name."
          }
        >
          <Input
            id="site-rename"
            autoFocus
            autoComplete="off"
            spellCheck={false}
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="font-mono"
          />
        </Field>
      </form>
    </Modal>
  )
}

/** The name as typed into the field: a conf.d file's without the suffix the rename keeps. */
function current(vhost: VHost) {
  return vhost.layout === "conf.d" ? vhost.name.replace(/\.conf$/, "") : vhost.name
}
