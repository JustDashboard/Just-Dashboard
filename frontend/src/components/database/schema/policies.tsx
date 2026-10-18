"use client"

import { useId, useState } from "react"
import { Pencil, Plus, Trash } from "@/components/icons"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { Segments } from "@/components/deploy/settings/segments"
import { Field, FormFact, FormNote, OptionList, OptionRow } from "@/components/form"
import { EmptyNote } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { VerbActions, VerbMenu } from "@/components/verbs"
import { read } from "@/components/database/home/read"
import { ReadFailed } from "@/components/database/fleet/read-failed"
import type { DbRole, DbRoles } from "@/components/database/ops/access-types"
import { useDatabase } from "@/components/database/shell/database-context"
import { ChangeDialog, type Risk } from "@/components/database/schema/change-dialog"
import { qualified } from "@/components/database/schema/changes"
import { notesFor, quoteName } from "@/components/database/schema/engine-notes"
import {
  BLANK_POLICY,
  COMMAND_WORD,
  POLICY_COMMANDS,
  TEMPLATES,
  alterPolicyRequest,
  clausesOf,
  createPolicyRequest,
  draftOf,
  filterWords,
  fromTemplate,
  guessColumn,
  rowSecurityRequest,
  templateProblem,
  type PolicyCommand,
  type PolicyDraft,
  type PolicyTemplateId,
  type TemplateInputs,
} from "@/components/database/schema/policy"
import type {
  DbPolicy,
  DbRowSecurity,
  DbTableDetail,
  DdlAnswer,
} from "@/components/database/schema/types"

/**
 * A table's row-level security: whether the table filters its rows by who
 * asks, the policies that decide it, and the forms that change both.
 *
 * It is read on its own route (`GET /rls`), and every change is a structure
 * change like the others — the form, the server's statement for it, the one
 * command — so what is read here is what runs.
 */

/** A table's row security; null `enabled` leaves it unread (not a PostgreSQL table, or not asked). */
export function useRowSecurity(
  id: number,
  schema: string,
  table: string,
  enabled: boolean,
  asked: number,
) {
  return usePoll(
    (signal) =>
      read<DbRowSecurity>(
        `/databases/${id}/rls`,
        (answer) => Array.isArray(answer.policies) && Array.isArray(answer.exempt),
        { schema: schema || undefined, table },
        signal,
      ),
    0,
    [id, schema, table, asked],
    { enabled: enabled && table !== "" },
  )
}

/** The server's accounts, for the policy form and the test: read on the read surface. */
export function useRoles(id: number, enabled: boolean) {
  return usePoll(
    (signal) =>
      read<DbRoles>(
        `/databases/${id}/server/roles`,
        (answer) => Array.isArray(answer.roles),
        undefined,
        signal,
      ),
    0,
    [id],
    { enabled },
  )
}

/** The roles a policy may name: the ones the engine did not make for itself. */
export function namedRoles(roles: DbRole[] | undefined): string[] {
  return (roles ?? []).filter((role) => !role.system).map((role) => role.name)
}

/**
 * The roles a test may become. A superuser is left out: no policy filters it,
 * so what it sees says nothing about them, and the server will not run as one.
 */
export function testableRoles(roles: DbRole[] | undefined): string[] {
  return (roles ?? []).filter((role) => !role.system && !role.superuser).map((role) => role.name)
}

const NARROW = 640
const HEAD = "h-8 px-3 first:pl-4 last:pr-4"
const CELL = "px-3 py-1 align-top first:pl-4 last:pr-4"
const ROW_ACTIONS = "justify-end [&_button]:size-6"

/** What a policy applies to, in the words of the form: "Reading, permissive". */
function appliesTo(policy: DbPolicy): string {
  return COMMAND_WORD[policy.command]
}

