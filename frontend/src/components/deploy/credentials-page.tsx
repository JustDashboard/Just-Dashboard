"use client"

import { useState } from "react"
import Link from "next/link"
import { Lightning, Pencil, Plus, Trash } from "@/components/icons"
import { ApiError, del, get, post, put } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentCredential, DeploymentCredentialKind } from "@/lib/types"
import { Page, PageHeader } from "@/components/page"
import { Panel, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Status } from "@/components/status-dot"
import { Modal } from "@/components/modal"
import { SidePanel } from "@/components/side-panel"
import { Tag } from "@/components/tag"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle } from "@/components/choice-card"
import { Field, FormFact, FormFacts, FormNote } from "@/components/form"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Textarea } from "@/components/ui/textarea"
import { VerbActions, type Verb } from "@/components/verbs"
import { GitHubAppPanel, useGitHubApp } from "@/components/deploy/github-app-card"

/**
 * Fleet-level secrets: the tokens and keys the server uses on behalf of every
 * project, rather than one held per project. A project references one of
 * these by id instead of carrying its own copy, so rotating a token happens
 * once here rather than once per project that used it.
 *
 * The page is three readings, the GitHub App (one App standing in for a
 * token and a webhook per repository) and the saved credentials as rows.
 */

const KIND_OPTIONS: { kind: DeploymentCredentialKind; title: string; hint: string }[] = [
  {
    kind: "git_bearer",
    title: "Git token over HTTPS",
    hint: "A personal access token, sent as the HTTPS password when cloning or pulling.",
  },
  {
    kind: "git_ssh",
    title: "SSH key",
    hint: "A private key used to clone and pull over SSH.",
  },
  {
    kind: "registry",
    title: "Registry login",
    hint: "A username and password or token for a container registry.",
  },
  {
    kind: "provider_token",
    title: "Provider token",
    hint: "Calls the provider's API — commit statuses, deploy keys, releases.",
  },
]

const KIND_LABEL: Record<DeploymentCredentialKind, string> = {
  git_bearer: "Git token",
  git_ssh: "SSH key",
  registry: "Registry",
  provider_token: "Provider token",
  github_app: "GitHub App",
}

const TARGET_HINT: Record<DeploymentCredentialKind, string> = {
  git_bearer: "The Git host this token signs in to.",
  git_ssh: "The Git host this key signs in to.",
  registry: "Required. The registry host, with its port if it has one.",
  provider_token: "The provider's API host, if this token is scoped to one.",
  github_app: "github.com",
}

const TARGET_PLACEHOLDER: Record<DeploymentCredentialKind, string> = {
  git_bearer: "github.com",
  git_ssh: "github.com",
  registry: "ghcr.io",
  provider_token: "api.github.com",
  github_app: "github.com",
}

const SECRET_LABEL: Record<DeploymentCredentialKind, string> = {
  git_bearer: "Token",
  git_ssh: "Private key",
  registry: "Password or token",
  provider_token: "Token",
  github_app: "Token",
}

const SECRET_HINT: Record<DeploymentCredentialKind, string> = {
  git_bearer: "A personal access token that can read the repositories it will clone.",
  git_ssh: "A private key in PEM form; the public half goes in the provider's deploy keys.",
  registry: "The password, or an access token, that goes with the username.",
  provider_token: "A token allowed to call the provider's API.",
  github_app: "",
}

type Draft = {
  id?: number
  kind: DeploymentCredentialKind
  name: string
  target: string
  username: string
  secret: string
}

function emptyDraft(kind: DeploymentCredentialKind): Draft {
  return { kind, name: "", target: "", username: "", secret: "" }
}

/**
 * The server wants a bare host and refuses anything else, but what an
 * operator has on the clipboard is usually a whole URL or an SSH remote.
 * Cutting the scheme, the user and the path off here means a paste works
 * rather than bouncing off "target must be a bare host".
 */
