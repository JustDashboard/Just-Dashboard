"use client"

import { useState } from "react"
import Link from "next/link"
import { Key, Lightning, Pencil, Plus, Trash } from "@/components/icons"
import { ApiError, del, get, post, put } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentCredential, DeploymentCredentialKind } from "@/lib/types"
import { Page, PageHeader } from "@/components/page"
import { Panel } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Modal } from "@/components/modal"
import { SidePanel } from "@/components/side-panel"
import { Tag } from "@/components/tag"
import { useConfirm } from "@/components/confirm-dialog"
import { Field, FormNote, OptionList, OptionRow } from "@/components/form"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import { VerbActions, type Verb } from "@/components/verbs"
import { GitHubAppCard } from "@/components/deploy/github-app-card"

/**
 * Fleet-level secrets: the tokens and keys the server uses on behalf of every
 * project, rather than one held per project. A project references one of
 * these by id instead of carrying its own copy, so rotating a token happens
 * once here rather than once per project that used it.
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

const GIT_KINDS: DeploymentCredentialKind[] = ["git_bearer", "git_ssh"]

const TARGET_HINT: Record<DeploymentCredentialKind, string> = {
  git_bearer: "The host this token authenticates to, such as github.com.",
  git_ssh: "The host this key authenticates to, such as github.com.",
  registry: "The registry host, such as ghcr.io or docker.io.",
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
  const [draft, setDraft] = useState<Draft>()
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState<DeploymentCredential>()
  const { confirm, dialog } = useConfirm()

  const save = async () => {
    if (!draft) return
    if (!draft.id && !draft.secret.trim()) {
      setFieldErrors({ secret: "Enter the secret to seal." })
      return
    }
    setSaving(true)
    setFieldErrors({})
    const body = {
      name: draft.name.trim(),
      ...(draft.id ? {} : { kind: draft.kind }),
      target: draft.target.trim() || undefined,
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
      setDraft(undefined)
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
      } else {
        notify.error(draft.id ? "Could not save credential" : "Could not add credential", caught)
      }
    } finally {
      setSaving(false)
    }
  }

  const edit = (credential: DeploymentCredential) => {
    setFieldErrors({})
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

      <Notice title="Secrets are sealed">
        Tokens and keys the server uses to read private repositories and registries. Secrets are
        sealed and never shown again.
      </Notice>

      <GitHubAppCard admin={admin} />

      <Panel plain>
        {credentials.error ? (
          <ErrorState error={credentials.error} onRetry={credentials.refresh} />
        ) : credentials.loading && !credentials.data ? (
          <LoadingPanel rows={3} />
        ) : (credentials.data?.length ?? 0) === 0 ? (
          <EmptyState
            icon={Key}
            title="No credentials"
            description="Add a Git token, an SSH key or a registry login so the server can reach private sources."
            action={addAction}
          />
        ) : (
          <RowList aria-label="Credentials" className="animate-rise">
            {credentials.data?.map((credential) => {
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
                    detail: "Change its name, target or secret.",
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
              return (
                <Row
                  key={credential.id}
                  title={
                    <span className="inline-flex min-w-0 items-center gap-2">
                      <span className="truncate">{credential.name}</span>
                      <Tag>{KIND_LABEL[credential.kind]}</Tag>
                    </span>
                  }
                  subtitle={
                    <span className="min-w-0 truncate">
                      {(credential.target || credential.username) && (
                        <>
                          <span className="font-mono">
                            {[credential.target, credential.username].filter(Boolean).join(" · ")}
                          </span>
                          {" · "}
                        </>
                      )}
                      {usageLabel(credential.usedBy)} · {lastUsedLabel(credential.lastUsedAt)}
                    </span>
                  }
                  trailing={
                    verbs.length > 0 && (
                      <VerbActions verbs={verbs} menuLabel={`Actions for ${credential.name}`} />
                    )
                  }
                />
              )
            })}
          </RowList>
        )}
      </Panel>

      <SidePanel
        open={Boolean(draft)}
        onOpenChange={(open) => {
          if (!open) {
            setDraft(undefined)
            setFieldErrors({})
          }
        }}
        title={draft?.id ? "Edit credential" : "Add credential"}
        description="A token or key the server uses to read a private repository or registry."
        width="md"
        footer={
          <Button pending={saving} disabled={!draft || !draft.name.trim()} onClick={save}>
            {draft?.id ? "Save credential" : "Add credential"}
          </Button>
        }
      >
        {draft && (
          <div className="space-y-5" aria-busy={saving}>
            {!draft.id && (
              <fieldset className="space-y-1.5">
                <legend className="eyebrow mb-1">Kind</legend>
                <OptionList role="group" aria-label="Credential kind">
                  {KIND_OPTIONS.map((option) => (
                    <OptionRow
                      key={option.kind}
                      title={option.title}
                      hint={option.hint}
                      checked={draft.kind === option.kind}
                      onCheckedChange={(checked) => checked && setDraft(emptyDraft(option.kind))}
                    />
                  ))}
                </OptionList>
              </fieldset>
            )}

            <Field label="Name" htmlFor="credential-name" error={fieldErrors.name}>
              <Input
                id="credential-name"
                value={draft.name}
                onChange={(event) => setDraft({ ...draft, name: event.target.value })}
              />
            </Field>

            <Field
              label="Target"
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
              />
            </Field>

            {draft.kind === "registry" && (
              <Field label="Username" htmlFor="credential-username" error={fieldErrors.username}>
                <Input
                  id="credential-username"
                  value={draft.username}
                  onChange={(event) => setDraft({ ...draft, username: event.target.value })}
                  autoComplete="off"
                />
              </Field>
            )}

            {draft.kind === "git_ssh" ? (
              <Field
                label="Secret"
                htmlFor="credential-secret"
                hint={`A private key in PEM form; the public half goes in the provider's deploy keys.${draft.id ? " Leave empty to keep the stored secret." : ""}`}
                error={fieldErrors.secret}
              >
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
              </Field>
            ) : (
              <Field
                label="Secret"
                htmlFor="credential-secret"
                hint={draft.id ? "Leave empty to keep the stored secret." : undefined}
                error={fieldErrors.secret}
              >
                <Input
                  id="credential-secret"
                  type="password"
                  value={draft.secret}
                  onChange={(event) => setDraft({ ...draft, secret: event.target.value })}
                  autoComplete="new-password"
                />
              </Field>
            )}

            <FormNote>Sealed with the server’s key. The dashboard never shows it again.</FormNote>
          </div>
        )}
      </SidePanel>

      <TestCredentialDialog credential={testing} onClose={() => setTesting(undefined)} />
      {dialog}
    </Page>
  )
}

function TestCredentialDialog({
  credential,
  onClose,
}: {
  credential?: DeploymentCredential
  onClose: () => void
}) {
  const [repository, setRepository] = useState("")
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<{ ok: boolean; message: string }>()

  // Reset on the way out rather than in an effect keyed on `credential`: the
  // dialog's own overlay blocks the row underneath, so the credential being
  // tested can only change between one close and the next open, never while
  // it is open — closing is the one moment a fresh attempt is guaranteed.
  const close = () => {
    onClose()
    setRepository("")
    setResult(undefined)
  }

  const run = async () => {
    if (!credential) return
    setBusy(true)
    try {
      const body =
        GIT_KINDS.includes(credential.kind) && repository.trim()
          ? { repository: repository.trim() }
          : {}
      const response = await post<{ ok: boolean; message: string }>(
        `/deploy/credentials/${credential.id}/test`,
        body,
      )
      setResult(response)
    } catch (caught) {
      notify.error("Could not run the test", caught)
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
          <Button onClick={() => void run()} pending={busy}>
            Test
          </Button>
        </>
      }
    >
      {credential && (
        <div className="space-y-4">
          {GIT_KINDS.includes(credential.kind) ? (
            <Field
              label="Repository"
              htmlFor="credential-test-repository"
              hint="A full URL or owner/name. Leave empty to test against its target alone."
            >
              <Input
                id="credential-test-repository"
                value={repository}
                onChange={(event) => setRepository(event.target.value)}
                placeholder="owner/name"
                className="font-mono"
                autoComplete="off"
              />
            </Field>
          ) : (
            <FormNote>
              {credential.kind === "registry"
                ? "Resolves a manifest from the credential's target."
                : "Calls the provider's API using this token."}
            </FormNote>
          )}
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
    kind === "git" ? GIT_KINDS.includes(credential.kind) : credential.kind === "registry",
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
