"use client"

import { useState } from "react"
import { Copy, Download, ShieldCheck, Warning } from "@/components/icons"
import { downloadUrl, post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { calendarDate, relativeTime } from "@/lib/format"
import { checkOutcome, checkTrouble, leafState, rootExpired } from "@/lib/private-certificates"
import { notify } from "@/lib/toast"
import type { Certificate, LocalCA, LocalCALeaf } from "@/lib/types"
import { FormNote } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { trustSentence } from "@/components/proxy/private-cert-dialog"
import { Button } from "@/components/ui/button"

/**
 * The local CA: a root of this server's own for names no public authority
 * signs. Its certificates are renewed here every day they are due and nginx
 * reloaded for the sites naming them; its root is what a device installs to
 * trust them, and anyone signed in may take it — a root is made to be
 * installed. Its key never leaves the server.
 *
 * With no CA yet the panel is an administrator's offer to make one, and
 * nothing at all to a reader, who could do nothing with it.
 */
export function LocalCAPanel({
  ca,
  error,
  loading,
  admin,
  onRetry,
  onIssue,
  onChanged,
}: {
  ca: LocalCA | undefined
  error: Error | undefined
  loading: boolean
  admin: boolean
  onRetry: () => void
  /** Opens the issuance form for the local CA. */
  onIssue: () => void
  onChanged: () => void
}) {
  const [creating, setCreating] = useState(false)

  const create = async () => {
    setCreating(true)
    try {
      const made = await post<LocalCA>("/certificates/local-ca")
      notify.success("Local CA created", {
        description: `${made.name ?? "Its root"} is ready to install on the devices that should trust what it signs.`,
      })
      onChanged()
    } catch (err) {
      notify.error("The local CA was not created", err)
    } finally {
      setCreating(false)
    }
  }

  // A reader has nothing to do with a CA that does not exist, even while
  // the page is finding out whether it does.
  if (!admin && !error && !ca?.exists) return null
  const exists = Boolean(ca?.exists)

  return (
    <Panel plain>
      <PanelHeader
        title="Local CA"
        actions={
          exists && (
            <>
              <Button size="sm" variant="outline" asChild>
                <a href={downloadUrl("/certificates/local-ca/root.pem")} download>
                  <Download className="size-3.5" />
                  Download root
                </a>
              </Button>
              {admin && (
                <Button size="sm" variant="outline" onClick={onIssue} disabled={Boolean(ca?.error)}>
                  <ShieldCheck className="size-3.5" />
                  Issue
                </Button>
              )}
            </>
          )
        }
      />
      <PanelBody flush>
        {loading ? (
          <LoadingRows rows={2} />
        ) : error ? (
          <ErrorState error={error} onRetry={onRetry} />
        ) : ca && exists ? (
          <LocalCABody ca={ca} />
        ) : (
          <div className="space-y-3 pt-1">
            <FormNote>
              A root of this server&rsquo;s own signs certificates for internal names and addresses
              that no public authority will, and renews them here. They are trusted only on devices
              where its root is installed.
            </FormNote>
            <Button size="sm" variant="outline" onClick={create} pending={creating}>
              <ShieldCheck className="size-3.5" />
              Create the local CA
            </Button>
          </div>
        )}
      </PanelBody>
    </Panel>
  )
}

function LocalCABody({ ca }: { ca: LocalCA }) {
  const trouble = checkTrouble(ca.lastCheck)
  return (
    <div className="animate-rise space-y-4 pt-1">
      <div className="space-y-1">
        <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-3 gap-y-1">
          <p className="min-w-0 text-body font-medium break-words">{ca.name}</p>
          {rootExpired(ca) ? (
            <Status verdict="critical" label="root expired" />
          ) : (
            <Status verdict="ok" label={`root valid until ${calendarDate(ca.notAfter)}`} />
          )}
        </div>
        <FormNote>
          {trustSentence("local-ca")} Its key stays on this server, readable by root only, and is
          never exported.
        </FormNote>
      </div>
      {ca.fingerprint && (
        <div className="flex min-w-0 items-start gap-2">
          <p className="min-w-0 flex-1 text-hint text-muted-foreground">
            <span className="mr-1.5">SHA-256</span>
            {/* Wrapped only between bytes, so no pair is split across lines. */}
            <span className="font-mono break-words text-foreground">
              {ca.fingerprint.replaceAll(":", ":\u200b")}
            </span>
          </p>
          <IconAction
            label="Copy the root's fingerprint"
            onClick={() => void copyText(ca.fingerprint ?? "", "Fingerprint copied")}
          >
            <Copy />
          </IconAction>
        </div>
      )}
      {ca.error && (
        <Notice tone="danger" icon={Warning} title="The local CA cannot sign">
          <span className="break-words">{ca.error}</span>
        </Notice>
      )}
      {ca.leaves.length === 0 ? (
        <EmptyNote className="py-3 text-left">Nothing issued yet.</EmptyNote>
      ) : (
        <ul aria-label="Certificates the local CA issued" className="divide-y divide-hairline">
          {ca.leaves.map((leaf) => (
            <LeafRow key={leaf.path} leaf={leaf} renewBefore={ca.renewBefore} />
          ))}
        </ul>
      )}
      {trouble ? (
        <Notice tone={trouble.tone} icon={Warning} title={trouble.title}>
          <p>Checked {relativeTime(ca.lastCheck?.at)}.</p>
          <ul className="space-y-1">
            {trouble.lines.map((line) => (
              <li key={line} className="break-words">
                {line}
              </li>
            ))}
          </ul>
        </Notice>
      ) : (
        <FormNote>
          Renewed {ca.renewBefore} days before they expire, checked daily.
          {ca.lastCheck &&
            ` Checked ${relativeTime(ca.lastCheck.at)}: ${checkOutcome(ca.lastCheck)}.`}
          {ca.nextCheck && ` Next check ${relativeTime(ca.nextCheck)}.`}
        </FormNote>
      )}
    </div>
  )
}

function LeafRow({ leaf, renewBefore }: { leaf: LocalCALeaf; renewBefore: number }) {
  const state = leafState(leaf, renewBefore)
  return (
    <li className="space-y-1 py-3 first:pt-0">
      <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-3 gap-y-1">
        <p className="min-w-0 truncate text-body font-medium" title={leaf.name}>
          {leaf.name}
        </p>
        {state === "cannot-renew" ? (
          <Status verdict="critical" label="cannot be renewed" />
        ) : state === "expired" ? (
          <Status verdict="critical" label="expired" />
        ) : state === "due" ? (
          <Status verdict="warning" label={`${leaf.daysLeft}d left, due`} />
        ) : (
          <Status verdict="ok" label={`renews ${calendarDate(leaf.renewsAt)}`} />
        )}
      </div>
      <p className="font-mono text-hint break-all text-muted-foreground">
        {leaf.domains.join(", ")}
      </p>
      <p className="text-hint break-all text-muted-foreground">
        Used by {leaf.usedBy.join(", ") || "no site"}
      </p>
      {leaf.error && <p className="text-hint break-words text-destructive">{leaf.error}</p>}
    </li>
  )
}

/**
 * The line a certificate's details carry when nothing trusts it by default:
 * one the local CA signed, or one that signed itself. Only the local CA's
 * certificates kept with the imports are renewed here — the daily check looks
 * nowhere else — so a copy a site names elsewhere says it is not.
 */
export function PrivateTrustNote({
  cert,
}: {
  cert: Pick<Certificate, "localCA" | "selfSigned" | "source">
}) {
  if (cert.localCA) {
    return (
      <FormNote>
        {trustSentence("local-ca")}{" "}
        {cert.source === "imported"
          ? "Renewed here before it expires, with nginx reloaded for the sites that name it."
          : "This copy is outside the imports, so the daily check does not renew it."}
      </FormNote>
    )
  }
  if (cert.selfSigned) return <FormNote>{trustSentence("self-signed")}</FormNote>
  return null
}
