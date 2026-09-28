"use client"

import { useState } from "react"
import { Copy, Eye, Key, Plus, Sparkles, Trash, UserMinus } from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, get, post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { cn } from "@/lib/utils"
import type { AuthFile } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { Field, FormSection, FormSections } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { ROW_BLEED } from "@/components/row-list"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Tag } from "@/components/tag"
import { Modal } from "@/components/modal"
import { VerbActions } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
  InputGroupToggle,
} from "@/components/ui/input-group"

/** Who the login dialog is for: a new login, or a password change for one already there. */
type LoginTarget = { file: string; user: string; change: boolean }

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
  const [target, setTarget] = useState<LoginTarget | null>(null)
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

  // The server refuses a file a site names unless told to go ahead, so the
  // sites are named here and going ahead is the confirmation.
  const removeFile = (file: AuthFile) => {
    const inUse = file.usedBy.length > 0
    return confirm({
      title: `Delete ${file.name}`,
      confirmLabel: inUse ? "Delete anyway" : "Delete",
      description: inUse ? (
        <p className="text-destructive">
          {file.usedBy.join(", ")} {file.usedBy.length === 1 ? "names" : "name"} this file. nginx
          keeps running without it, and every login to{" "}
          {file.usedBy.length === 1 ? "that site" : "those sites"} is refused. Point them at another
          file first.
        </p>
      ) : (
        <p>No site names this file. Its logins are gone for good once it is deleted.</p>
      ),
      action: async () => {
        await del(`/proxy/auth-files/${encodeURIComponent(file.name)}${inUse ? "?force=1" : ""}`)
        refresh()
      },
    })
  }

  return (
    <>
      <FormSections>
        <FormSection
          aside
          title="Password files"
          hint={`${data?.length ?? 0} files for HTTP basic authentication`}
          actions={
            <Button
              size="sm"
              variant="outline"
              onClick={() => setTarget({ file: "", user: "", change: false })}
            >
              <Plus className="size-4" />
              Add login
            </Button>
          }
        >
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
                              label={`Change the password of ${user}`}
                              size="icon-xs"
                              className="size-4 text-muted-foreground hover:text-foreground [&_svg:not([class*='size-'])]:size-2.5"
                              onClick={() => setTarget({ file: file.name, user, change: true })}
                            >
                              <Key />
                            </IconAction>
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
                    <div className="flex flex-wrap items-center gap-1.5">
                      {file.usedBy.length === 0 ? (
                        <span className="text-hint text-muted-foreground">
                          No site names this file.
                        </span>
                      ) : (
                        <>
                          <span className="text-hint text-muted-foreground">Used by</span>
                          {file.usedBy.map((site) => (
                            <Tag key={site} mono>
                              {site}
                            </Tag>
                          ))}
                        </>
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
        </FormSection>
      </FormSections>
      {target && (
        <AuthUserDialog
          key={`${target.file}:${target.user}`}
          target={target}
          files={data ?? []}
          onClose={() => setTarget(null)}
          onDone={refresh}
        />
      )}
      {dialog}
    </>
  )
}

/**
 * 20 characters from 15 random bytes, base64url: 120 bits, well inside bcrypt's
 * 72-byte limit, and nothing in it a shell or a URL would need quoted.
 */
function generatePassword() {
  const bytes = new Uint8Array(15)
  crypto.getRandomValues(bytes)
  return btoa(String.fromCharCode(...bytes))
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
}

function AuthUserDialog({
  target,
  files,
  onClose,
  onDone,
}: {
  target: LoginTarget
  files: AuthFile[]
  onClose: () => void
  onDone: () => void
}) {
  const [file, setFile] = useState(target.file)
  const [user, setUser] = useState(target.user)
  const [password, setPassword] = useState("")
  const [shown, setShown] = useState(false)
  const [busy, setBusy] = useState(false)

  const generate = () => {
    setPassword(generatePassword())
    // A generated password nobody can see is one nobody can hand on.
    setShown(true)
  }

  const submit = async () => {
    setBusy(true)
    try {
      await post("/proxy/auth-files/", { file: file.trim(), user: user.trim(), password })
      notify.success(
        target.change
          ? `The password of ${user} in ${file} is changed`
          : `${user} can sign in to ${file}`,
      )
      onClose()
      onDone()
    } catch (err) {
      notify.error("Not saved", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open
      onOpenChange={(open) => !open && onClose()}
      size="sm"
      title={target.change ? `Change the password of ${target.user}` : "Add a login"}
      description={
        target.change
          ? `The old password stops working for every site using ${target.file} as soon as this is saved.`
          : "Creates the password file if it does not exist, and replaces the entry if the user is already in it."
      }
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
        {!target.change && (
          <>
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
          </>
        )}
        <Field
          label={target.change ? "New password" : "Password"}
          htmlFor="auth-password"
          hint="At least 8 characters, at most 72 — bcrypt truncates anything longer, which would quietly make it a different password from the one you typed."
        >
          <InputGroup>
            <InputGroupInput
              id="auth-password"
              type={shown ? "text" : "password"}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="new-password"
              className={cn(shown && "font-mono")}
            />
            <InputGroupAddon align="inline-end" className="gap-0 p-0">
              <InputGroupToggle
                icon={Eye}
                label="Show"
                aria-label="Show the password"
                pressed={shown}
                onPressedChange={setShown}
              />
              <InputGroupButton aria-label="Generate a 20-character password" onClick={generate}>
                <Sparkles className="size-3.5" />
                <span className="max-sm:hidden">Generate</span>
              </InputGroupButton>
              <InputGroupButton
                aria-label="Copy the password"
                disabled={!password}
                onClick={() => void copyText(password, "Password copied")}
              >
                <Copy className="size-3.5" />
                <span className="max-sm:hidden">Copy</span>
              </InputGroupButton>
            </InputGroupAddon>
          </InputGroup>
        </Field>
      </div>
    </Modal>
  )
}
