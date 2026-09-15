"use client"

import { useCallback, useState } from "react"
import {
  Clock,
  Copy,
  FloppyDisk,
  Key,
  MagnifyingGlass,
  Pencil,
  Plus,
  Trash,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, get, post } from "@/lib/api"
import { bytes } from "@/lib/format"
import { cn, ringSafeScroll } from "@/lib/utils"
import type { DbConnection, RedisPage, RedisValue } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { useConfirm } from "@/components/confirm-dialog"
import { IconAction, RowActions } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"
import { EmptyNote, EmptyState, LoadingRows, Spinner } from "@/components/state"
import { Tag } from "@/components/tag"
import { Modal } from "@/components/modal"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { copyText } from "@/lib/clipboard"

type ConfirmFn = ReturnType<typeof useConfirm>["confirm"]

/**
 * Redis in its own vocabulary.
 *
 * The temptation is to render keys as a table and call it a data grid, which is
 * how most panels bolt Redis on and why none of them are pleasant to use: a key
 * has a type, a TTL and a shape that changes what "edit" even means. So this is
 * a key list beside a typed value editor, and the pattern box is a real SCAN
 * pattern rather than a client-side filter over one page — on a keyspace with
 * millions of keys, filtering what you happened to fetch is not searching.
 *
 * Paging is a cursor, not an offset, because SCAN is. The cursor stack is what
 * makes "previous" possible at all: SCAN can only go forward, so going back
 * means remembering where you have been.
 */
