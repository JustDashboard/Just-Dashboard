"use client"

import type { NetworkChangeStatus } from "@/lib/types"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Status } from "@/components/status-dot"
import { Notice } from "@/components/state"
import {
  BOOT_STATE,
  changeStatus,
  PERSISTENCE_STATE,
  RUNTIME_STATE,
  WATCHDOG_STATE,
} from "./change-reading"

/** A saved host change is separate evidence from reconnecting through it. */
export function NetworkChange({ change }: { change: NetworkChangeStatus }) {
  const status = changeStatus(change)
  return (
    <Panel plain aria-label="Latest network change">
      <PanelHeader
        title="Latest network change"
        actions={<Status tone={status.tone} label={status.label} />}
      />
      <PanelBody className="space-y-3">
        <DetailList>
          <Detail label="Runtime">{RUNTIME_STATE[change.runtime] ?? "Unknown"}</Detail>
          <Detail label="Persistence">{PERSISTENCE_STATE[change.persistence] ?? "Unknown"}</Detail>
          <Detail label="Boot restoration">{BOOT_STATE[change.boot] ?? "Unknown"}</Detail>
          <Detail label="Host recovery">{WATCHDOG_STATE[change.watchdog] ?? "Unknown"}</Detail>
        </DetailList>
        <p className="text-hint text-muted-foreground">
          These are the host&rsquo;s apply, save and recovery outcomes. Browser reconnect and
          application reachability have not been confirmed by this status.
        </p>
        {Boolean(change.recoveryErrors?.length) && (
          <div role="alert">
            <Notice tone="danger" title="Recovery needs attention">
              <ul className="list-inside list-disc space-y-1">
                {change.recoveryErrors?.map((error, index) => (
                  <li key={index}>{error}</li>
                ))}
              </ul>
            </Notice>
          </div>
        )}
        <details className="text-hint text-muted-foreground">
          <summary className="cursor-pointer focus-ring">Change record</summary>
          <dl className="mt-2 space-y-1 break-all">
            <div>
              <dt className="inline">Updated: </dt>
              <dd className="inline">
                <time dateTime={change.updatedAt}>{change.updatedAt}</time>
              </dd>
            </div>
            <div>
              <dt className="inline">ID: </dt>
              <dd className="inline font-mono">{change.id || "unavailable"}</dd>
            </div>
            <div>
              <dt className="inline">Generation: </dt>
              <dd className="inline font-mono">{change.generation || "unavailable"}</dd>
            </div>
          </dl>
        </details>
      </PanelBody>
    </Panel>
  )
}
