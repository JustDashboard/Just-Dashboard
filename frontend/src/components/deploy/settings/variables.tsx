"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import type { FormEvent } from "react"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { useMemoryState, useSessionState } from "@/lib/view-state"
import { ApiError, del, get, post, put } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { plural, relativeTime } from "@/lib/format"
import { hueFor, LANES } from "@/lib/hue"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type {
  DeploymentDatabaseLink,
  DeploymentDotenvImportPreview,
  DeploymentEnvironmentConfiguration,
  DeploymentVariable,
} from "@/lib/types"
import { browserInlined, pointsAtLocalhost } from "@/components/deploy/deployment-defaults"
import { InitialsMark } from "@/components/account/user-avatar"
import { FileIcon } from "@/components/files/file-icon"
import { ChoiceList, ChoiceRow, GroupRule } from "@/components/flow"
import {
  Field,
  FormFact,
  FormFacts,
  FormNote,
  FormSection,
  OptionList,
  OptionRow,
} from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { SearchInput } from "@/components/page"
import { Well } from "@/components/panel"
import {
  ProductGlyph,
  ProductGlyphs,
  ProductLogo,
  variableProduct,
} from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { EmptyNote, EmptyState, Notice } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { VerbActions, type Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
  InputGroupToggle,
} from "@/components/ui/input-group"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { useConfirm } from "@/components/confirm-dialog"
import {
  Code,
  Copy,
  Database,
  Eye,
  EyeOff,
  FileText,
  Key,
  Linked,
  LockClosed,
  Pencil,
  Plus,
  RefreshClockwise,
  Sparkles,
  Trash,
  Warning,
} from "@/components/icons"
import { LINK_STATUS } from "@/components/deploy/vocabulary"
import { NAME, PLATFORM_NAMES, readDotenv, REFUSAL_WORD } from "@/components/deploy/settings/dotenv"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { SettingSection, SettingsPage } from "@/components/deploy/settings/setting-card"
import {
  ConfigurationState,
  useConfiguration,
} from "@/components/deploy/settings/use-configuration"

/**
 * The variables a build, a running release, or a release task can read.
 *
 * The page is the list. It opened on an empty "Add variable" form that
 * filled the whole first screen — about 780px of fields before the first
 * variable at 1280, a thousand on a phone — so the thing an operator comes
 * here to check, what is set and who can read it, was a scroll away every
 * time. What exists comes first now, and writing one is a sheet: the editor
 * and the `.env` import each open over the list from the section's head, the
 * way every other thing you add in the deployment section does.
 *
 * Each variable is drawn as the service its name says holds it — Stripe's
 * mark on `STRIPE_SECRET_KEY`, Sentry's on `SENTRY_DSN` (`variableProduct`,
 * `keyProduct`'s argument applied to environment names) — and a typed
 * reference as the database it points at, by name and in its engine's colours,
 * rather than as `database.11`. Who can read it is a fixed three-slot column
 * at the row's edge, present slots lit and absent ones faint, so a column of
 * twenty reads as a matrix rather than twenty runs of tags against twenty
 * names (§4). The person who set it is their face.
 *
 * Where §15 pass 2's figures went: this page takes the `/git` exit. Every
 * count — how many variables, how many secret, pending, reaching the build,
 * holding a reference — is on a filter chip over the list, where it also
 * narrows the list to what it counts; a tile could only have said it. The
 * products the environment talks to sit under the section's title.
 *
 * `compact` is for the automation agent's preview-environment sheet: a sheet
 * cannot open a second sheet, so there the editor stays inline above the
 * list, and a preview environment is never the target of the project's own
 * "Deploy changes" that the settings frame's pending strip describes.
 */

type Scope = DeploymentVariable["scopes"][number]
type Sensitivity = DeploymentVariable["sensitivity"]

/**
 * Who can read a variable. The title says the whole of it (§7): "Build" alone
 * left the reader to guess that a build sees a secret only through a mount
 * that is gone when the step ends.
 */
const SCOPES: { scope: Scope; word: string; title: string }[] = [
  { scope: "build", word: "build", title: "Build — through temporary secret mounts" },
  {
    scope: "runtime",
    word: "runtime",
    title: "Runtime — as an environment variable in the container",
  },
  {
    scope: "release_task",
    word: "release task",
    title: "Release task — only the tasks that list it",
  },
]

type Filter = "all" | "pending" | Sensitivity | Scope | "reference"

const FILTERS: { key: Filter; label: string; test: (variable: DeploymentVariable) => boolean }[] = [
  { key: "all", label: "All", test: () => true },
  { key: "pending", label: "Pending", test: (variable) => variable.pending },
  { key: "secret", label: "Secrets", test: (variable) => variable.sensitivity === "secret" },
  { key: "plain", label: "Config", test: (variable) => variable.sensitivity === "plain" },
  { key: "build", label: "Build", test: (variable) => variable.scopes.includes("build") },
  { key: "runtime", label: "Runtime", test: (variable) => variable.scopes.includes("runtime") },
  {
    key: "release_task",
    label: "Release task",
    test: (variable) => variable.scopes.includes("release_task"),
  },
  { key: "reference", label: "References", test: (variable) => Boolean(variable.reference) },
]

const EDITOR_FORM = "variable-editor"

/**
 * A prefix two or more names share takes a hue, so a family — STRIPE_*,
 * NEXT_PUBLIC_* — reads as one down the list, as one process does down the
 * log console (§14). LANES, not every hue: the row also carries a warning.
 */
function prefixHues(names: string[]) {
  const count = new Map<string, number>()
  for (const name of new Set(names)) {
    const prefix = name.split("_")[0]
    if (name.includes("_")) count.set(prefix, (count.get(prefix) ?? 0) + 1)
  }
  return (name: string) => {
    const prefix = name.split("_")[0]
    return name.includes("_") && (count.get(prefix) ?? 0) > 1 ? hueFor(prefix, LANES) : undefined
  }
}

/** The typed-reference literal a stored reference reads back as — the same
 * `${{kind.target}}` shape the Value field accepts and PutVariable parses. */
function referenceLiteral(reference: NonNullable<DeploymentVariable["reference"]>) {
  return `\${{${reference.kind}.${reference.target}}}`
}

export function VariablesPanel({
  projectId,
  environmentId,
  compact = false,
}: {
  projectId: number
  environmentId: number
  compact?: boolean
}) {
  const { can } = useAuth()
  const state = useConfiguration(projectId, environmentId)
  // No `key={configuration.revision}` on the body: unlike Build/Runtime/General
  // it keeps no editable copy of server state that a save could leave stale —
  // the editor's fields reset themselves, and the list reads
  // `configuration.variables` fresh on every render. The one local state a
  // remount would destroy is the one-time generated value and which rows are
  // revealed, and both must survive the revision bump that saving *any*
  // variable causes.
  const body = (configuration: DeploymentEnvironmentConfiguration) => (
    <VariablesBody
      projectId={projectId}
      environmentId={environmentId}
      configuration={configuration}
      canEdit={can("system.admin")}
      compact={compact}
      onChanged={state.refresh}
    />
  )
  if (compact) return <ConfigurationState state={state}>{body}</ConfigurationState>
  return (
    <SettingsPage state={state} pageKinds={["variable"]}>
      {body}
    </SettingsPage>
  )
}

