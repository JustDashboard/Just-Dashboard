"use client"

import { useState } from "react"
import { Copy, Eye, EyeOff, Key } from "@/components/icons"
import { ApiError, get, put } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { passwordChecks } from "@/lib/password"
import { notify } from "@/lib/toast"
import type { SmartStackLogin, SmartStackState } from "@/lib/types"
import { CopyAction } from "@/components/copy"
import { FieldCheck, FieldChecks, FormNote } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Modal } from "@/components/modal"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { SecretField } from "@/components/secret-field"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { RevealedValue } from "@/components/deploy/settings/variable-type"
import { listWords, loginPasswordProblem } from "@/components/deploy/stack/stack-model"

/**
 * The one login every app of the stack signs in with, beside the apps from
 * `2xl` and over them narrower: the user name, the password behind Show and
 * Copy, and Change password.
 *
 * The password is read through the audited reveal route each time it is
 * shown or copied and is never kept past the page; a shown one is drawn
 * where the mask was, so the line keeps its height (list-rows-never-grow).
 */
export function StackLogin({
  state,
  projectId,
  canReveal,
  onChanged,
}: {
  state: SmartStackState
  projectId: number
  /** An administrator's session: the reveal and the change are theirs alone. */
  canReveal: boolean
  onChanged: () => void
}) {
  const login = loginOf(state)
  const [shown, setShown] = useState<string>()
  const [busy, setBusy] = useState("")
  const [changing, setChanging] = useState(false)
  if (!login) return null

  const apps = state.services.filter((service) => service.access !== "worker")
  const read = () =>
    get<{ name: string; username?: string; password: string }>(
      `/deploy/${projectId}/stack/credentials/login/reveal`,
    )
  const reveal = async () => {
    setBusy("reveal")
    try {
      setShown((await read()).password)
    } catch (caught) {
      notify.error("Could not show the password", caught)
    } finally {
      setBusy("")
    }
  }
  // Copies without drawing, for a screen somebody else can see.
  const copy = async () => {
    setBusy("copy")
    try {
      await copyText((await read()).password, "Password copied")
    } catch (caught) {
      notify.error("Could not copy the password", caught)
    } finally {
      setBusy("")
    }
  }

  return (
    <Panel plain data-slot="stack-login">
      <PanelHeader
        title="One login for every app"
        actions={
          canReveal &&
          login.stored && (
            <Button size="sm" variant="outline" onClick={() => setChanging(true)}>
              <Key />
              Change password
            </Button>
          )
        }
      />
      <PanelBody className="space-y-3 pt-3">
        {/* Two lines in the column beside the apps; side by side over them. */}
        <dl
          aria-label="The login"
          className="grid min-w-0 md:grid-cols-2 md:gap-x-8 2xl:grid-cols-1"
        >
          <div className="flex h-11 min-w-0 items-center gap-3 border-b border-hairline">
            <dt className="w-24 shrink-0 text-hint text-muted-foreground">User name</dt>
            <dd className="flex min-w-0 flex-1 items-center gap-1">
              <span className="truncate font-mono text-body">{login.username}</span>
              <CopyAction text={login.username} label="Copy the user name" className="shrink-0" />
            </dd>
          </div>
          <div className="flex h-11 min-w-0 items-center gap-3 border-b border-hairline">
            <dt className="w-24 shrink-0 text-hint text-muted-foreground">Password</dt>
            <dd className="flex min-w-0 flex-1 items-center gap-1">
              <span className="min-w-0 flex-1 text-body">
                {!login.stored ? (
                  <span className="block truncate text-muted-foreground">
                    Made by the stack&apos;s first setup
                  </span>
                ) : shown !== undefined ? (
                  <RevealedValue name="the password for every app" value={shown} />
                ) : (
                  <span className="block h-4 truncate leading-4" aria-label="Password hidden">
                    ••••••••••••
                  </span>
                )}
              </span>
              {canReveal && login.stored && (
                <>
                  <IconAction
                    label={shown !== undefined ? "Hide the password" : "Show the password"}
                    disabled={Boolean(busy)}
                    onClick={() => (shown !== undefined ? setShown(undefined) : void reveal())}
                  >
                    {shown !== undefined ? <EyeOff /> : <Eye />}
                  </IconAction>
                  <IconAction
                    label="Copy the password"
                    disabled={Boolean(busy)}
                    onClick={() => void copy()}
                  >
                    <Copy />
                  </IconAction>
                </>
              )}
            </dd>
          </div>
        </dl>
        <p className="text-hint leading-relaxed text-muted-foreground">
          {[
            apps.length > 0 && `${listWords(apps.map((app) => app.title))} all sign in with it.`,
            login.stored &&
              (canReveal
                ? "Showing or copying it is recorded in the audit log."
                : "An administrator can show it."),
          ]
            .filter(Boolean)
            .join(" ")}
        </p>
      </PanelBody>
      <ChangePasswordDialog
        open={changing}
        onOpenChange={setChanging}
        projectId={projectId}
        onChanged={() => {
          // What was shown is the old password now.
          setShown(undefined)
          onChanged()
        }}
      />
    </Panel>
  )
}

