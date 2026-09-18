"use client"

import { useState } from "react"
import type { FormEvent } from "react"
import { ApiError, del, get, post, put } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import type { DeploymentEnvironmentConfiguration, DeploymentVariable } from "@/lib/types"
import { Field, FormNote, FormSection, OptionList, OptionRow } from "@/components/form"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { SearchInput } from "@/components/page"
import { ROW_BLEED } from "@/components/row-list"
import { EmptyState, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { FilterChip } from "@/components/tabs"
import { VerbActions, type Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { useConfirm } from "@/components/confirm-dialog"
import { Eye, EyeOff, Key, Pencil, RefreshClockwise, Sparkles, Trash } from "@/components/icons"
import { humanize } from "@/components/deploy/vocabulary"
import {
  ConfigurationState,
  useConfiguration,
} from "@/components/deploy/settings/use-configuration"
import { PendingChanges } from "@/components/deploy/settings/pending-changes"
import { SettingCard } from "@/components/deploy/settings/setting-card"

/**
 * The variables a build, a running release, or a release task can read — the
 * Vercel model: one form to add or update a value, one list of what exists.
 *
 * `compact` is for the automation agent's preview-environment sheet: a
 * `SidePanel` is already a frame, so a second one around the add-variable
 * form would be the nested-frame the design system spends itself avoiding,
 * and a preview environment is never the target of the project's own
 * "Deploy changes" button that `PendingChanges` draws.
 */

type Scope = "build" | "runtime" | "release_task"

const SCOPES: [Scope, string, string][] = [
  ["build", "Build", "Reaches build-time commands through temporary secret mounts."],
  ["runtime", "Runtime", "Reaches the running container as an environment variable."],
  [
    "release_task",
    "Release task",
    "Reaches only the release tasks that explicitly list it in their environment.",
  ],
]

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
  return (
    <ConfigurationState state={state}>
      {(configuration) => (
        // No `key={configuration.revision}` here: unlike Build/Runtime/General,
        // this form keeps no editable copy of server state that a save could
        // leave stale — the add-variable fields already reset themselves, and
        // the list reads `configuration.variables` fresh on every render. The
        // one local state a remount would destroy is the one-time generated
        // value and which rows are revealed, and both must survive the
        // revision bump that saving *any* variable causes.
        <VariablesForm
          projectId={projectId}
          environmentId={environmentId}
          configuration={configuration}
          canEdit={can("system.admin")}
          compact={compact}
          onChanged={state.refresh}
        />
      )}
    </ConfigurationState>
  )
}

function VariablesForm({
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
  const { confirm, dialog } = useConfirm()
  const [name, setName] = useState("")
  const [value, setValue] = useState("")
  const [valueError, setValueError] = useState("")
  const [reference, setReference] = useState(false)
  const [sensitivity, setSensitivity] = useState<"plain" | "secret">("secret")
  const [scopes, setScopes] = useState<Scope[]>(["runtime"])
  const [showImport, setShowImport] = useState(false)
  const [dotenv, setDotenv] = useState("")
  const [query, setQuery] = useState("")
  const [busy, setBusy] = useState("")
  const [error, setError] = useState("")
  const [revealed, setRevealed] = useState<Record<string, string>>({})
  const [generated, setGenerated] = useState<{ name: string; value: string }>()
  // The row loaded into the form above, so the submit label, the "value was
  // never read back" hint and the client-side required check all agree on
  // whether this is an edit or a brand new variable.
  const [editingName, setEditingName] = useState<string>()
  const [revealingIntoForm, setRevealingIntoForm] = useState(false)
  // Reveal/rotate/remove all write with `configuration.revision`, which only
  // updates once the poll it came from refreshes; a row action's own error
  // goes to a toast (§22), never into this add-variable card.
  const [rowBusy, setRowBusy] = useState("")

  const base = `/deploy/${projectId}/environments/${environmentId}/variables`

  const toggleScope = (scope: Scope, checked: boolean) =>
    setScopes((current) =>
      checked ? [...new Set([...current, scope])] : current.filter((item) => item !== scope),
    )

  const resetForm = () => {
    setName("")
    setValue("")
    setValueError("")
    setReference(false)
    setSensitivity("secret")
    setScopes(["runtime"])
    setError("")
    setEditingName(undefined)
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

  // Row actions (reveal/rotate/remove) share `configuration.revision` with
  // this form, which only catches up once the poll behind it refreshes — two
  // quick actions can each read the same now-stale revision. Retrying once
  // against a freshly-read revision is cheap and turns a spurious
  // `revision_conflict` into a press that just worked.
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
    // Edit never reads the stored value back (variable.tsx has no reveal
    // by default), so `value` is empty unless the operator retyped it or
    // used "Reveal current value" — saving that as the new value would
    // silently wipe whatever was there.
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
    })
  }

  const importDotenv = () => {
    if (!dotenv.trim() || scopes.length === 0) {
      setError("Paste dotenv values and choose at least one scope.")
      return
    }
    void mutate("import", async () => {
      await post(`${base}/import`, {
        revision: configuration.revision,
        dotenv,
        sensitivity,
        scopes,
      })
      setDotenv("")
      setShowImport(false)
      notify.success("Variables imported", {
        description: "Values remain masked in the list below.",
      })
    })
  }

  const reveal = async (variable: DeploymentVariable) => {
    setRowBusy(`reveal-${variable.name}`)
    try {
      const result = await get<{ value: string }>(
        `${base}/${encodeURIComponent(variable.name)}/reveal`,
      )
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
    setSensitivity(variable.sensitivity)
    setScopes(variable.scopes)
    setShowImport(false)
    setError("")
    setValueError("")
    setEditingName(variable.name)
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
    // otherwise race the async `configuration.revision` refresh into a
    // conflict (§22's `withCurrentRevision` retries it once, but the row
    // that lost the race still shouldn't be double-pressed meanwhile).
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
    if (canEdit) {
      verbs.push({
        key: "edit",
        label: "Edit",
        detail: "Load this variable into the form above.",
        icon: Pencil,
        run: () => loadIntoForm(variable),
      })
      if (variable.sensitivity === "secret") {
        verbs.push({
          key: "rotate",
          label: "Rotate",
          detail: "Replace the stored value with a freshly generated secret.",
          icon: RefreshClockwise,
          disabled: Boolean(rowBusy),
          run: () => void rotate(variable),
        })
      }
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

  const filtered = configuration.variables.filter((variable) =>
    variable.name.toLowerCase().includes(query.trim().toLowerCase()),
  )

  const formFields = (
    <div className="space-y-4">
      <div className="grid min-w-0 gap-4 sm:grid-cols-2">
        <Field label="Variable name" htmlFor="variable-name">
          <Input
            id="variable-name"
            value={name}
            onChange={(event) => setName(event.target.value.toUpperCase())}
            autoComplete="off"
            spellCheck={false}
            readOnly={!canEdit}
            className="font-mono"
          />
        </Field>
        <Field
          label={reference ? "Typed reference" : "Value"}
          htmlFor="variable-value"
          hint={
            !reference && editingName
              ? "Enter the value again — the dashboard does not read it back."
              : undefined
          }
          error={valueError || undefined}
          trailing={
            <div className="flex items-center gap-3">
              {editingName && !reference && canEdit && (
                <Button
                  type="button"
                  size="xs"
                  variant="ghost"
                  onClick={() => void revealIntoForm()}
                  disabled={revealingIntoForm}
                  pending={revealingIntoForm}
                >
                  Reveal current value
                </Button>
              )}
              <label className="flex items-center gap-1.5 text-hint text-muted-foreground">
                <Switch
                  size="sm"
                  checked={reference}
                  onCheckedChange={setReference}
                  disabled={!canEdit}
                />
                Reference a stored value
              </label>
            </div>
          }
        >
          <Input
            id="variable-value"
            type={reference ? "text" : sensitivity === "secret" ? "password" : "text"}
            value={value}
            onChange={(event) => {
              setValue(event.target.value)
              if (valueError) setValueError("")
            }}
            autoComplete="off"
            spellCheck={false}
            readOnly={!canEdit}
            aria-invalid={Boolean(valueError)}
            placeholder={reference ? "${{credential.name}}" : undefined}
            className="font-mono"
          />
        </Field>
      </div>

      <div role="group" aria-label="Value type" className="flex items-center gap-1.5">
        <FilterChip
          type="button"
          selected={sensitivity === "secret"}
          onClick={() => setSensitivity("secret")}
          disabled={!canEdit}
        >
          Secret
        </FilterChip>
        <FilterChip
          type="button"
          selected={sensitivity === "plain"}
          onClick={() => setSensitivity("plain")}
          disabled={!canEdit}
        >
          Config
        </FilterChip>
      </div>

      <OptionList>
        {SCOPES.map(([scope, label, hint]) => (
          <OptionRow
            key={scope}
            title={label}
            hint={hint}
            checked={scopes.includes(scope)}
            onCheckedChange={(checked) => toggleScope(scope, checked)}
            disabled={!canEdit}
          />
        ))}
      </OptionList>

      {showImport && (
        <Field
          label="Dotenv values"
          htmlFor="dotenv-values"
          hint="Comments, empty values, and quoted multiline values are accepted. Duplicate names are rejected."
        >
          <Textarea
            id="dotenv-values"
            value={dotenv}
            onChange={(event) => setDotenv(event.target.value)}
            className="min-h-36 font-mono text-xs"
            placeholder={'API_URL=https://api.example.test\nTOKEN="multiline\\nvalue"'}
          />
          <Button
            type="button"
            size="sm"
            className="mt-1"
            onClick={importDotenv}
            disabled={Boolean(busy)}
            pending={busy === "import"}
          >
            Import variables
          </Button>
        </Field>
      )}

      {error && <FormNote tone="danger">{error}</FormNote>}
    </div>
  )

  const pasteToggle = canEdit && (
    <button
      type="button"
      onClick={() => setShowImport((open) => !open)}
      className="rounded-sm underline underline-offset-2 focus-ring hover:text-foreground"
    >
      {showImport ? "Hide the .env field" : "Paste a .env instead"}
    </button>
  )

  const formActions = canEdit && (
    <>
      <Button type="submit" disabled={Boolean(busy)} pending={busy === "save"}>
        {editingName ? "Save variable" : "Add variable"}
      </Button>
      <Button
        type="button"
        variant="outline"
        onClick={generate}
        disabled={Boolean(busy)}
        pending={busy === "generate"}
      >
        Generate secret
      </Button>
    </>
  )

  return (
    <div className="space-y-6">
      {!compact && <PendingChanges pending={configuration.pending} />}

      {generated && (
        <Notice icon={Sparkles} tone="warning" title={`${generated.name} was generated`}>
          <p>This value is shown once. Store it now; the list will keep only a fixed mask.</p>
          <Well className="mt-3 font-mono break-all select-all">{generated.value}</Well>
          <Button
            size="sm"
            variant="outline"
            className="mt-3"
            onClick={() => setGenerated(undefined)}
          >
            Dismiss
          </Button>
        </Notice>
      )}

      <form onSubmit={save}>
        {compact ? (
          <FormSection title="Add a variable">
            {formFields}
            <div className="flex flex-wrap items-center justify-between gap-3 pt-1">
              <div className="text-hint">{pasteToggle}</div>
              {formActions && <div className="flex flex-wrap gap-2">{formActions}</div>}
            </div>
          </FormSection>
        ) : (
          <SettingCard title="Add a variable" note={pasteToggle} action={formActions}>
            {formFields}
          </SettingCard>
        )}
      </form>

      <Panel plain>
        <PanelHeader
          title="Environment variables"
          actions={
            <SearchInput
              aria-label="Search variables"
              placeholder="Search variables…"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
          }
        />
        <PanelBody flush>
          {configuration.variables.length === 0 ? (
            <EmptyState
              icon={Key}
              title="No scoped variables"
              description="Add a value and choose whether your build, application, or release tasks can use it."
            />
          ) : filtered.length === 0 ? (
            <p className="py-6 text-center text-body text-muted-foreground">No variables match.</p>
          ) : (
            <ul aria-label="Environment variables" className="-mx-3 divide-y divide-hairline px-3">
              {filtered.map((variable) => (
                <li key={variable.name} className="min-w-0">
                  <div className={cn("group flex min-w-0 items-start gap-3 py-3", ROW_BLEED)}>
                    <div className="min-w-0 flex-1 space-y-1">
                      <div className="flex min-w-0 flex-wrap items-center gap-2">
                        <span className="truncate font-mono text-body font-medium">
                          {variable.name}
                        </span>
                        {variable.scopes.map((scope) => (
                          <Tag key={scope}>{humanize(scope)}</Tag>
                        ))}
                        {variable.pending && <Tag tone="warning">Pending</Tag>}
                      </div>
                      <p className="truncate font-mono text-hint text-muted-foreground">
                        {revealed[variable.name] ??
                          (variable.reference
                            ? `${variable.reference.kind}.${variable.reference.target}`
                            : variable.masked)}
                      </p>
                      <p className="text-hint text-muted-foreground">
                        updated {relativeTime(variable.createdAt)}
                      </p>
                    </div>
                    <VerbActions
                      verbs={verbsFor(variable)}
                      reveal
                      menuLabel={`Actions for ${variable.name}`}
                    />
                  </div>
                </li>
              ))}
            </ul>
          )}
        </PanelBody>
      </Panel>

      <FormNote>Changes apply on the next deployment.</FormNote>
      {dialog}
    </div>
  )
}
