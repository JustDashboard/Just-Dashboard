"use client"

import { useState } from "react"
import Link from "next/link"
import { Copy, Key, Pencil, Plus, Shield, Trash, Warning } from "@/components/icons"
import { notify } from "@/lib/toast"
import { ApiError, del, get, put } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { plural } from "@/lib/format"
import type { AuthFile } from "@/lib/types"
import type {
  AccessList,
  AccessListSaved,
  AccessListSpec,
  AccessListUse,
  AccessLists,
  AccessSource,
} from "@/lib/proxy/types-access-lists"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { Segments } from "@/components/deploy/settings/segments"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Disclosure, Field, FormFact, FormFacts, FormNote, FormSection } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Modal } from "@/components/modal"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ProductLogo } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { VerbMenu } from "@/components/verbs"
import { siteSettingsHref } from "@/components/proxy/site-tabs"
import {
  accessFor,
  accessForRun,
  checkEntry,
  checkOverlap,
  checkRealm,
  sameEntry,
} from "@/components/proxy/access-lists"
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
 *
 * A list is one line: its name over where it is, what it allows and denies
 * as counts, its password file, how many sites include it, and the one thing
 * wrong with it. What it holds — every address, the sites, its include line —
 * is in the sheet the row opens, which is also where it is edited. Each list
 * was a block of five to eight lines until 0.8.7.
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

  return (
    <>
      {/* An access explanation names a shared list as its owner and links here. */}
      <Panel plain id="access-lists" aria-label="Access lists" className="scroll-mt-6">
        <PanelHeader
          title="Access lists"
          actions={
            <>
              {lists.length > 0 && (
                <span className="numeric text-hint text-muted-foreground">
                  {plural(lists.length, "list")}
                </span>
              )}
              <Button size="xs" variant="outline" onClick={() => openForm(null)} disabled={!data}>
                <Plus className="size-3.5" />
                New list
              </Button>
            </>
          }
        />
        <PanelBody flush className="pt-3">
          {loading && !data ? (
            <LoadingRows rows={2} />
          ) : error && !data ? (
            <ErrorState error={error} onRetry={refresh} />
          ) : lists.length === 0 ? (
            <EmptyState
              icon={Shield}
              title="No access lists yet"
              description="A list holds addresses and a password file that sites share with one include line."
            />
          ) : (
            <div className="space-y-2">
              {error && (
                <FormNote tone="warning" role="status">
                  Could not read the lists again; these are from the last read.
                </FormNote>
              )}
              <ChoiceList aria-label="Access lists" className="animate-rise">
                {lists.map((list) => (
                  <AccessListRow
                    key={list.name}
                    list={list}
                    clientAddress={data?.clientAddress ?? ""}
                    onOpen={() => openForm(list)}
                    onDelete={() => remove(list)}
                  />
                ))}
              </ChoiceList>
            </div>
          )}
        </PanelBody>
      </Panel>
      <AccessListSheet
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

function sourceName(source: AccessSource, site: string) {
  return source.list ?? `${site}'s own rules`
}

function closesWithDenyAll(source: AccessSource) {
  return source.rules.some((rule) => rule.deny && rule.address === "all")
}

/**
 * Where nginx never reaches a list's addresses, or the rules after it, in a
 * site that reads it beside other rules. Each list judged on its own reads
 * as letting its addresses in; in one run the first deny all ends the check.
 */
function runClashes(name: string, uses: AccessListUse[]) {
  const out: string[] = []
  for (const use of uses) {
    for (const run of use.runs ?? []) {
      const at = run.sources.findIndex((source) => source.list === name)
      if (at < 0 || run.sources.length < 2) continue
      const own = run.sources[at]
      const where = run.block === "server" || run.block === "http" ? "" : ` in ${run.block}`
      const before = run.sources.slice(0, at).filter(closesWithDenyAll)
      if (own.rules.length > 0 && before.length > 0) {
        const first = sourceName(before[0], use.site)
        out.push(
          `${use.site} reads ${first} first${where}; its deny all ends the check before ${name}'s addresses.`,
        )
      }
      const after = run.sources.slice(at + 1).filter((source) => source.rules.length > 0)
      if (closesWithDenyAll(own) && after.length > 0) {
        const names = after.map((source) => sourceName(source, use.site)).join(" and ")
        out.push(
          `${use.site} reads ${names} after ${name}${where}; ${name}'s deny all ends the check before them.`,
        )
      }
    }
  }
  return out
}

/** The sites whose blocks, with everything read beside the list, refuse the address. */
function sitesRefusing(uses: AccessListUse[], address: string) {
  return uses.filter((use) =>
    (use.runs ?? []).some((run) => accessForRun(run, address) === "refused"),
  )
}

/**
 * The one thing most wrong with a list, for the end of its row: a password
 * file that is gone refuses every login; a list that refuses the reader locks
 * them out of the sites that include it; rules another list ends before they
 * are read do nothing. A list written by hand, or kept elsewhere, says so.
 */
function listState(list: AccessList, clientAddress: string) {
  if (list.authFileMissing) return <Status tone="danger" label="Password file gone" />
  if (
    accessFor(list, clientAddress) === "refused" ||
    sitesRefusing(list.usedBy, clientAddress).length > 0
  ) {
    return <Status tone="warning" label="Refuses you" />
  }
  if (runClashes(list.name, list.usedBy).length > 0) {
    return <Status tone="warning" label="Rules never read" />
  }
  if (list.link) return <Status tone="stopped" label="Kept elsewhere" />
  if (list.handWritten) return <Status tone="stopped" label="Written by hand" />
  return null
}

/** What a list lets in and keeps out, as counts: the addresses are in its sheet. */
function ruleWords(list: AccessListSpec) {
  const parts = [
    list.allow.length > 0 && `${list.allow.length} allowed`,
    list.deny.length > 0 && `${list.deny.length} denied`,
  ].filter(Boolean)
  return parts.join(" · ") || "no addresses"
}

function AccessListRow({
  list,
  clientAddress,
  onOpen,
  onDelete,
}: {
  list: AccessList
  clientAddress: string
  onOpen: () => void
  onDelete: () => void
}) {
  const sites = new Set(list.usedBy.map((use) => use.site)).size
  return (
    <ChoiceRow
      onSelect={onOpen}
      verb={`Open ${list.name}`}
      leading={<ProductLogo id={undefined} size="sm" fallback={Shield} />}
      title={list.name}
      description={<span className="font-mono">{list.path}</span>}
      trailing={
        <>
          <span className="numeric w-36 truncate text-hint text-muted-foreground max-lg:hidden">
            {ruleWords(list)}
          </span>
          <span
            className="flex w-28 min-w-0 items-center gap-1.5 text-hint text-muted-foreground max-md:hidden"
            title={list.authFile ? `${list.authFile} ${loginRule(list)}` : undefined}
          >
            {list.authFile && (
              <>
                <Key aria-hidden className="size-3 shrink-0" />
                <span className="truncate font-mono">{list.authFile}</span>
              </>
            )}
          </span>
          <span className="numeric w-16 text-hint text-muted-foreground max-sm:hidden">
            {sites === 0 ? "unused" : plural(sites, "site")}
          </span>
          <span className="flex justify-end sm:w-40">{listState(list, clientAddress)}</span>
        </>
      }
      actions={
        <VerbMenu
          label={`More actions for ${list.name}`}
          verbs={[
            {
              key: "edit",
              label: "Edit",
              icon: Pencil,
              run: onOpen,
              disabled: Boolean(list.link),
            },
            {
              key: "include",
              label: "Copy include line",
              icon: Copy,
              run: () => void copyText(list.include, "Include line copied"),
            },
            {
              key: "delete",
              label: "Delete",
              icon: Trash,
              danger: true,
              run: onDelete,
              disabled: Boolean(list.link),
            },
          ]}
        />
      }
    />
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

/**
 * A list, in the sheet its row opens: for one that exists, where it is, its
 * include line, the sites that include it and what is wrong with it, over
 * the fields that change it; for a new one, the fields alone. A list kept as
 * a link to a file elsewhere is shown as it is and changed on the server.
 */
function AccessListSheet({
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
    const next = { ...lists, [side]: result.entries }
    const overlap = checkOverlap(next.allow, next.deny)
    if (overlap) {
      setErrors((e) => ({ ...e, [side]: overlap }))
      return
    }
    setLists(next)
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
  const realmError = authFile ? (checkRealm(realm) ?? undefined) : undefined
  const ready = !busy && (editing || (trimmed && !nameError)) && !empty && !fileGone && !realmError
  const clashes = editing ? runClashes(editing.name, editing.usedBy) : []
  const enabledUses = editing?.usedBy.filter((use) => use.enabled) ?? []
  const target = editing?.name ?? trimmed
  // A list that is a link to a file elsewhere is read here and changed there.
  const locked = Boolean(editing?.link)
  // The sites that refuse the reader with the rules read beside the list,
  // which the list judged on its own does not say.
  const refusing = editing ? sitesRefusing(editing.usedBy, clientAddress) : []

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
    const overlap = checkOverlap(allowed.entries, denied.entries)
    if (overlap) {
      setErrors({ allow: overlap })
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
    <SidePanel
      open={open}
      onOpenChange={(next) => !busy && onOpenChange(next)}
      width="sm"
      // A list that exists opens on what it is; a new one on its name.
      initialFocus={editing ? "body" : "first"}
      title={
        <>
          <ProductLogo id={undefined} size="sm" fallback={Shield} />
          {editing ? `Edit ${editing.name}` : "New access list"}
        </>
      }
      description="Addresses to allow and deny, and a password file, for every site that includes the list."
      footer={
        <>
          <span className="mr-auto min-w-0 flex-1 basis-40 text-hint text-muted-foreground">
            {locked
              ? "Changed on the server, where the file is."
              : empty
                ? "Add an address or a password file."
                : enabledUses.length > 0
                  ? `Saving reloads nginx for ${siteNames(enabledUses)}.`
                  : editing
                    ? "No enabled site includes it."
                    : "No site uses it until one includes it."}
          </span>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            {locked ? "Close" : "Cancel"}
          </Button>
          {!locked && (
            <Button onClick={save} disabled={!ready} pending={busy}>
              Save
            </Button>
          )}
        </>
      }
    >
      <fieldset disabled={locked} className="grid min-w-0 gap-5">
        {editing && (
          <div className="space-y-3">
            <FormFacts>
              <FormFact label="File" mono>
                {editing.path}
              </FormFact>
            </FormFacts>
            <div className="flex min-w-0 items-center gap-1">
              <code className="min-w-0 truncate font-mono text-hint text-muted-foreground">
                {editing.include}
              </code>
              <IconAction
                label={`Copy the include line for ${editing.name}`}
                size="icon-xs"
                className="text-muted-foreground"
                onClick={() => void copyText(editing.include, "Include line copied")}
              >
                <Copy />
              </IconAction>
            </div>
          </div>
        )}
        {editing && (
          <FormSection title="Used by">
            {editing.usedBy.length === 0 ? (
              <p className="text-body text-muted-foreground">No site yet</p>
            ) : (
              <ul aria-label={`Sites using ${editing.name}`} className="divide-y divide-hairline">
                {editing.usedBy.map((use) => (
                  <li
                    key={`${use.site}:${use.path}`}
                    className="flex min-h-9 min-w-0 items-center gap-2 text-body"
                  >
                    <Link
                      href={siteSettingsHref(use.site, "access")}
                      className="min-w-0 truncate rounded-sm font-mono focus-ring hover:underline"
                    >
                      {use.site}
                    </Link>
                    {!use.enabled && (
                      <span className="text-hint text-muted-foreground">disabled</span>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </FormSection>
        )}
        {editing?.authFileMissing && (
          <Notice tone="danger" icon={Warning} title="Password file gone">
            <p>Every login is refused until the list names another.</p>
          </Notice>
        )}
        {refusing.length > 0 && verdict !== "refused" && (
          <Notice tone="warning" icon={Warning} title="Refuses you">
            <p>
              The dashboard sees you at <span className="font-mono break-all">{clientAddress}</span>
              , which {siteNames(refusing)} {refusing.length === 1 ? "refuses" : "refuse"} with the
              rules read beside the list.
            </p>
          </Notice>
        )}
        {editing?.link ? (
          <Notice icon={Warning} title="Kept elsewhere">
            <p>Not changed here: {editing.handWritten}. Change it on the server.</p>
          </Notice>
        ) : (
          editing?.handWritten && (
            <Notice tone="warning" icon={Warning} title="Written by hand">
              <p>
                {editing.handWritten[0].toUpperCase() + editing.handWritten.slice(1)}. Saving
                replaces the file with what this form shows.
              </p>
            </Notice>
          )
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
            error={realmError}
          >
            <Input
              id="access-realm"
              value={realm}
              onChange={(e) => setRealm(e.target.value)}
              placeholder="Restricted"
              aria-invalid={Boolean(realmError)}
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
        {clashes.length > 0 && (
          <Notice tone="warning" icon={Warning} title="Read beside other rules">
            {clashes.map((clash) => (
              <p key={clash}>{clash}</p>
            ))}
            <p>nginx stops at the first rule an address matches, across every list in a block.</p>
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
      </fieldset>
    </SidePanel>
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
