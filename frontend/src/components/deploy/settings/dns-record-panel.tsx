"use client"

import { useState } from "react"
import Link from "next/link"
import { Warning } from "@/components/icons"
import { get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentDNSRecordPlan } from "@/lib/types"
import { Well } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import {
  byHand,
  changeTone,
  changeVerb,
  currentLine,
  managedLine,
  opensCertificates,
  recordButton,
  recordState,
  refusalTitle,
  setLine,
  ttlLabel,
  whereLine,
} from "@/components/deploy/settings/dns-record-model"

/**
 * A domain's DNS record — what the name should hold, what its zone holds, and
 * the press that writes it with the DNS credentials saved under
 * Certificates.
 *
 * Opening the sheet asks the server for the dry run: A and AAAA to this
 * server's public addresses, or for a name served through Cloudflare Tunnel a
 * proxied CNAME to the tunnel, each beside what the zone holds now, then the
 * zone and provider they would be written through and, when this dashboard
 * wrote them before, who did and when. The one button says what it does in
 * the dry run's words — create, correct, or replace a record not written
 * here — and nothing is written until it is pressed. What cannot be written
 * from here is a notice with the server's sentence, leading to Certificates
 * when saving a provider's token is the answer.
 */
export function DNSRecordPanel({
  projectId,
  hostname,
  onOpenChange,
}: {
  projectId: number
  /** The name whose record the sheet is open on; none closes it. */
  hostname: string | undefined
  onOpenChange: (open: boolean) => void
}) {
  const path = hostname
    ? `/deploy/${projectId}/domains/${encodeURIComponent(hostname)}/dns-record`
    : undefined
  // Asked once as the sheet opens on a name, never polled: each read asks
  // the provider's API with the saved credentials.
  const dryRun = usePoll(
    (signal) => get<DeploymentDNSRecordPlan>(path!, undefined, signal),
    0,
    [path],
    { enabled: Boolean(path) },
  )
  // What the write answered, which is the sheet's until it is asked again.
  const [written, setWritten] = useState<DeploymentDNSRecordPlan>()
  const [writing, setWriting] = useState(false)
  const plan = written?.hostname === hostname ? written : dryRun.data
  // Closing forgets the write's answer: the sheet opened again asks afresh.
  const close = (open: boolean) => {
    if (!open) setWritten(undefined)
    onOpenChange(open)
  }

  const write = async () => {
    if (!path || !plan) return
    setWriting(true)
    try {
      const answer = await post<DeploymentDNSRecordPlan>(
        path,
        plan.foreign ? { replace: true } : undefined,
      )
      setWritten(answer)
      notify.success("DNS record written")
    } catch (failure) {
      notify.error("Could not write the record", failure)
      setWritten(undefined)
      dryRun.refresh()
    } finally {
      setWriting(false)
    }
  }

  const shown = plan?.hostname === hostname ? plan : undefined
  const state = shown ? recordState(shown) : undefined
  const managed = shown ? managedLine(shown.managed) : null
  const where = shown ? whereLine(shown) : null

  return (
    <SidePanel
      open={Boolean(hostname)}
      onOpenChange={close}
      title={hostname ?? "DNS record"}
      description="The DNS record this domain needs, what its zone holds, and the press that writes it."
      width="md"
      initialFocus="body"
      footer={
        <>
          <Button variant="outline" onClick={() => close(false)} disabled={writing}>
            Close
          </Button>
          {shown?.writable && (
            <Button onClick={() => void write()} pending={writing}>
              {recordButton(shown)}
            </Button>
          )}
        </>
      }
    >
      {dryRun.error && !shown ? (
        <ErrorState error={dryRun.error} onRetry={dryRun.refresh} retryable />
      ) : !shown || !state ? (
        <LoadingRows rows={3} />
      ) : (
        <div className="space-y-5">
          <div className="space-y-1.5">
            <div className="flex min-w-0 items-center justify-between gap-3">
              <p className="text-body font-medium">DNS record</p>
              <Status
                key={state.label}
                tone={state.tone}
                label={state.label}
                className="animate-rise"
              />
            </div>
            {/* A refusal is the notice's sentence below, said once. */}
            {!shown.refusal && (
              <p className="text-hint leading-relaxed text-muted-foreground">{shown.summary}</p>
            )}
          </div>

          {shown.changes.length > 0 && (
            <ul aria-label="Records" className="space-y-3">
              {shown.changes.map((change) => {
                const now = currentLine(change)
                const values = change.want.length > 0 ? change.want : change.current
                return (
                  <li
                    key={`${change.type}:${change.action}`}
                    className="min-w-0 animate-rise space-y-1"
                  >
                    <div className="flex min-w-0 items-center justify-between gap-3 text-xs">
                      <span className="text-muted-foreground">
                        {[ttlLabel(change.ttl), change.proxied ? "Proxied" : ""]
                          .filter(Boolean)
                          .join(" · ")}
                      </span>
                      <Status tone={changeTone(change)} label={changeVerb(change)} />
                    </div>
                    <Well className="min-w-0 py-2 break-all whitespace-pre-wrap">
                      {setLine({ type: change.type, name: change.name, values })}
                    </Well>
                    {now && <p className="text-hint text-muted-foreground">{now}</p>}
                  </li>
                )
              })}
            </ul>
          )}

          {byHand(shown).length > 0 && (
            <ul aria-label="Records to create by hand" className="space-y-3">
              {byHand(shown).map((set) => (
                <li key={set.type} className="min-w-0 space-y-1">
                  <div className="flex min-w-0 items-center justify-between gap-3 text-xs">
                    <span className="text-muted-foreground">
                      {[ttlLabel(set.ttl), set.proxied ? "Proxied" : ""]
                        .filter(Boolean)
                        .join(" · ")}
                    </span>
                    <Status tone="warning" label="To create by hand" />
                  </div>
                  <Well className="min-w-0 py-2 break-all whitespace-pre-wrap">{setLine(set)}</Well>
                </li>
              ))}
            </ul>
          )}

          {(where || managed) && (
            <div className="space-y-1 text-hint text-muted-foreground">
              {where && <p>{where}</p>}
              {managed && <p className="text-foreground">{managed}</p>}
            </div>
          )}

          {shown.refusal && (
            <Notice
              tone="warning"
              icon={Warning}
              title={refusalTitle(shown)}
              className="animate-rise"
            >
              {shown.refusal}{" "}
              {opensCertificates(shown) && (
                <Link
                  href="/proxy/certificates"
                  className="text-foreground underline underline-offset-4"
                >
                  Open Certificates
                </Link>
              )}
              {shown.refusalCode === "tunnel_not_connected" && (
                <Link href="/proxy/tunnel" className="text-foreground underline underline-offset-4">
                  Open Cloudflare Tunnel
                </Link>
              )}
            </Notice>
          )}
        </div>
      )}
    </SidePanel>
  )
}
