"use client"

import { useState } from "react"
import {
  Archive,
  ArrowLeft,
  CloudDownload,
  CloudUpload,
  LockClosed,
  Servers,
} from "@/components/icons"
import { post } from "@/lib/api"
import { bytes, plural, relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { BackupJob } from "@/lib/types"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Field, FieldRow, FormNote, OptionList, OptionRow } from "@/components/form"
import { Modal } from "@/components/modal"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import { KeyImport } from "@/components/backups/backup-key"
import { destinationProduct } from "@/components/backups/marks"
import {
  EMPTY_DESTINATION,
  archiveState,
  contentsWords,
  destinationBody,
  destinationReady,
  importSummary,
  importableArchives,
  knownDestinations,
  missingKeys,
  originTitle,
  shortKey,
  type Catalogue,
  type CatalogueArchive,
  type CatalogueJob,
  type CatalogueOrigin,
  type DestinationFields,
  type DestinationRequest,
  type ImportResult,
  type KnownDestination,
} from "@/components/backups/import-model"

type Source = { kind: "job"; destination: KnownDestination } | { kind: "entered" }

/**
 * Import from a destination: what a bucket, a directory, an SFTP server or an
 * rclone remote holds that this server did not write — another server's
 * archives, or this one's from before a rebuild — read and taken on as
 * imported runs, which restore here through the same dialogs as this
 * server's own.
 *
 * Two screens in one dialog. First where to read: the places this server's
 * jobs already write, as choices to take (§16 — the row is the advance), or a
 * destination typed in with its keys, which are held in this dialog alone and
 * sent with each request. Then the catalogue: grouped by the server that took
 * the archives and the job there, each archive with what it covers, its size
 * and time, whether it is sealed and whether a key here opens it, and the
 * import of one archive or of a job's whole history. A sealed archive whose
 * key is not here says which key it needs, and the key can be brought without
 * leaving the dialog.
 */
