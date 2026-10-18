"use client"

import { Key } from "@/components/icons"
import { get, postFile } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import { downloadText } from "@/lib/metrics-export"
import { notify } from "@/lib/toast"
import type { BackupArchiveKey } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import type { useConfirm } from "@/components/confirm-dialog"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
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
export function BackupKeyPanel({ encrypting, confirm }: { encrypting: boolean; confirm: Confirm }) {
  const { can } = useAuth()
  const key = usePoll((signal) => get<BackupArchiveKey>("/backups/key", undefined, signal), 60_000)
  const data = key.data
  if (!encrypting && !data?.exists && !key.error) return null
  const mayExport = can("system.admin") && can("destructive")

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
          </RowList>
        )}
        {data?.exists && !data.exportedAt && (
          <Notice tone="warning" title="Keep a copy of the key off this server">
            Only this server can open its encrypted archives until the key is exported. If the
            server is lost with the only copy, the archives are lost with it.
          </Notice>
        )}
      </PanelBody>
    </Panel>
  )
}
