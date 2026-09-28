"use client"

import { useRef, useState } from "react"
import { CheckCircle, CloudUpload, Warning } from "@/components/icons"
import { errorMessage, post } from "@/lib/api"
import type { SitePlacement, SitePlacementResult } from "@/lib/types"
import { Field } from "@/components/form"
import { Modal } from "@/components/modal"
import { Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
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
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onImported: (res: SitePlacementResult) => void
}) {
  // Keyed on open, so each opening starts empty.
  return (
    <SiteImportBody
      key={String(open)}
      open={open}
      onOpenChange={onOpenChange}
      onImported={onImported}
    />
  )
}

function SiteImportBody({
  open,
  onOpenChange,
  onImported,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onImported: (res: SitePlacementResult) => void
}) {
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
      onOpenChange={(next) => !busy && onOpenChange(next)}
      title="Import a site"
      description="A server block from another host or a backup, added as a new site behind nginx's test."
      size="lg"
      footer={
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
      }
    >
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
            <Checkbox checked={enable} onCheckedChange={(v) => edit(() => setEnable(v === true))} />
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