export function ImportDialog({
  jobs,
  onOpenChange,
  onImported,
}: {
  jobs: BackupJob[]
  onOpenChange: (open: boolean) => void
  onImported: () => void
}) {
  const known = knownDestinations(jobs)
  const [source, setSource] = useState<Source | null>(
    known.length === 0 ? { kind: "entered" } : null,
  )
  const [fields, setFields] = useState<DestinationFields>(EMPTY_DESTINATION)
  const [catalogue, setCatalogue] = useState<Catalogue | null>(null)
  const [reading, setReading] = useState(false)
  const [readError, setReadError] = useState<string | null>(null)
  const [keepCopy, setKeepCopy] = useState(false)
  const [copyTo, setCopyTo] = useState("/var/backups/just-dashboard/imported")
  const [importing, setImporting] = useState<string | null>(null)
  const [keyOpen, setKeyOpen] = useState(false)

  const request = (from: Source, hostKey?: string): DestinationRequest =>
    from.kind === "job"
      ? { jobId: from.destination.jobId }
      : destinationBody({ ...fields, hostKey: hostKey ?? fields.hostKey })

  const read = async (from: Source) => {
    setSource(from)
    setReading(true)
    setReadError(null)
    try {
      const next = await post<Catalogue>("/backups/catalogue", request(from))
      setCatalogue(next)
      // An SFTP server reached for the first time is held to the key it
      // showed for the import that follows, as a job's is.
      if (from.kind === "entered" && next.hostKey)
        setFields((f) => ({ ...f, hostKey: next.hostKey }))
    } catch (err) {
      setCatalogue(null)
      setReadError(err instanceof Error ? err.message : String(err))
    } finally {
      setReading(false)
    }
  }

  const importArchives = async (
    id: string,
    what: { keys: string[] } | { originId: string; originJobId: number },
  ) => {
    if (!source) return
    setImporting(id)
    try {
      const result = await post<ImportResult>("/backups/import", {
        ...request(source),
        ...what,
        copyTo: keepCopy && copyTo.trim() ? copyTo.trim() : undefined,
      })
      notify.success(importSummary(result), {
        description:
          result.skipped.length > 0
            ? result.skipped
                .slice(0, 2)
                .map((skip) => skip.reason)
                .join(" ")
            : "Restore them from the job's runs; they are read where they are.",
      })
      onImported()
      await read(source)
    } catch (err) {
      notify.error("Could not import", err)
    } finally {
      setImporting(null)
    }
  }

  const back = () => {
    setCatalogue(null)
    setReadError(null)
    setKeyOpen(false)
    setSource(known.length === 0 ? { kind: "entered" } : null)
  }

  const showingCatalogue = catalogue !== null
  const enteredReady = destinationReady(fields)
  return (
    <Modal
      open
      onOpenChange={onOpenChange}
      size="xl"
      title="Import from a destination"
      description="Read what a destination holds that this server did not write, and take it on as imported runs."
      actions={
        showingCatalogue || (source?.kind === "entered" && known.length > 0) ? (
          <Button size="sm" variant="ghost" onClick={back}>
            <ArrowLeft />
            Change destination
          </Button>
        ) : undefined
      }
      footer={
        !showingCatalogue && source?.kind === "entered" ? (
          <Button
            disabled={!enteredReady || reading}
            onClick={() => void read({ kind: "entered" })}
          >
            {reading ? "Reading the destination…" : "Read destination"}
          </Button>
        ) : undefined
      }
    >
      {showingCatalogue ? (
        <CatalogueView
          catalogue={catalogue}
          entered={source?.kind === "entered"}
          importing={importing}
          keyOpen={keyOpen}
          onKeyOpen={setKeyOpen}
          onKeyImported={() => {
            setKeyOpen(false)
            if (source) void read(source)
          }}
          keepCopy={keepCopy}
          onKeepCopy={setKeepCopy}
          copyTo={copyTo}
          onCopyTo={setCopyTo}
          onImportArchive={(archive) => void importArchives(archive.key, { keys: [archive.key] })}
          onImportJob={(origin, job) =>
            void importArchives(`${origin.id}/${job.originJobId}`, {
              originId: origin.id,
              originJobId: job.originJobId,
            })
          }
        />
      ) : (
        <div className="space-y-4">
          {readError && (
            <Notice tone="danger" title="The destination could not be read">
              <p className="break-words">{readError}</p>
            </Notice>
          )}
          {source?.kind !== "entered" && (
            <ChoiceList>
              {known.map((destination) => {
                const job = jobs.find((item) => item.id === destination.jobId)
                const product = job ? destinationProduct(job) : undefined
                return (
                  <ChoiceRow
                    key={destination.key}
                    verb={`Read ${destination.service} ${destination.place}`}
                    leading={
                      hasProductLogo(product) ? (
                        <ProductGlyph id={product} className="size-5" />
                      ) : destination.kind === "local" ? (
                        <Servers aria-hidden className="size-5 text-muted-foreground" />
                      ) : (
                        <CloudUpload aria-hidden className="size-5 text-muted-foreground" />
                      )
                    }
                    title={destination.service}
                    description={
                      <>
                        <span className="font-mono">{destination.place}</span>
                        {" · where "}
                        {destination.jobs.length === 1
                          ? destination.jobs[0]
                          : plural(destination.jobs.length, "job")}{" "}
                        writes
                      </>
                    }
                    trailing={
                      reading &&
                      source?.kind === "job" &&
                      source.destination.key === destination.key ? (
                        <span className="text-hint text-muted-foreground">Reading…</span>
                      ) : undefined
                    }
                    disabled={reading}
                    onSelect={() => void read({ kind: "job", destination })}
                  />
                )
              })}
              <ChoiceRow
                verb="Read another destination"
                leading={<CloudDownload aria-hidden className="size-5 text-muted-foreground" />}
                title="Another destination"
                description="A bucket, a directory, an SFTP server or an rclone remote, with its keys"
                disabled={reading}
                onSelect={() => setSource({ kind: "entered" })}
              />
            </ChoiceList>
          )}
          {source?.kind === "entered" && (
            <EnteredDestination
              fields={fields}
              onChange={(next) => setFields({ ...next, hostKey: undefined })}
            />
          )}
        </div>
      )}
    </Modal>
  )
}