function VariablesBody({
  projectId,
  environmentId,
  configuration,
  canEdit,
  compact,
  onChanged,
}: {
  projectId: number
  environmentId: number
  configuration: DeploymentEnvironmentConfiguration
  canEdit: boolean
  compact: boolean
  onChanged: () => void
}) {
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  // A row's readings sit beside its name where the list's own column has room
  // for both, and under it where it does not.
  const [column, columnWidth] = useColumnWidth()
  const wide = columnWidth >= 600
  // The editor is kept for the tab — its value and a pasted .env in memory
  // only, since those are the secrets — so a look at the source for the right
  // key does not mean starting the variable again.
  const draft = `deploy.${projectId}.${environmentId}.variables`
  // A failed run's remedy arrives as `?variable=NAME&scope=build[&value=…]`
  // and opens the editor on it: an existing variable with the scope its step
  // lacked added, or a new one with the value the server computed — a flag,
  // never a secret. The address is what the editor opens on, and is then
  // dropped so a reload does not open it again.
  const search = useSearchParams()
  const remedy = !compact && canEdit ? search.get("variable") : null
  const remedyScope = SCOPES.find((entry) => entry.scope === search.get("scope"))?.scope
  const remedyTarget = remedy
    ? configuration.variables.find((variable) => variable.name === remedy)
    : undefined
  const remedyValue = remedy && !remedyTarget ? (search.get("value") ?? "") : ""
  const [editorOpen, setEditorOpen] = useSessionState(`${draft}.open`, false, remedy ? true : null)
  const [importOpen, setImportOpen] = useSessionState(`${draft}.import`, false)
  const [name, setName] = useSessionState(`${draft}.name`, "", remedy)
  const [value, setValue] = useMemoryState(
    `${draft}.value`,
    "",
    remedy
      ? remedyTarget?.reference
        ? referenceLiteral(remedyTarget.reference)
        : remedyValue
      : null,
  )
  const [valueError, setValueError] = useState("")
  const [shown, setShown] = useState(false)
  const [reference, setReference] = useSessionState(
    `${draft}.reference`,
    false,
    remedy ? Boolean(remedyTarget?.reference) : null,
  )
  const [sensitivity, setSensitivity] = useSessionState<Sensitivity>(
    `${draft}.sensitivity`,
    "secret",
    remedy ? (remedyTarget?.sensitivity ?? (remedyValue ? "plain" : "secret")) : null,
  )
  const [scopes, setScopes] = useSessionState<Scope[]>(
    `${draft}.scopes`,
    ["runtime"],
    remedy
      ? [...new Set([...(remedyTarget?.scopes ?? []), ...(remedyScope ? [remedyScope] : [])])]
      : null,
  )
  useEffect(() => {
    if (remedy) router.replace(window.location.pathname, { scroll: false })
  }, [remedy, router])
  const [query, setQuery] = useSessionState(`${draft}.query`, "")
  const [filter, setFilter] = useSessionState<Filter>(`${draft}.filter`, "all")
  const [busy, setBusy] = useState("")
  const [error, setError] = useState("")
  const [revealed, setRevealed] = useState<Record<string, string>>({})
  const [generated, setGenerated] = useState<{ name: string; value: string }>()
  // The variable loaded into the editor, so the submit label, the "value was
  // never read back" hint and the client-side required check all agree on
  // whether this is an edit or a brand new variable.
  const [editingName, setEditingName] = useSessionState<string | undefined>(
    `${draft}.editing`,
    undefined,
    // "" is a new variable, overriding one this tab was editing before.
    remedy ? (remedyTarget ? remedy : "") : null,
  )
  const [revealingIntoForm, setRevealingIntoForm] = useState(false)
  // Reveal/rotate/remove all write with `configuration.revision`, which only
  // updates once the read behind it refreshes; a row action's own error goes
  // to a toast, never into the editor.
  const [rowBusy, setRowBusy] = useState("")

  const base = `/deploy/${projectId}/environments/${environmentId}/variables`
  const variables = configuration.variables

  // The databases the typed references point at, read only when there is a
  // reference to draw or one is being typed — in the sheet, or in the inline
  // editor `compact` keeps open above the list.
  const needsLinks =
    variables.some((variable) => variable.reference?.kind === "database") ||
    ((compact || editorOpen) && reference)
  const links = usePoll(
    (signal) =>
      get<DeploymentDatabaseLink[]>(
        `/deploy/${projectId}/environments/${environmentId}/database-links`,
        undefined,
        signal,
      ),
    15000,
    [projectId, environmentId],
    { enabled: needsLinks },
  )
  const linkFor = (variable: DeploymentVariable) =>
    variable.reference?.kind === "database"
      ? links.data?.find(
          (link) => String(link.connectionId) === variable.reference?.target.split(".")[0],
        )
      : undefined
  const productOf = (variable: DeploymentVariable) =>
    linkFor(variable)?.driver ?? variableProduct(variable.name)

  const pendingChange = new Map(
    configuration.pending.changes
      .filter((change) => change.kind === "variable")
      .map((change) => [change.name, change.change]),
  )
  const removed = [...pendingChange]
    .filter(([, change]) => change === "removed")
    .map(([changed]) => changed)
    .filter((changed) => !variables.some((variable) => variable.name === changed))

  const hueOf = prefixHues(variables.map((variable) => variable.name))

  const toggleScope = (scope: Scope, checked: boolean) =>
    setScopes((current) =>
      checked ? [...new Set([...current, scope])] : current.filter((item) => item !== scope),
    )

  // A name the framework compiles into the bundle is build input and public:
  // left at the runtime-only default it built as undefined, silently, while
  // the server saw the value. Only an untouched default is moved, and static
  // output, which has no runtime to read anything, takes the build alone.
  const staticOutput =
    configuration.build.method === "static" ||
    (configuration.build.method === "recipe" && Boolean(configuration.build.outputDirectory))
  const changeName = (next: string) => {
    setName(next)
    if (!editingName && browserInlined(next) && scopes.length === 1 && scopes[0] === "runtime") {
      setScopes(staticOutput ? ["build"] : ["runtime", "build"])
      setSensitivity("plain")
    }
  }

  const resetForm = () => {
    setName("")
    setValue("")
    setValueError("")
    setShown(false)
    setReference(false)
    setSensitivity("secret")
    setScopes(["runtime"])
    setError("")
    setEditingName(undefined)
  }

  const openEditor = () => {
    if (editingName) resetForm()
    setEditorOpen(true)
  }

  const closeEditor = () => {
    resetForm()
    setEditorOpen(false)
  }

  const mutate = async (label: string, action: () => Promise<void>) => {
    setBusy(label)
    setError("")
    try {
      await action()
      onChanged()
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : String(caught))
    } finally {
      setBusy("")
    }
  }

  // Row actions share `configuration.revision` with the editor, which only
  // catches up once the read behind it refreshes — two quick actions can each
  // read the same now-stale revision. Retrying once against a freshly-read
  // revision turns a spurious `revision_conflict` into a press that worked.
  const withCurrentRevision = async (action: (revision: number) => Promise<void>) => {
    try {
      await action(configuration.revision)
    } catch (caught) {
      if (!(caught instanceof ApiError) || caught.code !== "revision_conflict") throw caught
      const latest = await get<DeploymentEnvironmentConfiguration>(
        `/deploy/${projectId}/environments/${environmentId}/configuration`,
      )
      await action(latest.revision)
    }
  }

  const save = (event: FormEvent) => {
    event.preventDefault()
    if (!name.trim() || scopes.length === 0) {
      setError("Enter a variable name and choose at least one scope.")
      return
    }
    // Edit never reads the stored value back, so `value` is empty unless the
    // operator retyped it or used "Reveal current" — saving that as the new
    // value would silently wipe whatever was there.
    if (editingName && !reference && !value.trim()) {
      setValueError("Enter the value again — the dashboard does not read it back.")
      return
    }
    setValueError("")
    void mutate("save", async () => {
      await put(`${base}/${encodeURIComponent(name.trim())}`, {
        revision: configuration.revision,
        ...(reference ? { reference: value.trim() } : { value }),
        sensitivity,
        scopes,
      })
      notify.success("Variable saved", { description: "It is pending until the next deployment." })
      resetForm()
      setEditorOpen(false)
    })
  }

  const generate = () => {
    if (!name.trim() || scopes.length === 0) {
      setError("Enter a variable name and choose at least one scope.")
      return
    }
    void mutate("generate", async () => {
      const result = await post<{ generatedValue: string }>(
        `${base}/${encodeURIComponent(name.trim())}/generate`,
        { revision: configuration.revision, scopes },
      )
      setGenerated({ name: name.trim(), value: result.generatedValue })
      resetForm()
      setEditorOpen(false)
    })
  }

  const readValue = (variable: DeploymentVariable) =>
    get<{ value: string }>(`${base}/${encodeURIComponent(variable.name)}/reveal`)

  const reveal = async (variable: DeploymentVariable) => {
    setRowBusy(`reveal-${variable.name}`)
    try {
      const result = await readValue(variable)
      setRevealed((current) => ({ ...current, [variable.name]: result.value }))
    } catch (caught) {
      notify.error(`Could not reveal ${variable.name}`, caught)
    } finally {
      setRowBusy("")
    }
  }

  const hide = (variable: DeploymentVariable) =>
    setRevealed((current) => {
      const next = { ...current }
      delete next[variable.name]
      return next
    })

  // Copies without drawing: the value goes to the clipboard and never onto
  // the screen someone may be sharing.
  const copyValue = async (variable: DeploymentVariable) => {
    setRowBusy(`copy-${variable.name}`)
    try {
      const result = await readValue(variable)
      await copyText(result.value, `${variable.name} copied`)
    } catch (caught) {
      notify.error(`Could not copy ${variable.name}`, caught)
    } finally {
      setRowBusy("")
    }
  }

  const rotate = async (variable: DeploymentVariable) => {
    setRowBusy(`rotate-${variable.name}`)
    try {
      await withCurrentRevision(async (revision) => {
        const result = await post<{ generatedValue: string }>(
          `${base}/${encodeURIComponent(variable.name)}/rotate`,
          { revision },
        )
        setGenerated({ name: variable.name, value: result.generatedValue })
      })
      onChanged()
    } catch (caught) {
      notify.error(`Could not rotate ${variable.name}`, caught)
    } finally {
      setRowBusy("")
    }
  }

  const loadIntoForm = (variable: DeploymentVariable) => {
    setName(variable.name)
    setReference(Boolean(variable.reference))
    setValue(variable.reference ? referenceLiteral(variable.reference) : "")
    setShown(false)
    setSensitivity(variable.sensitivity)
    setScopes(variable.scopes)
    setError("")
    setValueError("")
    setEditingName(variable.name)
    if (!compact) setEditorOpen(true)
  }

  const revealIntoForm = async () => {
    if (!editingName) return
    setRevealingIntoForm(true)
    try {
      const result = await get<{ value: string }>(
        `${base}/${encodeURIComponent(editingName)}/reveal`,
      )
      setValue(result.value)
      setValueError("")
    } catch (caught) {
      notify.error(`Could not reveal ${editingName}`, caught)
    } finally {
      setRevealingIntoForm(false)
    }
  }

  const removeVariable = (variable: DeploymentVariable) =>
    confirm({
      title: `Remove ${variable.name}`,
      confirmLabel: "Remove variable",
      subject: {
        mark: <VariableMark variable={variable} product={productOf(variable)} />,
        name: <span className="font-mono">{variable.name}</span>,
        facts: (
          <>
            <FormFact label="Reaches">{scopeWords(variable.scopes)}</FormFact>
            <FormFact label="Set">
              {relativeTime(variable.createdAt)} by {variable.createdBy}
            </FormFact>
          </>
        ),
      },
      description:
        "The desired plan will stop including this variable. The live release remains unchanged until deployment.",
      action: async () => {
        setRowBusy(`remove-${variable.name}`)
        try {
          await withCurrentRevision((revision) =>
            del(`${base}/${encodeURIComponent(variable.name)}`, { body: { revision } }),
          )
          onChanged()
        } finally {
          setRowBusy("")
        }
      },
    })

  const verbsFor = (variable: DeploymentVariable): Verb[] => {
    const isRevealed = Boolean(revealed[variable.name])
    // Any one row action in flight disables the rest: two quick actions
    // otherwise race the revision refresh into a conflict.
    const verbs: Verb[] = [
      {
        key: "reveal",
        label: isRevealed ? "Hide" : "Reveal",
        detail: isRevealed ? "Hide the value again." : "Show the stored value once.",
        icon: isRevealed ? EyeOff : Eye,
        inline: true,
        disabled: Boolean(rowBusy),
        run: () => (isRevealed ? hide(variable) : void reveal(variable)),
      },
    ]
    if (canEdit)
      verbs.push({
        key: "edit",
        label: "Edit",
        detail: "Open it in the editor to change its value or who can read it.",
        icon: Pencil,
        run: () => loadIntoForm(variable),
      })
    verbs.push({
      key: "copy",
      label: "Copy value",
      detail: "Reads the stored value once and copies it without showing it. Audited.",
      icon: Copy,
      disabled: Boolean(rowBusy),
      run: () => void copyValue(variable),
    })
    // Compact is a preview environment inside the Automation page: the
    // project's Databases page is not where its links live.
    if (variable.reference?.kind === "database" && !compact)
      verbs.push({
        key: "database",
        label: "Open the database",
        detail: "The linked database this value is read from, on Databases & backups.",
        icon: Database,
        run: () => router.push(`/deploy/${projectId}/settings/databases`),
      })
    if (canEdit) {
      if (variable.sensitivity === "secret")
        verbs.push({
          key: "rotate",
          label: "Rotate",
          detail: "Replace the stored value with a freshly generated secret.",
          icon: RefreshClockwise,
          progressive: "Rotating…",
          disabled: Boolean(rowBusy),
          run: () => void rotate(variable),
        })
      verbs.push({
        key: "remove",
        label: "Remove",
        detail: "Stop including this variable in the desired plan.",
        icon: Trash,
        danger: true,
        disabled: Boolean(rowBusy),
        run: () => removeVariable(variable),
      })
    }
    return verbs
  }

  const needle = query.trim().toLowerCase()
  const active = FILTERS.find((item) => item.key === filter) ?? FILTERS[0]
  const sorted = [...variables].sort((a, b) => a.name.localeCompare(b.name))
  const filtered = sorted.filter(
    (variable) => active.test(variable) && variable.name.toLowerCase().includes(needle),
  )
  const products = [...new Set(sorted.map(productOf).filter((id): id is string => Boolean(id)))]

  const nameProblem = name && !NAME.test(name.trim())
  const exists =
    !editingName && variables.some((variable) => variable.name === name.trim().toUpperCase())
  const editing = editingName
    ? variables.find((variable) => variable.name === editingName)
    : undefined

  const editorFields = (
    <div className="space-y-6">
      <VariableSubject
        name={name.trim()}
        sensitivity={sensitivity}
        product={editing ? productOf(editing) : variableProduct(name.trim())}
        editing={editing}
      />
      {/* No section head: the sheet's title already says what this is. An
          existing variable is named by the subject above: the name is the
          address it is saved under, and a new name is a new variable. */}
      <div className="min-w-0 space-y-4">
        <div className="grid min-w-0 gap-4 sm:grid-cols-[minmax(0,1fr)_auto]">
          {!editingName && (
            <Field
              label="Variable name"
              htmlFor="variable-name"
              error={
                nameProblem
                  ? "Use a letter or underscore first, then up to 127 letters, numbers or underscores."
                  : undefined
              }
              hint={
                exists
                  ? `${name.trim()} exists — saving replaces its value and scopes.`
                  : browserInlined(name.trim())
                    ? "Compiled into the browser bundle: public, and read while the build runs."
                    : undefined
              }
            >
              <Input
                id="variable-name"
                value={name}
                onChange={(event) => changeName(event.target.value.toUpperCase())}
                autoComplete="off"
                spellCheck={false}
                className="font-mono"
              />
            </Field>
          )}
          <Field label="Type">
            <ValueType value={sensitivity} onChange={setSensitivity} />
          </Field>
        </div>
        <Field
          label={reference ? "Typed reference" : "Value"}
          htmlFor="variable-value"
          hint={
            reference ? (
              "Resolved when the release starts."
            ) : pointsAtLocalhost(name.trim(), value) ? (
              <span className="text-warning">
                Points at localhost, which inside the container is the app itself. Link a database
                or use a host the container can reach.
              </span>
            ) : editingName ? (
              "Enter the value again — the dashboard does not read it back."
            ) : undefined
          }
          error={valueError || undefined}
        >
          <InputGroup>
            <InputGroupInput
              id="variable-value"
              type={reference || sensitivity === "plain" || shown ? "text" : "password"}
              value={value}
              onChange={(event) => {
                setValue(event.target.value)
                if (valueError) setValueError("")
              }}
              autoComplete="off"
              spellCheck={false}
              aria-invalid={Boolean(valueError)}
              placeholder={reference ? "${{database.11}}" : undefined}
              className="font-mono"
            />
            <InputGroupAddon align="inline-end" className="gap-0 p-0">
              {!reference && sensitivity === "secret" && value && (
                <InputGroupButton
                  aria-label={shown ? "Hide value" : "Show value"}
                  onClick={() => setShown(!shown)}
                >
                  {shown ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
                </InputGroupButton>
              )}
              {!reference && editingName && !value && (
                <InputGroupButton
                  aria-label="Reveal current value"
                  pending={revealingIntoForm}
                  onClick={() => void revealIntoForm()}
                >
                  <Eye className="size-3.5" />
                  <span className="max-sm:hidden">Reveal current</span>
                </InputGroupButton>
              )}
              {!reference && !editingName && sensitivity === "secret" && (
                <InputGroupButton
                  aria-label="Generate secret"
                  pending={busy === "generate"}
                  disabled={Boolean(busy)}
                  onClick={generate}
                >
                  <Sparkles className="size-3.5" />
                  <span className="max-sm:hidden">Generate</span>
                </InputGroupButton>
              )}
              <InputGroupToggle
                icon={Linked}
                label="Reference"
                aria-label="Reference a stored value"
                pressed={reference}
                onPressedChange={(next) => {
                  setReference(next)
                  setShown(false)
                }}
              />
            </InputGroupAddon>
          </InputGroup>
        </Field>
        {reference && (links.data?.length ?? 0) > 0 && (
          <ReferencePicker links={links.data ?? []} value={value} onChange={setValue} />
        )}
      </div>
      <FormSection title="Who can read it" hint={`${scopes.length} of ${SCOPES.length}`}>
        <ScopeOptions scopes={scopes} onToggle={toggleScope} />
      </FormSection>
      {error && <FormNote tone="danger">{error}</FormNote>}
    </div>
  )

  const submitLabel = editingName ? "Save variable" : "Add variable"

  // What the live release still carries but the desired plan no longer has —
  // under the empty state too, since removing the last variable is exactly
  // when the reader needs to see that the release has not caught up.
  const ghosts = removed.length > 0 && (
    <div className="space-y-2 pt-2">
      <GroupRule label="Removed · not live yet" count={removed.length} />
      <ul aria-label="Removed variables" className="divide-y divide-hairline">
        {removed.map((removedName) => (
          <li key={removedName} className="flex min-w-0 items-center gap-3 py-2.5">
            <ProductLogo size="sm" fallback={Trash} />
            <span className="min-w-0 flex-1 truncate font-mono text-body text-muted-foreground line-through">
              {removedName}
            </span>
            <Status tone="warning" label="Removed · applies on the next deployment" />
          </li>
        ))}
      </ul>
    </div>
  )

  const list =
    variables.length === 0 ? (
      <div className="min-w-0 space-y-4">
        <EmptyState
          icon={Key}
          title="No scoped variables"
          description="Add a value and choose whether your build, application, or release tasks can read it — or bring a whole .env at once."
          action={
            canEdit &&
            !compact && (
              <div className="flex flex-wrap justify-center gap-2">
                <Button size="sm" onClick={openEditor}>
                  <Plus className="size-3.5" /> Add the first variable
                </Button>
                <Button size="sm" variant="outline" onClick={() => setImportOpen(true)}>
                  <FileText className="size-3.5" /> Import a .env file
                </Button>
              </div>
            )
          }
        />
        {ghosts}
      </div>
    ) : (
      <div className="min-w-0 space-y-4">
        <div className="min-w-0 space-y-3">
          <SearchInput
            aria-label="Search variables"
            placeholder="Search variables…"
            containerClassName="sm:w-full"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
          <ChipStrip role="group" aria-label="Show">
            {FILTERS.map((item) => {
              const count = variables.filter(item.test).length
              // The chosen one stays even at zero: a filter kept for the tab
              // outlives what it matched, and the list saying "No variables
              // match" needs the chip that explains why.
              if (item.key !== "all" && count === 0 && filter !== item.key) return null
              return (
                <FilterChip
                  key={item.key}
                  selected={filter === item.key}
                  onClick={() => setFilter(item.key)}
                >
                  {item.label}
                  <ChipCount className={cn(item.key === "pending" && "text-warning opacity-100")}>
                    {count}
                  </ChipCount>
                </FilterChip>
              )
            })}
          </ChipStrip>
        </div>
        {filtered.length === 0 ? (
          <div className="flex flex-col items-center gap-2 py-6">
            <EmptyNote className="py-0">No variables match.</EmptyNote>
            <Button
              size="sm"
              variant="ghost"
              onClick={() => {
                setQuery("")
                setFilter("all")
              }}
            >
              Clear filters
            </Button>
          </div>
        ) : (
          <ChoiceList aria-label="Environment variables">
            {filtered.map((variable, index) => (
              <VariableRow
                key={variable.name}
                index={index}
                variable={variable}
                projectId={projectId}
                link={linkFor(variable)}
                product={productOf(variable)}
                hue={hueOf(variable.name)}
                change={pendingChange.get(variable.name)}
                revealed={revealed[variable.name]}
                rotating={rowBusy === `rotate-${variable.name}`}
                wide={wide}
                compact={compact}
                canEdit={canEdit}
                onEdit={() => loadIntoForm(variable)}
                verbs={verbsFor(variable)}
              />
            ))}
          </ChoiceList>
        )}
        {filter === "all" && ghosts}
      </div>
    )

  const notice = generated && (
    <Notice
      tone="warning"
      icon={Warning}
      title={`${generated.name} was generated`}
      className="animate-rise"
    >
      <p>This value is shown once. Store it now; the list keeps only a fixed mask.</p>
      <Well className="mt-3 font-mono break-all select-all">{generated.value}</Well>
      <div className="mt-3 flex flex-wrap gap-2">
        <Button
          size="sm"
          variant="outline"
          onClick={() => void copyText(generated.value, `${generated.name} copied`)}
        >
          <Copy className="size-3.5" /> Copy
        </Button>
        <Button size="sm" variant="outline" onClick={() => setGenerated(undefined)}>
          Dismiss
        </Button>
      </div>
    </Notice>
  )

  if (compact) {
    return (
      <div className="space-y-8">
        {notice}
        {canEdit && (
          <form onSubmit={save} className="space-y-4">
            {editorFields}
            <div className="flex flex-wrap justify-end gap-2">
              {editingName && (
                <Button type="button" variant="outline" onClick={resetForm}>
                  Cancel
                </Button>
              )}
              <Button type="submit" disabled={Boolean(busy)} pending={busy === "save"}>
                {submitLabel}
              </Button>
            </div>
          </form>
        )}
        <FormSection title="Environment variables">
          <div ref={column} className="min-w-0">
            {list}
          </div>
        </FormSection>
        {dialog}
      </div>
    )
  }

  return (
    <>
      <SettingSection
        title="Environment variables"
        state={
          variables.length > 0 && (
            <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
              <span className="numeric">
                {variables.length} variable{variables.length === 1 ? "" : "s"}
              </span>
              <ProductGlyphs ids={products} />
            </span>
          )
        }
        actions={
          canEdit && (
            <>
              <Button size="sm" variant="outline" onClick={openEditor}>
                <Plus className="size-3.5" /> Add variable
              </Button>
              <Button size="sm" variant="outline" onClick={() => setImportOpen(true)}>
                <FileText className="size-3.5" /> Import .env
              </Button>
            </>
          )
        }
      >
        <div ref={column} className="min-w-0 space-y-5">
          {notice}
          {list}
        </div>
      </SettingSection>

      <SidePanel
        open={editorOpen && canEdit}
        onOpenChange={(open) => (open ? setEditorOpen(true) : closeEditor())}
        title={editingName ? "Edit variable" : "Add variable"}
        description={
          editingName
            ? "Change this variable's value, its type or who can read it."
            : "Name a value and choose who can read it."
        }
        width="md"
        footer={
          <>
            <FormNote className="mr-auto max-sm:hidden">Applies on your next deployment</FormNote>
            <Button variant="outline" onClick={closeEditor} disabled={busy === "save"}>
              Cancel
            </Button>
            <Button
              type="submit"
              form={EDITOR_FORM}
              disabled={Boolean(busy)}
              pending={busy === "save"}
            >
              {submitLabel}
            </Button>
          </>
        }
      >
        <form id={EDITOR_FORM} onSubmit={save}>
          {editorFields}
        </form>
      </SidePanel>

      <ImportSheet
        open={importOpen && canEdit}
        onOpenChange={setImportOpen}
        draft={draft}
        base={base}
        configuration={configuration}
        onImported={onChanged}
      />
      {dialog}
    </>
  )
}

