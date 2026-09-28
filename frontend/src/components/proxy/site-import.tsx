"use client"

import { useRef, useState } from "react"
import { CheckCircle, CloudUpload, Database, Warning } from "@/components/icons"
import { ApiError, errorMessage, post, postForm } from "@/lib/api"
import { plural } from "@/lib/format"
import type {
  NpmImportItem,
  NpmImportPreview,
  NpmImportResult,
  SitePlacement,
  SitePlacementResult,
} from "@/lib/types"
import { Field } from "@/components/form"
import { Modal } from "@/components/modal"
import { Well } from "@/components/panel"
import { Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"

const SITE_NAME = /^[a-z0-9][a-z0-9._-]{0,63}$/

/**
 * Brings a server block written elsewhere in as a new site: pasted, or a .conf
 * dropped on the field. Test runs nginx's own check with the file in place and
 * takes it back out; Import does the same and keeps it when nginx accepts it.
 * An existing site is never overwritten — the server refuses the name.
 */
export function SiteImportDialog({
  open,
  onOpenChange,
  onImported,
  onNpmImported,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onImported: (res: SitePlacementResult) => void
  onNpmImported: (res: NpmImportResult) => void
}) {
  // Keyed on open, so each opening starts empty.
  return (
    <SiteImportBody
      key={String(open)}
      open={open}
      onOpenChange={onOpenChange}
      onImported={onImported}
      onNpmImported={onNpmImported}
    />
  )
}

function SiteImportBody({
  open,
  onOpenChange,
  onImported,
  onNpmImported,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onImported: (res: SitePlacementResult) => void
  onNpmImported: (res: NpmImportResult) => void
}) {
  const [source, setSource] = useState("block")
  const [npmBusy, setNpmBusy] = useState(false)
  const [name, setName] = useState("")
  const [content, setContent] = useState("")
  const [enable, setEnable] = useState(true)
  const [busy, setBusy] = useState<"test" | "import">()
  const [error, setError] = useState<string>()
  const [preview, setPreview] = useState<SitePlacement>()
  const fileRef = useRef<HTMLInputElement>(null)
  const value = name.trim()
  const invalid = value !== "" && !SITE_NAME.test(value)
  const ready = value !== "" && !invalid && content.trim() !== ""

  const edit = (next: () => void) => {
    next()
    // A verdict on other text or another name would be read as this one's.
    setPreview(undefined)
    setError(undefined)
  }

  const load = async (file: File) => {
    const text = await file.text()
    edit(() => {
      setContent(text)
      const base = file.name.toLowerCase().replace(/\.conf$/, "")
      if (!value && SITE_NAME.test(base)) setName(base)
    })
  }

  const run = async (commit: boolean) => {
    if (!ready) return
    setBusy(commit ? "import" : "test")
    setError(undefined)
    try {
      const body = { name: value, content, enable, reload: true }
      if (commit) {
        onImported(await post<SitePlacementResult>("/proxy/sites/import", body))
        onOpenChange(false)
      } else {
        setPreview(await post<SitePlacement>("/proxy/sites/import/preview", body))
      }
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(undefined)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={(next) => !busy && !npmBusy && onOpenChange(next)}
      title="Import a site"
      description="A server block from another host or a backup, or everything an Nginx Proxy Manager served, added behind nginx's test."
      size="lg"
      footer={
        source === "npm" ? (
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={npmBusy}>
            Cancel
          </Button>
        ) : (
          <>
            <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={Boolean(busy)}>
              Cancel
            </Button>
            <Button
              variant="outline"
              onClick={() => void run(false)}
              disabled={Boolean(busy) || !ready}
              pending={busy === "test"}
            >
              Test
            </Button>
            <Button
              onClick={() => void run(true)}
              disabled={Boolean(busy) || !ready}
              pending={busy === "import"}
            >
              {enable ? "Import and reload" : "Import"}
            </Button>
          </>
        )
      }
    >
      <Tabs value={source} onValueChange={(v) => !busy && !npmBusy && setSource(v)}>
        <TabsList>
          <TabsTrigger value="block">Server block</TabsTrigger>
          <TabsTrigger value="npm">Nginx Proxy Manager</TabsTrigger>
        </TabsList>
        <TabsContent value="block">
          <form
            className="space-y-3"
            onSubmit={(e) => {
              e.preventDefault()
              void run(false)
            }}
          >
            {error && (
              <Notice title="Not imported" tone="danger">
                <span className="break-words whitespace-pre-wrap">{error}</span>
              </Notice>
            )}
            {preview && <PreviewVerdict preview={preview} />}
            <Field
              label="Site name"
              htmlFor="site-import-name"
              error={
                invalid
                  ? "Lower-case letters, digits, dots, dashes and underscores, up to 64."
                  : undefined
              }
              hint="The file's name on the host. In conf.d it gains .conf, which is what makes nginx read it."
            >
              <Input
                id="site-import-name"
                autoFocus
                autoComplete="off"
                spellCheck={false}
                value={name}
                onChange={(e) => edit(() => setName(e.target.value))}
                className="font-mono"
              />
            </Field>
            <Field
              label="Server block"
              htmlFor="site-import-content"
              hint="Paste it, or drop a .conf file on the field."
            >
              <Textarea
                id="site-import-content"
                rows={14}
                spellCheck={false}
                value={content}
                onChange={(e) => edit(() => setContent(e.target.value))}
                onDragOver={(e) => e.preventDefault()}
                onDrop={(e) => {
                  const file = e.dataTransfer.files[0]
                  if (!file) return
                  e.preventDefault()
                  void load(file)
                }}
                placeholder={"server {\n    listen 80;\n    server_name example.com;\n    ...\n}"}
                className="font-mono text-xs"
              />
            </Field>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <label className="flex items-center gap-2 text-hint text-muted-foreground">
                <Checkbox
                  checked={enable}
                  onCheckedChange={(v) => edit(() => setEnable(v === true))}
                />
                Enable it, and reload nginx
              </label>
              <Button
                type="button"
                size="xs"
                variant="outline"
                onClick={() => fileRef.current?.click()}
              >
                <CloudUpload className="size-3.5" />
                Choose a file
              </Button>
              <input
                ref={fileRef}
                type="file"
                accept=".conf,text/plain"
                className="hidden"
                onChange={(e) => {
                  const file = e.target.files?.[0]
                  if (file) void load(file)
                  e.target.value = ""
                }}
              />
            </div>
          </form>
        </TabsContent>
        <TabsContent value="npm">
          <NpmImport
            onBusy={setNpmBusy}
            onImported={(res) => {
              onNpmImported(res)
              onOpenChange(false)
            }}
          />
        </TabsContent>
      </Tabs>
    </Modal>
  )
}

/** What the test found: nginx's verdict, the hostnames, and what is worth knowing. */
function PreviewVerdict({ preview }: { preview: SitePlacement }) {
  const valid = preview.validation.valid
  return (
    <Notice
      tone={valid ? (preview.warnings.length > 0 ? "warning" : "success") : "danger"}
      icon={valid ? CheckCircle : Warning}
      title={
        valid
          ? `nginx accepts it as ${preview.name}`
          : preview.refusedBefore
            ? "nginx already refuses the configuration as it is"
            : `nginx refuses the configuration with ${preview.name} added`
      }
    >
      <p className="font-mono text-hint break-all">{preview.path}</p>
      {preview.serverNames.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {preview.serverNames.map((host) => (
            <Tag key={host} mono>
              {host}
            </Tag>
          ))}
        </div>
      )}
      {preview.warnings.map((warning) => (
        <p key={warning}>{warning}</p>
      ))}
      {!valid && (
        <pre className="max-h-40 overflow-auto font-mono text-hint whitespace-pre-wrap">
          {preview.validation.output}
        </pre>
      )}
      <p className="text-hint text-muted-foreground">
        Tested with the file in place, then taken back out — nothing on the host changed.
      </p>
    </Notice>
  )
}

const KIND_WORD: Record<NpmImportItem["kind"], string> = {
  proxy: "Proxy site",
  redirect: "Redirect site",
  stream: "Stream",
  access: "Password file",
}

const importable = (item: NpmImportItem) => !item.skipped && item.conflicts.length === 0

/**
 * Reads an Nginx Proxy Manager database.sqlite into a list of what it maps
 * to here, and adds the ticked items behind one nginx test and one reload.
 * The server holds the mapped plan behind a token for a quarter of an hour;
 * nothing on the host changes until Import.
 */
function NpmImport({
  onBusy,
  onImported,
}: {
  onBusy: (busy: boolean) => void
  onImported: (res: NpmImportResult) => void
}) {
  const [busy, setBusy] = useState<"read" | "import">()
  const [error, setError] = useState<string>()
  const [preview, setPreview] = useState<NpmImportPreview>()
  const [chosen, setChosen] = useState<string[]>([])
  const [shown, setShown] = useState<string>()
  const fileRef = useRef<HTMLInputElement>(null)

  const working = (next: "read" | "import" | undefined) => {
    setBusy(next)
    onBusy(next !== undefined)
  }

  const read = async (file: File) => {
    working("read")
    setError(undefined)
    setPreview(undefined)
    try {
      const form = new FormData()
      form.append("file", file)
      const res = await postForm<NpmImportPreview>("/proxy/import/npm", form)
      setPreview(res)
      setChosen(res.items.filter(importable).map((item) => item.id))
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      working(undefined)
    }
  }

  const apply = async () => {
    if (!preview || chosen.length === 0) return
    working("import")
    setError(undefined)
    try {
      onImported(
        await post<NpmImportResult>("/proxy/import/npm/apply", {
          token: preview.token,
          ids: chosen,
        }),
      )
    } catch (err) {
      // The plan is gone server-side; a list that can no longer be applied
      // would only invite the same refusal again.
      if (err instanceof ApiError && err.status === 410) setPreview(undefined)
      setError(errorMessage(err))
    } finally {
      working(undefined)
    }
  }

  const byId = new Map(preview?.items.map((item) => [item.id, item]))
  const toggle = (id: string, on: boolean) =>
    setChosen((c) => (on ? [...c, id] : c.filter((x) => x !== id)))
  const required = new Set(chosen.flatMap((id) => byId.get(id)?.requires ?? []))
  const streams = preview?.items.some((item) => item.kind === "stream" && importable(item))

  return (
    <div className="space-y-3">
      {error && (
        <Notice title="Not imported" tone="danger">
          <span className="break-words whitespace-pre-wrap">{error}</span>
        </Notice>
      )}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="min-w-0 flex-1 text-hint text-muted-foreground">
          NPM keeps everything in <span className="font-mono">database.sqlite</span> on its /data
          volume. Its proxy hosts, redirection hosts, streams and access lists are mapped; nothing
          changes on the host until Import.
        </p>
        <Button
          type="button"
          size="xs"
          variant="outline"
          onClick={() => fileRef.current?.click()}
          disabled={Boolean(busy)}
          pending={busy === "read"}
        >
          <Database className="size-3.5" />
          {preview ? "Choose another database" : "Choose database.sqlite"}
        </Button>
        <input
          ref={fileRef}
          type="file"
          accept=".sqlite,.db,application/vnd.sqlite3,application/x-sqlite3"
          className="hidden"
          onChange={(e) => {
            const file = e.target.files?.[0]
            if (file) void read(file)
            e.target.value = ""
          }}
        />
      </div>
      {preview && preview.items.length === 0 && (
        <Notice title="Nothing to import">
          The database has no proxy hosts, redirection hosts, streams or access lists that are not
          deleted.
        </Notice>
      )}
      {streams && !preview?.streamsIncluded && (
        <Notice title="nginx does not read stream.d yet" tone="warning">
          A stream is written and ignored until nginx.conf includes it — the Streams page has the
          lines to add.
        </Notice>
      )}
      {preview && preview.items.length > 0 && (
        <>
          <div className="divide-y divide-hairline">
            {preview.items.map((item) => (
              <NpmImportRow
                key={item.id}
                item={item}
                checked={chosen.includes(item.id)}
                required={required.has(item.id) && !chosen.includes(item.id)}
                shown={shown === item.id}
                onShow={() => setShown((s) => (s === item.id ? undefined : item.id))}
                onCheckedChange={(on) => toggle(item.id, on)}
                disabled={Boolean(busy)}
              />
            ))}
          </div>
          <p className="text-hint text-muted-foreground">
            A stream port NPM still listens on is one nginx cannot take until NPM lets it go — the
            reload says so.
          </p>
          <div className="flex justify-end">
            <Button
              onClick={() => void apply()}
              disabled={Boolean(busy) || chosen.length === 0}
              pending={busy === "import"}
            >
              Import {plural(chosen.length, "item")} and reload
            </Button>
          </div>
        </>
      )}
    </div>
  )
}

function NpmImportRow({
  item,
  checked,
  required,
  shown,
  onShow,
  onCheckedChange,
  disabled,
}: {
  item: NpmImportItem
  checked: boolean
  required: boolean
  shown: boolean
  onShow: () => void
  onCheckedChange: (checked: boolean) => void
  disabled: boolean
}) {
  const id = `npm-item-${item.id}`
  const can = importable(item)
  return (
    <div className="space-y-1.5 py-2.5">
      <div className="flex min-w-0 items-start gap-2.5">
        <Checkbox
          id={id}
          className="mt-0.5"
          checked={checked || required}
          disabled={disabled || !can || required}
          onCheckedChange={(v) => onCheckedChange(v === true)}
        />
        <label htmlFor={id} className="min-w-0 flex-1 space-y-0.5">
          <span className="flex flex-wrap items-baseline gap-x-2">
            <span className="font-mono break-all">{item.name}</span>
            <span className="text-hint text-muted-foreground">
              {KIND_WORD[item.kind]} · {item.source}
              {item.kind !== "access" && !item.enabled && " · off in NPM"}
            </span>
          </span>
          <span className="block font-mono text-hint break-all text-muted-foreground">
            {item.target}
          </span>
        </label>
        {(item.content || item.advanced) && (
          <Button type="button" size="xs" variant="ghost" onClick={onShow}>
            {shown ? "Hide file" : "Show file"}
          </Button>
        )}
      </div>
      <div className="space-y-1 pl-6.5 text-hint">
        {(item.domains.length > 0 || item.tls) && (
          <div className="flex flex-wrap gap-1">
            {item.domains.map((domain) => (
              <Tag key={domain} mono>
                {domain}
              </Tag>
            ))}
            {item.tls && <Tag tone="success">HTTPS</Tag>}
          </div>
        )}
        {item.users && item.users.length > 0 && (
          <p className="text-muted-foreground">
            Users: <span className="font-mono">{item.users.join(", ")}</span>
          </p>
        )}
        {required && <p className="text-muted-foreground">Added because a ticked site uses it.</p>}
        {item.skipped && <p className="text-muted-foreground">Not importable: {item.skipped}</p>}
        {item.conflicts.map((conflict) => (
          <p key={conflict} className="text-warning">
            {conflict}
          </p>
        ))}
        {item.notes.map((note) => (
          <p key={note} className="text-muted-foreground">
            {note}
          </p>
        ))}
        {shown && item.content && (
          <Well className="max-h-60 whitespace-pre-wrap">{item.content}</Well>
        )}
        {shown && item.advanced && (
          <>
            <p className="text-muted-foreground">NPM&apos;s custom configuration, not written:</p>
            <Well className="max-h-40 whitespace-pre-wrap">{item.advanced}</Well>
          </>
        )}
      </div>
    </div>
  )
}
