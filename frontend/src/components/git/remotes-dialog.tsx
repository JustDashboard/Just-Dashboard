"use client"

import { useState } from "react"
import { Pencil, Plus, Trash } from "@/components/icons"
import { errorMessage, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { GitRemote } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { Modal } from "@/components/modal"
import { Field, FieldRow } from "@/components/form"
import { Notice, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { IconAction } from "@/components/icon-action"

/**
 * The remotes this repository pushes to and fetches from. Usually one, called
 * origin; a fork adds a second. The URLs are shown with any embedded
 * credential scrubbed, and a remote can be added or forgotten — nothing on
 * the remote itself changes either way.
 */
export function RemotesDialog({
  open,
  onOpenChange,
  repoPath,
  canControl,
  canDestruct,
  confirm,
  onChanged,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  repoPath: string
  canControl: boolean
  canDestruct: boolean
  confirm: (req: ConfirmRequest) => void
  onChanged: () => void
}) {
  const [name, setName] = useState("")
  const [url, setUrl] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const [editing, setEditing] = useState(false)
  const remotes = usePoll(
    (signal) => get<GitRemote[]>("/git/remotes", { path: repoPath }, signal),
    0,
    [repoPath],
    { enabled: open },
  )

  const add = async () => {
    setBusy(true)
    setError(undefined)
    try {
      await post(
        editing ? "/git/remote/update" : "/git/remote",
        { name: name.trim(), url: url.trim() },
        { query: { path: repoPath } },
      )
      notify.success(`${editing ? "Updated" : "Added"} remote ${name.trim()}`)
      setEditing(false)
      setName("")
      setUrl("")
      remotes.refresh()
      onChanged()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const remove = (r: GitRemote) =>
    confirm({
      title: `Forget remote ${r.name}`,
      confirmLabel: "Forget",
      description: (
        <p>
          <span className="font-mono">{r.name}</span> and its remote-tracking branches are removed
          from this checkout. The repository at{" "}
          <span className="font-mono break-all">{r.fetchUrl}</span> is untouched, and adding the
          remote back is the whole of the undo.
        </p>
      ),
      action: async () => {
        await post("/git/remote/delete", { name: r.name }, { query: { path: repoPath } })
        remotes.refresh()
        onChanged()
      },
    })

  const list = remotes.data ?? []
  const canAdd = name.trim().length > 0 && url.trim().length > 0 && !busy

  return (
    <Modal
      open={open}
      onOpenChange={(next) => !busy && onOpenChange(next)}
      title="Remotes"
      size="md"
      footer={
        <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
          Close
        </Button>
      }
    >
      <div className="space-y-4">
        {remotes.loading && !remotes.data && <LoadingRows rows={2} />}
        {list.length > 0 && (
          <ul className="divide-y divide-hairline rounded-md border border-hairline">
            {list.map((r) => (
              <li key={r.name} className="group flex min-w-0 items-center gap-3 px-3 py-2">
                <span className="min-w-0 flex-1">
                  <span className="block font-mono text-xs font-medium">{r.name}</span>
                  <span className="block truncate font-mono text-hint text-muted-foreground">
                    {r.fetchUrl}
                  </span>
                  {r.pushUrl && (
                    <span className="block truncate font-mono text-hint text-muted-foreground">
                      push {r.pushUrl}
                    </span>
                  )}
                </span>
                {canControl && (
                  <IconAction
                    label={`Edit ${r.name}`}
                    disabled={busy}
                    onClick={() => {
                      setName(r.name)
                      setUrl(r.fetchUrl.includes("***@") ? "" : r.fetchUrl)
                      setEditing(true)
                      setError(undefined)
                    }}
                  >
                    <Pencil />
                  </IconAction>
                )}
                {canDestruct && (
                  <IconAction
                    label={`Forget ${r.name}`}
                    className="text-destructive"
                    onClick={() => remove(r)}
                  >
                    <Trash />
                  </IconAction>
                )}
              </li>
            ))}
          </ul>
        )}
        {remotes.data && list.length === 0 && (
          <p className="text-body text-muted-foreground">
            No remotes. Add one to push this repository somewhere.
          </p>
        )}
        {canControl && (
          <form
            className="space-y-3 border-t border-hairline pt-3"
            onSubmit={(e) => {
              e.preventDefault()
              if (canAdd) void add()
            }}
          >
            {error && (
              <Notice title="git refused" tone="danger">
                {error}
              </Notice>
            )}
            <FieldRow>
              <Field
                label="Name"
                htmlFor="remote-name"
                hint="origin for the main one, upstream for a fork's source."
              >
                <Input
                  id="remote-name"
                  autoComplete="off"
                  spellCheck={false}
                  value={name}
                  disabled={editing || busy}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="origin"
                  className="font-mono"
                />
              </Field>
              <Field
                label="URL"
                htmlFor="remote-url"
                hint="https://, ssh:// or git@host:owner/repo.git."
              >
                <Input
                  id="remote-url"
                  autoComplete="off"
                  spellCheck={false}
                  value={url}
                  onChange={(e) => setUrl(e.target.value)}
                  placeholder="git@github.com:owner/repo.git"
                  className="font-mono"
                />
              </Field>
            </FieldRow>
            <Button type="submit" size="sm" variant="outline" disabled={!canAdd} pending={busy}>
              <Plus className="size-3.5" />
              {editing ? "Save remote" : "Add remote"}
            </Button>
            {editing && (
              <Button
                size="sm"
                variant="ghost"
                disabled={busy}
                onClick={() => {
                  setEditing(false)
                  setName("")
                  setUrl("")
                  setError(undefined)
                }}
              >
                Cancel edit
              </Button>
            )}
          </form>
        )}
      </div>
    </Modal>
  )
}