/**
 * A variable in the list. Its name in the family's hue, what it holds on the
 * line under it — a mask, the database it points at, or the value while it is
 * revealed — and who set it, as their face. Where the list's column is wide
 * the row's edge carries its pending state and the three places it can reach;
 * narrower, they go under the name at the card's full width. The state of the
 * database a reference reads is always under the name it describes.
 */
function VariableRow({
  variable,
  projectId,
  link,
  product,
  hue,
  change,
  revealed,
  rotating,
  wide,
  compact,
  canEdit,
  index,
  onEdit,
  verbs,
}: {
  variable: DeploymentVariable
  projectId: number
  link?: DeploymentDatabaseLink
  product?: string
  hue?: string
  change?: "added" | "changed" | "removed"
  revealed?: string
  rotating: boolean
  wide: boolean
  compact: boolean
  canEdit: boolean
  index: number
  onEdit: () => void
  verbs: Verb[]
}) {
  const state = rotating ? (
    <Status tone="running" label="Rotating…" />
  ) : variable.pending ? (
    <Status tone="warning" label={change ? `Pending · ${change}` : "Pending"} />
  ) : browserInlined(variable.name) && !variable.scopes.includes("build") ? (
    // The bundle is compiled while the build runs; a runtime-only value is
    // undefined in every visitor's browser however it is set here.
    <Status tone="warning" label="Not in the build" />
  ) : undefined
  // The state of the database a reference reads, where it is anything but
  // connected — a dot and its word (§4) under the name it describes, not a
  // bare dot in the value's line where it read as a separator. Connected is
  // the quiet case, and Databases already draws it.
  const linkState = link && link.status !== "connected" && (
    <Status tone={LINK_STATUS[link.status].tone} label={LINK_STATUS[link.status].label} />
  )
  // Wide, the edge holds the variable's own state and reach; narrow, all of
  // it goes under the name, the three slots first so they line up down the list.
  const under = wide ? (
    linkState
  ) : (
    <>
      <Reach scopes={variable.scopes} />
      {state}
      {linkState}
    </>
  )
  return (
    <ChoiceRow
      index={index}
      verb={`Edit ${variable.name}`}
      disabled={!canEdit}
      onSelect={onEdit}
      busy={rotating}
      leading={<VariableMark variable={variable} product={product} />}
      title={<VariableName name={variable.name} hue={hue} />}
      description={
        <ValueLine
          variable={variable}
          link={link}
          projectId={projectId}
          compact={compact}
          wide={wide}
        />
      }
      trailing={
        wide ? (
          <>
            {state}
            <Reach scopes={variable.scopes} />
          </>
        ) : undefined
      }
      actions={<VerbActions dim verbs={verbs} menuLabel={`Actions for ${variable.name}`} />}
    >
      {(under || revealed !== undefined) && (
        <div className="min-w-0 space-y-2 sm:pl-11">
          {under && <div className="flex flex-wrap items-center gap-x-4 gap-y-1">{under}</div>}
          {revealed !== undefined && (
            // The value invites a click to select it, and the row around it
            // opens the editor on a press — so a press here stays here.
            <div
              onClick={(event) => event.stopPropagation()}
              className="flex min-w-0 items-start gap-1"
            >
              <Well className="min-w-0 flex-1 py-1.5 font-mono text-hint break-all select-all">
                {revealed}
              </Well>
              <IconAction
                label={`Copy ${variable.name}`}
                onClick={() => void copyText(revealed, `${variable.name} copied`)}
              >
                <Copy />
              </IconAction>
            </div>
          )}
        </div>
      )}
    </ChoiceRow>
  )
}

