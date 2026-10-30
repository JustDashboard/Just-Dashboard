"use client"

import { useState } from "react"
import { Key } from "@/components/icons"
import { get, post, postFile } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import { downloadText } from "@/lib/metrics-export"
import { notify } from "@/lib/toast"
import type { BackupArchiveKey } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import type { useConfirm } from "@/components/confirm-dialog"
import { Field, FormNote } from "@/components/form"
import { Modal } from "@/components/modal"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"
import { keyReach } from "@/components/backups/encryption"

type Confirm = ReturnType<typeof useConfirm>["confirm"]

/**
 * The server's backup key: the one key every encrypted archive is sealed to,
 * by its public half, with when it was made and whether a copy has left.
 *
 * It is drawn only once there is something to say — a job that encrypts, or a
 * key one made — because a page whose jobs all write in the clear has no key
 * to keep. And it says *not exported yet* in amber with a notice under it,
 * because an encrypted archive outlives the server only if a copy of its key
 * does: lose the server with the only key on it, and every archive sealed to
 * it goes too.
 *
 * Exporting is behind an ordinary confirmation, like the other things here
 * that cannot be taken back: nothing can recall a copy once it has been saved.
 */
export function BackupKeyPanel({
  encrypting,
  confirm,
  importing = false,
}: {
  encrypting: boolean
  confirm: Confirm
  /** Jobs were imported, so a key from another server may be what opens them. */
  importing?: boolean
}) {
  const { can } = useAuth()
  const key = usePoll((signal) => get<BackupArchiveKey>("/backups/key", undefined, signal), 60_000)
  const [importOpen, setImportOpen] = useState(false)
  const data = key.data
  const imported = data?.imported ?? []
  if (!encrypting && !importing && !data?.exists && imported.length === 0 && !key.error) return null
  const mayExport = can("system.admin") && can("destructive")
  const mayImport = can("system.admin")

  const exportKey = () =>
    confirm({
      title: "Export the backup key",
      confirmLabel: "Export key",
      workingLabel: "Exporting the backup key…",
      subject: data?.recipient
        ? {
            mark: <ProductLogo size="sm" fallback={Key} />,
            name: "This server's backup key",
            facts: <span className="font-mono break-all">{data.recipient}</span>,
          }
        : undefined,
      description: (
        <>
          <p>
            Saves the key as a text file. It opens every archive this server has encrypted
            {data && data.encryptedArchives > 0 ? `, ${data.encryptedArchives} kept now,` : ""} and
            every one it encrypts after, wherever a copy of them ends up.
          </p>
          <p>
            Keep it somewhere other than this server, such as a password manager. Nothing here can
            take a copy back, and the export is recorded in the audit log.
          </p>
        </>
      ),
      action: async () => {
        const file = await postFile("/backups/key/export", {})
        downloadText(file.filename, await file.blob.text(), "text/plain")
        notify.success("Backup key exported", {
          description: `Saved as ${file.filename}. The file says how to open an archive with it.`,
        })
        key.refresh()
        return "reported"
      },
    })

  return (
    <Panel plain aria-label="Backup key">
      <PanelHeader
        title="Backup key"
        actions={
          <span className="flex items-center gap-3">
            {data && keyReach(data) && (
              <span className="numeric text-hint text-muted-foreground">{keyReach(data)}</span>
            )}
            {mayImport && (
              <Button
                size="sm"
                variant="ghost"
                disabled={!data}
                onClick={() => setImportOpen(true)}
              >
                Import key…
              </Button>
            )}
            {mayExport && (
              <Button size="sm" variant="outline" disabled={!data} onClick={() => void exportKey()}>
                Export key…
              </Button>
            )}
          </span>
        }
      />
      <PanelBody flush className="space-y-3 pt-3">
        {key.error && <ErrorState error={key.error} onRetry={key.refresh} retryable />}
        {!data && !key.error && <LoadingRows rows={1} />}
        {data && (
          <RowList>
            <Row
              leading={<ProductLogo size="sm" fallback={Key} />}
              title="This server's backup key"
              mono={data.exists}
              subtitle={
                data.exists
                  ? data.recipient
                  : "Made when a job that encrypts is saved, or when the key is exported"
              }
              trailing={
                data.exists &&
                (data.exportedAt ? (
                  <Status
                    tone="running"
                    label={
                      <span title={timestamp(data.exportedAt)}>
                        exported {relativeTime(data.exportedAt)}
                      </span>
                    }
                  />
                ) : (
                  <Status tone="warning" label="not exported yet" />
                ))
              }
            />
            {/* A key brought from another server opens what that server sealed;
                nothing new is sealed to it, so it has no export to keep up. */}
            {imported.map((other) => (
              <Row
                key={other.recipient}
                leading={<ProductLogo size="sm" fallback={Key} />}
                title="Imported key"
                mono
                subtitle={other.recipient}
                trailing={
                  <span
                    className="text-hint text-muted-foreground"
                    title={timestamp(other.importedAt)}
                  >
                    opens archives from another server · imported {relativeTime(other.importedAt)}
                  </span>
                }
              />
            ))}
          </RowList>
        )}
        {data?.exists && !data.exportedAt && (
          <Notice tone="warning" title="Keep a copy of the key off this server">
            Only this server can open its encrypted archives until the key is exported. If the
            server is lost with the only copy, the archives are lost with it.
          </Notice>
        )}
      </PanelBody>
      {importOpen && (
        <Modal
          open
          onOpenChange={setImportOpen}
          title="Import a backup key"
          description="Paste or choose the key file another server exported, so the archives it sealed open here."
        >
          <KeyImport
            onImported={() => {
              setImportOpen(false)
              key.refresh()
            }}
          />
        </Modal>
      )}
    </Panel>
  )
}

