"use client"

import { useState } from "react"
import Link from "next/link"
import { Copy, Eye, Key, Plus, Sparkles, Trash, UserMinus } from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, get, post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { cn } from "@/lib/utils"
import { plural } from "@/lib/format"
import type { AuthFile } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Field, FormFact, FormFacts, FormSection } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Modal } from "@/components/modal"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { VerbMenu } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
  InputGroupToggle,
} from "@/components/ui/input-group"
import { siteSettingsHref } from "@/components/proxy/site-tabs"

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
 *
 * A file is one line: its name over where it is, how many logins it holds
 * and which sites name it. Its logins — each with a new password or a removal
 * a press away — are in the sheet the row opens, where a block of them under
 * every file made a list of three files a screen tall.
 */
export function AuthFilesPanel() {
  const { confirm, dialog } = useConfirm()
  const [target, setTarget] = useState<LoginTarget | null>(null)
  // The file whose sheet is open, by name, so the sheet follows the list as
  // logins come and go.
  const [opened, setOpened] = useState<string | null>(null)
  const { data, error, loading, refresh } = usePoll<AuthFile[]>(
    (signal) => get("/proxy/auth-files/", undefined, signal),
    0,
  )
  const open = data?.find((file) => file.name === opened)

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
        setOpened(null)
        refresh()
      },
    })
  }
  const addLogin = (file = "") => setTarget({ file, user: "", change: false })

  return (
    <>
      <Panel plain aria-label="Password files">
        <PanelHeader
          title="Password files"
          actions={
            <>
              {data && data.length > 0 && (
                <span className="numeric text-hint text-muted-foreground">
                  {plural(data.length, "file")}
                </span>
              )}
              <Button size="xs" variant="outline" onClick={() => addLogin()}>
                <Plus className="size-3.5" />
                Add login
              </Button>
            </>
          }
        />
        <PanelBody flush className="pt-3">
          {loading ? (
            <LoadingRows rows={2} />
          ) : error ? (
            <ErrorState error={error} />
          ) : !data?.length ? (
            <EmptyState
              icon={Key}
              title="No password files yet"
              description="Add a login, then choose the file in a site's password field."
            />
          ) : (
            <ChoiceList aria-label="Password files" className="animate-rise">
              {data.map((file) => (
                <ChoiceRow
                  key={file.name}
                  onSelect={() => setOpened(file.name)}
                  verb={`Open ${file.name}`}
                  leading={<ProductLogo id={undefined} size="sm" fallback={Key} />}
                  title={file.name}
                  description={<span className="font-mono">{file.path}</span>}
                  trailing={
                    <>
                      <span
                        className={cn(
                          "numeric w-20 text-hint max-sm:hidden",
                          file.users.length === 0 ? "text-warning" : "text-muted-foreground",
                        )}
                      >
                        {file.users.length === 0 ? "no logins" : plural(file.users.length, "login")}
                      </span>
                      <span
                        className="w-40 truncate text-right text-hint text-muted-foreground max-md:hidden"
                        title={file.usedBy.join(", ")}
                      >
                        {usedByWords(file.usedBy)}
                      </span>
                    </>
                  }
                  actions={
                    <VerbMenu
                      label={`More actions for ${file.name}`}
                      verbs={[
                        {
                          key: "add",
                          label: "Add login",
                          icon: Plus,
                          run: () => addLogin(file.name),
                        },
                        {
                          key: "delete",
                          label: "Delete file",
                          icon: Trash,
                          danger: true,
                          run: () => removeFile(file),
                        },
                      ]}
                    />
                  }
                />
              ))}
            </ChoiceList>
          )}
        </PanelBody>
      </Panel>
      <AuthFileSheet
        file={open}
        onClose={() => setOpened(null)}
        onAdd={(file) => addLogin(file.name)}
        onChange={(file, user) => setTarget({ file: file.name, user, change: true })}
        onRemove={removeUser}
        onDelete={removeFile}
      />
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

/** Which sites name a file, as the row has room to say it. */
function usedByWords(sites: string[]) {
  if (sites.length === 0) return "no site names it"
  if (sites.length === 1) return sites[0]
  return plural(sites.length, "site")
}

/**
 * One password file: where it is, the logins in it — each with a new
 * password or a removal — and the sites that name it, each a way to that
 * site's access settings.
 */
function AuthFileSheet({
  file,
  onClose,
  onAdd,
  onChange,
  onRemove,
  onDelete,
}: {
  file: AuthFile | undefined
  onClose: () => void
  onAdd: (file: AuthFile) => void
  onChange: (file: AuthFile, user: string) => void
  onRemove: (file: AuthFile, user: string) => void
  onDelete: (file: AuthFile) => void
}) {
  return (
    <SidePanel
      open={file !== undefined}
      onOpenChange={(open) => !open && onClose()}
      width="sm"
      initialFocus="body"
      title={
        <>
          <ProductLogo id={undefined} size="sm" fallback={Key} />
          {file?.name}
        </>
      }
      description="The logins in this password file and the sites that name it"
      footer={
        file && (
          <>
            <Button
              variant="outline"
              className="mr-auto text-destructive"
              onClick={() => onDelete(file)}
            >
              <Trash className="size-4" />
              Delete file
            </Button>
            <Button onClick={() => onAdd(file)}>
              <Plus className="size-4" />
              Add login
            </Button>
          </>
        )
      }
    >
      {file && (
        <div className="space-y-6">
          <FormFacts>
            <FormFact label="File" mono>
              {file.path}
            </FormFact>
          </FormFacts>
          <FormSection title="Logins">
            {file.users.length === 0 ? (
              <p className="text-body text-muted-foreground">Empty: this file admits nobody.</p>
            ) : (
              <ul aria-label={`Logins in ${file.name}`} className="divide-y divide-hairline">
                {file.users.map((user) => (
                  <li key={user} className="flex min-h-11 min-w-0 items-center gap-2">
                    <span className="min-w-0 flex-1 truncate font-mono text-body">{user}</span>
                    <IconAction
                      label={`Change the password of ${user}`}
                      onClick={() => onChange(file, user)}
                    >
                      <Key />
                    </IconAction>
                    <IconAction
                      label={`Remove ${user}`}
                      className="hover:text-destructive"
                      onClick={() => onRemove(file, user)}
                    >
                      <UserMinus />
                    </IconAction>
                  </li>
                ))}
              </ul>
            )}
          </FormSection>
          <FormSection title="Used by">
            {file.usedBy.length === 0 ? (
              <p className="text-body text-muted-foreground">No site names this file.</p>
            ) : (
              <ul aria-label={`Sites using ${file.name}`} className="divide-y divide-hairline">
                {file.usedBy.map((site) => (
                  <li key={site} className="flex min-h-11 min-w-0 items-center">
                    <Link
                      href={siteSettingsHref(site, "access")}
                      className="min-w-0 truncate rounded-sm font-mono text-body focus-ring hover:underline"
                    >
                      {site}
                    </Link>
                  </li>
                ))}
              </ul>
            )}
          </FormSection>
        </div>
      )}
    </SidePanel>
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
          hint="8 to 72 characters."
          info="bcrypt truncates anything longer than 72, which would quietly make it a different password from the one you typed."
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