/** A name in monospace, its shared prefix in the family's hue. */
function VariableName({ name, hue }: { name: string; hue?: string }) {
  if (!hue) return <span className="font-mono">{name}</span>
  const cut = name.indexOf("_")
  return (
    <span className="font-mono">
      <span style={{ color: hue }}>{name.slice(0, cut)}</span>
      {name.slice(cut)}
    </span>
  )
}

/**
 * The tile a variable is drawn on: the service its name says holds it, the
 * engine a reference points at, or — for a name that says nothing — a lock
 * for a secret and brackets for a setting. Never a guess: `DATABASE_URL`
 * without a reference is no product.
 */
function VariableMark({
  variable,
  product,
}: {
  variable: Pick<DeploymentVariable, "sensitivity">
  product?: string
}) {
  return (
    <ProductLogo
      size="sm"
      id={product}
      fallback={variable.sensitivity === "secret" ? LockClosed : Code}
    />
  )
}

/**
 * What the variable holds, and who set it when. Narrow, the person is their
 * face alone — the name was what the line lost to its ellipsis on most rows.
 */
function ValueLine({
  variable,
  link,
  projectId,
  compact,
  wide,
}: {
  variable: DeploymentVariable
  link?: DeploymentDatabaseLink
  projectId: number
  compact: boolean
  wide: boolean
}) {
  const reference = variable.reference
  return (
    <>
      {reference?.kind === "database" ? (
        link ? (
          <>
            <ProductGlyph id={link.driver} className="inline-block align-[-2px]" />{" "}
            {compact ? (
              // A preview environment's links are not the project's
              // Databases page, and leaving the sheet mid-review loses it.
              <span className="text-foreground/90">{link.name}</span>
            ) : (
              <Link
                href={`/deploy/${projectId}/settings/databases`}
                className="rounded-sm text-foreground/90 focus-ring hover:underline"
              >
                {link.name}
              </Link>
            )}
            {/* Narrow, the engine's glyph and the database's name already say
                what this is, and the line's room goes to who set it. */}
            <span className={cn(!wide && "sr-only")}> · reference</span>
          </>
        ) : (
          <>database #{reference.target} · reference</>
        )
      ) : reference ? (
        <>
          <Key className="inline-block size-3 align-[-1px]" />{" "}
          <span className="font-mono">
            {reference.kind}.{reference.target}
          </span>
        </>
      ) : (
        <span className="font-mono">{variable.masked}</span>
      )}
      <span> · set {relativeTime(variable.createdAt)} </span>
      {wide ? (
        <>
          <span>by </span>
          <InitialsMark
            name={variable.createdBy}
            size="xs"
            className="inline-flex size-3.5 align-[-3px]"
          />{" "}
          {variable.createdBy}
        </>
      ) : (
        <span title={variable.createdBy}>
          <span className="sr-only">by {variable.createdBy}</span>
          <InitialsMark
            name={variable.createdBy}
            size="xs"
            className="inline-flex size-3.5 align-[-3px]"
          />
        </span>
      )}
    </>
  )
}