function bareHost(value: string) {
  const host = value
    .trim()
    .replace(/^[a-z][a-z0-9+.-]*:\/\//i, "")
    .replace(/^[^@/]+@/, "")
    .split("/")[0]
  // `host:5000` is a registry with a port; `host:owner/repo` is an SSH remote.
  return host.replace(/:(?!\d+$).*$/, "")
}

function usageLabel(usedBy: number) {
  return usedBy > 0 ? `used by ${plural(usedBy, "project")}` : "not in use"
}

function lastUsedLabel(lastUsedAt: string | undefined) {
  return lastUsedAt ? `last used ${relativeTime(lastUsedAt)}` : "never used"
}

export function CredentialsPage() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const credentials = usePoll(
    (signal) => get<DeploymentCredential[]>("/deploy/credentials", undefined, signal),
    15000,
  )
  const githubApp = useGitHubApp()
  const [draft, setDraft] = useState<Draft>()
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [formError, setFormError] = useState("")
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState<DeploymentCredential>()
  const { confirm, dialog } = useConfirm()

  const closeDraft = () => {
    setDraft(undefined)
    setFieldErrors({})
    setFormError("")
  }

  // What the server will refuse without reading the secret. The button waits
  // for these rather than letting a press bounce off a 400.
  const incomplete =
    !draft ||
    !draft.name.trim() ||
    (!draft.id && !draft.secret.trim()) ||
    (draft.kind === "registry" && (!bareHost(draft.target) || !draft.username.trim()))

  const save = async () => {
    if (!draft || incomplete) return
    setSaving(true)
    setFieldErrors({})
    setFormError("")
    const body = {
      name: draft.name.trim(),
      ...(draft.id ? {} : { kind: draft.kind }),
      target: bareHost(draft.target) || undefined,
      username: draft.kind === "registry" ? draft.username.trim() || undefined : undefined,
      // An edit's blank secret means "keep the sealed one"; a create requires it.
      secret: draft.id ? draft.secret || undefined : draft.secret,
    }
    try {
      if (draft.id) {
        await put(`/deploy/credentials/${draft.id}`, body)
        notify.success("Credential saved")
      } else {
        await post("/deploy/credentials", body)
        notify.success("Credential added")
      }
      closeDraft()
      credentials.refresh()
    } catch (caught) {
      if (caught instanceof ApiError && caught.field) {
        setFieldErrors({ [caught.field]: caught.message })
      } else if (
        caught instanceof ApiError &&
        caught.status === 409 &&
        caught.code === "name_taken"
      ) {
        setFieldErrors({ name: "That name is already used." })
      } else if (caught instanceof ApiError && caught.status === 400) {
        // A shape refusal names what is wrong in its message, and belongs
        // under the form rather than in a toast that closes over it.
        setFormError(caught.message)
      } else {
        notify.error(draft.id ? "Could not save credential" : "Could not add credential", caught)
      }
    } finally {
      setSaving(false)
    }
  }

  const edit = (credential: DeploymentCredential) => {
    setFieldErrors({})
    setFormError("")
    setDraft({
      id: credential.id,
      kind: credential.kind,
      name: credential.name,
      target: credential.target,
      username: credential.username ?? "",
      secret: "",
    })
  }

  const remove = (credential: DeploymentCredential) =>
    confirm({
      title: `Remove ${credential.name}`,
      confirmLabel: "Remove credential",
      description: (
        <p>
          Projects reading from it lose access to the repository or registry it authenticates to.
          This does not change what is already deployed.
        </p>
      ),
      // `credential_in_use` (the refusal an operator will actually hit) needs
      // no special case: the confirm dialog's own failure toast already names
      // the server's message, which for this code names the projects.
      action: async () => {
        await del(`/deploy/credentials/${credential.id}`)
      },
      onDone: () => credentials.refresh(),
    })

  const addAction = admin && (
    <Button size="sm" onClick={() => setDraft(emptyDraft("git_bearer"))}>
      <Plus className="size-3.5" />
      Add credential
    </Button>
  )

  const list = credentials.data
  const inUse = list?.filter((credential) => credential.usedBy > 0).length
  const app = githubApp.data
  const figure = (value: number | undefined) =>
    value === undefined ? <Skeleton className="h-6 w-8" /> : value

  return (
    <Page>
      <PageHeader
        eyebrow={
          <Link href="/deploy" className="rounded-sm focus-ring hover:underline">
            Deployments
          </Link>
        }
        title="Credentials"
        actions={addAction}
      />

      <StatGrid columns={3}>
        <StatTile
          key={list ? "credentials" : "credentials-loading"}
          label="Credentials"
          value={figure(list?.length)}
          hint="tokens, keys and logins the server holds"
          className={list && "animate-rise"}
        />
        <StatTile
          key={list ? "in-use" : "in-use-loading"}
          label="In use"
          value={figure(inUse)}
          hint="referenced by a project's current source"
          className={list && "animate-rise"}
        />
        <StatTile
          key={app || githubApp.error ? "app" : "app-loading"}
          label="GitHub App"
          value={
            githubApp.error ? (
              "Unavailable"
            ) : app ? (
              app.configured ? (
                "Connected"
              ) : (
                "Not connected"
              )
            ) : (
              <Skeleton className="h-6 w-24" />
            )
          }
          hint={
            app?.configured
              ? `installed on ${plural(app.installations.length, "account")}`
              : "one App in place of a token and a webhook per repository"
          }
          className={(app || githubApp.error) && "animate-rise"}
        />
      </StatGrid>

      <GitHubAppPanel admin={admin} status={githubApp} onChange={credentials.refresh} />

      <Panel plain>
        <PanelHeader title="Saved credentials" />
        {credentials.error ? (
          <ErrorState error={credentials.error} onRetry={credentials.refresh} />
        ) : credentials.loading && !list ? (
          <LoadingRows rows={3} className="pt-4" />
        ) : (list?.length ?? 0) === 0 ? (
          <EmptyNote>
            No credentials yet. Add a Git token, an SSH key or a registry login so the server can
            reach private sources.
          </EmptyNote>
        ) : (
          <RowList aria-label="Credentials" className="animate-rise">
            {list?.map((credential) => {
              const verbs: Verb[] = []
              if (admin) {
                verbs.push({
                  key: "test",
                  label: "Test",
                  detail: "Check that the server can use it right now.",
                  icon: Lightning,
                  inline: true,
                  run: () => setTesting(credential),
                })
                // An App credential follows its installation; there is
                // nothing on it to edit.
                if (credential.kind !== "github_app") {
                  verbs.push({
                    key: "edit",
                    label: "Edit credential",
                    detail: "Change its name, host or secret.",
                    icon: Pencil,
                    run: () => edit(credential),
                  })
                }
              }
              if (admin && can("destructive")) {
                verbs.push({
                  key: "remove",
                  label: "Remove credential",
                  detail: "Projects using it lose access to the repository or registry.",
                  icon: Trash,
                  danger: true,
                  run: () => remove(credential),
                })
              }
              const where = [credential.target, credential.username].filter(Boolean).join(" · ")
              return (
                <Row
                  key={credential.id}
                  title={credential.name}
                  subtitle={
                    <>
                      {where && (
                        <>
                          <span className="font-mono">{where}</span>
                          {" · "}
                        </>
                      )}
                      {usageLabel(credential.usedBy)} · {lastUsedLabel(credential.lastUsedAt)}
                    </>
                  }
                  trailing={
                    <>
                      <Tag>{KIND_LABEL[credential.kind]}</Tag>
                      {verbs.length > 0 && (
                        <VerbActions verbs={verbs} menuLabel={`Actions for ${credential.name}`} />
                      )}
                    </>
                  }
                />
              )
            })}
          </RowList>
        )}
      </Panel>

      <SidePanel
        open={Boolean(draft)}
        onOpenChange={(open) => !open && closeDraft()}
        title={draft?.id ? "Edit credential" : "Add credential"}
        description="A token or key the server uses to read a private repository or registry."
        width="sm"
        footer={
          <Button pending={saving} disabled={incomplete} onClick={save}>
            {draft?.id ? "Save credential" : "Add credential"}
          </Button>
        }
      >
        {draft && (
          <div className="space-y-5" aria-busy={saving}>
            {draft.id ? (
              <FormFacts>
                <FormFact label="Kind">{KIND_LABEL[draft.kind]}</FormFact>
              </FormFacts>
            ) : (
              <fieldset className="space-y-2">
                <legend className="eyebrow mb-2">Kind</legend>
                <div className="grid gap-2 sm:grid-cols-2">
                  {KIND_OPTIONS.map((option) => (
                    <ChoiceCard
                      key={option.kind}
                      selected={draft.kind === option.kind}
                      onClick={() => {
                        if (draft.kind === option.kind) return
                        setFieldErrors({})
                        setFormError("")
                        setDraft({ ...emptyDraft(option.kind), name: draft.name })
                      }}
                    >
                      <ChoiceCardTitle>{option.title}</ChoiceCardTitle>
                      <ChoiceCardHint>{option.hint}</ChoiceCardHint>
                    </ChoiceCard>
                  ))}
                </div>
              </fieldset>
            )}

            <Field
              label="Name"
              htmlFor="credential-name"
              hint="Letters, digits, dots, dashes and underscores."
              error={fieldErrors.name}
            >
              <Input
                id="credential-name"
                value={draft.name}
                onChange={(event) => setDraft({ ...draft, name: event.target.value })}
                placeholder={draft.kind === "registry" ? "ghcr-deploy" : "github-deploy"}
                autoComplete="off"
              />
            </Field>

            <Field
              label="Host"
              htmlFor="credential-target"
              hint={TARGET_HINT[draft.kind]}
              error={fieldErrors.target}
            >
              <Input
                id="credential-target"
                value={draft.target}
                onChange={(event) => setDraft({ ...draft, target: event.target.value })}
                placeholder={TARGET_PLACEHOLDER[draft.kind]}
                className="font-mono"
                autoComplete="off"
                spellCheck={false}
              />
            </Field>

            {draft.kind === "registry" && (
              <Field
                label="Username"
                htmlFor="credential-username"
                hint="Required. The account the registry knows this login by."
                error={fieldErrors.username}
              >
                <Input
                  id="credential-username"
                  value={draft.username}
                  onChange={(event) => setDraft({ ...draft, username: event.target.value })}
                  autoComplete="off"
                />
              </Field>
            )}

            <Field
              label={SECRET_LABEL[draft.kind]}
              htmlFor="credential-secret"
              hint={
                draft.id
                  ? `${SECRET_HINT[draft.kind]} Leave empty to keep the stored secret.`
                  : SECRET_HINT[draft.kind]
              }
              error={fieldErrors.secret}
            >
              {draft.kind === "git_ssh" ? (
                <Textarea
                  id="credential-secret"
                  value={draft.secret}
                  onChange={(event) => setDraft({ ...draft, secret: event.target.value })}
                  placeholder={
                    draft.id
                      ? "Leave empty to keep the stored key"
                      : "-----BEGIN OPENSSH PRIVATE KEY-----"
                  }
                  className="min-h-40 font-mono text-xs"
                  autoComplete="off"
                  spellCheck={false}
                />
              ) : (
                <Input
                  id="credential-secret"
                  type="password"
                  value={draft.secret}
                  onChange={(event) => setDraft({ ...draft, secret: event.target.value })}
                  autoComplete="new-password"
                />
              )}
            </Field>

            <FormNote>Sealed with the server’s key. The dashboard never shows it again.</FormNote>

            {formError && (
              <FormNote tone="danger" role="alert">
                {formError}
              </FormNote>
            )}
          </div>
        )}
      </SidePanel>

      {/* A probe marks the credential used, so the row's "last used" is
          stale the moment the dialog closes unless the list re-reads. */}
      <TestCredentialDialog
        credential={testing}
        onClose={() => {
          setTesting(undefined)
          credentials.refresh()
        }}
      />
      {dialog}
    </Page>
  )
}

