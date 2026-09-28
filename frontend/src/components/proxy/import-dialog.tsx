"use client"

import { useState } from "react"
import {
  Check,
  CheckCircle,
  Copy,
  FileText,
  Key,
  RefreshClockwise,
  Warning,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { ApiError, post } from "@/lib/api"
import { calendarDate, plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { ImportInspection, ImportResult } from "@/lib/types"
import { useCopy } from "@/hooks/use-copy"
import { Field } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Modal } from "@/components/modal"
import { useProxy } from "@/components/proxy/proxy-context"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"

/**
 * Not every certificate comes from Let's Encrypt.
 *
 * One a company bought, one from an internal CA, one a hosting provider handed
 * over — and they arrive as whatever the authority's tooling wrote: a .pfx
 * with a password, a .crt and a bundle in no particular order, a DER file, a
 * key encrypted at export. Dropping the files is enough; the server sorts
 * them out.
 *
 * Nothing is written until the reader has seen what would be: the names, the
 * chain, the expiry, and the import it would replace. The key is checked
 * against the certificate on the way, because a mismatched pair is accepted
 * by every text editor and refused by nginx at reload — which on a live server
 * means finding out during an outage.
 */
export function ImportDialog({
  open,
  onOpenChange,
  onDone,
  initialName,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onDone: () => void
  /** An import to replace: its name starts the form, and the server asks before overwriting it. */
  initialName?: string
}) {
  return (
    <ImportDialogBody
      key={`${open}:${initialName ?? ""}`}
      open={open}
      onOpenChange={onOpenChange}
      onDone={onDone}
      initialName={initialName}
    />
  )
}

type Pfx = { name: string; data: string }

const PEM_BLOCK = /-----BEGIN ([A-Z0-9 ]+)-----[\s\S]*?-----END \1-----/g

function bytesToBase64(bytes: Uint8Array): string {
  let binary = ""
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary)
}

/** A DER certificate as PEM: the same bytes, base64 in 64-column lines. */
function derToPem(bytes: Uint8Array): string {
  const lines = bytesToBase64(bytes).match(/.{1,64}/g) ?? []
  return `-----BEGIN CERTIFICATE-----\n${lines.join("\n")}\n-----END CERTIFICATE-----\n`
}

/**
 * Sorts one dropped file into certificates and a key. A combined .pem holds
 * both; an EC PARAMETERS block is dropped, since only the key after it is
 * the key. A file with no PEM armour at all is a DER certificate, which is
 * what a .cer or .crt from a Windows authority usually is.
 */
function splitPem(bytes: Uint8Array): { certs: string[]; key: string } {
  const text = new TextDecoder().decode(bytes)
  if (!text.includes("-----BEGIN ")) return { certs: [derToPem(bytes)], key: "" }
  const certs: string[] = []
  let key = ""
  for (const match of text.matchAll(PEM_BLOCK)) {
    if (match[1] === "CERTIFICATE") certs.push(match[0])
    else if (match[1].endsWith("PRIVATE KEY") && !key) key = match[0]
  }
  return { certs, key }
}

