"use client"

import { useMemo } from "react"
import { get } from "@/lib/api"
import type { CertbotState, LogSource } from "@/lib/types"
import { fileSource, journalSource } from "@/lib/log-sources"
import { usePoll } from "@/hooks/use-poll"
import { ServiceLogs, type ServiceLogSource } from "@/components/logs/service-logs"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { LoadingRows } from "@/components/state"

/** certbot's own log, where every run — the timer's and a click's — writes what it did. */
export const LETSENCRYPT_LOG = "/var/log/letsencrypt/letsencrypt.log"

/**
 * The unit a renewal runs as: the timer's service where a timer schedules it
 * (`snap.certbot.renew.timer` starts `snap.certbot.renew.service`), else the
 * packaged one.
 */
export function renewalUnit(state: CertbotState | undefined): string {
  const timer = [state?.renewSource, state?.renewUnit].find((unit) => unit?.endsWith(".timer"))
  return timer ? timer.replace(/\.timer$/, ".service") : "certbot.service"
}

/**
 * The renewals nobody pressed a button for: what certbot did each time its
 * timer ran — which names it renewed, which were not due, which failed and
 * why — read on the page that says a certificate is expiring. The job
 * consoles above are the runs started here; this is every run.
 *
 * certbot writes its own log whichever way it was started, so that is read
 * first when the host has it; the unit's journal says when the timer fired
 * and how it exited, and stands alone on a host whose certbot logs only
 * there.
 */
export function RenewalLog({ certbot }: { certbot: CertbotState | undefined }) {
  // One question about the file before the pane opens: a source the pane
  // cannot read would be a picker entry that answers with an error.
  const file = usePoll(
    (signal) => get<LogSource>("/logs/source", { source: fileSource(LETSENCRYPT_LOG) }, signal),
    0,
  )
  const settled = file.data !== undefined || file.error !== undefined
  const unit = renewalUnit(certbot)
  const sources = useMemo<ServiceLogSource[]>(() => {
    const journal: ServiceLogSource = {
      id: journalSource(unit),
      label: unit,
      kind: "journal",
      lens: "certbot",
      product: "lets-encrypt",
    }
    // Missing, or outside the directories the dashboard may read: either
    // way the journal is what there is to read.
    if (!file.data) return [journal]
    return [
      {
        id: fileSource(LETSENCRYPT_LOG),
        label: "letsencrypt.log",
        kind: "system",
        path: LETSENCRYPT_LOG,
        lens: "certbot",
        product: "lets-encrypt",
      },
      journal,
    ]
  }, [file.data, unit])

  return (
    <Panel plain>
      <PanelHeader title="Renewals" />
      <PanelBody flush className="pt-3">
        {!settled ? (
          <LoadingRows rows={4} />
        ) : (
          <ServiceLogs
            sources={sources}
            storageKey="proxy.certificates.renewals"
            pickerLabel="Renewal log"
            paneClassName="h-[min(60vh,32rem)] min-h-80"
          />
        )}
      </PanelBody>
    </Panel>
  )
}
