"use client"

import { useState } from "react"
import { Archive, RotateCounterClockwise, Trash } from "@/components/icons"
import { del, errorMessage, post } from "@/lib/api"
import { bytes, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { SiteBackup, SitePlacementResult } from "@/lib/types"
import type { PollState } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { Field, FormSection, FormSections } from "@/components/form"
import { Modal } from "@/components/modal"
import { ROW_BLEED } from "@/components/row-list"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { VerbActions } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"

const SITE_NAME = /^[a-z0-9][a-z0-9._-]{0,63}$/

function backupPath(b: SiteBackup) {
  return `/proxy/site-backups/${encodeURIComponent(b.layout)}/${encodeURIComponent(b.file)}`
}

/**
 * The copies deletes leave beside a site — <name>.bak — and the leftovers of
 * editors and package managers. nginx reads none of them and the Sites list
 * does not show them, so without this they are found only from a terminal. A
 * restore is tested like an import; a purge is for good.
 */
export function SiteBackupsPanel({
  backups,
  onRestored,
}: {
  /** Read by the page, which reads it again after every delete. */
  backups: PollState<SiteBackup[]>
  onRestored: (res: SitePlacementResult) => void
}) {
  const { confirm, dialog } = useConfirm()
  const [restoring, setRestoring] = useState<SiteBackup | null>(null)
  const { data, error, loading, refresh } = backups

  const purge = (b: SiteBackup) =>
    confirm({
      title: `Delete ${b.file}`,
      confirmLabel: "Delete",
      description: (
        <p>
          <span className="font-mono">{b.path}</span> is removed for good.
          {b.site && !b.siteExists
            ? ` It is the only copy of ${b.site} left on this host.`
            : " nginx never read it, so no site changes."}
        </p>
      ),
      action: async () => {
        await del(backupPath(b))
        refresh()
      },
    })

  return (
    <>
      <FormSections>
        <FormSection
          aside
          title="Deleted & backups"
          hint={`${data?.length ?? 0} copies nginx does not read`}
        >
          {loading ? (
            <LoadingRows rows={2} />
          ) : error ? (
            <ErrorState error={error} />
          ) : !data?.length ? (
            <EmptyState
              icon={Archive}
              title="No backups"
              description="Deleting a site keeps its file here as <name>.bak, to restore or delete."
              className="mt-2"
            />
          ) : (
            <ul className="animate-rise divide-y divide-hairline">
              {data.map((b) => (
                <li
                  key={b.path}
                  className={cn(
                    "group flex min-w-0 items-start gap-3 py-3 transition-colors hover:bg-row-hover",
                    ROW_BLEED,
                  )}
                >
                  <div className="min-w-0 flex-1 space-y-1">
                    <div className="flex min-w-0 flex-wrap items-baseline gap-x-2">
                      <span className="font-mono text-body font-medium break-all">{b.file}</span>
                      <span className="text-hint text-muted-foreground">
                        {b.layout} · {bytes(b.size)} · {relativeTime(b.modified)}
                      </span>
                    </div>
                    <div className="flex flex-wrap items-center gap-1.5 text-hint text-muted-foreground">
                      {!b.restorable ? (
                        <span>Cannot be restored: {b.reason}.</span>
                      ) : b.site && b.siteExists ? (
                        <>
                          <Tag mono>{b.site}</Tag> exists — restore it under another name
                        </>
                      ) : b.site ? (
                        <>
                          Restores as <Tag mono>{b.site}</Tag>
                        </>
                      ) : (
                        <span>Restores under a name you give it.</span>
                      )}
                    </div>
                  </div>
                  <VerbActions
                    dim
                    className="shrink-0"
                    verbs={[
                      ...(b.restorable
                        ? [
                            {
                              key: "restore",
                              label: "Restore",
                              icon: RotateCounterClockwise,
                              run: () => setRestoring(b),
                            },
                          ]
                        : []),
                      {
                        key: "delete",
                        label: "Delete for good",
                        icon: Trash,
                        danger: true,
                        run: () => purge(b),
                      },
                    ]}
                  />
                </li>
              ))}
            </ul>
          )}
        </FormSection>
      </FormSections>
      {restoring && (
        <RestoreDialog
          key={restoring.path}
          backup={restoring}
          onClose={() => setRestoring(null)}
          onRestored={(res) => {
            refresh()
            onRestored(res)
          }}
        />
      )}
      {dialog}
    </>
  )
}

function RestoreDialog({
  backup,
  onClose,
  onRestored,
}: {
  backup: SiteBackup
  onClose: () => void
  onRestored: (res: SitePlacementResult) => void
}) {
  const [name, setName] = useState(backup.siteExists ? "" : (backup.site ?? ""))
  const [enable, setEnable] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const value = name.trim()
  const invalid = value !== "" && !SITE_NAME.test(value)
  const taken = backup.siteExists && value === backup.site

  const submit = async () => {
    if (!value || invalid || taken) return
    setBusy(true)
    setError(undefined)
    try {
      const res = await post<SitePlacementResult>(`${backupPath(backup)}/restore`, {
        as: value,
        enable,
        reload: true,
      })
      onRestored(res)
      onClose()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open
      onOpenChange={(next) => !busy && !next && onClose()}
      title={`Restore ${backup.file}`}
      size="sm"
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={busy || !value || invalid || taken} pending={busy}>
            {enable ? "Restore and reload" : "Restore"}
          </Button>
        </>
      }
    >
      <form
        className="space-y-3"
        onSubmit={(e) => {
          e.preventDefault()
          void submit()
        }}
      >
        {error && (
          <Notice title="Not restored" tone="danger">
            <span className="break-words whitespace-pre-wrap">{error}</span>
          </Notice>
        )}
        <Field
          label="Site name"
          htmlFor="site-restore-name"
          error={
            invalid
              ? "Lower-case letters, digits, dots, dashes and underscores, up to 64."
              : taken
                ? `${backup.site} exists — give the copy another name.`
                : undefined
          }
          hint={`Written into ${backup.layout} and tested by nginx; the backup stays where it is.`}
        >
          <Input
            id="site-restore-name"
            autoFocus
            autoComplete="off"
            spellCheck={false}
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="font-mono"
          />
        </Field>
        {backup.layout === "sites-available" && (
          <label className="flex items-center gap-2 text-hint text-muted-foreground">
            <Checkbox checked={enable} onCheckedChange={(v) => setEnable(v === true)} />
            Enable it, and reload nginx
          </label>
        )}
      </form>
    </Modal>
  )
}
