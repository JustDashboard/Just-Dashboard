"use client"

import { Fragment, useState } from "react"
import Link from "next/link"
import { Copy, Pencil, Plus, Shield, Trash, Warning } from "@/components/icons"
import { notify } from "@/lib/toast"
import { ApiError, del, get, put } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { AuthFile } from "@/lib/types"
import type {
  AccessList,
  AccessListSaved,
  AccessListSpec,
  AccessListUse,
  AccessLists,
} from "@/lib/proxy/types-access-lists"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { Segments } from "@/components/deploy/settings/segments"
import { Disclosure, Field, FormNote, FormSection, FormSections } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Modal } from "@/components/modal"
import { Well } from "@/components/panel"
import { ROW_BLEED } from "@/components/row-list"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { VerbActions } from "@/components/verbs"
import { accessFor, checkEntry, sameEntry } from "@/components/proxy/access-lists"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

/**
 * Named access lists: allowed and denied addresses and a password file, kept
 * in one file under jd-access that sites include.
 *
 * An office range or a VPN in front of ten staging sites was ten copies of the
 * same lines in ten site forms, and the day the range changed it was ten edits
 * and ten reloads — and the site that was missed kept letting the old range
 * in. Here the list is edited once, tested with every site that includes it,
 * and reloaded once. A list that would refuse the person editing it says so
 * before it is saved, since it would lock them out of all of those sites.
 */
export function AccessListsPanel() {
  const { confirm, dialog } = useConfirm()
  const { data, error, loading, refresh } = usePoll<AccessLists>(
    (signal) => get("/proxy/access-lists/", undefined, signal),
    30_000,
  )
  // Remounted per opening, so a new list never starts from the last draft.
  const [form, setForm] = useState<{ open: boolean; editing: AccessList | null; session: number }>({
    open: false,
    editing: null,
    session: 0,
  })
  const [output, setOutput] = useState<{ title: string; text: string } | null>(null)
  const openForm = (editing: AccessList | null) =>
    setForm((f) => ({ open: true, editing, session: f.session + 1 }))

  const remove = (list: AccessList) => {
    // The server refuses it too; saying which sites here saves a round trip
    // to a confirmation that could only fail.
    if (list.usedBy.length > 0) {
      notify.info(`${list.name} is in use`, {
        description: `Take its include out of ${siteNames(list.usedBy)} before deleting it.`,
      })
      return
    }
    confirm({
      title: `Delete ${list.name}`,
      confirmLabel: "Delete",
      description: (
        <p>
          No site includes <b>{list.name}</b>, so nothing nginx serves changes. A copy stays beside
          it as <code className="font-mono break-all">{list.name}.conf.bak</code>.
        </p>
      ),
      action: async () => {
        try {
          await del(`/proxy/access-lists/${encodeURIComponent(list.name)}`)
        } finally {
          refresh()
        }
      },
    })
  }

  const lists = data?.lists ?? []
  const sites = new Set(lists.flatMap((list) => list.usedBy.map((use) => use.site))).size

  return (
    <>
      <FormSections>
        <FormSection
          aside
          title="Access lists"
          hint={
            data
              ? `${plural(lists.length, "list")}${sites > 0 ? `, included by ${plural(sites, "site")}` : ""}`
              : "Addresses and passwords sites share"
          }
          actions={
            <Button size="sm" variant="outline" onClick={() => openForm(null)} disabled={!data}>
              <Plus className="size-4" />
              New list
            </Button>
          }
        >
          {loading && !data ? (
            <LoadingRows rows={2} />
          ) : error && !data ? (
            <ErrorState error={error} onRetry={refresh} />
          ) : lists.length === 0 ? (
            <EmptyState
              icon={Shield}
              title="No access lists yet"
              description="A list holds allowed and denied addresses and a password file. Sites take it in with one include line, so an edit changes every site that uses it."
              className="mt-2"
            />
          ) : (
            <>
              {error && (
                <FormNote tone="warning" role="status">
                  Could not read the lists again; these are from the last read.
                </FormNote>
              )}
              <ul aria-label="Access lists" className="animate-rise divide-y divide-hairline">
                {lists.map((list) => (
                  <AccessListRow
                    key={list.name}
                    list={list}
                    clientAddress={data?.clientAddress ?? ""}
                    onEdit={() => openForm(list)}
                    onDelete={() => remove(list)}
                  />
                ))}
              </ul>
            </>
          )}
        </FormSection>
      </FormSections>
      <AccessListDialog
        key={form.session}
        open={form.open}
        onOpenChange={(open) => setForm((f) => ({ ...f, open }))}
        editing={form.editing}
        names={lists.map((list) => list.name)}
        clientAddress={data?.clientAddress ?? ""}
        onSaved={refresh}
        onOutput={setOutput}
      />
      <Modal
        open={output !== null}
        onOpenChange={(open) => !open && setOutput(null)}
        title={`nginx output — ${output?.title ?? ""}`}
        size="lg"
        initialFocus="body"
      >
        <Well className="max-h-[60svh] break-all whitespace-pre-wrap">{output?.text}</Well>
      </Modal>
      {dialog}
    </>
  )
}