/** A destination typed in: the kinds a job writes to, and the keys each takes. */
function EnteredDestination({
  fields,
  onChange,
}: {
  fields: DestinationFields
  onChange: (fields: DestinationFields) => void
}) {
  const set = (patch: Partial<DestinationFields>) => onChange({ ...fields, ...patch })
  return (
    <div className="space-y-4">
      <Field label="Destination" htmlFor="import-kind">
        <Select
          value={fields.kind}
          onValueChange={(kind) => set({ kind: kind as DestinationFields["kind"] })}
        >
          <SelectTrigger id="import-kind" size="sm" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="s3">Amazon S3 or any S3-compatible bucket</SelectItem>
            <SelectItem value="b2">Backblaze B2</SelectItem>
            <SelectItem value="sftp">An SFTP server</SelectItem>
            <SelectItem value="rclone">
              Google Drive, OneDrive, Dropbox, WebDAV and more, through rclone
            </SelectItem>
            <SelectItem value="local">A directory on this server</SelectItem>
          </SelectContent>
        </Select>
      </Field>
      {fields.kind === "local" ? (
        <Field
          label="Directory"
          htmlFor="import-path"
          hint="Where the archives are: a disk moved here, or a copy of another server's directory."
        >
          <Input
            id="import-path"
            value={fields.path}
            onChange={(e) => set({ path: e.target.value })}
            className="font-mono text-body"
          />
        </Field>
      ) : fields.kind === "sftp" ? (
        <>
          <FieldRow>
            <Field label="Server" htmlFor="import-sftp-host">
              <Input
                id="import-sftp-host"
                value={fields.host}
                onChange={(e) => set({ host: e.target.value })}
                placeholder="backup.example.com"
                spellCheck={false}
              />
            </Field>
            <Field label="Port" htmlFor="import-sftp-port">
              <Input
                id="import-sftp-port"
                value={fields.port}
                onChange={(e) => set({ port: e.target.value.replace(/[^0-9]/g, "") })}
                inputMode="numeric"
              />
            </Field>
          </FieldRow>
          <FieldRow>
            <Field label="Account" htmlFor="import-sftp-user">
              <Input
                id="import-sftp-user"
                value={fields.username}
                onChange={(e) => set({ username: e.target.value })}
                spellCheck={false}
              />
            </Field>
            <Field
              label="Directory"
              htmlFor="import-sftp-path"
              hint="Under the account's home unless it starts with /."
            >
              <Input
                id="import-sftp-path"
                value={fields.path === EMPTY_DESTINATION.path ? "" : fields.path}
                onChange={(e) => set({ path: e.target.value })}
                className="font-mono text-body"
              />
            </Field>
          </FieldRow>
          <FieldRow>
            <Field
              label="Password"
              htmlFor="import-sftp-password"
              hint="A password, a key, or both."
            >
              <Input
                id="import-sftp-password"
                type="password"
                value={fields.password}
                onChange={(e) => set({ password: e.target.value })}
              />
            </Field>
            <Field
              label="Key passphrase"
              htmlFor="import-sftp-passphrase"
              hint="Only for a protected key."
            >
              <Input
                id="import-sftp-passphrase"
                type="password"
                value={fields.passphrase}
                onChange={(e) => set({ passphrase: e.target.value })}
              />
            </Field>
          </FieldRow>
          <Field label="Private key" htmlFor="import-sftp-key" hint="OpenSSH or PEM.">
            <Textarea
              id="import-sftp-key"
              value={fields.privateKey}
              onChange={(e) => set({ privateKey: e.target.value })}
              rows={3}
              spellCheck={false}
              className="font-mono text-xs"
              placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
            />
          </Field>
        </>
      ) : fields.kind === "rclone" ? (
        <>
          <Field
            label="Remote configuration"
            htmlFor="import-rclone-config"
            hint="What rclone config show <name> prints, token included."
          >
            <Textarea
              id="import-rclone-config"
              value={fields.rcloneConfig}
              onChange={(e) => set({ rcloneConfig: e.target.value })}
              rows={5}
              spellCheck={false}
              className="font-mono text-xs"
              placeholder={'[gdrive]\ntype = drive\ntoken = {"access_token":"…"}'}
            />
          </Field>
          <Field
            label="Folder"
            htmlFor="import-rclone-path"
            hint="Inside the remote, where the archives are."
          >
            <Input
              id="import-rclone-path"
              value={fields.path === EMPTY_DESTINATION.path ? "" : fields.path}
              onChange={(e) => set({ path: e.target.value })}
              className="font-mono text-body"
            />
          </Field>
        </>
      ) : (
        <>
          <FieldRow>
            <Field label="Bucket" htmlFor="import-bucket">
              <Input
                id="import-bucket"
                value={fields.bucket}
                onChange={(e) => set({ bucket: e.target.value })}
              />
            </Field>
            <Field
              label="Prefix"
              htmlFor="import-prefix"
              hint="The folder inside the bucket the archives are in."
            >
              <Input
                id="import-prefix"
                value={fields.prefix}
                onChange={(e) => set({ prefix: e.target.value })}
              />
            </Field>
          </FieldRow>
          <FieldRow>
            <Field
              label="Endpoint"
              htmlFor="import-endpoint"
              hint={fields.kind === "b2" ? "From the bucket's details page." : "Blank for AWS."}
            >
              <Input
                id="import-endpoint"
                value={fields.endpoint}
                onChange={(e) => set({ endpoint: e.target.value })}
                placeholder={fields.kind === "b2" ? "s3.us-west-004.backblazeb2.com" : ""}
              />
            </Field>
            <Field label="Region" htmlFor="import-region">
              <Input
                id="import-region"
                value={fields.region}
                onChange={(e) => set({ region: e.target.value })}
                placeholder="us-east-1"
              />
            </Field>
          </FieldRow>
          <FieldRow>
            <Field label="Access key ID" htmlFor="import-access">
              <Input
                id="import-access"
                value={fields.accessKey}
                onChange={(e) => set({ accessKey: e.target.value })}
              />
            </Field>
            <Field label="Secret access key" htmlFor="import-secret">
              <Input
                id="import-secret"
                type="password"
                value={fields.secretKey}
                onChange={(e) => set({ secretKey: e.target.value })}
              />
            </Field>
          </FieldRow>
        </>
      )}
      <FormNote>
        The keys are used to read the destination and kept, sealed with this server&apos;s master
        key, only with a job an import makes. Nothing is written there.
      </FormNote>
    </div>
  )
}

