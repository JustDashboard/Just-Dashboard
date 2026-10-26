"use client"

import { useState } from "react"
import type { FormEvent } from "react"
import Link from "next/link"
import { ApiError, del, get, put } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentSharedVariable, DeploymentVariable } from "@/lib/types"
import { InitialsMark } from "@/components/account/user-avatar"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Field, FormFact, FormFacts, FormNote } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ProductGlyph, variableProduct } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { VerbActions, type Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Copy, Eye, EyeOff, Linked, Pencil, Plus, Trash } from "@/components/icons"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { ValueType, VariableMark } from "@/components/deploy/settings/variable-type"
import {
  parseSharedReference,
  readersByProject,
  removalNote,
  SHARED_NAME,
  sharedBase,
  sharedReference,
  waitingReaders,
  type SharedScope,
} from "@/components/deploy/shared-variables-model"

/**
 * Shared variables: a value defined once and read by any environment whose
 * own variable references it — the server's as `${{server.NAME}}` from every
 * project, a project's as `${{project.NAME}}` from that project's
 * environments. Nothing arrives by name: an environment reads a shared value
 * only through a variable it declares, with that variable's scopes, which is
 * why each row here names the variables that read it.
 *
 * A reference resolves when a run starts, so a change here reaches a running
 * release on its next deploy, and the readers whose live release started
 * before the change are marked "Saved · not live yet" — the settings pages'
 * own words for a saved value a release has not picked up.
 *
 * The list is the same in its two places — the server's page under
 * Deployments, and a project's Variables settings — and `frame` puts its head
 * and body into each place's own section shape.
 */

type Sensitivity = DeploymentVariable["sensitivity"]

type Draft = { name: string; value: string; sensitivity: Sensitivity; editing: boolean }

const EDITOR_FORM = "shared-variable-editor"

function without(values: Record<string, string>, key: string) {
  const rest = { ...values }
  delete rest[key]
  return rest
}

export function useSharedVariables(scope: SharedScope, enabled = true) {
  const base = sharedBase(scope)
  return usePoll(
    (signal) => get<DeploymentSharedVariable[]>(base, undefined, signal),
    15000,
    [base],
    { enabled },
  )
}

