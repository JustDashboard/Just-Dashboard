"use client"

import { useState } from "react"
import type { FormEvent } from "react"
import Link from "next/link"
import { ApiError, del, get, put } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { relativeTime } from "@/lib/format"
import { NOT_A_SIGN_IN, secretValueField } from "@/lib/secret-field"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentSharedVariable, DeploymentVariable } from "@/lib/types"
import { InitialsMark } from "@/components/account/user-avatar"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Field, FormFact, FormFacts, FormNote } from "@/components/form"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph, variableProduct } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
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
import { RevealedValue, ValueType, VariableMark } from "@/components/deploy/settings/variable-type"
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
    const value = revealed[variable.name]
    const isShown = value !== undefined
    // Both inline in every state, so the row's edge is one width and revealing
    // a value moves nothing. The copy takes what the row is showing.
    const verbs: Verb[] = []
    if (canEdit)
      verbs.push({
        key: "reveal",
        label: isShown ? "Hide value" : "Reveal value",
        icon: isShown ? EyeOff : Eye,
        inline: true,
        progressive: "Revealing…",
        disabled: revealing === variable.name,
        run: () => void reveal(variable),
      })
    verbs.push({
      key: "copy",
      label: isShown ? `Copy ${variable.name}` : "Copy reference",
      icon: Copy,
      inline: true,
      run: () =>
        void (isShown
          ? copyText(value, `${variable.name} copied`)
          : copyText(variable.reference, "Reference copied")),
    })
    if (canEdit) {
      verbs.push({ key: "edit", label: "Edit", icon: Pencil, run: () => edit(variable) })
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
                    {...NOT_A_SIGN_IN}
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
                  {...secretValueField(draft.sensitivity === "secret" && !shown)}
                  value={draft.value}
                  onChange={(event) => setDraft({ ...draft, value: event.target.value })}
                  placeholder={draft.editing ? "Unchanged" : undefined}
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
 * One shared variable, drawn as an environment's variable is: its name and the
 * reference that reads it on one line, and at its edge whether a reader still
 * runs an earlier value, how many read it and who changed it. Its value,
 * revealed, takes the reference's place. Narrow, the reference goes under the
 * name with the count beside it. Nothing ever hangs under the row.
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
  const waiting = waitingReaders(variable) > 0
  const value =
    revealed !== undefined ? (
      <RevealedValue name={variable.name} value={revealed} />
    ) : (
      <span className="block h-4 truncate font-mono leading-4 text-foreground/90">
        {variable.reference}
      </span>
    )
  const changed = `Changed ${relativeTime(variable.updatedAt)}${variable.updatedBy ? ` by ${variable.updatedBy}` : ""}`
  const changedBy = variable.updatedBy && (
    <span title={changed} className="inline-flex shrink-0">
      <span className="sr-only">{changed}</span>
      <InitialsMark name={variable.updatedBy} size="xs" className="size-4" />
    </span>
  )
  return (
    <ChoiceRow
      index={index}
      verb={`Edit ${variable.name}`}
      disabled={!canEdit}
      onSelect={onEdit}
      leading={<VariableMark variable={variable} product={variableProduct(variable.name)} />}
      title={<span className="font-mono">{variable.name}</span>}
      columns={wide}
      className={wide ? "min-h-11 py-1.5" : "py-2"}
      description={
        <span className="flex min-w-0 items-center gap-2">
          <span className="min-w-0 flex-1">{value}</span>
          {wide ? (
            waiting && (
              <Status
                key="waiting"
                tone="notice"
                label="Saved · not live yet"
                className="animate-rise"
              />
            )
          ) : (
            <SharedReaders variable={variable} waiting={waiting} />
          )}
        </span>
      }
      trailing={
        wide ? (
          <>
            <SharedReaders variable={variable} wide />
            {changedBy}
          </>
        ) : undefined
      }
      actions={<VerbActions dim verbs={verbs} menuLabel={`Actions for ${variable.name}`} />}
    />
  )
}

/**
 * How many variables read this one, opening on who they are. The readers were
 * a band under the row, which made a read variable a taller card than an unread
 * one, with empty room under its mark. Each is a link to the page it is read
 * from, and says if it still runs an earlier value. On a phone the row has room
 * for one word here, so a value not live yet says that instead of the count.
 */
function SharedReaders({
  variable,
  wide = false,
  waiting = false,
}: {
  variable: DeploymentSharedVariable
  wide?: boolean
  waiting?: boolean
}) {
  // One width, so the edge is the same on every row and the values line up.
  const width = wide && "w-14 text-right"
  if (variable.usedBy.length === 0)
    return (
      <span className={cn("shrink-0 text-hint whitespace-nowrap text-muted-foreground", width)}>
        Not read
      </span>
    )
  const count = `Read by ${variable.usedBy.length}`
  return (
    <Popover>
      <PopoverTrigger
        aria-label={count}
        className={cn(
          "numeric shrink-0 rounded-sm text-hint whitespace-nowrap text-muted-foreground focus-ring hover:text-foreground",
          width,
        )}
      >
        {waiting ? <Status tone="notice" label="Not live yet" /> : count}
      </PopoverTrigger>
      {/* A press in here reaches the row through React even from a portal, and
          the row would open its editor. */}
      <PopoverContent align="end" onClick={(event) => event.stopPropagation()}>
        <ul aria-label={`Variables reading ${variable.name}`} className="space-y-2 text-hint">
          {readersByProject(variable.usedBy).map((project) => (
            <li key={project.projectId} className="min-w-0 text-muted-foreground">
              <Link
                href={`/deploy/${project.projectId}/settings/variables`}
                className="rounded-sm font-medium text-foreground focus-ring hover:underline"
              >
                {project.projectName}
              </Link>
              {project.uses.map((use) => (
                <span key={`${use.environmentId}-${use.variable}`} className="block break-words">
                  {use.environmentName} as <span className="font-mono">{use.variable}</span>
                  {use.waiting && <span className="text-warning"> (not live yet)</span>}
                </span>
              ))}
            </li>
          ))}
        </ul>
      </PopoverContent>
    </Popover>
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