export function RedisBrowser({ conn, confirm }: { conn: DbConnection; confirm: ConfirmFn }) {
  const { can } = useAuth()
  const [db, setDb] = useState("0")
  const [pattern, setPattern] = useState("*")
  const [applied, setApplied] = useState("*")
  const [cursor, setCursor] = useState("0")
  const [history, setHistory] = useState<string[]>([])
  const [selected, setSelected] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [renaming, setRenaming] = useState<string | null>(null)

  const databases = usePoll(
    (signal) =>
      get<{ name: string; size: number }[]>(`/databases/${conn.id}/schemas`, undefined, signal),
    0,
    [conn.id],
  )
  const page = usePoll(
    (signal) =>
      get<RedisPage>(
        `/databases/${conn.id}/keys`,
        { db, pattern: applied, cursor, count: 100 },
        signal,
      ),
    0,
    [conn.id, db, applied, cursor],
  )
  const value = usePoll(
    (signal) =>
      selected
        ? get<RedisValue>(`/databases/${conn.id}/keys/value`, { db, key: selected }, signal)
        : Promise.resolve(null as unknown as RedisValue),
    0,
    [conn.id, db, selected],
  )

  const search = () => {
    setCursor("0")
    setHistory([])
    setApplied(pattern.trim() || "*")
  }
  const next = () => {
    if (!page.data || page.data.done) return
    setHistory((h) => [...h, cursor])
    setCursor(page.data.cursor)
  }
  const prev = () => {
    setHistory((h) => {
      if (h.length === 0) return h
      setCursor(h[h.length - 1])
      return h.slice(0, -1)
    })
  }

  const canWrite = can("service.control")
  const reload = useCallback(() => {
    page.refresh()
    value.refresh()
  }, [page, value])

  const renameKey = async (from: string, to: string) => {
    await post(`/databases/${conn.id}/keys/rename`, { key: from, to }, { query: { db } })
    notify.success(`Renamed to ${to}`)
    setRenaming(null)
    // Follow the key rather than keeping a name the server no longer has.
    setSelected(to)
    page.refresh()
  }

  const createKey = async (key: string, type: string, field: string, val: string, ttl: number) => {
    await post(
      `/databases/${conn.id}/keys/value`,
      { key, type, field, value: val, ttl, create: true },
      { query: { db } },
    )
    notify.success(`Created ${key}`)
    setCreating(false)
    setSelected(key)
    page.refresh()
  }

  const deleteKey = (key: string) =>
    confirm({
      title: "Delete key",
      confirmLabel: "Delete",
      description: (
        <p>
          Permanently removes <span className="font-mono text-xs">{key}</span> and its value.
        </p>
      ),
      action: async (c) => {
        await del(`/databases/${conn.id}/keys`, { body: { key }, confirm: c, query: { db } })
        notify.success("Key deleted")
        setSelected(null)
        page.refresh()
      },
    })

  return (
    <div className="grid gap-4 lg:grid-cols-[22rem_minmax(0,1fr)] [&>*]:min-w-0">
      <Panel>
        <PanelHeader
          title="Keys"
          actions={
            <>
              {canWrite && (
                <Button size="sm" variant="outline" onClick={() => setCreating(true)}>
                  <Plus className="size-3.5" />
                  New key
                </Button>
              )}
              {databases.data && databases.data.length > 1 ? (
                <Select
                  value={db}
                  onValueChange={(v) => {
                    setDb(v)
                    setCursor("0")
                    setHistory([])
                    setSelected(null)
                  }}
                >
                  {/* Wide enough for "db15 · 12345": at w-24 the count was
                    clipped mid-digit, which reads as a rendering fault rather
                    than as a number that did not fit. */}
                  <SelectTrigger size="sm" className="w-32">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {databases.data.map((d) => (
                      <SelectItem key={d.name} value={d.name}>
                        db{d.name}
                        {d.size > 0 && ` · ${d.size}`}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              ) : null}
            </>
          }
        />
        <PanelBody className="space-y-3">
          <div className="flex items-center gap-1.5">
            <Input
              value={pattern}
              onChange={(e) => setPattern(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && search()}
              className="h-8 font-mono text-xs"
              placeholder="user:*"
            />
            <Button size="sm" variant="outline" onClick={search}>
              <MagnifyingGlass className="size-3.5" />
            </Button>
          </div>
          <p className="text-hint text-muted-foreground">
            A glob pattern, matched by the server with SCAN — not a filter over this page.
          </p>

          <div
            className={cn("max-h-[calc(100svh-30rem)] space-y-0.5 overflow-y-auto", ringSafeScroll)}
          >
            {page.loading && <LoadingRows rows={5} />}
            {page.data?.keys.length === 0 && <EmptyNote>No keys match this pattern.</EmptyNote>}
            {page.data?.keys.map((k) => (
              <button
                key={k.key}
                onClick={() => setSelected(k.key)}
                className={cn(
                  "flex w-full min-w-0 flex-col rounded-md px-2 py-1.5 text-left transition-colors",
                  selected === k.key
                    ? "bg-plot-primary font-medium text-foreground"
                    : "hover:bg-accent",
                )}
              >
                <span className="truncate font-mono text-xs">{k.key}</span>
                <span className="truncate text-micro text-muted-foreground">
                  {k.type} · {k.size.toLocaleString()}
                  {k.ttl >= 0 && ` · ttl ${k.ttl}s`}
                </span>
              </button>
            ))}
          </div>
        </PanelBody>
        <PanelFooter>
          <Button size="sm" variant="outline" disabled={history.length === 0} onClick={prev}>
            Previous
          </Button>
          <Button size="sm" variant="outline" disabled={page.data?.done} onClick={next}>
            Next
          </Button>
          {page.data?.done && <span className="text-hint text-muted-foreground">End of scan</span>}
        </PanelFooter>
      </Panel>

      <Panel>
        <PanelHeader
          title={selected ?? "Pick a key"}
          actions={
            selected &&
            canWrite && (
              <>
                <Button size="sm" variant="ghost" onClick={() => setRenaming(selected)}>
                  <Pencil className="size-3.5" />
                  Rename
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  className="text-destructive"
                  onClick={() => deleteKey(selected)}
                >
                  <Trash className="size-3.5" />
                  Delete
                </Button>
              </>
            )
          }
        />
        <PanelBody flush>
          {!selected && <EmptyState icon={Key} title="Select a key to see its value" />}
          {selected && value.loading && (
            <div className="flex items-center gap-2 p-6 text-sm text-muted-foreground">
              <Spinner /> Reading…
            </div>
          )}
          {value.data && (
            <RedisValueView
              // Remounting on the key is what resets the editor's draft. Syncing
              // it in an effect instead left a window where the previous key's
              // text was on screen under the new key's name — and one Save away
              // from being written to the wrong key.
              key={value.data.key}
              conn={conn}
              db={db}
              value={value.data}
              canWrite={canWrite}
              confirm={confirm}
              onChanged={reload}
            />
          )}
        </PanelBody>
      </Panel>

      {creating && <NewKeyDialog onClose={() => setCreating(false)} onCreate={createKey} />}
      {renaming && (
        <RenameKeyDialog
          from={renaming}
          onClose={() => setRenaming(null)}
          onRename={(to) => renameKey(renaming, to)}
        />
      )}
    </div>
  )
}

/**
 * Creating a key, and renaming one.
 *
 * Both are things the server has always been able to do — `create: true` on the
 * set route and the rename route beside it, each with live coverage — and
 * neither had any way to be asked for from here. A browser that can only edit
 * keys something else created is not a browser you can keep a database in.
 *
 * A new key needs its type up front because Redis has no untyped key: the type
 * is fixed by the command that creates it, which is why this is a dialog rather
 * than an empty row in the list.
 */
function NewKeyDialog({
  onClose,
  onCreate,
}: {
  onClose: () => void
  onCreate: (key: string, type: string, field: string, value: string, ttl: number) => Promise<void>
}) {
  const [key, setKey] = useState("")
  const [type, setType] = useState("string")
  const [field, setField] = useState("")
  const [value, setValue] = useState("")
  const [ttl, setTtl] = useState("-1")
  const [busy, setBusy] = useState(false)
  // A hash needs a field name and a sorted set needs a score; the others take
  // the value alone. Asking for the right thing beats one box labelled "value"
  // that means something different per type.
  const needsField = type === "hash" || type === "zset"

  const submit = async () => {
    setBusy(true)
    try {
      await onCreate(key.trim(), type, field.trim(), value, Number(ttl) || -1)
    } catch (err) {
      notify.error("Could not create the key", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open
      onOpenChange={onClose}
      title="New key"
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={!key.trim() || busy} onClick={submit} pending={busy}>
            <Plus className="size-3.5" />
            Create
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        <div className="space-y-1.5">
          <Label htmlFor="redis-new-key">Key</Label>
          <Input
            id="redis-new-key"
            value={key}
            onChange={(e) => setKey(e.target.value)}
            className="font-mono text-xs"
            placeholder="app:session:1"
          />
        </div>
        <div className="grid grid-cols-2 gap-3">
          <div className="space-y-1.5">
            <Label>Type</Label>
            <Select value={type} onValueChange={setType}>
              <SelectTrigger size="sm">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {["string", "list", "set", "zset", "hash"].map((t) => (
                  <SelectItem key={t} value={t}>
                    {t}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="redis-new-ttl">TTL seconds</Label>
            <Input
              id="redis-new-ttl"
              value={ttl}
              onChange={(e) => setTtl(e.target.value)}
              className="font-mono text-xs"
            />
          </div>
        </div>
        {needsField && (
          <div className="space-y-1.5">
            <Label htmlFor="redis-new-field">{type === "zset" ? "Score" : "Field"}</Label>
            <Input
              id="redis-new-field"
              value={field}
              onChange={(e) => setField(e.target.value)}
              className="font-mono text-xs"
              placeholder={type === "zset" ? "100" : "name"}
            />
          </div>
        )}
        <div className="space-y-1.5">
          <Label htmlFor="redis-new-value">{type === "zset" ? "Member" : "Value"}</Label>
          <Textarea
            id="redis-new-value"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            className="min-h-24 font-mono text-xs"
          />
        </div>
      </div>
    </Modal>
  )
}

function RenameKeyDialog({
  from,
  onClose,
  onRename,
}: {
  from: string
  onClose: () => void
  onRename: (to: string) => Promise<void>
}) {
  const [to, setTo] = useState(from)
  const [busy, setBusy] = useState(false)

  const submit = async () => {
    setBusy(true)
    try {
      await onRename(to.trim())
    } catch (err) {
      notify.error("Could not rename the key", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open
      onOpenChange={onClose}
      title="Rename key"
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={!to.trim() || to === from || busy} onClick={submit} pending={busy}>
            <FloppyDisk className="size-3.5" />
            Rename
          </Button>
        </>
      }
    >
      <div className="space-y-1.5">
        <Label htmlFor="redis-rename">New name</Label>
        <Input
          id="redis-rename"
          value={to}
          onChange={(e) => setTo(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && to.trim() && to !== from && submit()}
          className="font-mono text-xs"
        />
        <p className="text-hint text-muted-foreground">
          Redis renames in place. An existing key of that name is overwritten.
        </p>
      </div>
    </Modal>
  )
}

function RedisValueView({
  conn,
  db,
  value,
  canWrite,
  confirm,
  onChanged,
}: {
  conn: DbConnection
  db: string
  value: RedisValue
  canWrite: boolean
  confirm: ConfirmFn
  onChanged: () => void
}) {
  const [draft, setDraft] = useState(value.string ?? "")
  const [ttl, setTtl] = useState(String(value.ttl))
  const [busy, setBusy] = useState(false)

  const save = async () => {
    setBusy(true)
    try {
      await post(
        `/databases/${conn.id}/keys/value`,
        { key: value.key, type: "string", value: draft },
        {
          query: { db },
        },
      )
      notify.success("Value saved")
      onChanged()
    } catch (err) {
      notify.error("Could not save", err)
    } finally {
      setBusy(false)
    }
  }

  const saveTtl = async () => {
    setBusy(true)
    try {
      await post(
        `/databases/${conn.id}/keys/expire`,
        { key: value.key, ttl: Number(ttl) || -1 },
        {
          query: { db },
        },
      )
      notify.success(Number(ttl) > 0 ? `Expires in ${ttl}s` : "Expiry cleared")
      onChanged()
    } catch (err) {
      notify.error("Could not set the TTL", err)
    } finally {
      setBusy(false)
    }
  }

  const copy = (text: string) => void copyText(text, "Copied")

  return (
    <div className="space-y-3 p-4">
      <div className="flex flex-wrap items-center gap-2">
        <Tag>{value.type}</Tag>
        <div className="flex items-center gap-1.5">
          <Clock className="size-3.5 text-muted-foreground" />
          <Input
            value={ttl}
            onChange={(e) => setTtl(e.target.value)}
            className="h-7 w-24 font-mono text-xs"
            disabled={!canWrite}
          />
          <span className="text-hint text-muted-foreground">seconds (-1 = never)</span>
          {canWrite && (
            <Button size="sm" variant="outline" onClick={saveTtl} disabled={busy}>
              Set
            </Button>
          )}
        </div>
      </div>

      {value.type === "string" && (
        <div className="space-y-2">
          <Label className="text-xs">Value</Label>
          <Textarea
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            className="h-56 font-mono text-xs"
            readOnly={!canWrite}
          />
          <div className="flex items-center gap-1.5">
            {canWrite && (
              <Button size="sm" onClick={save} pending={busy}>
                <FloppyDisk className="size-3.5" />
                Save
              </Button>
            )}
            <Button size="sm" variant="outline" onClick={() => copy(draft)}>
              <Copy className="size-3.5" />
              Copy
            </Button>
            <span className="text-hint text-muted-foreground">{bytes(draft.length)}</span>
          </div>
        </div>
      )}

      {value.type !== "string" && (
        <MemberEditor
          conn={conn}
          db={db}
          value={value}
          canWrite={canWrite}
          confirm={confirm}
          onChanged={onChanged}
        />
      )}

      {value.truncated && (
        <p className="text-xs text-muted-foreground">
          Showing the first 500 entries. Use the Query tab of your Redis client for the rest.
        </p>
      )}
      {value.type === "stream" && (
        <p className="text-xs text-muted-foreground">
          Streams are append-only structures with their own semantics; this shows their entries but
          does not edit them.
        </p>
      )}
    </div>
  )
}

/**
 * The collection editor.
 *
 * Every write here names one member. That is the whole rule: a view showing 500
 * of a list's 10,000 entries must never be able to save "the list", because it
 * would silently drop the 9,500 it never showed. So there is no Save-all button
 * — there is add-a-member, edit-this-member and remove-this-member, each of
 * which means exactly what it says however much of the collection is on screen.
 */
function MemberEditor({
  conn,
  db,
  value,
  canWrite,
  confirm,
  onChanged,
}: {
  conn: DbConnection
  db: string
  value: RedisValue
  canWrite: boolean
  confirm: ConfirmFn
  onChanged: () => void
}) {
  const [editing, setEditing] = useState<{ field: string; value: string } | null>(null)
  const [adding, setAdding] = useState(false)

  const rows = memberRows(value)
  const labels = memberLabels(value.type)

  const write = async (field: string, member: string) => {
    await post(
      `/databases/${conn.id}/keys/value`,
      { key: value.key, type: value.type, field, value: member },
      { query: { db } },
    )
    notify.success("Saved")
    setEditing(null)
    setAdding(false)
    onChanged()
  }

  const remove = (member: string, shown: string) =>
    confirm({
      title: `Remove ${value.type} member`,
      confirmLabel: "Remove",
      description: (
        <p>
          Removes <span className="font-mono text-xs">{shown}</span> from <b>{value.key}</b>. The
          rest of the {value.type} is left alone.
        </p>
      ),
      action: async (c) => {
        await del(`/databases/${conn.id}/keys`, {
          body: { key: value.key, type: value.type, member },
          confirm: c,
          query: { db },
        })
        notify.success("Member removed")
        onChanged()
      },
    })

  const editable = value.type !== "stream"

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between gap-2">
        <Label className="text-xs">
          {rows.length} {value.type} {rows.length === 1 ? "entry" : "entries"}
        </Label>
        {canWrite && editable && (
          <Button size="sm" variant="outline" onClick={() => setAdding(true)}>
            <Plus className="size-3.5" />
            Add
          </Button>
        )}
      </div>

      <div className="overflow-hidden rounded-md border border-hairline">
        <Table containerClassName="max-h-[26rem]">
          <TableHeader>
            <TableRow>
              {labels.map((h) => (
                <TableHead key={h}>{h}</TableHead>
              ))}
              {canWrite && editable && <TableHead className="w-20" />}
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((r) => (
              <TableRow key={r.key} className="group">
                <TableCell className="max-w-[14rem] truncate font-mono">{r.key}</TableCell>
                <TableCell className="max-w-md truncate font-mono">{r.value}</TableCell>
                {canWrite && editable && (
                  <TableCell className="w-20">
                    <RowActions>
                      <IconAction
                        label="Edit"
                        onClick={() => setEditing({ field: r.field, value: r.editValue })}
                      >
                        <Pencil />
                      </IconAction>
                      <IconAction
                        label="Remove"
                        className="text-destructive"
                        onClick={() => remove(r.member, r.key)}
                      >
                        <Trash />
                      </IconAction>
                    </RowActions>
                  </TableCell>
                )}
              </TableRow>
            ))}
            {rows.length === 0 && (
              <TableRow>
                <TableCell colSpan={3} className="text-muted-foreground">
                  Nothing in this {value.type}.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>

      {(editing || adding) && (
        <MemberDialog
          type={value.type}
          initialField={editing?.field ?? ""}
          initialValue={editing?.value ?? ""}
          isNew={adding}
          onClose={() => {
            setEditing(null)
            setAdding(false)
          }}
          onSave={write}
        />
      )}
    </div>
  )
}

type MemberRow = {
  /** What identifies the row on screen. */
  key: string
  value: string
  /** What the write API takes in its `field` slot — an index, a hash field or a score. */
  field: string
  /** What the delete API takes as the member to remove. */
  member: string
  editValue: string
}

// memberRows flattens whichever collection this key holds into one shape, so the
// table below renders once rather than four times.
function memberRows(v: RedisValue): MemberRow[] {
  if (v.hash)
    return Object.entries(v.hash).map(([f, val]) => ({
      key: f,
      value: val,
      field: f,
      member: f,
      editValue: val,
    }))
  if (v.list)
    return v.list.map((val, i) => ({
      key: String(i),
      value: val,
      field: String(i),
      member: String(i),
      editValue: val,
    }))
  if (v.set)
    return v.set.map((val) => ({ key: val, value: val, field: "", member: val, editValue: val }))
  if (v.zset)
    return v.zset.map((z) => ({
      key: String(z.score),
      value: z.member,
      field: String(z.score),
      member: z.member,
      editValue: z.member,
    }))
  if (v.stream)
    return v.stream.map((e) => ({
      key: e.id,
      value: JSON.stringify(e.values),
      field: e.id,
      member: e.id,
      editValue: "",
    }))
  return []
}

function memberLabels(type: string): string[] {
  switch (type) {
    case "hash":
      return ["Field", "Value"]
    case "list":
      return ["#", "Value"]
    case "zset":
      return ["Score", "Member"]
    case "stream":
      return ["ID", "Fields"]
    default:
      return ["Member", "Value"]
  }
}

/** The one form behind adding and editing a collection member. What the "field"
 *  box means changes with the type, so it is labelled per type rather than
 *  called something generic that is wrong for three of the four. */
function MemberDialog({
  type,
  initialField,
  initialValue,
  isNew,
  onClose,
  onSave,
}: {
  type: string
  initialField: string
  initialValue: string
  isNew: boolean
  onClose: () => void
  onSave: (field: string, value: string) => Promise<void>
}) {
  const [field, setField] = useState(initialField)
  const [member, setMember] = useState(initialValue)
  const [busy, setBusy] = useState(false)

  const fieldLabel: Record<string, string> = {
    hash: "Field name",
    list: isNew ? "Position (blank to append)" : "Position",
    zset: "Score",
    set: "",
  }
  const label = fieldLabel[type] ?? ""

  const save = async () => {
    setBusy(true)
    try {
      await onSave(field, member)
    } catch (err) {
      notify.error("Could not save", err)
    } finally {
      setBusy(false)
    }
  }

  const needsField = type === "hash" || type === "zset" || (type === "list" && !isNew)
  const valid = member !== "" && (!needsField || field !== "")

  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      size="sm"
      title={
        <>
          {isNew ? "Add" : "Edit"} {type} member
        </>
      }
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={save} disabled={!valid || busy} pending={busy}>
            Save
          </Button>
        </>
      }
    >
      <div className="grid gap-3">
        {label && (
          <div className="space-y-1.5">
            <Label>{label}</Label>
            <Input
              value={field}
              onChange={(e) => setField(e.target.value)}
              className="font-mono text-xs"
              disabled={type === "list" && !isNew}
              autoFocus={isNew}
            />
          </div>
        )}
        <div className="space-y-1.5">
          <Label>Value</Label>
          <Textarea
            value={member}
            onChange={(e) => setMember(e.target.value)}
            className="min-h-24 font-mono text-xs"
            autoFocus={!isNew}
          />
        </div>
        {type === "set" && !isNew && (
          <p className="text-xs text-muted-foreground">
            A set has no positions, so editing a member adds the new value; remove the old one if
            you meant to replace it.
          </p>
        )}
      </div>
    </Modal>
  )
}