/**
 * The three places a variable can reach, always in the same three slots: the
 * ones it reaches in the foreground, the ones it does not faint, so a column
 * of rows reads down as a matrix. A reader hears the reached ones by name.
 */
function Reach({ scopes }: { scopes: Scope[] }) {
  return (
    <span className="flex shrink-0 items-center gap-2.5">
      <span className="sr-only">Reaches {scopeWords(scopes)}</span>
      {SCOPES.map((item) => (
        <Tag
          key={item.scope}
          aria-hidden
          className={cn(scopes.includes(item.scope) ? "text-foreground/85" : "opacity-25")}
        >
          {item.word}
        </Tag>
      ))}
    </span>
  )
}

function scopeWords(scopes: Scope[]) {
  return SCOPES.filter((item) => scopes.includes(item.scope))
    .map((item) => item.word)
    .join(", ")
}

/**
 * The editor opens on the thing it writes (the sheet anatomy's first rule):
 * the mark the row will carry, which changes as the name is typed —
 * `STRIPE_` is Stripe before the key is finished — the name, and for an
 * existing variable when and by whom it was last set. A new one has no facts
 * yet: its type is the toggle under it and when it applies is the footer's.
 */
function VariableSubject({
  name,
  sensitivity,
  product,
  editing,
}: {
  name: string
  sensitivity: Sensitivity
  product?: string
  editing?: DeploymentVariable
}) {
  return (
    <div className="flex min-w-0 items-center gap-3">
      <VariableMark variable={{ sensitivity }} product={product} />
      <div className="min-w-0 space-y-0.5">
        <p
          className={cn(
            "truncate font-mono text-body font-medium",
            !name && "font-sans text-muted-foreground",
          )}
        >
          {name || "New variable"}
        </p>
        {editing && (
          <FormFacts>
            <FormFact label="Revision">{editing.revision}</FormFact>
            <FormFact label="Set">
              {relativeTime(editing.createdAt)} by {editing.createdBy}
            </FormFact>
          </FormFacts>
        )}
      </div>
    </div>
  )
}