function ImportDialogBody({
  open,
  onOpenChange,
  onDone,
  initialName,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onDone: () => void
  initialName?: string
}) {
  const [name, setName] = useState(initialName ?? "")
  const [certificate, setCertificate] = useState("")
  const [key, setKey] = useState("")
  const [pfx, setPfx] = useState<Pfx | null>(null)
  const [password, setPassword] = useState("")
  const [dropped, setDropped] = useState<string[]>([])
  const [dragging, setDragging] = useState(false)
  const [fetchIssuer, setFetchIssuer] = useState(false)
  const [busy, setBusy] = useState(false)
  const [inspection, setInspection] = useState<ImportInspection | null>(null)
  const [replace, setReplace] = useState(false)
  const [result, setResult] = useState<ImportResult | null>(null)

  // Any change to what would be imported makes the inspection stale, so the
  // dialog goes back to the first step rather than import something unseen.
  const edit = (set: (value: string) => void) => (value: string) => {
    set(value)
    setInspection(null)
    setReplace(false)
  }

  const readFiles = async (files: FileList | null) => {
    if (!files?.length) return
    const certs: string[] = []
    let nextKey = ""
    let nextPfx: Pfx | null = null
    for (const file of Array.from(files)) {
      const bytes = new Uint8Array(await file.arrayBuffer())
      if (/\.(pfx|p12)$/i.test(file.name)) {
        nextPfx = { name: file.name, data: bytesToBase64(bytes) }
        continue
      }
      const split = splitPem(bytes)
      certs.push(...split.certs)
      nextKey ||= split.key
    }
    if (nextPfx) setPfx(nextPfx)
    if (certs.length)
      setCertificate((current) => [current.trim(), ...certs].filter(Boolean).join("\n"))
    if (nextKey) setKey(nextKey)
    setDropped((current) => [...current, ...Array.from(files, (f) => f.name)])
    setInspection(null)
    setReplace(false)
  }

  const needsPassword = pfx !== null || /ENCRYPTED/.test(key)
  const ready = pfx !== null ? true : Boolean(certificate.trim() && key.trim())

  const body = (fetch: boolean) => ({
    name: name.trim(),
    certificate,
    key: pfx ? "" : key,
    pfx: pfx?.data,
    password: needsPassword ? password : "",
    fetchIssuer: fetch,
  })

  const inspect = async (fetch = fetchIssuer) => {
    setBusy(true)
    try {
      const res = await post<ImportInspection>("/certificates/import/inspect", body(fetch))
      setInspection(res)
      setFetchIssuer(fetch)
      setReplace(false)
      if (!name.trim()) setName(res.name)
    } catch (err) {
      notify.error("Could not be read", err)
    } finally {
      setBusy(false)
    }
  }

  const submit = async () => {
    setBusy(true)
    try {
      const res = await post<ImportResult>("/certificates/import", {
        ...body(fetchIssuer),
        replace,
      })
      setResult(res)
      notify.success(res.replaced ? `${res.name} replaced` : `${res.name} imported`, {
        description: res.replaced
          ? "nginx keeps serving the previous one until it reloads."
          : "Point a site at the paths below to start serving it.",
      })
      onDone()
    } catch (err) {
      // Taken between the inspection and this press: inspect again, which
      // shows what is there now and asks for the replacement explicitly.
      if (err instanceof ApiError && err.code === "certificate_exists") await inspect()
      else notify.error("Not imported", err)
    } finally {
      setBusy(false)
    }
  }

  const footer = result ? (
    <Button onClick={() => onOpenChange(false)}>Done</Button>
  ) : inspection ? (
    <>
      <Button variant="outline" onClick={() => setInspection(null)} disabled={busy}>
        Back
      </Button>
      <Button
        variant={inspection.replaced ? "destructive" : undefined}
        onClick={submit}
        disabled={busy || (inspection.replaced && !replace)}
        pending={busy}
      >
        {inspection.replaced ? "Replace it" : "Import"}
      </Button>
    </>
  ) : (
    <Button
      onClick={() => inspect()}
      disabled={busy || !ready || (needsPassword && !password)}
      pending={busy}
    >
      Inspect
    </Button>
  )

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      size="lg"
      title={initialName ? `Replace ${initialName}` : "Import a certificate"}
      description="For a certificate you bought or were given. Nothing here renews it — that is what the expiry column is for."
      footer={footer}
    >
      {result ? (
        <ImportOutcome result={result} />
      ) : inspection ? (
        <ImportPreview
          inspection={inspection}
          replace={replace}
          onReplaceChange={setReplace}
          onFetchIssuer={() => inspect(true)}
          busy={busy}
        />
      ) : (
        <div className="grid gap-4">
          <label
            onDragOver={(e) => {
              e.preventDefault()
              setDragging(true)
            }}
            onDragLeave={() => setDragging(false)}
            onDrop={(e) => {
              e.preventDefault()
              setDragging(false)
              void readFiles(e.dataTransfer.files)
            }}
            className={cn(
              "flex cursor-pointer items-center gap-3 rounded-lg border border-dashed px-4 py-3 transition-colors focus-within:border-border-strong hover:border-border-strong",
              dragging ? "border-border-strong bg-accent" : "border-border",
            )}
          >
            <FileText className="size-4 shrink-0 text-muted-foreground" />
            <span className="min-w-0 flex-1">
              <span className="block truncate text-body font-medium">
                {dropped.length ? dropped.join(", ") : "Drop the files here, or choose them"}
              </span>
              <span className="block text-hint text-muted-foreground">
                .crt, .cer, .pem, .key, .pfx or .p12 — several at once, in any order
              </span>
            </span>
            <input
              type="file"
              multiple
              accept=".crt,.cer,.der,.pem,.key,.pfx,.p12,.txt"
              className="sr-only"
              onChange={(e) => {
                void readFiles(e.target.files)
                e.target.value = ""
              }}
            />
          </label>
          <Field
            label="Name"
            htmlFor="import-name"
            hint="Names the directory it is stored in. Left empty, it is made from the certificate's common name."
          >
            <Input
              id="import-name"
              value={name}
              onChange={(e) => edit(setName)(e.target.value)}
              placeholder="example.com"
              className="font-mono text-xs"
            />
          </Field>
          {pfx && (
            <Notice tone="default" icon={Key} title={pfx.name}>
              <div className="flex flex-wrap items-center gap-2">
                <span>The certificate, its chain and the key are read from this file.</span>
                <Button
                  size="xs"
                  variant="outline"
                  onClick={() => {
                    setPfx(null)
                    setInspection(null)
                  }}
                >
                  Use PEM instead
                </Button>
              </div>
            </Notice>
          )}
          <Field
            label={pfx ? "Extra chain certificates" : "Certificate"}
            htmlFor="import-cert"
            hint="The leaf and the chain, in any order: it is saved leaf first, without the root."
          >
            <Textarea
              id="import-cert"
              value={certificate}
              onChange={(e) => edit(setCertificate)(e.target.value)}
              rows={pfx ? 3 : 6}
              className="font-mono text-micro"
              placeholder={"-----BEGIN CERTIFICATE-----\n…"}
            />
          </Field>
          {!pfx && (
            <Field label="Private key" htmlFor="import-key">
              <Textarea
                id="import-key"
                value={key}
                onChange={(e) => edit(setKey)(e.target.value)}
                rows={5}
                className="font-mono text-micro"
                placeholder={"-----BEGIN PRIVATE KEY-----\n…"}
              />
            </Field>
          )}
          {needsPassword && (
            <Field
              label={pfx ? "PFX password" : "Key password"}
              htmlFor="import-password"
              hint="Used once to open it. The key is saved decrypted, with mode 0600, since nginx cannot ask for a password at start."
            >
              <Input
                id="import-password"
                type="password"
                autoComplete="off"
                value={password}
                onChange={(e) => edit(setPassword)(e.target.value)}
              />
            </Field>
          )}
        </div>
      )}
    </Modal>
  )
}