/**
 * Bringing a key exported elsewhere: the file Export key… saved, pasted or
 * chosen. The first one on a server that holds no key becomes its own, so a
 * moved or rebuilt server keeps sealing to the key already kept off it; any
 * after that only opens what was sealed to it. The text is held in this
 * component alone and sent once.
 */
export function KeyImport({
  wanted = [],
  onImported,
}: {
  /** The keys the archives in view were sealed to, by their public half. */
  wanted?: string[]
  onImported: (recipient: string) => void
}) {
  const [text, setText] = useState("")
  const [busy, setBusy] = useState(false)
  const submit = async () => {
    setBusy(true)
    try {
      const res = await post<{ recipient: string; became: "own" | "opens" | "held" }>(
        "/backups/key/import",
        { key: text },
      )
      notify.success(
        res.became === "own"
          ? "Imported as this server's backup key"
          : res.became === "opens"
            ? "Imported to open the archives sealed to it"
            : "This server already holds that key",
        { description: res.recipient },
      )
      setText("")
      onImported(res.recipient)
    } catch (err) {
      notify.error("Could not import the key", err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="space-y-3">
      {wanted.length > 0 && (
        <div className="space-y-1">
          <p className="eyebrow">Sealed to</p>
          <ul className="space-y-0.5 font-mono text-xs break-all">
            {wanted.map((recipient) => (
              <li key={recipient}>{recipient}</li>
            ))}
          </ul>
        </div>
      )}
      <Field
        label="Key file"
        htmlFor="backup-key-import"
        hint="just-dashboard-backup-key.txt, or its AGE-SECRET-KEY-1 line. Sealed with this server's master key on arrival."
      >
        <Textarea
          id="backup-key-import"
          value={text}
          onChange={(event) => setText(event.target.value)}
          rows={4}
          spellCheck={false}
          autoComplete="off"
          className="font-mono text-xs"
          placeholder="AGE-SECRET-KEY-1…"
        />
      </Field>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <input
          type="file"
          accept=".txt,text/plain"
          aria-label="Open a saved key"
          className="text-hint text-muted-foreground file:mr-2 file:rounded-md file:border file:border-hairline file:bg-transparent file:px-2 file:py-1 file:text-foreground"
          onChange={(event) => {
            const file = event.target.files?.[0]
            if (file) void file.text().then(setText)
          }}
        />
        <Button size="sm" disabled={busy || !text.trim()} onClick={() => void submit()}>
          {busy ? "Importing…" : "Import key"}
        </Button>
      </div>
      <FormNote>
        Anyone holding this file can open every archive sealed to it. Import it only on a server you
        administer.
      </FormNote>
    </div>
  )
}