/** "app.example.com and docs.example.com", or the first two and a count. */
function siteNames(uses: AccessListUse[]) {
  const names = [...new Set(uses.map((use) => use.site))]
  if (names.length <= 2) return names.join(" and ")
  return `${names.slice(0, 2).join(", ")} and ${plural(names.length - 2, "more site")}`
}

/** How the password and the addresses combine, after the file's name. */
function loginRule(spec: AccessListSpec) {
  if (spec.allow.length > 0) {
    return spec.satisfy === "any" ? "or an allowed address" : "and an allowed address"
  }
  return spec.deny.length > 0 ? "from any address not denied" : "from any address"
}

const SHOWN_ENTRIES = 12

function Entries({ entries }: { entries: string[] }) {
  const more = entries.length - SHOWN_ENTRIES
  return (
    <span className="flex min-w-0 flex-wrap items-center gap-1">
      {entries.slice(0, SHOWN_ENTRIES).map((entry) => (
        <Tag key={entry} mono className="max-w-full break-all whitespace-normal">
          {entry}
        </Tag>
      ))}
      {more > 0 && <span className="text-muted-foreground">and {more} more</span>}
    </span>
  )
}

function AccessListRow({
  list,
  clientAddress,
  onEdit,
  onDelete,
}: {
  list: AccessList
  clientAddress: string
  onEdit: () => void
  onDelete: () => void
}) {
  const facts: { label: string; value: React.ReactNode }[] = []
  if (list.allow.length > 0) {
    facts.push({ label: "Allows", value: <Entries entries={list.allow} /> })
  }
  if (list.deny.length > 0) {
    facts.push({ label: "Denies", value: <Entries entries={list.deny} /> })
  }
  if (list.authFile) {
    facts.push({
      label: "Password",
      value: (
        <span className="flex flex-wrap items-baseline gap-x-1.5">
          <Tag mono>{list.authFile}</Tag>
          <span className="text-muted-foreground">{loginRule(list)}</span>
        </span>
      ),
    })
  }
  facts.push({
    label: "Used by",
    value:
      list.usedBy.length === 0 ? (
        <span className="text-muted-foreground">No site yet</span>
      ) : (
        <span className="flex flex-wrap gap-x-3 gap-y-0.5">
          {list.usedBy.map((use) => (
            <span key={`${use.site}:${use.path}`} className="min-w-0 break-all">
              <Link
                href={`/proxy/sites?site=${encodeURIComponent(use.site)}`}
                className="rounded-sm font-medium underline-offset-2 focus-ring hover:underline"
              >
                {use.site}
              </Link>
              {!use.enabled && <span className="text-muted-foreground"> · disabled</span>}
            </span>
          ))}
        </span>
      ),
  })
  const refusesYou = accessFor(list, clientAddress) === "refused"

  return (
    <li
      className={cn(
        "flex min-w-0 items-start gap-3 py-3 transition-colors hover:bg-row-hover",
        ROW_BLEED,
      )}
    >
      <div className="min-w-0 flex-1 space-y-2">
        <div className="flex min-w-0 flex-wrap items-baseline gap-x-2">
          <span className="text-body font-medium">{list.name}</span>
          <span className="truncate font-mono text-hint text-muted-foreground">{list.path}</span>
        </div>
        <dl className="grid grid-cols-[4.5rem_minmax(0,1fr)] items-baseline gap-x-3 gap-y-1.5 text-hint">
          {facts.map((fact) => (
            <Fragment key={fact.label}>
              <dt className="text-muted-foreground">{fact.label}</dt>
              <dd className="min-w-0">{fact.value}</dd>
            </Fragment>
          ))}
        </dl>
        {refusesYou && (
          <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
            <Status tone="warning" label="Refuses you" />
            <span className="text-hint break-all text-muted-foreground">
              The dashboard sees you at {clientAddress}.
            </span>
          </div>
        )}
        {list.authFileMissing && (
          <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
            <Status tone="danger" label="Password file gone" />
            <span className="text-hint text-muted-foreground">
              Every login is refused until the list names another.
            </span>
          </div>
        )}
        {list.handWritten && (
          <FormNote>
            Written by hand: {list.handWritten}. Saving it from the form rewrites it.
          </FormNote>
        )}
        <div className="flex min-w-0 items-center gap-1">
          <code className="min-w-0 truncate font-mono text-hint text-muted-foreground">
            {list.include}
          </code>
          <IconAction
            label={`Copy the include line for ${list.name}`}
            size="icon-xs"
            className="text-muted-foreground"
            onClick={() => void copyText(list.include, "Include line copied")}
          >
            <Copy />
          </IconAction>
        </div>
      </div>
      <VerbActions
        dim
        className="shrink-0"
        menuLabel={`More actions for ${list.name}`}
        verbs={[
          { key: "edit", label: `Edit ${list.name}`, icon: Pencil, inline: true, run: onEdit },
          { key: "delete", label: "Delete", icon: Trash, danger: true, run: onDelete },
        ]}
      />
    </li>
  )
}