/** The stack's login, from its own field or, from a server that lists it only so, its credential. */
function loginOf(state: SmartStackState): SmartStackLogin | undefined {
  if (state.login) return state.login
  const credential = state.credentials.find((one) => one.name === "login")
  return credential
    ? { username: credential.username ?? "admin", stored: credential.stored }
    : undefined
}

/**
 * A new password for every app. The server keeps the old one until every app
 * answers to the new one, so a pass cut short can still sign in, and moves
 * them over in a pass it starts at once.
 */
function ChangePasswordDialog({
  open,
  onOpenChange,
  projectId,
  onChanged,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  projectId: number
  onChanged: () => void
}) {
  const [password, setPassword] = useState("")
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const [failure, setFailure] = useState<unknown>()
  const checks = passwordChecks(password)
  const problem = loginPasswordProblem(password)

  const close = (next: boolean) => {
    if (busy) return
    if (!next) {
      setPassword("")
      setRefusal(undefined)
      setFailure(undefined)
    }
    onOpenChange(next)
  }

  const submit = async () => {
    if (problem || busy) return
    setBusy(true)
    setRefusal(undefined)
    setFailure(undefined)
    try {
      await put(`/deploy/${projectId}/stack/login`, { password })
      notify.success("Password changed", {
        description: "Every app is being moved to it. The stack's page follows the pass.",
      })
      setBusy(false)
      setPassword("")
      onOpenChange(false)
      onChanged()
    } catch (caught) {
      if (caught instanceof ApiError && caught.code === "password_weak") setRefusal(caught.message)
      else setFailure(caught)
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={close}
      title="Change the password for every app"
      footer={
        <>
          <Button variant="outline" onClick={() => close(false)} disabled={busy}>
            Cancel
          </Button>
          <Button pending={busy} disabled={Boolean(problem)} onClick={() => void submit()}>
            Change password
          </Button>
        </>
      }
    >
      <form
        className="space-y-4"
        onSubmit={(event) => {
          event.preventDefault()
          void submit()
        }}
      >
        <SecretField
          id="stack-login-password"
          label="New password"
          value={password}
          onChange={(value) => {
            setPassword(value)
            setRefusal(undefined)
          }}
          shown={false}
          error={refusal}
          hint={
            <FieldChecks>
              <FieldCheck met={checks.long}>12 characters or more</FieldCheck>
              <FieldCheck met={checks.mixed}>
                three of upper case, lower case, digits, symbols
              </FieldCheck>
            </FieldChecks>
          }
        />
        <FormNote>
          The user name stays admin. Each app takes the new password on the pass this starts, and
          keeps the old one until it does.
        </FormNote>
        {failure !== undefined && (
          <Notice tone="danger" title="The password was not changed">
            {failure instanceof Error ? failure.message : String(failure)}
          </Notice>
        )}
        <button type="submit" hidden tabIndex={-1} aria-label="Change password" />
      </form>
    </Modal>
  )
}
