"use client"

import { useState } from "react"
import { Trash } from "@/components/icons"
import { del, errorMessage, post } from "@/lib/api"
import { calendarDate } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { Certificate, Job } from "@/lib/types"
import { Modal } from "@/components/modal"
import { ROW_BLEED } from "@/components/row-list"
import { Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { cn } from "@/lib/utils"

/**
 * An expired certbot lineage or import that no site names: nothing serves
 * it, and a lineage's renewal fails on it every run.
 */
export function expiredUnused(cert: Certificate): boolean {
  return (
    (cert.source === "certbot" || cert.source === "imported") &&
    !cert.error &&
    cert.expired &&
    cert.usedBy.length === 0
  )
}

/**
 * The expired certificates no site names, as a checklist to delete. Offered
 * only when there is one. Lineages go through one certbot job, which revokes
 * nothing; imports are removed at once.
 */
export function ExpiredCleanup({
  certs,
  certbotBusy,
  onJob,
  onDeleted,
}: {
  certs: Certificate[]
  certbotBusy: boolean
  onJob: (job: Job) => void
  onDeleted: () => void
}) {
  const candidates = certs.filter(expiredUnused)
  const [open, setOpen] = useState(false)
  const [unchecked, setUnchecked] = useState<Set<string>>(new Set())
  const [pending, setPending] = useState(false)
  if (candidates.length === 0) return null

  const chosen = candidates.filter((cert) => !unchecked.has(cert.path))
  const lineages = chosen.filter((cert) => cert.source === "certbot")
  const toggle = (path: string) =>
    setUnchecked((current) => {
      const next = new Set(current)
      if (next.has(path)) next.delete(path)
      else next.add(path)
      return next
    })

  const submit = async () => {
    setPending(true)
    const failed: string[] = []
    let removed = 0
    for (const cert of chosen.filter((c) => c.source === "imported")) {
      try {
        await del(`/certificates/imported/${encodeURIComponent(cert.name)}`)
        removed++
      } catch (err) {
        failed.push(`${cert.name}: ${errorMessage(err)}`)
      }
    }
    if (lineages.length > 0) {
      try {
        onJob(
          await post<Job>("/certificates/delete", {
            names: lineages.map((cert) => cert.name),
            force: false,
          }),
        )
      } catch (err) {
        failed.push(errorMessage(err))
      }
    }
    setPending(false)
    if (failed.length > 0) {
      notify.warning("Some certificates were not deleted", { description: failed.join(" · ") })
    } else if (removed > 0) {
      notify.success(
        `Deleted ${removed} imported ${removed === 1 ? "certificate" : "certificates"}`,
      )
    }
    setOpen(false)
    setUnchecked(new Set())
    onDeleted()
  }

  return (
    <>
      <Notice
        className="mt-4"
        title={`${candidates.length} expired ${candidates.length === 1 ? "certificate is" : "certificates are"} named by no site`}
      >
        <div className="flex flex-wrap items-center gap-2">
          <span>
            Nothing serves them, and certbot fails every renewal run on an expired lineage.
          </span>
          <Button size="xs" variant="outline" onClick={() => setOpen(true)}>
            <Trash className="size-3.5" />
            Delete expired and unused
          </Button>
        </div>
      </Notice>
      <Modal
        open={open}
        onOpenChange={setOpen}
        title="Delete expired and unused certificates"
        description="Expired certificates that no site names, to delete."
        footer={
          <Button
            variant="destructive"
            pending={pending}
            disabled={chosen.length === 0 || (lineages.length > 0 && certbotBusy)}
            onClick={submit}
          >
            Delete {chosen.length} {chosen.length === 1 ? "certificate" : "certificates"}
          </Button>
        }
      >
        <div className="space-y-4">
          <p className="text-body text-muted-foreground">
            certbot deletes a lineage&apos;s files and its renewal configuration and revokes
            nothing. An import goes with its key and the copies kept when it was replaced. A site
            that starts naming one before this runs keeps it.
          </p>
          {lineages.length > 0 && certbotBusy && (
            <p className="text-hint text-muted-foreground">
              certbot is running. Its lineages can be deleted once it finishes.
            </p>
          )}
          <ul aria-label="Expired and unused certificates" className="divide-y divide-hairline">
            {candidates.map((cert) => {
              const id = `cleanup-${cert.source}-${cert.name}`
              return (
                <li
                  key={cert.path}
                  className={cn("flex min-w-0 items-center gap-3 py-2.5", ROW_BLEED)}
                >
                  <Checkbox
                    id={id}
                    checked={!unchecked.has(cert.path)}
                    onCheckedChange={() => toggle(cert.path)}
                  />
                  <label htmlFor={id} className="min-w-0 flex-1">
                    <span className="block text-body font-medium break-all">{cert.name}</span>
                    <span className="block text-hint break-all text-muted-foreground">
                      {cert.domains.join(", ") || "No names reported"} · expired{" "}
                      {calendarDate(cert.notAfter)}
                    </span>
                  </label>
                  <Tag>{cert.source}</Tag>
                </li>
              )
            })}
          </ul>
        </div>
      </Modal>
    </>
  )
}
