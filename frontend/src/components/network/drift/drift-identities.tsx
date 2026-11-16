"use client"

import { InfoTip } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import type { DriftReport } from "@/lib/network/drift"
import { ShortDigest } from "@/components/network/drift/marks"

/**
 * The three identities the report keeps apart: the exact bytes of the saved
 * file, the normalised configuration those bytes say, and the candidate the
 * recovery journal holds. A journal candidate that is not the saved file is
 * amber, and the tip beside its phase says why that alone is not drift.
 */
export function DriftIdentities({ report }: { report: DriftReport }) {
  return (
    <Panel plain aria-label="Configuration identities">
      <PanelHeader title="Configuration identities" />
      <PanelBody>
        <dl className="grid grid-cols-start-main gap-x-6 gap-y-2 text-body">
          <dt className="text-muted-foreground">Saved file</dt>
          <dd>
            <ShortDigest value={report.savedGeneration} />
          </dd>
          <dt className="text-muted-foreground">Normalized configuration</dt>
          <dd>
            <ShortDigest value={report.canonicalGeneration} />
          </dd>
          <dt className="text-muted-foreground">Journal candidate</dt>
          <dd>
            <ShortDigest value={report.change?.generation} against={report.savedGeneration} />
          </dd>
          {report.change && (
            <>
              <dt className="text-muted-foreground">Journal phase</dt>
              <dd className="flex items-center gap-1.5">
                {report.change.phase.replaceAll("_", " ")}
                <InfoTip label="Why a candidate can differ">
                  A recovered candidate can differ from the restored saved configuration.
                </InfoTip>
              </dd>
            </>
          )}
        </dl>
      </PanelBody>
    </Panel>
  )
}