/** The catalogue: who took the archives, which job, and each archive with where it stands. */
function CatalogueView({
  catalogue,
  entered,
  importing,
  keyOpen,
  onKeyOpen,
  onKeyImported,
  keepCopy,
  onKeepCopy,
  copyTo,
  onCopyTo,
  onImportArchive,
  onImportJob,
}: {
  catalogue: Catalogue
  entered: boolean
  importing: string | null
  keyOpen: boolean
  onKeyOpen: (open: boolean) => void
  onKeyImported: () => void
  keepCopy: boolean
  onKeepCopy: (keep: boolean) => void
  copyTo: string
  onCopyTo: (path: string) => void
  onImportArchive: (archive: CatalogueArchive) => void
  onImportJob: (origin: CatalogueOrigin, job: CatalogueJob) => void
}) {
  const missing = missingKeys(catalogue)
  return (
    <div className="space-y-5" data-testid="backup-catalogue">
      <div className="space-y-1">
        <p className="font-mono text-body break-all">{catalogue.destination}</p>
        <p className="numeric text-hint text-muted-foreground">
          {catalogue.archives === 0
            ? "No archives another server wrote"
            : `${plural(catalogue.archives, "archive")} this server did not write`}
          {catalogue.ours > 0 &&
            ` · ${plural(catalogue.ours, "archive")} of this server's own left out`}
          {catalogue.truncated && " · the destination holds more than one reading lists"}
        </p>
      </div>
      {entered && catalogue.hostKeyFingerprint && (
        <FormNote>
          The server presented <span className="font-mono">{catalogue.hostKeyFingerprint}</span>.
          Compare it with the server&apos;s own; an import holds the destination to this key.
        </FormNote>
      )}
      {missing.length > 0 && (
        <Notice
          tone="warning"
          title={`Sealed to ${plural(missing.length, "key")} this server does not hold`}
        >
          <p>Import the key the server that wrote them exported, and they open here.</p>
          {keyOpen ? (
            <div className="mt-3">
              <KeyImport wanted={missing} onImported={onKeyImported} />
            </div>
          ) : (
            <Button size="xs" variant="outline" className="mt-2" onClick={() => onKeyOpen(true)}>
              Import a key…
            </Button>
          )}
        </Notice>
      )}
      {catalogue.origins.length === 0 && (
        <EmptyNote>
          Nothing here to import. Archives this server wrote and still records are not listed.
        </EmptyNote>
      )}
      {catalogue.origins.map((origin) => (
        <section
          key={origin.id || "unnamed"}
          aria-label={originTitle(origin)}
          className="space-y-3"
        >
          <header className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1 border-b border-hairline pb-1.5">
            <h3 className="text-title font-semibold tracking-tight">{originTitle(origin)}</h3>
            {origin.id && (
              <span
                className="font-mono text-hint text-muted-foreground"
                title={`Install ${origin.id}`}
              >
                install {origin.id.slice(0, 8)}
              </span>
            )}
          </header>
          {origin.jobs.map((job) => {
            const ready = importableArchives(job)
            const id = `${origin.id}/${job.originJobId}`
            return (
              <div key={`${job.originJobId}:${job.name}`} className="space-y-1.5">
                <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2">
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-body font-medium">{job.name}</p>
                    <p className="numeric text-hint text-muted-foreground">
                      {plural(job.archives.length, "archive")} · {bytes(job.bytes)}
                      {job.newest && ` · newest ${relativeTime(job.newest)}`}
                      {job.importedJob ? " · imported here" : ""}
                    </p>
                  </div>
                  {origin.id && ready.length > 0 && (
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={importing !== null}
                      onClick={() => onImportJob(origin, job)}
                    >
                      {importing === id
                        ? "Importing…"
                        : ready.length === job.archives.length
                          ? `Import all ${ready.length}`
                          : `Import ${ready.length} of ${job.archives.length}`}
                    </Button>
                  )}
                </div>
                <RowList>
                  {job.archives.map((archive) => (
                    <ArchiveRow
                      key={archive.key}
                      archive={archive}
                      busy={importing === archive.key}
                      disabled={importing !== null}
                      onImport={() => onImportArchive(archive)}
                    />
                  ))}
                </RowList>
              </div>
            )
          })}
        </section>
      ))}
      {catalogue.archives > 0 && (
        <div className="space-y-2">
          <OptionList>
            <OptionRow
              title="Keep a copy of each archive on this server"
              hint="Off, an imported run reads the destination's object on every restore. On, the copy is read first while it is there and still proves its digest."
              checked={keepCopy}
              onCheckedChange={onKeepCopy}
            />
          </OptionList>
          {keepCopy && (
            <Field label="Copy into" htmlFor="import-copy-to">
              <Input
                id="import-copy-to"
                value={copyTo}
                onChange={(e) => onCopyTo(e.target.value)}
                className="font-mono text-body"
              />
            </Field>
          )}
        </div>
      )}
    </div>
  )
}

