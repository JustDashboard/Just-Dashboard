"use client"

import { useRef, useState } from "react"
import { useMemoryState } from "@/lib/view-state"
import Link from "next/link"
import {
  Box,
  CheckCircle,
  CodeBracket,
  CrossCircle,
  Eye,
  EyeOff,
  GitHubMark,
  Key,
  Lightning,
  Pencil,
  Plus,
  Trash,
  type Icon,
} from "@/components/icons"
import { ApiError, del, get, post, put } from "@/lib/api"
import { calendarDate, plural, relativeTime } from "@/lib/format"
import { sshKeyShape, tokenProduct, type SshKeyShape } from "@/lib/secrets"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { usePoll } from "@/hooks/use-poll"
import type {
  DeploymentCredential,
  DeploymentCredentialKind,
  DeploymentFleet,
  DeploymentSummary,
  GitHubAppInstallation,
} from "@/lib/types"
import { Page, PageHeader } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Status } from "@/components/status-dot"
import { Modal } from "@/components/modal"
import { SidePanel } from "@/components/side-panel"
import { Tag } from "@/components/tag"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle, ChoiceGrid } from "@/components/choice-card"
import { ChoiceList, ChoiceRow, GroupRule } from "@/components/flow"
import {
  Field,
  FieldCheck,
  FieldRow,
  FormFact,
  FormFacts,
  FormNote,
  FormSection,
} from "@/components/form"
import { ProductGlyph, ProductGlyphs, ProductLogo, hostProduct } from "@/components/product-logo"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
  InputGroupToggle,
} from "@/components/ui/input-group"
import { NumberTicker } from "@/components/ui/number-ticker"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { Textarea } from "@/components/ui/textarea"
import { VerbActions, type Verb } from "@/components/verbs"
import { AccountFace, GitHubAppPanel, projectsUsing } from "@/components/deploy/github-app-card"
import { SourceBranch } from "@/components/git/glyphs"
import { ProjectMark } from "@/components/deploy/project-mark"
import { useGitHubApp } from "@/hooks/use-github"

/**
 * Fleet-level secrets: the tokens and keys the server uses on behalf of every
 * project, rather than one held per project. A project references one of
 * these by id instead of carrying its own copy, so rotating a token happens
 * once here rather than once per project that used it.
 *
 * The page opens on four readings — how many are held and across which hosts,
 * how many a project's source reads through, how many were saved and never
 * used, and when one was last used — then the GitHub App, whose state is said
 * in its own section (its header and its setup path) rather than as a third
 * statement in a tile, and then the credentials as cards you open to edit,
 * each drawn as the host it signs in to: github.com is GitHub's mark and
 * gitlab.com GitLab's, which is how the reader already knows them (§14).
 */

/**
 * `short` is the line on the sheet's compact cards, which hold two lines at a
 * phone's width; `hint` is the whole sentence the empty page has room for.
 */
