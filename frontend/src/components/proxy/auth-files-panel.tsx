"use client"

import { useState } from "react"
import { Key, Plus, Trash, UserMinus } from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, get, post } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { AuthFile } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { Field } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ROW_BLEED } from "@/components/row-list"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Tag } from "@/components/tag"
import { Modal } from "@/components/modal"
import { VerbActions } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

/**
 * Passwords for the site form's basic-auth option.
 *
 * The field existed and there was nothing to put in it, which made the feature
 * useless from here: pointing at /etc/nginx/.htpasswd only helps somebody who
 * has already been to a terminal and run htpasswd — the thing this page exists
 * to avoid. Putting a staging site behind a password is one of the two or
 * three commonest reasons to reach for a reverse proxy at all.
 */
export function AuthFilesPanel() {
  const { confirm, dialog } = useConfirm()
  const { data, error, loading, refresh } = usePoll<AuthFile[]>(
    (signal) => get("/proxy/auth-files/", undefined, signal),
    0,
  )

  const removeUser = (file: AuthFile, user: string) =>
    confirm({
      title: `Remove ${user}`,
      confirmLabel: "Remove",
      description: (
        <p>
          <b>{user}</b> can no longer sign in to any site using <b>{file.name}</b>. Removing the
          last login leaves the file in place, admitting nobody.
        </p>
      ),
      action: async () => {
        await del(
          `/proxy/auth-files/${encodeURIComponent(file.name)}/users/${encodeURIComponent(user)}`,
        )
        refresh()
      },
    })

  const removeFile = (file: AuthFile) =>
    confirm({
      title: `Delete ${file.name}`,
      confirmLabel: "Delete",
      description: (
        <p className="text-destructive">
          Any site pointing at this file stops nginx from starting at its next reload. Change those
          sites first.
        </p>
      ),
      action: async () => {
        await del(`/proxy/auth-files/${encodeURIComponent(file.name)}`)
        refresh()
      },
    })

  return (
    <>
      <Panel plain>
        <PanelHeader
          title="Password files"
          actions={<AuthUserDialog files={data ?? []} onDone={refresh} />}
        />
        <PanelBody flush>
          {loading ? (
            <LoadingRows rows={2} />
          ) : error ? (
            <ErrorState error={error} />
          ) : !data?.length ? (
            <EmptyState
              icon={Key}
              title="No password files yet"
              description="Add a login, then choose the file in a site's password field to put that site behind it."
              className="mt-2"
            />
          ) : (
            <ul className="animate-rise divide-y divide-hairline">
              {data.map((file) => (
                <li
                  key={file.name}
                  className={cn(
                    "group flex min-w-0 items-start gap-3 py-3 transition-colors hover:bg-row-hover",
                    ROW_BLEED,
                  )}
                >
                  <div className="min-w-0 flex-1 space-y-1.5">
                    <div className="flex min-w-0 flex-wrap items-baseline gap-x-2">
                      <span className="text-body font-medium">{file.name}</span>
                      <span className="truncate font-mono text-hint text-muted-foreground">
                        {file.path}
                      </span>
                    </div>
                    <div className="flex flex-wrap items-center gap-1.5">
                      {file.users.length === 0 ? (
                        <span className="text-hint text-muted-foreground">
                          Empty — this file admits nobody.
                        </span>
                      ) : (
                        file.users.map((user) => (
                          <Tag key={user} mono className="gap-1 pr-0.5">
                            {user}
                            <IconAction
                              label={`Remove ${user}`}
                              size="icon-xs"
                              className="size-4 text-muted-foreground hover:text-destructive [&_svg:not([class*='size-'])]:size-2.5"
                              onClick={() => removeUser(file, user)}
                            >
                              <UserMinus />
                            </IconAction>
                          </Tag>
                        ))
                      )}
                    </div>
                  </div>
                  <VerbActions
                    dim
                    className="shrink-0"
                    verbs={[
                      {
                        key: "delete",
                        label: "Delete file",
                        icon: Trash,
                        danger: true,
                        run: () => removeFile(file),
                      },
                    ]}
                  />
                </li>
              ))}
            </ul>
          )}
        </PanelBody>
      </Panel>
      {dialog}
    </>
  )
}

function AuthUserDialog({ files, onDone }: { files: AuthFile[]; onDone: () => void }) {
  const [open, setOpen] = useState(false)
  const [file, setFile] = useState("")
  const [user, setUser] = useState("")
  const [password, setPassword] = useState("")
  const [busy, setBusy] = useState(false)

  const submit = async () => {
    setBusy(true)
    try {
      await post("/proxy/auth-files/", { file: file.trim(), user: user.trim(), password })
      notify.success(`${user} can sign in to ${file}`)
      setOpen(false)
      setUser("")
      setPassword("")
      onDone()
    } catch (err) {
      notify.error("Not saved", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <Button size="sm" variant="outline" onClick={() => setOpen(true)}>
        <Plus className="size-4" />
        Add login
      </Button>
      <Modal
        open={open}
        onOpenChange={setOpen}
        size="sm"
        title="Add a login"
        description="Creates the password file if it does not exist, and replaces the entry if the user is already in it."
        footer={
          <Button
            onClick={submit}
            disabled={busy || !file.trim() || !user.trim() || password.length < 8}
            pending={busy}
          >
            Save
          </Button>
        }
      >
        <div className="grid gap-4">
          <Field
            label="File"
            htmlFor="auth-file"
            hint="A new name creates the file; an existing one adds to it."
          >
            <Input
              id="auth-file"
              value={file}
              onChange={(e) => setFile(e.target.value)}
              list="auth-file-names"
              placeholder="staging"
              className="font-mono text-xs"
            />
            <datalist id="auth-file-names">
              {files.map((f) => (
                <option key={f.name} value={f.name} />
              ))}
            </datalist>
          </Field>
          <Field label="User" htmlFor="auth-user">
            <Input id="auth-user" value={user} onChange={(e) => setUser(e.target.value)} />
          </Field>
          <Field
            label="Password"
            htmlFor="auth-password"
            hint="At least 8 characters, at most 72 — bcrypt truncates anything longer, which would quietly make it a different password from the one you typed."
          >
            <Input
              id="auth-password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="new-password"
            />
          </Field>
        </div>
      </Modal>
    </>
  )
}
