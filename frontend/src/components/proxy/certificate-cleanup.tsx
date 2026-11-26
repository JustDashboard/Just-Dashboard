"use client"

import { useState } from "react"
import { Trash } from "@/components/icons"
import { del, errorMessage, post } from "@/lib/api"
import { certSource, cleanupTicked, unusedDeletable } from "@/lib/certificates"
import { calendarDate, plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { CaddyCertificateDelete, Certificate, Job } from "@/lib/types"
import { Modal } from "@/components/modal"
import { ROW_BLEED } from "@/components/row-list"
import { Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { cn } from "@/lib/utils"
import { SOURCE_LABELS } from "@/components/proxy/certificate-inventory"

/**
 * The certificates nothing on this server serves, as a checklist to delete:
 * certbot's lineages, the imports and what Caddy holds, expired or not.
 * Offered only when there is one.
 *
 * It was "Delete expired and unused" over lineages and imports only, so a
 * server whose every certificate is Caddy's — routes left by deleted
 * deployments, each with a certificate Caddy kept — was offered nothing. Each
 * line names the certificate and where it came from. One that has expired,
 * or that Caddy holds and would order again, is ticked; a valid lineage or
 * import is listed unticked, since it may be kept for something this server
 * cannot see.
 *
 * Lineages go through one certbot job, which revokes nothing; imports and
 * Caddy's are removed at once, each checked again by the server, which keeps
 * one a site or a route has started serving since.
 */
export function UnusedCleanup({
  certs,
  certbotBusy,
  certbotGone,
  onJob,
  onDeleted,
}: {
  certs: Certificate[]
  certbotBusy: boolean
  /** certbot is not here to delete its lineages with. */
  certbotGone: boolean
  onJob: (job: Job) => void
  onDeleted: () => void
}) {
  const candidates = certs.filter(
    (cert) => unusedDeletable(cert) && !(cert.source === "certbot" && certbotGone),
  )
  const [open, setOpen] = useState(false)
  // What the operator changed from the default tick, by path.
  const [flipped, setFlipped] = useState<Set<string>>(new Set())
  const [pending, setPending] = useState(false)
  if (candidates.length === 0) return null

  const ticked = (cert: Certificate) => cleanupTicked(cert) !== flipped.has(cert.path)
  const chosen = candidates.filter(ticked)
  const lineages = chosen.filter((cert) => cert.source === "certbot")
  const toggle = (path: string) =>
    setFlipped((current) => {
      const next = new Set(current)
      if (next.has(path)) next.delete(path)
      else next.add(path)
      return next
    })

  const submit = async () => {
    setPending(true)
    const failed: string[] = []
    let removed = 0
    for (const cert of chosen.filter((c) => c.source !== "certbot")) {
      try {
        if (cert.source === "caddy") {
          await del<CaddyCertificateDelete>("/certificates/caddy", { body: { path: cert.path } })
        } else {
          await del(`/certificates/imported/${encodeURIComponent(cert.name)}`)
        }
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
      notify.success(`Deleted ${plural(removed, "certificate")}`)
    }
    setOpen(false)
    setFlipped(new Set())
    onDeleted()
  }

  return (
    <>
      <Notice
        className="mt-4"
        data-slot="unused-cleanup"
        title={plural(candidates.length, "unused certificate")}
      >
        <Button size="xs" variant="outline" onClick={() => setOpen(true)}>
          <Trash className="size-3.5" />
          Delete unused
        </Button>
      </Notice>
      <Modal
        open={open}
        onOpenChange={setOpen}
        title="Delete unused certificates"
        description="Certificates nothing on this server serves, to delete."
        footer={
          <Button
            variant="destructive"
            pending={pending}
            disabled={chosen.length === 0 || (lineages.length > 0 && certbotBusy)}
            onClick={submit}
          >
            Delete {plural(chosen.length, "certificate")}
          </Button>
        }
      >
        <div className="space-y-4">
          <p className="text-body text-muted-foreground">
            Nothing is revoked. Caddy orders a new certificate if a route asks for the name again; a
            certbot lineage goes with its renewal, and an import with its key. One that something
            starts serving before this runs is kept.
          </p>
          {lineages.length > 0 && certbotBusy && (
            <p className="text-hint text-muted-foreground">
              certbot is running. Its lineages can be deleted once it finishes.
            </p>
          )}
          <ul aria-label="Unused certificates" className="divide-y divide-hairline">
            {candidates.map((cert) => {
              const id = `cleanup-${cert.source}-${cert.path}`
              return (
                <li
                  key={cert.path}
                  className={cn("flex min-w-0 items-center gap-3 py-2.5", ROW_BLEED)}
                >
                  <Checkbox
                    id={id}
                    checked={ticked(cert)}
                    onCheckedChange={() => toggle(cert.path)}
                  />
                  <label htmlFor={id} className="min-w-0 flex-1">
                    <span className="block text-body font-medium break-all">{cert.name}</span>
                    <span className="block text-hint break-all text-muted-foreground">
                      {cert.domains.join(", ") || "No names reported"} ·{" "}
                      {cert.expired ? "expired" : "valid until"} {calendarDate(cert.notAfter)}
                    </span>
                  </label>
                  <Tag>{SOURCE_LABELS[certSource(cert)]}</Tag>
                </li>
              )
            })}
          </ul>
        </div>
      </Modal>
    </>
  )
}