/**
 * The second step: what the import would write, and what it would replace.
 * Replacing is its own tick, with both expiry dates side by side, because a
 * name reused by accident used to swap the certificate under a live site.
 */
function ImportPreview({
  inspection,
  replace,
  onReplaceChange,
  onFetchIssuer,
  busy,
}: {
  inspection: ImportInspection
  replace: boolean
  onReplaceChange: (replace: boolean) => void
  onFetchIssuer: () => void
  busy: boolean
}) {
  const cert = inspection.certificate
  const existing = inspection.existing
  return (
    <div className="space-y-4">
      <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 text-body">
        <dt className="text-muted-foreground">Saved as</dt>
        <dd className="font-mono text-xs">{inspection.name}</dd>
        <dt className="text-muted-foreground">Covers</dt>
        <dd className="flex flex-wrap gap-x-3 gap-y-1">
          {cert.domains.map((domain) => (
            <Tag key={domain} mono>
              {domain}
            </Tag>
          ))}
        </dd>
        <dt className="text-muted-foreground">Expires</dt>
        <dd>
          {calendarDate(cert.notAfter)} ·{" "}
          {cert.expired ? "already expired" : `in ${plural(cert.daysLeft, "day")}`}
        </dd>
        <dt className="text-muted-foreground">Chain</dt>
        <dd className="space-y-1">
          <Status
            verdict={inspection.chainComplete ? "ok" : "warning"}
            label={inspection.chainComplete ? "Reaches a root" : "Stops short of a root"}
          />
          <p className="font-mono text-xs break-words text-muted-foreground">
            {inspection.chain.join(" → ")}
          </p>
        </dd>
      </dl>
      {inspection.issuerURL && (
        <Notice tone="warning" icon={Warning} title="An intermediate is missing">
          <div className="space-y-2">
            <p>
              The certificate says its issuer can be fetched from{" "}
              <code className="font-mono break-all">{inspection.issuerURL}</code>. Fetching it adds
              it to the chain only if it really signed this one.
            </p>
            <Button size="xs" variant="outline" onClick={onFetchIssuer} pending={busy}>
              Fetch it
            </Button>
          </div>
        </Notice>
      )}
      {inspection.replaced && (
        <Notice tone="warning" icon={Warning} title={`${inspection.name} is already imported`}>
          <div className="space-y-2">
            {existing ? (
              <p>
                The pair there now covers {existing.domains.join(", ")} and expires{" "}
                {calendarDate(existing.notAfter)}; this one expires {calendarDate(cert.notAfter)}.
              </p>
            ) : (
              <p>The pair there now could not be read.</p>
            )}
            {inspection.usedBy.length > 0 && (
              <p>
                Served by {inspection.usedBy.join(", ")}, which keep the old pair until nginx
                reloads.
              </p>
            )}
            <label className="flex items-center gap-2">
              <Checkbox checked={replace} onCheckedChange={(v) => onReplaceChange(Boolean(v))} />
              Replace it, keeping the old pair beside it as <code className="font-mono">.bak</code>
            </label>
          </div>
        </Notice>
      )}
      {inspection.warnings.map((warning) => (
        <Notice key={warning} tone="warning" icon={Warning} title="Worth knowing">
          {warning}
        </Notice>
      ))}
    </div>
  )
}