/**
 * The live probe. Every kind needs something concrete to try — a repository
 * to list, an image to resolve — because the server refuses a bare "does this
 * token work" with nothing to point it at, and a dialog that let the field
 * stay empty was a dialog whose Test button only ever produced an error.
 */
function TestCredentialDialog({
  credential,
  onClose,
}: {
  credential?: DeploymentCredential
  onClose: () => void
}) {
  const [subject, setSubject] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [result, setResult] = useState<{ ok: boolean; message: string }>()

  // Reset on the way out rather than in an effect keyed on `credential`: the
  // dialog's own overlay blocks the row underneath, so the credential being
  // tested can only change between one close and the next open, never while
  // it is open — closing is the one moment a fresh attempt is guaranteed.
  const close = () => {
    onClose()
    setSubject("")
    setError("")
    setResult(undefined)
  }

  const registry = credential?.kind === "registry"
  const target = credential?.target

  const run = async () => {
    if (!credential || !subject.trim()) return
    setBusy(true)
    setError("")
    setResult(undefined)
    try {
      const response = await post<{ ok: boolean; message: string }>(
        `/deploy/credentials/${credential.id}/test`,
        { repository: subject.trim() },
      )
      setResult(response)
    } catch (caught) {
      if (caught instanceof ApiError && caught.status === 400) {
        setError(caught.message)
      } else {
        notify.error("Could not run the test", caught)
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={Boolean(credential)}
      onOpenChange={(open) => !busy && !open && close()}
      title={credential ? `Test ${credential.name}` : "Test credential"}
      description="Send a live check using this credential right now."
      size="sm"
      footer={
        <>
          <Button variant="outline" onClick={close} disabled={busy}>
            Close
          </Button>
          <Button onClick={() => void run()} pending={busy} disabled={!subject.trim()}>
            Test
          </Button>
        </>
      }
    >
      {credential && (
        <div className="space-y-4">
          <Field
            label={registry ? "Image" : "Repository"}
            htmlFor="credential-test-subject"
            hint={
              registry
                ? `An image on ${target} to resolve with this login.`
                : target
                  ? `owner/name on ${target}, or a full Git URL.`
                  : "A full Git URL — this credential has no saved host."
            }
            error={error}
          >
            <Input
              id="credential-test-subject"
              value={subject}
              onChange={(event) => setSubject(event.target.value)}
              placeholder={registry ? `${target}/owner/app:latest` : "owner/name"}
              className="font-mono"
              autoComplete="off"
              spellCheck={false}
            />
          </Field>
          {result && (
            <div role="status">
              <Status tone={result.ok ? "running" : "danger"} label={result.message} />
            </div>
          )}
        </div>
      )}
    </Modal>
  )
}

/**
 * The credential picker a source form hands its `credentialId` through —
 * shared here so the new-project Git/image steps and this page's own Source
 * settings card draw the same control rather than three copies of it.
 */
export function CredentialSelect({
  id,
  kind,
  value,
  onChange,
  disabled,
}: {
  id?: string
  /** Which kinds are offered: a repository credential, or a registry login. */
  kind: "git" | "registry"
  value: number | undefined
  onChange: (value: number | undefined) => void
  disabled?: boolean
}) {
  // Fetched once per mount rather than polled: this is a form control, not a
  // reading, and a stale list for a few minutes costs nothing a reopen won't fix.
  const credentials = usePoll(
    (signal) => get<DeploymentCredential[]>("/deploy/credentials", undefined, signal),
    0,
  )
  const options = (credentials.data ?? []).filter((credential) =>
    kind === "git"
      ? credential.kind === "git_bearer" || credential.kind === "git_ssh"
      : credential.kind === "registry",
  )
  return (
    <div className="space-y-1.5">
      <Select
        value={value ? String(value) : "none"}
        onValueChange={(next) => onChange(next === "none" ? undefined : Number(next))}
        disabled={disabled}
      >
        <SelectTrigger id={id} className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="none">Server default</SelectItem>
          {options.map((credential) => (
            <SelectItem key={credential.id} value={String(credential.id)}>
              {credential.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Link
        href="/deploy/credentials"
        className="w-fit rounded-sm text-hint text-muted-foreground underline underline-offset-2 focus-ring hover:text-foreground"
      >
        Manage credentials…
      </Link>
    </div>
  )
}