/**
 * Secret or setting, as the two faces of one control. It was two filter chips
 * — a control for narrowing a list, pressed into service as a choice — beside
 * a field three times their height.
 */
function ValueType({
  value,
  onChange,
}: {
  value: Sensitivity
  onChange: (value: Sensitivity) => void
}) {
  return (
    <ToggleGroup
      type="single"
      variant="outline"
      aria-label="Value type"
      value={value}
      onValueChange={(next) => next && onChange(next as Sensitivity)}
      className="w-full sm:w-auto"
    >
      <ToggleGroupItem value="secret" className="h-11 flex-1 gap-1.5 text-body sm:h-9 sm:flex-none">
        <LockClosed className={cn("size-3.5", value === "secret" && "text-brand")} /> Secret
      </ToggleGroupItem>
      <ToggleGroupItem value="plain" className="h-11 flex-1 gap-1.5 text-body sm:h-9 sm:flex-none">
        <Code className={cn("size-3.5", value === "plain" && "text-brand")} /> Config
      </ToggleGroupItem>
    </ToggleGroup>
  )
}

function ScopeOptions({
  scopes,
  onToggle,
}: {
  scopes: Scope[]
  onToggle: (scope: Scope, checked: boolean) => void
}) {
  return (
    <OptionList>
      {SCOPES.map((item) => (
        <OptionRow
          key={item.scope}
          title={item.title}
          checked={scopes.includes(item.scope)}
          onCheckedChange={(checked) => onToggle(item.scope, checked)}
        />
      ))}
    </OptionList>
  )
}