function CopyPath({ label, path }: { label: string; path: string }) {
  const { copy, copied } = useCopy()
  return (
    <p className="flex min-w-0 items-center gap-1">
      <span className="min-w-0 break-all">
        {label}: <code className="font-mono">{path}</code>
      </span>
      <IconAction label={`Copy the ${label.toLowerCase()} path`} onClick={() => copy(path)}>
        {copied ? <Check /> : <Copy />}
      </IconAction>
    </p>
  )
}

/**
 * What an import left on disk, or a certificate made here: where the pair is,
 * what it covers, and — when it replaced one a site may be serving — that
 * nginx serves the new one only once it reloads, with the reload to hand.
 */
export function ImportOutcome({ result }: { result: ImportResult }) {
  const { hasNginx } = useProxy()
  const [reloading, setReloading] = useState(false)
  const [reloaded, setReloaded] = useState(false)

  const reload = async () => {
    setReloading(true)
    try {
      await post("/proxy/reload", { kind: "nginx" })
      setReloaded(true)
      notify.success("nginx reloaded")
    } catch (err) {
      notify.error("nginx did not reload", err)
    } finally {
      setReloading(false)
    }
  }

  return (
    <div className="space-y-3">
      <Notice
        tone="success"
        icon={CheckCircle}
        title={result.replaced ? `${result.name} was replaced` : `${result.name} is on disk`}
      >
        <div className="space-y-1">
          <CopyPath label="Certificate" path={result.certPath} />
          <CopyPath label="Key" path={result.keyPath} />
          <p>
            Covers {result.certificate.domains.join(", ")} · expires in{" "}
            {result.certificate.daysLeft} days.
          </p>
        </div>
      </Notice>
      {result.replaced &&
        (reloaded ? (
          <Notice tone="success" icon={CheckCircle} title="nginx reloaded">
            {result.usedBy?.length
              ? `${result.usedBy.join(", ")} serve the new one now.`
              : "Sites using this certificate serve the new one now."}{" "}
            The previous pair is kept beside it as <code className="font-mono">.bak</code>.
          </Notice>
        ) : (
          <Notice tone="warning" icon={RefreshClockwise} title="Sites pick it up on a reload">
            <div className="flex flex-wrap items-center gap-2">
              <span>
                {result.usedBy?.length
                  ? `${result.usedBy.join(", ")} keep serving the old one until nginx reloads.`
                  : "A site using this certificate keeps serving the old one until nginx reloads."}{" "}
                The previous pair is kept beside the new one as{" "}
                <code className="font-mono">.bak</code>.
              </span>
              {hasNginx && (
                <Button size="xs" variant="outline" onClick={reload} pending={reloading}>
                  Reload nginx now
                </Button>
              )}
            </div>
          </Notice>
        ))}
      {result.warnings.map((warning) => (
        <Notice key={warning} tone="warning" icon={Warning} title="Worth knowing">
          {warning}
        </Notice>
      ))}
    </div>
  )
}