/** The reading: the switches' state, then the policies. */
export function Policies({
  security,
  mayChange,
  mayDrop,
  onSwitches,
  onEdit,
  onDrop,
}: {
  security: DbRowSecurity
  /** The reader may change policies and the switches. */
  mayChange: boolean
  /** ...and may take one away. */
  mayDrop: boolean
  onSwitches: () => void
  onEdit: (policy: DbPolicy) => void
  onDrop: (policy: DbPolicy) => void
}) {
  const [box, width] = useColumnWidth<HTMLDivElement>()
  const narrow = width > 0 && width < NARROW
  const acts = mayChange
  const row = (policy: DbPolicy) =>
    acts ? (
      <VerbActions
        dim={!narrow}
        className={ROW_ACTIONS}
        verbs={[
          {
            key: "edit",
            label: `Edit ${policy.name}`,
            icon: Pencil,
            inline: true,
            run: () => onEdit(policy),
          },
          ...(mayDrop
            ? [
                {
                  key: "drop",
                  label: `Drop ${policy.name}`,
                  icon: Trash,
                  inline: true,
                  run: () => onDrop(policy),
                },
              ]
            : []),
        ]}
      />
    ) : null

  const who = (policy: DbPolicy) =>
    policy.roles.length === 0 ? (
      <span className="text-muted-foreground">everybody</span>
    ) : (
      policy.roles.join(", ")
    )

  return (
    <div ref={box} className="min-w-0 shrink-0">
      <section
        aria-label="Row-level security"
        className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-hairline px-4 py-3"
      >
        <Status
          tone={security.enabled ? "running" : "stopped"}
          label={security.enabled ? "On" : "Off"}
        />
        {security.forced && <Tag>forced on the owner</Tag>}
        <p className="min-w-0 flex-1 text-body">{filterWords(security)}</p>
        {mayChange && (
          <Button size="xs" variant="outline" onClick={onSwitches}>
            {security.enabled ? "Change…" : "Turn on…"}
          </Button>
        )}
        {security.exempt.length > 0 && (
          <p className="flex basis-full flex-wrap items-center gap-1.5 text-hint text-muted-foreground">
            No policy filters
            {security.exempt.map((name) => (
              <Tag key={name} mono>
                {name}
              </Tag>
            ))}
            : an administrator or a role that bypasses row-level security.
          </p>
        )}
      </section>

      {security.policies.length === 0 ? (
        <EmptyNote className="px-4 py-8">
          This table has no policy.
          {mayChange ? " Add one to say which rows a role may see and write." : ""}
        </EmptyNote>
      ) : narrow ? (
        <ul aria-label="Policies" className="divide-y divide-hairline border-b border-hairline">
          {security.policies.map((policy) => (
            <li key={policy.name} className="group flex items-start gap-2 py-2 pr-2 pl-4">
              <div className="min-w-0 flex-1 space-y-1">
                <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                  <span className="font-mono text-xs font-medium break-all">{policy.name}</span>
                  <Tag>{policy.permissive ? "permissive" : "restrictive"}</Tag>
                </div>
                <p className="text-hint text-muted-foreground">
                  {appliesTo(policy)} · for {who(policy)}
                </p>
                {policy.using && (
                  <p className="font-mono text-xs break-words">
                    <span className="text-muted-foreground">using </span>
                    {policy.using}
                  </p>
                )}
                {policy.withCheck && (
                  <p className="font-mono text-xs break-words">
                    <span className="text-muted-foreground">with check </span>
                    {policy.withCheck}
                  </p>
                )}
              </div>
              {row(policy)}
            </li>
          ))}
        </ul>
      ) : (
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead className={HEAD}>Name</TableHead>
              <TableHead className={HEAD}>Applies to</TableHead>
              <TableHead className={HEAD}>Roles</TableHead>
              <TableHead className={HEAD}>Using</TableHead>
              <TableHead className={HEAD}>With check</TableHead>
              {acts && (
                <TableHead className={cn(HEAD, "w-px")}>
                  <span className="sr-only">Actions</span>
                </TableHead>
              )}
            </TableRow>
          </TableHeader>
          <TableBody>
            {security.policies.map((policy) => (
              <TableRow key={policy.name} className="group">
                <TableCell className={cn(CELL, "font-mono font-medium")}>{policy.name}</TableCell>
                <TableCell className={CELL}>
                  <span className="flex flex-wrap items-center gap-x-2">
                    {appliesTo(policy)}
                    <Tag>{policy.permissive ? "permissive" : "restrictive"}</Tag>
                  </span>
                </TableCell>
                <TableCell className={cn(CELL, "font-mono")}>{who(policy)}</TableCell>
                <TableCell className={cn(CELL, "max-w-80 font-mono")}>
                  {policy.using ? (
                    <span className="block truncate" title={policy.using}>
                      {policy.using}
                    </span>
                  ) : (
                    <span className="text-muted-foreground">none</span>
                  )}
                </TableCell>
                <TableCell className={cn(CELL, "max-w-80 font-mono")}>
                  {policy.withCheck ? (
                    <span className="block truncate" title={policy.withCheck}>
                      {policy.withCheck}
                    </span>
                  ) : (
                    <span className="text-muted-foreground">none</span>
                  )}
                </TableCell>
                {acts && <TableCell className={cn(CELL, "pl-0")}>{row(policy)}</TableCell>}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}

/** The reading before its answer, and when it has none. */
export function PoliciesPlaceholder({
  error,
  onRetry,
}: {
  error: Error | undefined
  onRetry: () => void
}) {
  if (error) {
    return (
      <div className="p-4">
        <ReadFailed error={error} onRetry={onRetry} />
      </div>
    )
  }
  return (
    <div aria-hidden className="space-y-3 px-4 py-4">
      {[48, 72, 60].map((width) => (
        <Skeleton key={width} className="h-5" style={{ width: `${width}%` }} />
      ))}
    </div>
  )
}

/* ------------------------------------------------------------ the switches */

const TURNING_OFF: Risk = {
  title: "Every row becomes visible",
  description:
    "The policies are kept, and nothing applies them: everyone who may read the table sees all its rows until it is turned on again.",
  question: "Turn row-level security off?",
}

const UNFORCING: Risk = {
  title: "The owner sees every row",
  description:
    "The policies still apply to other roles. The owner, and any role that inherits the owner's rights, is no longer held to them.",
  question: "Stop holding the owner to the policies?",
}

/** Turning row-level security on and off, and holding the table's owner to it. */
export function RowSecurityDialog({
  detail,
  security,
  mayTurnOff,
  onClose,
  onDone,
}: {
  detail: DbTableDetail
  security: DbRowSecurity
  /** The reader may take the filter off: that is the destructive capability's. */
  mayTurnOff: boolean
  onClose: () => void
  onDone: (answer: DdlAnswer) => void
}) {
  const now = { enabled: security.enabled, forced: security.forced }
  const [wanted, setWanted] = useState(now)
  const request = rowSecurityRequest(detail.schema, detail.name, now, wanted)
  const turningOff = now.enabled && !wanted.enabled
  const unforcing = now.forced && !wanted.forced
  return (
    <ChangeDialog
      onClose={onClose}
      onDone={onDone}
      title="Row-level security"
      subject={{
        name: qualified(detail.schema, detail.name),
        facts: security.owner ? (
          <FormFact label="Owner" mono>
            {security.owner}
          </FormFact>
        ) : undefined,
      }}
      request={request}
      waiting="Change a switch to see the statement."
      command="Apply"
      done={`Changed row-level security on ${detail.name}`}
      dirty={request !== null}
      confirm={turningOff ? TURNING_OFF : unforcing ? UNFORCING : null}
    >
      <OptionList>
        <OptionRow
          title="Filter the table's rows by its policies"
          hint={
            now.enabled && !mayTurnOff
              ? "Turning it off needs the destructive capability, which this role does not have."
              : "Off, the policies are kept and nothing applies them."
          }
          checked={wanted.enabled}
          disabled={now.enabled && !mayTurnOff}
          onCheckedChange={(enabled) => setWanted({ ...wanted, enabled })}
        />
        <OptionRow
          title="Hold the table's owner to them as well"
          hint={
            now.forced && !mayTurnOff
              ? "Letting the owner go needs the destructive capability, which this role does not have."
              : "An owner is not filtered otherwise, and the dashboard's own account usually is one."
          }
          checked={wanted.forced}
          disabled={now.forced && !mayTurnOff}
          onCheckedChange={(forced) => setWanted({ ...wanted, forced })}
        />
      </OptionList>
      {!wanted.enabled && wanted.forced && (
        <FormNote>The owner is held to the policies only while security is on.</FormNote>
      )}
    </ChangeDialog>
  )
}

/* ------------------------------------------------------------- the policies */

/** The roles a policy applies to: the chosen ones, and a menu of the rest. */
function RolePicker({
  label,
  roles,
  chosen,
  onChange,
}: {
  label: string
  roles: string[]
  chosen: string[]
  onChange: (chosen: string[]) => void
}) {
  const rest = roles.filter((role) => !chosen.includes(role))
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1.5">
      {chosen.length === 0 ? (
        <span className="text-hint text-muted-foreground">Everybody</span>
      ) : (
        <ChipStrip role="group" aria-label={label} className="max-sm:mx-0 max-sm:px-0">
          {chosen.map((role) => (
            <FilterChip
              key={role}
              selected
              className="font-mono"
              aria-label={`${role}: press to take it out`}
              onClick={() => onChange(chosen.filter((one) => one !== role))}
            >
              {role}
            </FilterChip>
          ))}
        </ChipStrip>
      )}
      {rest.length > 0 && (
        <VerbMenu
          align="start"
          label="Roles to add"
          verbs={rest.map((role) => ({
            key: role,
            label: role,
            icon: Plus,
            run: () => onChange([...chosen, role]),
          }))}
          trigger={
            <Button size="xs" variant="ghost">
              <Plus />
              Add a role
            </Button>
          }
        />
      )}
    </div>
  )
}

/** A policy to add, or one to change. */
export function PolicyDialog({
  detail,
  policy,
  roles,
  onClose,
  onDone,
}: {
  detail: DbTableDetail
  /** Absent for a new policy. */
  policy?: DbPolicy
  /** The accounts to choose from; none when the list could not be read. */
  roles: string[]
  onClose: () => void
  onDone: (answer: DdlAnswer) => void
}) {
  const ids = useId()
  const { engine } = useDatabase()
  const notes = notesFor(engine)
  const [draft, setDraft] = useState<PolicyDraft>(policy ? draftOf(policy) : BLANK_POLICY)
  const columns = detail.columns.map((column) => column.name)
  // A template fills the form until the reader writes in it, and only then
  // is the form theirs.
  const [template, setTemplate] = useState<{ id: PolicyTemplateId; inputs: TemplateInputs }>()
  const [edited, setEdited] = useState(false)
  // The name the template gave, so that one the reader wrote is not replaced.
  const [filledName, setFilledName] = useState("")

  const edit = (patch: Partial<PolicyDraft>) => {
    // The name is the reader's whatever the template says; the rest is the
    // template's until they write in it.
    if (Object.keys(patch).some((key) => key !== "name")) setEdited(true)
    setDraft({ ...draft, ...patch })
  }
  const fill = (id: PolicyTemplateId, inputs: TemplateInputs) => {
    setTemplate({ id, inputs })
    const chosen = TEMPLATES.find((one) => one.id === id)
    const filled = chosen
      ? fromTemplate(chosen, detail.name, inputs, (name) => quoteName(name, notes))
      : null
    if (filled && (!edited || template?.id !== id)) {
      const named = draft.name.trim() === "" || draft.name === filledName
      setDraft({ ...draft, ...filled, name: named ? filled.name : draft.name })
      setFilledName(filled.name)
      setEdited(false)
    }
  }
  const pick = (id: PolicyTemplateId) => {
    const chosen = TEMPLATES.find((one) => one.id === id)
    fill(id, {
      column: guessColumn(id, columns),
      setting: chosen?.setting ?? "",
      role: roles[0] ?? "",
    })
  }
  const chosen = template && TEMPLATES.find((one) => one.id === template.id)

  const takes = clausesOf(draft.command)
  const request = policy
    ? alterPolicyRequest(detail.schema, detail.name, policy, draft)
    : createPolicyRequest(detail.schema, detail.name, draft)
  const table = qualified(detail.schema, detail.name)

  return (
    <ChangeDialog
      onClose={onClose}
      onDone={onDone}
      title={policy ? "Edit policy" : "Add policy"}
      subject={{
        name: policy ? `${policy.name} on ${table}` : table,
        facts: policy ? (
          <>
            <FormFact label="Applies to">{appliesTo(policy)}</FormFact>
            <FormFact label="Kind">{policy.permissive ? "permissive" : "restrictive"}</FormFact>
          </>
        ) : undefined,
      }}
      request={request}
      waiting={
        policy
          ? "Change the roles or a condition to see the statement."
          : "Name the policy and write the condition it applies."
      }
      command={policy ? "Save policy" : "Add policy"}
      done={policy ? `Changed ${policy.name}` : `Added ${draft.name.trim() || "the policy"}`}
      dirty={
        policy ? request !== null : request !== null || draft.name !== "" || draft.using !== ""
      }
      size="lg"
    >
      {!policy && (
        <Field
          label="Start from"
          hint="Fills the form below. The statement is shown before it runs."
        >
          <ChipStrip role="group" aria-label="Templates" className="max-sm:mx-0 max-sm:px-0">
            {TEMPLATES.map((one) => (
              <FilterChip
                key={one.id}
                selected={template?.id === one.id}
                onClick={() => pick(one.id)}
              >
                {one.label}
              </FilterChip>
            ))}
          </ChipStrip>
        </Field>
      )}
      {chosen && template && (
        <div className="space-y-3 rounded-md border border-hairline p-3">
          <p className="text-hint text-muted-foreground">{chosen.says}</p>
          <div className="flex flex-wrap items-end gap-3">
            {chosen.needs.includes("column") && (
              <Field label="Column" className="w-44">
                <Select
                  value={template.inputs.column}
                  onValueChange={(column) => fill(chosen.id, { ...template.inputs, column })}
                >
                  <SelectTrigger
                    aria-label="Column the policy compares"
                    className="h-8 font-mono text-xs"
                  >
                    <SelectValue placeholder="Choose" />
                  </SelectTrigger>
                  <SelectContent>
                    {columns.map((name) => (
                      <SelectItem key={name} value={name} className="font-mono text-xs">
                        {name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            )}
            {chosen.needs.includes("setting") && (
              <Field label="Setting" htmlFor={`${ids}-setting`} className="w-52">
                <Input
                  id={`${ids}-setting`}
                  autoComplete="off"
                  spellCheck={false}
                  className="h-8 font-mono text-xs"
                  value={template.inputs.setting}
                  onChange={(event) =>
                    fill(chosen.id, { ...template.inputs, setting: event.target.value })
                  }
                />
              </Field>
            )}
            {chosen.needs.includes("role") && (
              <Field label="Role" className="w-44">
                <Select
                  value={template.inputs.role}
                  onValueChange={(role) => fill(chosen.id, { ...template.inputs, role })}
                >
                  <SelectTrigger
                    aria-label="Role the policy is for"
                    className="h-8 font-mono text-xs"
                  >
                    <SelectValue placeholder="Choose" />
                  </SelectTrigger>
                  <SelectContent>
                    {roles.map((name) => (
                      <SelectItem key={name} value={name} className="font-mono text-xs">
                        {name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            )}
          </div>
          {templateProblem(chosen, template.inputs) && (
            <FormNote tone="warning">{templateProblem(chosen, template.inputs)}</FormNote>
          )}
        </div>
      )}
      {!policy && (
        <Field label="Name" htmlFor={`${ids}-name`}>
          <Input
            id={`${ids}-name`}
            autoComplete="off"
            spellCheck={false}
            className="font-mono text-xs"
            value={draft.name}
            onChange={(event) => edit({ name: event.target.value })}
          />
        </Field>
      )}
      {!policy && (
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Applies to">
            <Select
              value={draft.command}
              onValueChange={(command) => edit({ command: command as PolicyCommand })}
            >
              <SelectTrigger aria-label="Command the policy applies to" className="h-8">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {POLICY_COMMANDS.map((command) => (
                  <SelectItem key={command} value={command}>
                    {COMMAND_WORD[command]}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field
            label="Kind"
            hint={
              draft.restrictive
                ? "A row must meet this as well as one permissive policy."
                : "A row is let through if any permissive policy lets it."
            }
          >
            <Segments
              label="Kind of policy"
              value={draft.restrictive ? "restrictive" : "permissive"}
              options={[
                { value: "permissive", label: "Permissive" },
                { value: "restrictive", label: "Restrictive" },
              ]}
              onChange={(kind) => edit({ restrictive: kind === "restrictive" })}
            />
          </Field>
        </div>
      )}
      <Field label="For" hint="Empty is every role.">
        <RolePicker
          label="Roles the policy applies to"
          roles={roles}
          chosen={draft.roles}
          onChange={(chosen) => edit({ roles: chosen })}
        />
      </Field>
      {takes.using && (
        <Field
          label="A row is visible when"
          htmlFor={`${ids}-using`}
          hint="USING. A condition on the row's columns, written bare."
        >
          <Textarea
            id={`${ids}-using`}
            rows={2}
            autoComplete="off"
            spellCheck={false}
            className="font-mono text-xs"
            value={draft.using}
            onChange={(event) => edit({ using: event.target.value })}
          />
        </Field>
      )}
      {takes.withCheck && (
        <Field
          label="A row may be written when"
          htmlFor={`${ids}-check`}
          hint={
            draft.command === "ALL" || draft.command === "UPDATE"
              ? "WITH CHECK. Empty, the condition above is used."
              : "WITH CHECK."
          }
        >
          <Textarea
            id={`${ids}-check`}
            rows={2}
            autoComplete="off"
            spellCheck={false}
            className="font-mono text-xs"
            value={draft.withCheck}
            onChange={(event) => edit({ withCheck: event.target.value })}
          />
        </Field>
      )}
      {policy && (
        <FormNote>
          A policy&rsquo;s command and kind cannot be changed, and a condition cannot be taken away:
          drop the policy and add it again for that. A condition left empty stays as it is.
        </FormNote>
      )}
    </ChangeDialog>
  )
}