const KIND_OPTIONS: {
  kind: DeploymentCredentialKind
  title: string
  short: string
  hint: string
}[] = [
  {
    kind: "git_bearer",
    title: "Git token over HTTPS",
    short: "Token sent as the HTTPS password",
    hint: "A personal access token, sent as the HTTPS password when cloning or pulling.",
  },
  {
    kind: "git_ssh",
    title: "SSH key",
    short: "Private key for clone and pull",
    hint: "A private key used to clone and pull over SSH.",
  },
  {
    kind: "registry",
    title: "Registry login",
    short: "Login for a container registry",
    hint: "A username and password or token for a container registry.",
  },
  {
    kind: "provider_token",
    title: "Provider token",
    short: "Calls the provider's API",
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

/**
 * The glyph a kind is chosen by. These are kinds, not products, so they stay
 * glyphs in the pickers — muted, and the chosen one in the brand (§14) — and
 * stand in on a credential's tile only where its host names no product.
 */
const KIND_GLYPH: Record<DeploymentCredentialKind, Icon> = {
  git_bearer: SourceBranch,
  git_ssh: Key,
  registry: Box,
  provider_token: CodeBracket,
  github_app: GitHubMark,
}

/**
 * What a credential is when its host names no product: any Git token or key
 * is git's, any registry login Docker's (the argument `imageProduct` makes
 * for an unknown image), and a provider token keeps its glyph.
 */
const KIND_PRODUCT: Partial<Record<DeploymentCredentialKind, string>> = {
  git_bearer: "git",
  git_ssh: "git",
  registry: "docker",
  github_app: "github",
}

/** The second half of a suggested name: `ghcr-registry`, `gitlab-ssh`. */
const KIND_WORD: Record<DeploymentCredentialKind, string> = {
  git_bearer: "token",
  git_ssh: "ssh",
  registry: "registry",
  provider_token: "api",
  github_app: "app",
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

/** The server's rule for a credential's name (credentials_store.go). */
const NAME_RULE = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/

/** What a pasted key's first line says it is, in the words its format uses. */
const KEY_WORD: Record<Exclude<SshKeyShape, "public">, string> = {
  openssh: "OpenSSH private key",
  rsa: "RSA private key",
  ec: "EC private key",
  dsa: "DSA private key",
  pkcs8: "PKCS #8 private key",
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

function usageLabel(projects: number) {
  return projects > 0 ? `used by ${plural(projects, "project")}` : "not in use"
}

function lastUsedLabel(lastUsedAt: string | undefined) {
  return lastUsedAt ? `last used ${relativeTime(lastUsedAt)}` : "never used"
}

function credentialProduct(credential: Pick<DeploymentCredential, "kind" | "target">) {
  return hostProduct(credential.target) ?? KIND_PRODUCT[credential.kind]
}

// The words a host is named by that say nothing about whose it is.
const GENERIC_LABELS = new Set(["api", "www", "registry", "git", "hub", "index"])

/** A name for the credential being made, from its host and kind: `ghcr-registry`. */
function suggestedName(draft: Draft) {
  // A registry's port is not part of whose it is, and a name may not hold a colon.
  const labels = (bareHost(draft.target) || TARGET_PLACEHOLDER[draft.kind])
    .replace(/:\d+$/, "")
    .split(".")
  const stem = labels.find((label) => !GENERIC_LABELS.has(label)) ?? labels[0]
  return `${stem}-${KIND_WORD[draft.kind]}`
}

/**
 * The name that is saved: what was typed, or the suggestion the empty field
 * shows — so the grey name in the field is the one the press would save.
 */
function effectiveName(draft: Draft) {
  return draft.name.trim() || suggestedName(draft)
}

/**
 * A credential drawn as what it signs in to: the host's own product on
 * ProductLogo's tile, git's or Docker's where the host names none. An SSH key
 * carries a key in the tile's corner — the sessions list's browser-over-system
 * badge — so "SSH key on Codeberg" reads Codeberg first and key second. A
 * GitHub App credential is the account whose installation mints it, as that
 * account's face with GitHub's mark in the corner.
 *
 * A credential nothing reads is not dimmed: that is a reading, and the Never
 * used tile says it.
 */
function CredentialMark({
  credential,
  installation,
}: {
  credential: Pick<DeploymentCredential, "kind" | "target">
  installation?: GitHubAppInstallation
}) {
  const badge =
    credential.kind === "github_app" && installation ? (
      <ProductGlyph id="github" className="size-2.5" />
    ) : credential.kind === "git_ssh" ? (
      <Key className="size-2.5 text-muted-foreground" />
    ) : undefined
  return (
    <span className="relative flex shrink-0">
      {credential.kind === "github_app" && installation ? (
        <AccountFace
          account={installation.account}
          className="size-8 rounded-lg border border-hairline"
        />
      ) : (
        <ProductLogo
          id={credentialProduct(credential)}
          size="sm"
          fallback={KIND_GLYPH[credential.kind]}
        />
      )}
      {badge && (
        <span
          aria-hidden
          className="absolute -right-1 -bottom-1 flex size-4 items-center justify-center rounded-sm border border-hairline bg-background"
        >
          {badge}
        </span>
      )}
    </span>
  )
}

/** The same mark bare, at a line's height, for a select option or a fact. */
function CredentialGlyph({
  credential,
}: {
  credential: Pick<DeploymentCredential, "kind" | "target">
}) {
  const product = credentialProduct(credential)
  if (product) return <ProductGlyph id={product} />
  const Glyph = KIND_GLYPH[credential.kind]
  return <Glyph aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
}

export function CredentialsPage() {
  const { can } = useAuth()
  const admin = can("system.admin")
  // The list is an administrator's read (the server answers anyone else with
  // a 403), so it is not asked for without the capability.
  const credentials = usePoll(
    (signal) => get<DeploymentCredential[]>("/deploy/credentials", undefined, signal),
    15000,
    [],
    { enabled: admin },
  )
  const githubApp = useGitHubApp()
  const list = credentials.data
  // The projects behind a credential are drawn as themselves on its card, so
  // the fleet is read once — and only when some credential names one.
  const fleet = usePoll(
    (signal) => get<DeploymentFleet>("/deploy/", { view: "fleet" }, signal),
    60000,
    [],
    { enabled: Boolean(list?.some((credential) => credential.usedByProjectIds?.length)) },
  )
  const projects = new Map(
    (fleet.data?.deployments ?? []).map((deployment) => [deployment.id, deployment]),
  )
  // The open form is kept in memory for the tab — memory, because the secret
  // is typed into it — so a look at the provider for the token does not
  // mean starting the credential again.
  const [draft, setDraft] = useMemoryState<Draft | undefined>("deploy.credentials.draft", undefined)
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [formError, setFormError] = useState("")
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState<DeploymentCredential>()
  const [probing, setProbing] = useState<number>()
  const { confirm, dialog } = useConfirm()
  // On a phone the kind goes under the name, which otherwise had the width the
  // kind and the verbs left it — none. Chosen once, so it is in the page once.
  const wide = useMediaQuery("(min-width: 640px)")

  const installations = githubApp.data?.installations ?? []
  const installationOf = (credential: DeploymentCredential) =>
    installations.find((installation) => installation.credentialId === credential.id)

  const closeDraft = () => {
    setDraft(undefined)
    setFieldErrors({})
    setFormError("")
  }

  const open = (next: Draft) => {
    setFieldErrors({})
    setFormError("")
    setDraft(next)
  }

  // What the server will refuse without reading the secret. The button waits
  // for these rather than letting a press bounce off a 400.
  const incomplete =
    !draft ||
    !NAME_RULE.test(effectiveName(draft)) ||
    (!draft.id && !draft.secret.trim()) ||
    (draft.kind === "registry" && (!bareHost(draft.target) || !draft.username.trim()))

  const save = async () => {
    if (!draft || incomplete) return
    setSaving(true)
    setFieldErrors({})
    setFormError("")
    const body = {
      name: effectiveName(draft),
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

  const edit = (credential: DeploymentCredential) =>
    open({
      id: credential.id,
      kind: credential.kind,
      name: credential.name,
      target: credential.target,
      username: credential.username ?? "",
      secret: "",
    })

  const remove = (credential: DeploymentCredential) => {
    const projects = projectsUsing(credential)
    confirm({
      title: `Remove ${credential.name}`,
      confirmLabel: "Remove credential",
      subject: {
        mark: <CredentialMark credential={credential} installation={installationOf(credential)} />,
        name: credential.name,
        facts: (
          <>
            {credential.target && (
              <FormFact label="Host" mono>
                {credential.target}
              </FormFact>
            )}
            <FormFact label="Kind">{KIND_LABEL[credential.kind]}</FormFact>
          </>
        ),
      },
      // The server stays the authority: the verb is not withheld while a
      // project reads through it, because its refusal names those projects and
      // the confirm dialog's own failure toast carries that message.
      description:
        projects > 0 ? (
          <p>
            It signs in for {plural(projects, "project")}. The server refuses to remove it while
            their sources point at it — move them to another credential first.
          </p>
        ) : (
          <p>Nothing deploys through it; removing it cannot change a running project.</p>
        ),
      action: async () => {
        await del(`/deploy/credentials/${credential.id}`)
      },
      onDone: () => credentials.refresh(),
    })
  }

  // Only an administrator reads the list, so every card is an administrator's.
  const verbsFor = (credential: DeploymentCredential): Verb[] => {
    const verbs: Verb[] = [
      {
        key: "test",
        label: "Test",
        detail: "Check that the server can use it right now.",
        icon: Lightning,
        inline: true,
        progressive: "Testing…",
        run: () => setTesting(credential),
      },
    ]
    // An App credential follows its installation; there is nothing on it to
    // edit, and the account that owns it is where it is changed.
    const installation = installationOf(credential)
    if (credential.kind === "github_app" && installation) {
      verbs.push({
        key: "installation",
        label: "Show its installation",
        detail: "The account on GitHub whose installation mints it.",
        icon: GitHubMark,
        run: () => window.open(installation.htmlUrl, "_blank", "noreferrer"),
      })
    } else if (credential.kind !== "github_app") {
      verbs.push({
        key: "edit",
        label: "Edit credential",
        detail: "Change its name, host, username or secret.",
        icon: Pencil,
        run: () => edit(credential),
      })
    }
    if (can("destructive")) {
      verbs.push({
        key: "remove",
        label: "Remove credential",
        detail: "Refused while a project's source reads through it.",
        icon: Trash,
        danger: true,
        run: () => remove(credential),
      })
    }
    return verbs
  }

  const card = (credential: DeploymentCredential) => (
    <CredentialCard
      key={credential.id}
      credential={credential}
      installation={installationOf(credential)}
      projects={(credential.usedByProjectIds ?? []).flatMap((id) => projects.get(id) ?? [])}
      verbs={verbsFor(credential)}
      onEdit={credential.kind !== "github_app" ? () => edit(credential) : undefined}
      probing={probing === credential.id}
      wide={wide}
    />
  )

  // What a project's source reads through first; the rest under it.
  const groups = [
    { key: "in-use", label: "In use", rows: (list ?? []).filter((c) => c.usedBy > 0) },
    { key: "idle", label: "Not in use", rows: (list ?? []).filter((c) => c.usedBy === 0) },
  ].filter((group) => group.rows.length > 0)
  const editing = draft?.id ? list?.find((credential) => credential.id === draft.id) : undefined

  return (
    <Page>
      <PageHeader
        eyebrow={
          <Link href="/deploy" className="rounded-sm focus-ring hover:underline">
            Deployments
          </Link>
        }
        title="Credentials"
        actions={
          admin && (
            <Button size="sm" onClick={() => open(emptyDraft("git_bearer"))}>
              <Plus className="size-3.5" />
              Add credential
            </Button>
          )
        }
      />

      {/* Nothing saved is said once, by the kinds below, not by four tiles
          of zeroes — as Notifications shows no tiles without a channel. */}
      {admin && (list === undefined || list.length > 0) && (
        <CredentialReadings list={list} failed={Boolean(credentials.error)} />
      )}

      <GitHubAppPanel
        admin={admin}
        status={githubApp}
        credentials={list}
        onChange={credentials.refresh}
      />

      <Panel plain>
        <PanelHeader
          title="Saved credentials"
          actions={
            list &&
            list.length > 0 && (
              <span className="numeric text-hint text-muted-foreground">
                {plural(list.length, "credential")}
              </span>
            )
          }
        />
        <PanelBody>
          {!admin ? (
            <EmptyNote>An administrator manages the saved credentials.</EmptyNote>
          ) : credentials.error ? (
            <ErrorState error={credentials.error} onRetry={credentials.refresh} />
          ) : credentials.loading && !list ? (
            <LoadingRows rows={3} />
          ) : (list?.length ?? 0) === 0 ? (
            // The empty list is the same picker the sheet opens on: the four
            // kinds, each pressed straight into its form.
            <ChoiceGrid columns={2} className="animate-rise">
              {KIND_OPTIONS.map((option, index) => (
                <ChoiceCard
                  key={option.kind}
                  verb={`Add ${option.title}`}
                  mark={KIND_GLYPH[option.kind]}
                  title={option.title}
                  description={option.hint}
                  index={index}
                  onClick={() => open(emptyDraft(option.kind))}
                />
              ))}
            </ChoiceGrid>
          ) : groups.length > 1 ? (
            <ul aria-label="Credentials" className="animate-rise space-y-5">
              {groups.map((group) => (
                <li key={group.key} className="flex min-w-0 flex-col gap-2">
                  <GroupRule label={group.label} count={group.rows.length} />
                  <ChoiceList aria-label={group.label}>{group.rows.map(card)}</ChoiceList>
                </li>
              ))}
            </ul>
          ) : (
            <ChoiceList aria-label="Credentials" className="animate-rise">
              {list?.map(card)}
            </ChoiceList>
          )}
        </PanelBody>
      </Panel>

      <SidePanel
        open={Boolean(draft)}
        onOpenChange={(open) => !open && closeDraft()}
        title={draft?.id ? "Edit credential" : "Add credential"}
        description="A token or key the server uses to read a private repository or registry."
        width="md"
        footer={
          <>
            <Button variant="outline" onClick={closeDraft} disabled={saving}>
              Cancel
            </Button>
            <Button pending={saving} disabled={incomplete} onClick={save}>
              {draft?.id ? "Save credential" : "Add credential"}
            </Button>
          </>
        }
      >
        {draft && (
          <CredentialForm
            draft={draft}
            onChange={setDraft}
            onKind={(kind) => open({ ...emptyDraft(kind), name: draft.name })}
            saved={editing}
            installation={editing && installationOf(editing)}
            fieldErrors={fieldErrors}
            formError={formError}
            saving={saving}
          />
        )}
      </SidePanel>

      {/* A probe marks the credential used, so the card's "last used" is
          stale the moment the dialog closes unless the list re-reads. */}
      <TestCredentialDialog
        credential={testing}
        installation={testing && installationOf(testing)}
        onProbe={setProbing}
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
 * Four readings over the saved credentials, each a question an operator asks
 * of a keyring — the account keys page asks the same four of its API keys: how
 * many are held and across which hosts, which of them a project actually
 * reads through, which were saved and never used (a credential nobody uses is
 * one nobody would notice being used), and when one was last reached for.
 * Drawn only once something is saved: an empty keyring is the kinds below.
 */
function CredentialReadings({
  list,
  failed,
}: {
  list: DeploymentCredential[] | undefined
  failed: boolean
}) {
  // A count arrives by counting: the figure rises with its tile and settles on
  // the value, rather than the value being there before the tile is.
  const figure = (value: number | undefined) =>
    value === undefined ? (
      failed ? (
        "—"
      ) : (
        <Skeleton className="h-6 w-8" />
      )
    ) : (
      <NumberTicker value={value} />
    )
  const ready = list !== undefined
  const hosts = new Set((list ?? []).map((credential) => credential.target).filter(Boolean))
  const products = [...new Set((list ?? []).flatMap((c) => credentialProduct(c) ?? []))]
  const inUse = (list ?? []).filter((credential) => credential.usedBy > 0)
  // Projects, the unit the cards count in, and each once: two credentials
  // behind one project (its Git token and its registry login) are one project.
  const projectIds = new Set(inUse.flatMap((credential) => credential.usedByProjectIds ?? []))
  const reached =
    projectIds.size || inUse.reduce((sum, credential) => sum + projectsUsing(credential), 0)
  const unused = (list ?? [])
    .filter((credential) => !credential.lastUsedAt)
    .sort((a, b) => a.createdAt.localeCompare(b.createdAt))
  const latest = (list ?? [])
    .filter((credential) => credential.lastUsedAt)
    .sort((a, b) => (b.lastUsedAt ?? "").localeCompare(a.lastUsedAt ?? ""))[0]
  const rise = ready ? "animate-rise" : undefined
  const key = (name: string) => (ready ? name : `${name}-loading`)

  return (
    <StatGrid columns={4} dense>
      <StatTile
        key={key("credentials")}
        label="Credentials"
        value={figure(list?.length)}
        hint={
          ready && (
            <span className="inline-flex max-w-full min-w-0 items-center gap-2">
              <span className="truncate">
                {/* A host is optional for every kind but a registry login. */}
                {hosts.size > 0 ? `across ${plural(hosts.size, "host")}` : "no host named"}
              </span>
              <ProductGlyphs ids={products} />
            </span>
          )
        }
        className={rise}
      />
      <StatTile
        key={key("in-use")}
        label="In use"
        value={figure(list && inUse.length)}
        hint={
          ready &&
          (reached > 0
            ? `${plural(reached, "project")} read through them`
            : "nothing reads one yet")
        }
        className={rise}
      />
      <StatTile
        key={key("never-used")}
        label="Never used"
        value={figure(list && unused.length)}
        tone={unused.length > 0 ? "warning" : "default"}
        hint={
          ready &&
          (unused.length > 0
            ? `${unused[0].name} added ${calendarDate(unused[0].createdAt)}`
            : "every credential has been used")
        }
        className={rise}
      />
      <StatTile
        key={key("last-used")}
        label="Last used"
        value={
          ready ? (
            latest ? (
              relativeTime(latest.lastUsedAt)
            ) : (
              "—"
            )
          ) : failed ? (
            "—"
          ) : (
            <Skeleton className="h-6 w-20" />
          )
        }
        hint={
          ready &&
          (latest ? (
            <span className="inline-flex max-w-full min-w-0 items-center gap-1.5">
              <CredentialGlyph credential={latest} />
              <span className="truncate">{latest.name}</span>
            </span>
          ) : (
            "never"
          ))
        }
        className={rise}
      />
    </StatGrid>
  )
}

/**
 * One saved credential, as a card that opens its editor: drawn as its host,
 * named, with where it signs in as the literal the server holds and how much
 * it is used after it. The kind is a property of the card, so it sits at the
 * card's edge (§4) — under the name on a phone. The projects reading through
 * it are drawn as themselves under the line, each at a line's height beside
 * its name, so "used by 2 projects" says which two.
 *
 * An App credential gets the card without its control: there is nothing on it
 * to edit. Its kind still lines up with the others', in the arrow's place.
 */
function CredentialCard({
  credential,
  installation,
  projects,
  verbs,
  onEdit,
  probing,
  wide,
}: {
  credential: DeploymentCredential
  installation?: GitHubAppInstallation
  projects: DeploymentSummary[]
  verbs: Verb[]
  onEdit?: () => void
  probing: boolean
  wide: boolean
}) {
  const where = [credential.target, credential.username].filter(Boolean).join(" · ")
  // The participle is the Test verb's own, declared once with it (§13).
  const testing = verbs.find((verb) => verb.key === "test")?.progressive
  const tag =
    probing && testing ? (
      <TextShimmer className="text-xs font-medium">{testing}</TextShimmer>
    ) : (
      <Tag>{KIND_LABEL[credential.kind]}</Tag>
    )
  const shown = projects.slice(0, 3)
  const usage = `${usageLabel(projectsUsing(credential))} · ${lastUsedLabel(credential.lastUsedAt)}`
  return (
    <ChoiceRow
      leading={<CredentialMark credential={credential} installation={installation} />}
      title={credential.name}
      verb={`Edit ${credential.name}`}
      onSelect={onEdit}
      disabled={!onEdit}
      busy={probing}
      description={
        // On a phone the line holds the literal alone; how it is used goes
        // under the name, where it has the card's width instead of the
        // hundred pixels the verbs leave.
        wide || !where ? (
          <>
            {where && (
              <>
                <span className="font-mono">{where}</span>
                {" · "}
              </>
            )}
            {usage}
          </>
        ) : (
          <span className="font-mono">{where}</span>
        )
      }
      trailing={
        wide ? (
          <>
            {tag}
            {!onEdit && <span aria-hidden className="w-3.5 shrink-0" />}
          </>
        ) : undefined
      }
      actions={<VerbActions dim verbs={verbs} menuLabel={`Actions for ${credential.name}`} />}
    >
      {(!wide || shown.length > 0) && (
        <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1.5 text-hint text-muted-foreground sm:pl-11">
          {!wide && tag}
          {!wide && where && <span>{usage}</span>}
          {shown.length > 0 && (
            <span className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
              <span>reads the source of</span>
              {shown.map((project) => (
                <span key={project.id} className="inline-flex min-w-0 items-center gap-1.5">
                  <ProjectMark deployment={project} size="xs" />
                  <span className="truncate text-foreground/80">{project.name}</span>
                </span>
              ))}
              {projects.length > shown.length && (
                <span className="numeric">+{projects.length - shown.length}</span>
              )}
            </span>
          )}
        </div>
      )}
    </ChoiceRow>
  )
}

/**
 * The add and edit form. Editing opens on the credential itself — its mark,
 * its name and what is known about it — because the kind is fixed once
 * created; adding opens on the four kinds as cards. Then the name, then where
 * it signs in and the secret, each a section (§7's sheet anatomy).
 *
 * Every field reads what is typed into it: the host draws the product it
 * names, a token its issuer from its prefix, a key its format from its first
 * line — so a GitLab token pasted for github.com, or the public half of a key
 * pair, is caught before it is sealed and never shown again.
 */
function CredentialForm({
  draft,
  onChange,
  onKind,
  saved,
  installation,
  fieldErrors,
  formError,
  saving,
}: {
  draft: Draft
  onChange: (draft: Draft) => void
  onKind: (kind: DeploymentCredentialKind) => void
  saved?: DeploymentCredential
  installation?: GitHubAppInstallation
  fieldErrors: Record<string, string>
  formError: string
  saving: boolean
}) {
  const host = bareHost(draft.target)
  const hostMark = hostProduct(host)
  const HostGlyph = KIND_GLYPH[draft.kind]
  const name = effectiveName(draft)
  const projects = saved ? projectsUsing(saved) : 0

  const hostField = (
    <Field
      label="Host"
      htmlFor="credential-target"
      hint={TARGET_HINT[draft.kind]}
      error={fieldErrors.target}
    >
      <InputGroup>
        {/* The product the host names, drawn as it is typed — the way
            an API key's name field draws the service it will be for. */}
        <InputGroupAddon>
          {hostMark ? <ProductGlyph id={hostMark} /> : <HostGlyph aria-hidden />}
        </InputGroupAddon>
        <InputGroupInput
          id="credential-target"
          value={draft.target}
          onChange={(event) => onChange({ ...draft, target: event.target.value })}
          placeholder={TARGET_PLACEHOLDER[draft.kind]}
          className="font-mono"
          autoComplete="off"
          spellCheck={false}
        />
        {/* A pasted URL is fine: say what it will be saved as, before
            the press rather than after. */}
        {host && host !== draft.target.trim() && (
          <InputGroupAddon align="inline-end">
            <InputGroupText>
              saves as <span className="font-mono text-foreground">{host}</span>
            </InputGroupText>
          </InputGroupAddon>
        )}
      </InputGroup>
    </Field>
  )

  return (
    <div className="space-y-6" aria-busy={saving}>
      {saved ? (
        <div className="flex min-w-0 items-start gap-3">
          <CredentialMark credential={saved} installation={installation} />
          <div className="min-w-0 space-y-1">
            <p className="truncate text-body font-medium">{saved.name}</p>
            <FormFacts>
              <FormFact label="Kind">
                <span className="inline-flex items-center gap-1 align-[-2px]">
                  <KindGlyph kind={saved.kind} />
                  {KIND_LABEL[saved.kind]}
                </span>
              </FormFact>
              {saved.target && (
                <FormFact label="Host" mono>
                  <span className="inline-flex items-center gap-1 align-[-2px]">
                    <CredentialGlyph credential={saved} />
                    {saved.target}
                  </span>
                </FormFact>
              )}
              <FormFact label="Used by">
                {projects > 0 ? plural(projects, "project") : "nothing"}
              </FormFact>
              <FormFact label="Last used">
                {saved.lastUsedAt ? relativeTime(saved.lastUsedAt) : "never"}
              </FormFact>
              <FormFact label="Added">{calendarDate(saved.createdAt)}</FormFact>
            </FormFacts>
          </div>
        </div>
      ) : draft.id ? (
        // A draft kept from an earlier visit, before the list is back: the
        // kind is the draft's own.
        <FormFacts>
          <FormFact label="Kind">
            <span className="inline-flex items-center gap-1 align-[-2px]">
              <KindGlyph kind={draft.kind} />
              {KIND_LABEL[draft.kind]}
            </span>
          </FormFact>
        </FormFacts>
      ) : (
        <fieldset className="min-w-0">
          <legend className="mb-1.5 text-body font-medium">Kind</legend>
          <div className="grid grid-cols-2 gap-2">
            {KIND_OPTIONS.map((option) => {
              const Glyph = KIND_GLYPH[option.kind]
              const selected = draft.kind === option.kind
              return (
                <ChoiceCard
                  key={option.kind}
                  selected={selected}
                  className="min-h-0 flex-row items-start gap-2.5 px-3 py-2.5"
                  onClick={() => {
                    if (!selected) onKind(option.kind)
                  }}
                >
                  <Glyph
                    aria-hidden
                    className={cn(
                      "mt-px size-4 shrink-0",
                      selected ? "text-brand" : "text-muted-foreground",
                    )}
                  />
                  <span className="flex min-w-0 flex-col gap-0.5">
                    <ChoiceCardTitle>{option.title}</ChoiceCardTitle>
                    <ChoiceCardHint className="line-clamp-2">{option.short}</ChoiceCardHint>
                  </span>
                </ChoiceCard>
              )
            })}
          </div>
        </fieldset>
      )}

      <div className="space-y-2">
        <Field
          label="Name"
          htmlFor="credential-name"
          hint="Left empty, it is saved under the name shown."
          error={fieldErrors.name}
        >
          <Input
            id="credential-name"
            value={draft.name}
            onChange={(event) => onChange({ ...draft, name: event.target.value })}
            placeholder={suggestedName(draft)}
            autoComplete="off"
            spellCheck={false}
          />
        </Field>
        {/* The server's own rule for a name, lit as it is met, so a refusal is
            never the first the reader hears of it. */}
        <p className="flex flex-wrap gap-x-4 gap-y-1" aria-live="polite">
          <FieldCheck met={/^[A-Za-z0-9]/.test(name)}>starts with a letter or digit</FieldCheck>
          <FieldCheck met={/^[A-Za-z0-9._-]*$/.test(name)}>letters, digits . - _ only</FieldCheck>
          <FieldCheck met={name.length <= 64}>64 at most</FieldCheck>
        </p>
      </div>

      <FormSection title="Where it signs in">
        {draft.kind === "registry" ? (
          <FieldRow>
            {hostField}
            <Field
              label="Username"
              htmlFor="credential-username"
              hint="Required. The account the registry knows this login by."
              error={fieldErrors.username}
            >
              <Input
                id="credential-username"
                value={draft.username}
                onChange={(event) => onChange({ ...draft, username: event.target.value })}
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
          </FieldRow>
        ) : (
          hostField
        )}
      </FormSection>

      <FormSection title="Secret">
        <SecretField
          draft={draft}
          onChange={onChange}
          host={host}
          error={fieldErrors.secret}
          projects={projects}
        />
        <FormNote>Sealed with the server’s key. The dashboard never shows it again.</FormNote>
      </FormSection>

      {formError && (
        <FormNote tone="danger" role="alert">
          {formError}
        </FormNote>
      )}
    </div>
  )
}

function KindGlyph({ kind }: { kind: DeploymentCredentialKind }) {
  const Glyph = KIND_GLYPH[kind]
  return <Glyph aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
}

/**
 * The secret, and what it says about itself. A token names its issuer by its
 * prefix, so the field draws it and warns when the issuer is not the host's;
 * a key names its format on its first line, so the field names it and warns
 * on the public half. A key can also be read from its file. The hint under
 * the field stays the sentence it always was — the reading is beside it, not
 * instead of it.
 */
function SecretField({
  draft,
  onChange,
  host,
  error,
  projects,
}: {
  draft: Draft
  onChange: (draft: Draft) => void
  host: string
  error?: string
  projects: number
}) {
  const [shown, setShown] = useState(false)
  const file = useRef<HTMLInputElement>(null)
  const label = SECRET_LABEL[draft.kind]
  const hint = draft.id
    ? `${SECRET_HINT[draft.kind]} Leave empty to keep the stored secret.`
    : SECRET_HINT[draft.kind]
  const hostMark = hostProduct(host)
  const token = draft.kind === "git_ssh" ? undefined : tokenProduct(draft.secret)
  const shape = draft.kind === "git_ssh" ? sshKeyShape(draft.secret) : undefined
  const set = (secret: string) => onChange({ ...draft, secret })

  return (
    <>
      {draft.kind === "git_ssh" ? (
        <Field
          label={label}
          htmlFor="credential-secret"
          hint={hint}
          error={error}
          trailing={
            <>
              {shape && shape !== "public" && <FieldCheck met>{KEY_WORD[shape]}</FieldCheck>}
              <Button size="xs" variant="outline" onClick={() => file.current?.click()}>
                Choose file
              </Button>
              <input
                ref={file}
                type="file"
                className="hidden"
                onChange={(event) => {
                  const chosen = event.target.files?.[0]
                  event.target.value = ""
                  if (!chosen) return
                  // The server refuses a key over 64 KiB, and a file that size
                  // picked by mistake is megabytes of text in the field.
                  if (chosen.size > 64 * 1024) {
                    notify.error("That file is too large to be a private key")
                    return
                  }
                  chosen
                    .text()
                    .then(set, (caught) => notify.error("Could not read the key file", caught))
                }}
              />
            </>
          }
        >
          <Textarea
            id="credential-secret"
            value={draft.secret}
            onChange={(event) => set(event.target.value)}
            placeholder={
              draft.id
                ? "Leave empty to keep the stored key"
                : "-----BEGIN OPENSSH PRIVATE KEY-----"
            }
            className="min-h-40 font-mono sm:text-xs"
            autoComplete="off"
            spellCheck={false}
          />
        </Field>
      ) : (
        <Field label={label} htmlFor="credential-secret" hint={hint} error={error}>
          <InputGroup>
            <InputGroupInput
              id="credential-secret"
              type={shown ? "text" : "password"}
              value={draft.secret}
              onChange={(event) => set(event.target.value)}
              autoComplete="new-password"
              spellCheck={false}
              className={cn(shown && "font-mono")}
            />
            {token && (
              <InputGroupAddon align="inline-end">
                <InputGroupText>
                  <ProductGlyph id={token.product} />
                  <span className="max-sm:sr-only">{token.word}</span>
                </InputGroupText>
              </InputGroupAddon>
            )}
            <InputGroupAddon align="inline-end" className="gap-0 p-0">
              <InputGroupToggle
                icon={shown ? EyeOff : Eye}
                label="Show"
                aria-label={`Show ${label.toLowerCase()}`}
                pressed={shown}
                onPressedChange={setShown}
              />
            </InputGroupAddon>
          </InputGroup>
        </Field>
      )}
      {token && hostMark && token.product !== hostMark && (
        <FormNote tone="warning">
          This looks like a {token.word}, but the host is {host}.
        </FormNote>
      )}
      {shape === "public" && (
        <FormNote tone="warning">
          That is the public half — paste the private key; the public half goes in the provider’s
          deploy keys.
        </FormNote>
      )}
      {draft.id && draft.secret && projects > 0 && (
        <FormNote tone="warning">
          {projects === 1
            ? "1 project reads its source through this; its next run uses the new secret."
            : `${projects} projects read their source through this; their next run uses the new secret.`}
        </FormNote>
      )}
    </>
  )
}

/**
 * The live probe. Every kind needs something concrete to try — a repository
 * to list, an image to resolve — because the server refuses a bare "does this
 * token work" with nothing to point it at, and a dialog that let the field
 * stay empty was a dialog whose Test button only ever produced an error.
 *
 * What was tried last is remembered per credential for the tab, so a retest
 * after fixing the token is one press. While the server works, a bar sweeps
 * across the top (§11: working, and cannot say how far); an answer the reader
 * has to act on is a notice, and a success is a line.
 */
function TestCredentialDialog({
  credential,
  installation,
  onProbe,
  onClose,
}: {
  credential?: DeploymentCredential
  installation?: GitHubAppInstallation
  /** Which credential a probe is out for, so its card can say so. */
  onProbe: (id: number | undefined) => void
  onClose: () => void
}) {
  const [subject, setSubject] = useMemoryState(`deploy.credentials.test.${credential?.id ?? 0}`, "")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [result, setResult] = useState<{ ok: boolean; message: string; at: string }>()

  // Reset on the way out rather than in an effect keyed on `credential`: the
  // dialog's own overlay blocks the row underneath, so the credential being
  // tested can only change between one close and the next open, never while
  // it is open — closing is the one moment a fresh attempt is guaranteed.
  const close = () => {
    onClose()
    setError("")
    setResult(undefined)
  }

  const registry = credential?.kind === "registry"
  const target = credential?.target

  const run = async () => {
    // Enter reaches here too, and the button's `pending` stops only a press.
    if (!credential || !subject.trim() || busy) return
    setBusy(true)
    onProbe(credential.id)
    setError("")
    setResult(undefined)
    try {
      const response = await post<{ ok: boolean; message: string }>(
        `/deploy/credentials/${credential.id}/test`,
        { repository: subject.trim() },
      )
      setResult({ ...response, at: new Date().toISOString() })
    } catch (caught) {
      if (caught instanceof ApiError && caught.status === 400) {
        setError(caught.message)
      } else {
        notify.error("Could not run the test", caught)
      }
    } finally {
      setBusy(false)
      onProbe(undefined)
    }
  }

  return (
    <Modal
      open={Boolean(credential)}
      onOpenChange={(open) => !busy && !open && close()}
      title={credential ? `Test ${credential.name}` : "Test credential"}
      description="Send a live check using this credential right now."
      size="sm"
      bodyClassName="relative p-4"
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
      {busy && (
        <div aria-hidden className="absolute inset-x-0 top-0 h-0.5 overflow-hidden bg-meter-track">
          <div className="h-full w-1/3 animate-sweep bg-brand" />
        </div>
      )}
      {credential && (
        <div className="space-y-4">
          <div className="flex min-w-0 items-center gap-3">
            <CredentialMark credential={credential} installation={installation} />
            <FormFacts>
              {target && (
                <FormFact label="Host" mono>
                  {target}
                </FormFact>
              )}
              <FormFact label="Kind">{KIND_LABEL[credential.kind]}</FormFact>
              {credential.username && (
                <FormFact label="Username" mono>
                  {credential.username}
                </FormFact>
              )}
            </FormFacts>
          </div>
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
              onKeyDown={(event) => {
                if (event.key === "Enter") void run()
              }}
              placeholder={registry ? `${target}/owner/app:latest` : "owner/name"}
              className="font-mono"
              autoComplete="off"
              spellCheck={false}
            />
          </Field>
          {result && (
            <div role="status" className="animate-rise">
              {result.ok ? (
                <div className="space-y-1">
                  <Status
                    tone="running"
                    icon={CheckCircle}
                    label={result.message}
                    className="items-start whitespace-normal [&>svg]:mt-px"
                  />
                  <p className="pl-5 text-hint text-muted-foreground">
                    tested {relativeTime(result.at)}
                  </p>
                </div>
              ) : (
                <Notice tone="danger" icon={CrossCircle} title="The server could not use it">
                  {result.message}
                </Notice>
              )}
            </div>
          )}
        </div>
      )}
    </Modal>
  )
}

/**
 * The credential picker a source form hands its `credentialId` through —
 * shared here so the new-project Git/image steps and a project's own Source
 * settings draw the same control rather than three copies of it.
 *
 * Each option is drawn the way its card is on Credentials — the host's mark
 * before the name — with the host and kind at the far end, outside the part
 * the trigger repeats, so the closed control still reads the bare name. When
 * nothing of the right kind is saved, the line under it says so and where to
 * add one.
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
  // A saved credential the list does not hold — deleted since, or of the
  // other kind — matches no item, and the trigger was drawn empty.
  const missing = Boolean(
    credentials.data && value && !options.some((credential) => credential.id === value),
  )
  const link =
    "rounded-sm text-hint text-muted-foreground underline underline-offset-2 focus-ring hover:text-foreground"
  return (
    <div className="space-y-1.5">
      <Select
        value={value ? String(value) : "none"}
        onValueChange={(next) => onChange(next === "none" ? undefined : Number(next))}
        disabled={disabled}
      >
        <SelectTrigger id={id} className="w-full">
          <SelectValue>{missing ? `Credential #${value} · not found` : undefined}</SelectValue>
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="none">Server default</SelectItem>
          {options.map((credential) => (
            <SelectItem
              key={credential.id}
              value={String(credential.id)}
              hint={[credential.target, KIND_LABEL[credential.kind]].filter(Boolean).join(" · ")}
            >
              <CredentialGlyph credential={credential} />
              {credential.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {credentials.data && options.length === 0 ? (
        <p className="text-hint text-muted-foreground">
          No saved {kind === "git" ? "Git credentials" : "registry logins"} —{" "}
          <Link href="/deploy/credentials" className={link}>
            Add one
          </Link>
        </p>
      ) : (
        <Link href="/deploy/credentials" className={cn("block w-fit", link)}>
          Manage credentials…
        </Link>
      )}
    </div>
  )
}