const NAME = /^[a-z0-9][a-z0-9_-]{0,62}$/
const NO_FILE = "__none__"

type Side = "allow" | "deny"

/**
 * The entries a side holds once what is typed in its box is added: every
 * address in the box, split on spaces and commas, so a pasted range list
 * goes in whole. Anything the server would refuse, or a repeat, is the error.
 */
function entriesOf(values: string[], draft: string): { entries: string[] } | { error: string } {
  const entries = [...values]
  for (const entry of draft.split(/[\s,]+/).filter(Boolean)) {
    const error = checkEntry(entry)
    if (error) return { error }
    if (entries.some((value) => sameEntry(value, entry))) {
      return { error: `${entry} is already on the list` }
    }
    entries.push(entry)
  }
  return { entries }
}

function AccessListDialog({
  open,
  onOpenChange,
  editing,
  names,
  clientAddress,
  onSaved,
  onOutput,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  editing: AccessList | null
  /** The lists there are, so a new one is not given a taken name. */
  names: string[]
  clientAddress: string
  onSaved: () => void
  onOutput: (output: { title: string; text: string }) => void
}) {
  const [name, setName] = useState(editing?.name ?? "")
  const [lists, setLists] = useState<Record<Side, string[]>>({
    allow: editing?.allow ?? [],
    deny: editing?.deny ?? [],
  })
  const [drafts, setDrafts] = useState<Record<Side, string>>({ allow: "", deny: "" })
  const [errors, setErrors] = useState<Partial<Record<Side, string>>>({})
  const [authFile, setAuthFile] = useState(editing?.authFile ?? "")
  const [realm, setRealm] = useState(editing?.realm ?? "")
  const [satisfy, setSatisfy] = useState<"all" | "any">(editing?.satisfy ?? "all")
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<{ message: string; raw?: string } | null>(null)
  const files = usePoll<AuthFile[]>(
    (signal) => get("/proxy/auth-files/", undefined, signal),
    0,
    [],
    { enabled: open },
  )

  const add = (side: Side) => {
    const result = entriesOf(lists[side], drafts[side])
    if ("error" in result) {
      setErrors((e) => ({ ...e, [side]: result.error }))
      return
    }
    setLists((l) => ({ ...l, [side]: result.entries }))
    setDrafts((d) => ({ ...d, [side]: "" }))
  }
  const remove = (side: Side, index: number) =>
    setLists((l) => ({ ...l, [side]: l[side].filter((_, i) => i !== index) }))
  const type = (side: Side, text: string) => {
    setDrafts((d) => ({ ...d, [side]: text }))
    setErrors((e) => ({ ...e, [side]: undefined }))
  }

  // What would be saved: the entries, with whatever is typed and valid.
  const typed = (side: Side) => {
    const result = entriesOf(lists[side], drafts[side])
    return "entries" in result ? result.entries : lists[side]
  }
  const allow = typed("allow")
  const deny = typed("deny")
  const either = satisfy === "any" && allow.length > 0 && Boolean(authFile)
  const spec: AccessListSpec = {
    allow,
    deny,
    authFile: authFile || undefined,
    realm: authFile && realm.trim() ? realm.trim() : undefined,
    satisfy: either ? "any" : "all",
  }
  const verdict = accessFor(spec, clientAddress)

  const trimmed = name.trim()
  const nameError = editing
    ? undefined
    : trimmed && !NAME.test(trimmed)
      ? "Lowercase letters, digits, dashes or underscores, starting with a letter or digit."
      : names.includes(trimmed)
        ? `There is already a list called ${trimmed}.`
        : undefined
  const fileNames = files.data?.map((file) => file.name) ?? []
  const fileGone = Boolean(authFile) && files.data !== undefined && !fileNames.includes(authFile)
  const empty = allow.length === 0 && deny.length === 0 && !authFile
  const ready = !busy && (editing || (trimmed && !nameError)) && !empty && !fileGone
  const enabledUses = editing?.usedBy.filter((use) => use.enabled) ?? []
  const target = editing?.name ?? trimmed

  const save = async () => {
    const allowed = entriesOf(lists.allow, drafts.allow)
    const denied = entriesOf(lists.deny, drafts.deny)
    if ("error" in allowed || "error" in denied) {
      setErrors({
        allow: "error" in allowed ? allowed.error : undefined,
        deny: "error" in denied ? denied.error : undefined,
      })
      return
    }
    setLists({ allow: allowed.entries, deny: denied.entries })
    setDrafts({ allow: "", deny: "" })
    setRefusal(null)
    setBusy(true)
    try {
      const res = await put<AccessListSaved>(`/proxy/access-lists/${encodeURIComponent(target)}`, {
        ...spec,
        allow: allowed.entries,
        deny: denied.entries,
        overwrite: Boolean(editing),
      })
      const live = res.list.usedBy.filter((use) => use.enabled)
      if (res.reloadError) {
        const text = res.reload?.output || res.reload?.validation?.output
        notify.warning(`${target} saved, not reloaded`, {
          description: `If nginx is running, its sites keep the previous rules until a reload succeeds. ${res.reloadError}`,
          duration: 12_000,
          action: text
            ? { label: "Show nginx output", onClick: () => onOutput({ title: target, text }) }
            : undefined,
        })
      } else if (live.length > 0) {
        notify.success(`${target} saved`, { description: `Live on ${siteNames(live)}.` })
      } else {
        notify.success(`${target} saved`, {
          description:
            res.list.usedBy.length > 0
              ? "Only disabled sites include it."
              : "No site includes it yet: paste its include line into a site.",
        })
      }
      onSaved()
      onOpenChange(false)
    } catch (err) {
      notify.error(`${target || "The list"} not saved`, err)
      // Kept in the form rather than behind a toast's button, which the open
      // dialog would not let anybody press.
      if (err instanceof ApiError && err.code === "invalid_config") {
        setRefusal({ message: err.message, raw: err.raw })
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={(next) => !busy && onOpenChange(next)}
      size="md"
      title={editing ? `Edit ${editing.name}` : "New access list"}
      description="Addresses to allow and deny, and a password file, for every site that includes the list."
      footer={
        <>
          <span className="mr-auto text-hint text-muted-foreground">
            {empty
              ? "Add an address or a password file."
              : enabledUses.length > 0
                ? `Saving reloads nginx for ${siteNames(enabledUses)}.`
                : editing
                  ? "No enabled site includes it."
                  : "No site uses it until one includes it."}
          </span>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={save} disabled={!ready} pending={busy}>
            Save
          </Button>
        </>
      }
    >
      <div className="grid gap-5">
        {editing?.handWritten && (
          <Notice tone="warning" icon={Warning} title="Written by hand">
            <p>
              {editing.handWritten[0].toUpperCase() + editing.handWritten.slice(1)}. Saving replaces
              the file with what this form shows.
            </p>
          </Notice>
        )}
        {!editing && (
          <Field
            label="Name"
            htmlFor="access-name"
            hint="Sites include the list by this name, so it cannot change later."
            error={nameError}
          >
            <Input
              id="access-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="office"
              className="font-mono text-xs"
              autoComplete="off"
              aria-invalid={Boolean(nameError)}
            />
          </Field>
        )}
        <AddressField
          id="access-allow"
          label="Allow only these"
          hint="Filling this in refuses every other address."
          placeholder="10.0.0.0/8"
          values={lists.allow}
          draft={drafts.allow}
          error={errors.allow}
          onDraft={(text) => type("allow", text)}
          onAdd={() => add("allow")}
          onRemove={(i) => remove("allow", i)}
        />
        <AddressField
          id="access-deny"
          label="Deny"
          hint="Checked first: refused even inside an allowed range."
          placeholder="203.0.113.7"
          values={lists.deny}
          draft={drafts.deny}
          error={errors.deny}
          onDraft={(text) => type("deny", text)}
          onAdd={() => add("deny")}
          onRemove={(i) => remove("deny", i)}
        />
        <Field
          label="Password file"
          htmlFor="access-auth"
          hint={
            files.loading
              ? "Reading the password files…"
              : files.error
                ? "The password files could not be read."
                : fileNames.length === 0
                  ? "No password files yet. Add a login under Password files first."
                  : authFile
                    ? "Asks for a login from this file."
                    : "None: the addresses alone decide."
          }
          error={fileGone ? `${authFile} is gone. Choose another file, or none.` : undefined}
        >
          <Select
            value={authFile || NO_FILE}
            onValueChange={(v) => setAuthFile(v === NO_FILE ? "" : v)}
          >
            <SelectTrigger id="access-auth" className="w-full font-mono text-xs">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={NO_FILE}>None</SelectItem>
              {fileNames.map((file) => (
                <SelectItem key={file} value={file}>
                  {file}
                </SelectItem>
              ))}
              {authFile && !fileNames.includes(authFile) && (
                <SelectItem value={authFile} disabled>
                  {authFile}
                </SelectItem>
              )}
            </SelectContent>
          </Select>
        </Field>
        {authFile && (
          <Field
            label="Login prompt"
            htmlFor="access-realm"
            hint="What the browser's sign-in box says."
          >
            <Input
              id="access-realm"
              value={realm}
              onChange={(e) => setRealm(e.target.value)}
              placeholder="Restricted"
              maxLength={100}
            />
          </Field>
        )}
        {authFile && allow.length > 0 && (
          <Field
            label="Let in"
            hint={
              satisfy === "any"
                ? "An allowed address gets in without the password, and the password gets in from anywhere, even to paths a site refuses everyone, such as dotfiles."
                : "A visitor needs an allowed address and the password."
            }
          >
            <Segments
              label="Let in"
              value={satisfy}
              onChange={setSatisfy}
              fill
              options={[
                { value: "all", label: "Address and password" },
                { value: "any", label: "Either one" },
              ]}
            />
          </Field>
        )}
        {verdict === "refused" && !empty && (
          <Notice tone="warning" icon={Warning} title="This list refuses you">
            <p>
              The dashboard sees you at <span className="font-mono break-all">{clientAddress}</span>
              . If you reach{" "}
              {enabledUses.length > 0 ? siteNames(enabledUses) : "the sites that include it"} the
              same way, saving locks you out of them.
            </p>
          </Notice>
        )}
        {verdict === "password" && (
          <FormNote>
            From <span className="font-mono break-all">{clientAddress}</span>, where the dashboard
            sees you, you would sign in with a login from {authFile}.
          </FormNote>
        )}
        {refusal && (
          <div role="alert">
            <Notice tone="danger" icon={Warning} title="nginx refused it">
              <p className="break-words">{refusal.message}</p>
              <p>{editing ? "The list is as it was." : "Nothing was written."}</p>
              {refusal.raw && (
                <Disclosure quiet summary="nginx output">
                  <Well className="max-h-60 break-all whitespace-pre-wrap">{refusal.raw}</Well>
                </Disclosure>
              )}
            </Notice>
          </div>
        )}
      </div>
    </Modal>
  )
}

function AddressField({
  id,
  label,
  hint,
  placeholder,
  values,
  draft,
  error,
  onDraft,
  onAdd,
  onRemove,
}: {
  id: string
  label: string
  hint: string
  placeholder: string
  values: string[]
  draft: string
  error?: string
  onDraft: (text: string) => void
  onAdd: () => void
  onRemove: (index: number) => void
}) {
  return (
    <Field label={label} htmlFor={id} hint={hint} error={error}>
      {/* The box before the entries: a dialog opening on an entry's remove
          button would open its tooltip, and Escape would close that rather
          than the dialog. */}
      <div className="space-y-2">
        <div className="flex gap-2">
          <Input
            id={id}
            value={draft}
            onChange={(e) => onDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault()
                onAdd()
              }
            }}
            placeholder={placeholder}
            className="font-mono text-xs"
            autoComplete="off"
            aria-invalid={Boolean(error)}
          />
          <Button
            size="sm"
            variant="outline"
            onClick={onAdd}
            disabled={!draft.trim()}
            aria-label={`Add to ${label.toLowerCase()}`}
          >
            <Plus className="size-3.5" />
          </Button>
        </div>
        {values.length > 0 && (
          <div className="flex flex-wrap items-center gap-1.5">
            {values.map((value, i) => (
              <Tag key={value} mono className="max-w-full gap-1.5 pr-0.5">
                <span className="min-w-0 truncate">{value}</span>
                <IconAction
                  label={`Remove ${value}`}
                  size="icon-xs"
                  className="size-4 text-muted-foreground hover:text-destructive [&_svg:not([class*='size-'])]:size-2.5"
                  onClick={() => onRemove(i)}
                >
                  <Trash />
                </IconAction>
              </Tag>
            ))}
          </div>
        )}
      </div>
    </Field>
  )
}