export function SharedVariables({
  scope,
  frame,
}: {
  scope: SharedScope
  frame: (parts: {
    /** How many there are, for a head that counts; nothing while loading. */
    count?: number
    actions?: React.ReactNode
    body: React.ReactNode
  }) => React.ReactNode
}) {
  const { can } = useAuth()
  const canEdit = can("system.admin")
  const canRemove = can("destructive")
  const base = sharedBase(scope)
  const variables = useSharedVariables(scope)
  const list = variables.data
  const [column, columnWidth] = useColumnWidth()
  const wide = columnWidth >= 600
  const { confirm, dialog } = useConfirm()
  const [draft, setDraft] = useState<Draft>()
  const [shown, setShown] = useState(false)
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState("")
  const [revealed, setRevealed] = useState<Record<string, string>>({})
  const [revealing, setRevealing] = useState("")
  const word = scope.kind === "server" ? "server" : "project"

  const open = (next: Draft) => {
    setShown(false)
    setFormError("")
    setDraft(next)
  }
  const close = () => {
    setDraft(undefined)
    setFormError("")
  }
  const add = () => open({ name: "", value: "", sensitivity: "secret", editing: false })
  const edit = (variable: DeploymentSharedVariable) =>
    open({ name: variable.name, value: "", sensitivity: variable.sensitivity, editing: true })

  const name = draft?.name.trim() ?? ""
  const nameProblem = Boolean(draft && !draft.editing && name && !SHARED_NAME.test(name))
  const exists = Boolean(
    draft && !draft.editing && list?.some((variable) => variable.name === name),
  )
  // A new one needs its value; an edit with the field left empty keeps the
  // sealed one, which is how the type changes without retyping a secret.
  const incomplete = !draft || !name || nameProblem || (!draft.editing && !draft.value)

  const save = async (event?: FormEvent) => {
    event?.preventDefault()
    if (!draft || incomplete) return
    setSaving(true)
    setFormError("")
    try {
      const saved = await put<DeploymentSharedVariable>(`${base}/${encodeURIComponent(name)}`, {
        sensitivity: draft.sensitivity,
        ...(draft.value || !draft.editing ? { value: draft.value } : {}),
      })
      notify.success(draft.editing ? "Shared variable saved" : "Shared variable added", {
        description:
          saved.usedBy.length > 0
            ? "Each variable reading it takes it on its next deploy."
            : undefined,
      })
      setRevealed((current) => without(current, name))
      close()
      variables.refresh()
    } catch (caught) {
      if (caught instanceof ApiError && caught.status === 400) {
        setFormError(caught.message)
      } else {
        notify.error("Could not save the shared variable", caught)
      }
    } finally {
      setSaving(false)
    }
  }

  const reveal = async (variable: DeploymentSharedVariable) => {
    if (revealed[variable.name] !== undefined) {
      setRevealed((current) => without(current, variable.name))
      return
    }
    setRevealing(variable.name)
    try {
      const answer = await get<{ name: string; value: string }>(
        `${base}/${encodeURIComponent(variable.name)}/reveal`,
      )
      setRevealed((current) => ({ ...current, [variable.name]: answer.value }))
    } catch (caught) {
      notify.error(`Could not reveal ${variable.name}`, caught)
    } finally {
      setRevealing("")
    }
  }

  const remove = (variable: DeploymentSharedVariable) =>
    confirm({
      title: `Remove ${variable.name}`,
      confirmLabel: "Remove variable",
      subject: {
        mark: <VariableMark variable={variable} product={variableProduct(variable.name)} />,
        name: <span className="font-mono">{variable.name}</span>,
        facts: (
          <FormFact label="Read as" mono>
            {variable.reference}
          </FormFact>
        ),
      },
      // The server stays the authority: the verb is not withheld while the
      // variable is read, because its refusal names the readers.
      description: <p>{removalNote(variable)}</p>,
      action: async () => {
        await del(`${base}/${encodeURIComponent(variable.name)}`)
      },
      onDone: () => variables.refresh(),
    })

  const verbsFor = (variable: DeploymentSharedVariable): Verb[] => {
    const verbs: Verb[] = [
      {
        key: "copy",
        label: "Copy reference",
        icon: Copy,
        run: () => void copyText(variable.reference, "Reference copied"),
      },
    ]
    if (canEdit) {
      const isShown = revealed[variable.name] !== undefined
      verbs.push(
        {
          key: "reveal",
          label: isShown ? "Hide value" : "Reveal value",
          icon: isShown ? EyeOff : Eye,
          progressive: "Revealing…",
          disabled: revealing === variable.name,
          run: () => void reveal(variable),
        },
        { key: "edit", label: "Edit", icon: Pencil, run: () => edit(variable) },
      )
    }
    if (canRemove) {
      verbs.push({
        key: "remove",
        label: "Remove",
        icon: Trash,
        danger: true,
        run: () => remove(variable),
      })
    }
    return verbs
  }

  const addButton = canEdit && (
    <Button size="sm" variant="outline" onClick={add}>
      <Plus className="size-3.5" /> Add variable
    </Button>
  )

  const body = (
    <div ref={column} className="min-w-0">
      {variables.error && !list ? (
        <ErrorState error={variables.error} onRetry={variables.refresh} />
      ) : !list ? (
        <LoadingRows rows={3} />
      ) : list.length === 0 ? (
        <EmptyState
          icon={Linked}
          title={`No ${word} variables yet`}
          description={
            scope.kind === "server"
              ? "Define a value once and read it from any project's variables as ${{server.NAME}}."
              : "Define a value once and read it from any of this project's environments as ${{project.NAME}}."
          }
          action={
            canEdit && (
              <Button size="sm" onClick={add}>
                <Plus className="size-3.5" /> Add the first {word} variable
              </Button>
            )
          }
        />
      ) : (
        <ChoiceList aria-label={`${word === "server" ? "Server" : "Project"} variables`}>
          {list.map((variable, index) => (
            <SharedRow
              key={variable.name}
              index={index}
              variable={variable}
              revealed={revealed[variable.name]}
              wide={wide}
              canEdit={canEdit}
              onEdit={() => edit(variable)}
              verbs={verbsFor(variable)}
            />
          ))}
        </ChoiceList>
      )}
    </div>
  )

  const editing = draft?.editing
    ? list?.find((variable) => variable.name === draft.name)
    : undefined
  return (
    <>
      {frame({
        count: list?.length,
        actions: list && list.length > 0 ? addButton : undefined,
        body,
      })}
      <SidePanel
        open={Boolean(draft) && canEdit}
        onOpenChange={(next) => !next && close()}
        title={draft?.editing ? `Edit ${draft.name}` : `Add ${word} variable`}
        description="A value defined once and read by reference."
        width="md"
        footer={
          <>
            <FormNote className="mr-auto max-sm:hidden">
              Readers take it on their next deploy
            </FormNote>
            <Button variant="outline" onClick={close} disabled={saving}>
              Cancel
            </Button>
            <Button type="submit" form={EDITOR_FORM} pending={saving} disabled={incomplete}>
              {draft?.editing ? "Save variable" : "Add variable"}
            </Button>
          </>
        }
      >
        {draft && (
          <form id={EDITOR_FORM} onSubmit={(event) => void save(event)} className="space-y-6">
            {editing && (
              <FormFacts>
                <FormFact label="Read as" mono>
                  {editing.reference}
                </FormFact>
                <FormFact label="Read by">
                  {editing.usedBy.length === 0
                    ? "nothing yet"
                    : `${editing.usedBy.length} variable${editing.usedBy.length === 1 ? "" : "s"}`}
                </FormFact>
              </FormFacts>
            )}
            <div className="grid min-w-0 gap-4 sm:grid-cols-[minmax(0,1fr)_auto]">
              {!draft.editing && (
                <Field
                  label="Variable name"
                  htmlFor="shared-variable-name"
                  error={
                    nameProblem
                      ? "Use a letter first, then up to 127 letters, numbers or underscores."
                      : undefined
                  }
                  hint={
                    exists ? (
                      <span key="exists" className="block animate-rise">
                        {name} exists — saving replaces its value and type.
                      </span>
                    ) : name && !nameProblem ? (
                      <span key="read-as" className="block font-mono">
                        {sharedReference(scope.kind, name)}
                      </span>
                    ) : undefined
                  }
                >
                  <Input
                    id="shared-variable-name"
                    value={draft.name}
                    onChange={(event) =>
                      setDraft({ ...draft, name: event.target.value.toUpperCase() })
                    }
                    autoComplete="off"
                    spellCheck={false}
                    placeholder="STRIPE_SECRET_KEY"
                    className="font-mono"
                  />
                </Field>
              )}
              <Field label="Type">
                <ValueType
                  value={draft.sensitivity}
                  onChange={(sensitivity) => setDraft({ ...draft, sensitivity })}
                />
              </Field>
            </div>
            <Field
              label="Value"
              htmlFor="shared-variable-value"
              hint={draft.editing ? "Leave it empty to keep the saved value." : undefined}
            >
              <InputGroup>
                <InputGroupInput
                  id="shared-variable-value"
                  type={draft.sensitivity === "plain" || shown ? "text" : "password"}
                  value={draft.value}
                  onChange={(event) => setDraft({ ...draft, value: event.target.value })}
                  autoComplete="off"
                  spellCheck={false}
                  placeholder={draft.editing ? "Unchanged" : undefined}
                  className="font-mono"
                />
                {draft.sensitivity === "secret" && draft.value && (
                  <InputGroupAddon align="inline-end" className="gap-0 p-0">
                    <InputGroupButton
                      aria-label={shown ? "Hide value" : "Show value"}
                      onClick={() => setShown(!shown)}
                    >
                      {shown ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
                    </InputGroupButton>
                  </InputGroupAddon>
                )}
              </InputGroup>
            </Field>
            {formError && (
              <FormNote key={formError} tone="danger" className="animate-rise">
                {formError}
              </FormNote>
            )}
          </form>
        )}
      </SidePanel>
      {dialog}
    </>
  )
}

/**
 * One shared variable: its name, the reference that reads it, and who
 * changed it when; at its edge whether a reader still runs an earlier value
 * and how many read it; under it the readers themselves, each a link to the
 * variables page it is read from. Narrow, the edge goes under the name.
 */
function SharedRow({
  variable,
  revealed,
  wide,
  canEdit,
  index,
  onEdit,
  verbs,
}: {
  variable: DeploymentSharedVariable
  revealed?: string
  wide: boolean
  canEdit: boolean
  index: number
  onEdit: () => void
  verbs: Verb[]
}) {
  const waiting = waitingReaders(variable)
  const state = waiting > 0 && (
    <Status key="waiting" tone="notice" label="Saved · not live yet" className="animate-rise" />
  )
  const count = (
    <span className="numeric text-hint whitespace-nowrap text-muted-foreground">
      {variable.usedBy.length === 0 ? "Not read" : `Read by ${variable.usedBy.length}`}
    </span>
  )
  const readers = readersByProject(variable.usedBy)
  return (
    <ChoiceRow
      index={index}
      verb={`Edit ${variable.name}`}
      disabled={!canEdit}
      onSelect={onEdit}
      leading={<VariableMark variable={variable} product={variableProduct(variable.name)} />}
      title={<span className="font-mono">{variable.name}</span>}
      description={
        <>
          <span className="font-mono text-foreground/90">{variable.reference}</span>
          <span> · changed {relativeTime(variable.updatedAt)} </span>
          {variable.updatedBy && (
            <span title={variable.updatedBy}>
              <span className={cn(!wide && "sr-only")}>by </span>
              <InitialsMark
                name={variable.updatedBy}
                size="xs"
                className="inline-flex size-3.5 align-[-3px]"
              />
              <span className={cn(!wide && "sr-only")}> {variable.updatedBy}</span>
            </span>
          )}
        </>
      }
      trailing={
        wide ? (
          <>
            {state}
            {count}
          </>
        ) : undefined
      }
      actions={<VerbActions dim verbs={verbs} menuLabel={`Actions for ${variable.name}`} />}
    >
      {(!wide || readers.length > 0 || revealed !== undefined) && (
        <div className="min-w-0 space-y-2 sm:pl-11">
          {!wide && (
            <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
              {count}
              {state}
            </div>
          )}
          {readers.length > 0 && (
            // A press on a reader follows its link and opens nothing else.
            <ul
              aria-label={`Variables reading ${variable.name}`}
              onClick={(event) => event.stopPropagation()}
              className="flex min-w-0 flex-wrap gap-x-4 gap-y-1 text-hint text-muted-foreground"
            >
              {readers.map((project) => (
                <li key={project.projectId} className="min-w-0">
                  <Link
                    href={`/deploy/${project.projectId}/settings/variables`}
                    className="rounded-sm text-foreground/90 focus-ring hover:underline"
                  >
                    {project.projectName}
                  </Link>
                  {project.uses.map((use) => (
                    <span key={`${use.environmentId}-${use.variable}`}>
                      {" · "}
                      {use.environmentName} as <span className="font-mono">{use.variable}</span>
                      {use.waiting && <span className="text-warning"> (not live yet)</span>}
                    </span>
                  ))}
                </li>
              ))}
            </ul>
          )}
          {revealed !== undefined && (
            // The value invites a click to select it, and the row around it
            // opens the editor on a press — so a press here stays here.
            <div
              onClick={(event) => event.stopPropagation()}
              className="flex min-w-0 animate-rise items-start gap-1"
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

/** The server's shared variables, under Deployments. */
export function SharedVariablesPage() {
  return (
    <Page>
      <PageContext
        eyebrow={
          <Link href="/deploy" className="rounded-sm focus-ring hover:underline">
            Deployments
          </Link>
        }
        title="Shared variables"
      />
      <SharedVariables
        scope={{ kind: "server" }}
        frame={({ count, actions, body }) => (
          <Panel plain>
            <PanelHeader
              title="Server variables"
              actions={
                <span className="flex flex-wrap items-center gap-3">
                  {count !== undefined && count > 0 && (
                    <span className="numeric text-hint text-muted-foreground">
                      {count} variable{count === 1 ? "" : "s"}
                    </span>
                  )}
                  {actions}
                </span>
              }
            />
            <PanelBody>{body}</PanelBody>
          </Panel>
        )}
      />
    </Page>
  )
}

/**
 * The shared variables an environment's variable can read, chosen by name
 * rather than typed as `${{server.NAME}}`: this project's first, then the
 * server's. Choosing a secret one makes the variable secret too, which the
 * server requires of a variable reading a secret.
 */
export function SharedReferencePicker({
  projectId,
  value,
  onChange,
}: {
  projectId: number
  value: string
  onChange: (reference: string, sensitivity: Sensitivity) => void
}) {
  const project = useSharedVariables({ kind: "project", projectId })
  const server = useSharedVariables({ kind: "server" })
  const groups = [
    { label: "This project", rows: project.data ?? [] },
    { label: "This server", rows: server.data ?? [] },
  ].filter((group) => group.rows.length > 0)
  if (groups.length === 0) return null
  const chosen = parseSharedReference(value)
  const all = groups.flatMap((group) => group.rows)
  return (
    // The Reference toggle reveals it, so it rises in under the field.
    <div className="animate-rise">
      <Field label="Shared variable" htmlFor="variable-shared-reference">
        <Select
          value={chosen ? sharedReference(chosen.kind, chosen.name) : ""}
          onValueChange={(reference) => {
            const picked = all.find((variable) => variable.reference === reference)
            if (picked) onChange(picked.reference, picked.sensitivity)
          }}
        >
          <SelectTrigger id="variable-shared-reference" className="w-full">
            <SelectValue placeholder="Choose a shared variable" />
          </SelectTrigger>
          <SelectContent>
            {groups.map((group) => (
              <SelectGroup key={group.label}>
                <SelectLabel>{group.label}</SelectLabel>
                {group.rows.map((variable) => {
                  const product = variableProduct(variable.name)
                  return (
                    <SelectItem
                      key={variable.reference}
                      value={variable.reference}
                      hint={
                        <span aria-hidden className="font-mono">
                          {variable.reference}
                        </span>
                      }
                    >
                      {product && <ProductGlyph id={product} />}
                      <span className="font-mono">{variable.name}</span>
                    </SelectItem>
                  )
                })}
              </SelectGroup>
            ))}
          </SelectContent>
        </Select>
      </Field>
    </div>
  )
}