function ArchiveRow({
  archive,
  busy,
  disabled,
  onImport,
}: {
  archive: CatalogueArchive
  busy: boolean
  disabled: boolean
  onImport: () => void
}) {
  const state = archiveState(archive)
  const facts = [
    contentsWords(archive.contents),
    bytes(archive.bytes),
    archive.encrypted
      ? archive.recipient
        ? archive.openable
          ? "sealed · a key here opens it"
          : `sealed to ${shortKey(archive.recipient)}`
        : "sealed"
      : undefined,
  ].filter(Boolean)
  return (
    <Row
      leading={
        archive.encrypted ? (
          <LockClosed aria-hidden className="size-4 text-muted-foreground" />
        ) : (
          <Archive aria-hidden className="size-4 text-muted-foreground" />
        )
      }
      title={
        <span className="numeric" title={archive.key}>
          {archive.startedAt ? timestamp(archive.startedAt) : archive.name}
        </span>
      }
      subtitle={
        <>
          {facts.join(" · ")}
          {archive.problem && state.state !== "importable" && (
            <span className="block text-hint text-muted-foreground">{archive.problem}</span>
          )}
        </>
      }
      trailing={
        <span className="flex items-center gap-3">
          <Status tone={state.tone} label={state.label} />
          {state.state === "importable" && (
            <Button
              size="xs"
              variant="outline"
              disabled={disabled}
              aria-label={`Import the archive of ${archive.startedAt ? timestamp(archive.startedAt) : archive.name}`}
              onClick={onImport}
            >
              {busy ? "Importing…" : "Import"}
            </Button>
          )}
        </span>
      }
    />
  )
}