/**
 * A database this environment links, chosen by name rather than typed as
 * `${{database.11}}` — the literal the field still takes for every other kind
 * of stored value.
 */
function ReferencePicker({
  links,
  value,
  onChange,
}: {
  links: DeploymentDatabaseLink[]
  value: string
  onChange: (value: string) => void
}) {
  const chosen = /^\$\{\{database\.(\d+)\}\}$/.exec(value.trim())?.[1] ?? ""
  return (
    <Field label="Linked database" htmlFor="variable-reference">
      <Select value={chosen} onValueChange={(id) => onChange(`\${{database.${id}}}`)}>
        <SelectTrigger id="variable-reference" className="w-full">
          <SelectValue placeholder="Choose a linked database" />
        </SelectTrigger>
        <SelectContent>
          {links.map((link) => (
            <SelectItem
              key={link.connectionId}
              value={String(link.connectionId)}
              hint={
                <span aria-hidden className="font-mono">
                  {link.hostname}
                </span>
              }
            >
              <ProductGlyph id={link.driver} />
              {link.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </Field>
  )
}

/** The verdict on a name the import leaves out because the deployment sets it. */
const LEFT_OUT = "left out · set by the deployment"

type ImportVerdict = {
  name: string
  line: number
  value: string
  tone: DotTone
  label: string
  refused: boolean
}

/**
 * A pasted `.env`, read before it is imported.
 *
 * It was a textarea folded into the add-variable form, sharing that form's
 * type and scopes — so a single variable's half-typed choices were silently
 * applied to forty names — with nothing saying what the import would add,
 * replace or refuse until it had. It has its own sheet, its own type and
 * scopes (secret and runtime until changed, the import's own defaults), and
 * a list of every name in the paste with what importing it does: read here
 * as it is typed, then from the server's own dry run, which also knows which
 * values are unchanged. One refused name refuses the whole import, so the
 * command waits until there are none.
 */
function ImportSheet({
  open,
  onOpenChange,
  draft,
  base,
  configuration,
  onImported,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  draft: string
  base: string
  configuration: DeploymentEnvironmentConfiguration
  onImported: () => void
}) {
  const [dotenv, setDotenv] = useMemoryState(`${draft}.dotenv`, "")
  const [sensitivity, setSensitivity] = useSessionState<Sensitivity>(
    `${draft}.import.sensitivity`,
    "secret",
  )
  const [scopes, setScopes] = useSessionState<Scope[]>(`${draft}.import.scopes`, ["runtime"])
  const [importing, setImporting] = useState(false)
  const [error, setError] = useState("")
  const [preview, setPreview] = useState<{
    key: string
    answer?: DeploymentDotenvImportPreview
    refusal?: string
  }>()
  const file = useRef<HTMLInputElement>(null)

  const reading = useMemo(() => readDotenv(dotenv), [dotenv])
  const browserNames = Array.from(
    dotenv.matchAll(/^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=/gm),
    (match) => match[1],
  ).filter((entry) => browserInlined(entry))
  // A Compose file may interpolate ${PORT} itself, so its stack keeps what
  // the paste says unless the reader leaves it out.
  const [keepPlatform, setKeepPlatform] = useState(configuration.build.method === "compose")
  const platform = PLATFORM_NAMES.filter((name) =>
    reading.entries.some((entry) => entry.name === name && !entry.refused),
  )
  const skip = keepPlatform ? [] : platform
  const body = {
    revision: configuration.revision,
    dotenv,
    sensitivity,
    scopes,
    ...(skip.length ? { skip } : {}),
  }
  const key = JSON.stringify(body)
  const ready = open && dotenv.trim() !== "" && scopes.length > 0 && !reading.error

  // The server's answer, asked once the paste has settled — each dry run is
  // an audited read of the stored values, so it is not asked on every pause
  // in typing. Keyed on the exact body it answered, so a verdict on a paste
  // that has since changed is never drawn beside the new one.
  useEffect(() => {
    if (!ready) return
    const controller = new AbortController()
    const timer = setTimeout(() => {
      post<DeploymentDotenvImportPreview>(`${base}/import`, JSON.parse(key), {
        query: { dryRun: 1 },
        signal: controller.signal,
      })
        .then((answer) => setPreview({ key, answer }))
        .catch((caught) => {
          if (controller.signal.aborted) return
          // Input the import would refuse as a whole — a reference cycle, a
          // type or scope it does not take — comes back as the import's own
          // error, and is the reason Import cannot be pressed. Anything else
          // (the server unreachable, a stale revision) says nothing about the
          // paste: the client's reading stands in, and the import itself is
          // still the authority.
          if (caught instanceof ApiError && (caught.status === 400 || caught.status === 422))
            setPreview({ key, refusal: caught.message })
        })
    }, 800)
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
  }, [ready, key, base])

  const existing = new Set(configuration.variables.map((variable) => variable.name))
  const hueOf = prefixHues([...existing, ...reading.entries.map((entry) => entry.name)])
  const server = preview?.key === key ? preview.answer : undefined
  const refusedWhole = ready && preview?.key === key ? preview.refusal : undefined
  const verdicts: ImportVerdict[] = reading.entries.map((entry) => {
    const answer = server?.variables.find(
      (item) => item.name === entry.name && item.line === entry.line,
    )
    const refusal = answer?.reason ?? entry.refused
    if (answer?.change === "refused" || refusal)
      return {
        ...entry,
        tone: "danger",
        label: `refused · ${REFUSAL_WORD[refusal ?? "invalid_name"]}`,
        refused: true,
      }
    if (answer?.change === "skipped" || (!answer && skip.includes(entry.name)))
      return { ...entry, tone: "stopped", label: LEFT_OUT, refused: false }
    if (answer?.change === "unchanged")
      return { ...entry, tone: "stopped", label: "unchanged", refused: false }
    if (answer?.change === "changed" || (!answer && existing.has(entry.name)))
      return { ...entry, tone: "warning", label: "replaces the current value", refused: false }
    return { ...entry, tone: "running", label: "new", refused: false }
  })
  const count = (label: string) => verdicts.filter((verdict) => verdict.label === label).length
  const refused = verdicts.filter((verdict) => verdict.refused).length
  // Each count in the colour of the verdict it counts, so the refusal that
  // holds Import back is found in the footer beside it.
  const counts: { key: string; count: number; className?: string }[] = [
    { key: "new", count: count("new") },
    { key: "replaced", count: count("replaces the current value"), className: "text-warning" },
    { key: "unchanged", count: count("unchanged") },
    { key: "left out", count: count(LEFT_OUT) },
    { key: "refused", count: refused, className: "text-destructive" },
  ]

  const close = () => {
    setError("")
    onOpenChange(false)
  }

  const importDotenv = async () => {
    if (!dotenv.trim() || scopes.length === 0) {
      setError("Paste dotenv values and choose at least one scope.")
      return
    }
    setImporting(true)
    setError("")
    try {
      await post(`${base}/import`, body)
      notify.success("Variables imported", {
        description: "Values remain masked in the list.",
      })
      setDotenv("")
      setPreview(undefined)
      onImported()
      onOpenChange(false)
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : String(caught))
    } finally {
      setImporting(false)
    }
  }

  const choose = (picked: File | undefined) => {
    if (!picked) return
    void picked.text().then(setDotenv)
  }

  return (
    <SidePanel
      open={open}
      onOpenChange={(next) => (next ? onOpenChange(true) : close())}
      title="Import a .env"
      description="Paste or choose a dotenv file; every name in it is listed with what importing it does."
      width="lg"
      footer={
        <>
          {verdicts.length > 0 && (
            <FormNote className="numeric mr-auto max-sm:basis-full">
              {plural(verdicts.length, "variable")}
              {counts
                .filter((part) => part.count > 0)
                .map((part) => (
                  <span key={part.key}>
                    {" · "}
                    <span className={part.className}>
                      {part.count} {part.key}
                    </span>
                  </span>
                ))}
            </FormNote>
          )}
          <Button variant="outline" onClick={close} disabled={importing}>
            Cancel
          </Button>
          <Button
            onClick={() => void importDotenv()}
            pending={importing}
            disabled={
              verdicts.length === 0 ||
              verdicts.every((verdict) => verdict.label === LEFT_OUT) ||
              refused > 0 ||
              Boolean(reading.error || refusedWhole)
            }
          >
            Import variables
          </Button>
        </>
      }
    >
      <div className="space-y-6">
        <div className="flex min-w-0 items-center gap-3">
          <FileIcon entry={{ name: ".env", isDir: false, isSymlink: false }} className="size-8" />
          <div className="min-w-0 space-y-0.5">
            <p className="font-mono text-body font-medium">.env</p>
            <FormFacts>
              <FormFact label="Into">{configuration.variables.length} existing variables</FormFact>
              <FormFact label="Applies">on your next deployment</FormFact>
            </FormFacts>
          </div>
        </div>

        <FormSection title="Paste">
          <Field
            label="Dotenv values"
            htmlFor="dotenv-values"
            hint="Comments, empty values and quoted multi-line values are read. A name given twice is refused."
            trailing={
              <Button
                type="button"
                size="xs"
                variant="outline"
                onClick={() => file.current?.click()}
              >
                <FileText className="size-3" /> Choose a file
              </Button>
            }
          >
            <Textarea
              id="dotenv-values"
              value={dotenv}
              onChange={(event) => setDotenv(event.target.value)}
              spellCheck={false}
              className="min-h-36 font-mono sm:text-xs"
              placeholder={'API_URL=https://api.example.test\nTOKEN="multiline\\nvalue"'}
            />
          </Field>
          <input
            ref={file}
            type="file"
            accept=".env,text/plain"
            className="hidden"
            onChange={(event) => {
              choose(event.target.files?.[0])
              event.target.value = ""
            }}
          />
        </FormSection>

        <FormSection title="What this imports">
          {reading.error && <FormNote tone="danger">{reading.error}</FormNote>}
          {refusedWhole && <FormNote tone="danger">{refusedWhole}</FormNote>}
          {verdicts.length === 0 ? (
            !reading.error && (
              <EmptyNote>
                Each name in the paste appears here with what importing it does.
              </EmptyNote>
            )
          ) : (
            <ul aria-label="What this imports" className="divide-y divide-hairline">
              {verdicts.map((verdict) => (
                <li
                  key={`${verdict.line}-${verdict.name}`}
                  className="flex min-w-0 animate-rise items-center gap-3 py-2"
                >
                  <VariableMark
                    variable={{ sensitivity }}
                    product={variableProduct(verdict.name)}
                  />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-body font-medium">
                      <VariableName name={verdict.name} hue={hueOf(verdict.name)} />
                    </span>
                    <span className="block truncate font-mono text-hint text-muted-foreground">
                      <span className="numeric">line {verdict.line}</span> ·{" "}
                      {sensitivity === "secret"
                        ? "••••••••"
                        : verdict.value.length > 40
                          ? `${verdict.value.slice(0, 40)}…`
                          : verdict.value || "empty"}
                    </span>
                  </span>
                  <Status tone={verdict.tone} label={verdict.label} />
                </li>
              ))}
            </ul>
          )}
          {platform.length > 0 && (
            <OptionList>
              <OptionRow
                title={`Leave out ${platform.join(" and ")}`}
                hint="The deployment sets them: PORT is the internal port the proxy and the readiness check connect to, and the recipe builds and runs in production."
                checked={!keepPlatform}
                onCheckedChange={(checked) => setKeepPlatform(!checked)}
              />
            </OptionList>
          )}
        </FormSection>
        <FormSection title="Import as">
          <ValueType value={sensitivity} onChange={setSensitivity} />
          {!scopes.includes("build") && browserNames.length > 0 && (
            <FormNote tone="warning">
              {browserNames.join(", ")} {browserNames.length === 1 ? "is" : "are"} compiled into the
              browser bundle while the build runs; without Build{" "}
              {browserNames.length === 1 ? "it builds" : "they build"} as undefined.
            </FormNote>
          )}
          <ScopeOptions
            scopes={scopes}
            onToggle={(scope, checked) =>
              setScopes((current) =>
                checked
                  ? [...new Set([...current, scope])]
                  : current.filter((item) => item !== scope),
              )
            }
          />
        </FormSection>

        {error && <FormNote tone="danger">{error}</FormNote>}
      </div>
    </SidePanel>
  )
}
