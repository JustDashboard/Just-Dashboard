"use client"

import { useState } from "react"
import type { PollState } from "@/hooks/use-poll"
import type { DNSView, HostRecords } from "@/lib/types"
import { FormSection, FormSections } from "@/components/form"
import { ErrorState, Notice } from "@/components/state"
import { SaveBarProvider } from "@/components/deploy/settings/save-bar"
import { Bar } from "@/components/network/dns/ghost"
import { HostRecordsEditor } from "@/components/network/dns/host-records"
import { OwnerHandoff } from "@/components/network/dns/resolver-owner"
import {
  ResolverOutcomeNote,
  ResolverSettings,
  type ResolverOutcome,
} from "@/components/network/dns/upstreams"

/** The sections the column holds, for the heads it draws while the first read is out. */
const SECTIONS = ["Upstreams", "DNS over TLS", "DNSSEC", "Cache", "Host records"]

/**
 * The resolver's settings, in the shape every settings page in the product
 * has (`deploy/settings/setting-card.tsx`): one centred column of sections
 * divided by hairlines, each head over its fields carrying what the section
 * currently is, and one Save floating at the foot while an edit is held.
 *
 * They were an inline form in the middle of a reading page, a helper sentence
 * under every field and an Apply of its own, with the host's records four
 * screens further down behind a second Save. Both are forms of this column
 * now. The resolver's is saved first and asks before it is: a wrong upstream
 * stops every lookup on the host, so its confirmation carries the checks the
 * server will hold the change to, and what they found stands at the head of
 * the column.
 */
export function SettingsView({
  view,
  hosts,
  onChanged,
}: {
  /** Undefined while the first read is out. */
  view?: DNSView
  hosts: PollState<HostRecords>
  onChanged: () => void
}) {
  const [outcome, setOutcome] = useState<ResolverOutcome>()
  if (!view) {
    return (
      <div aria-hidden className="mx-auto w-full max-w-3xl min-w-0 pb-20">
        <FormSections>
          {SECTIONS.map((title) => (
            <FormSection key={title} aside title={title} hint={<Bar className="w-40" />}>
              <div
                data-slot="skeleton"
                className="h-9 w-full animate-pulse rounded-md bg-muted sm:max-w-md"
              />
            </FormSection>
          ))}
        </FormSections>
      </div>
    )
  }
  const { resolved, resolvConf, owner } = view
  // Resolved must be running for the dashboard's drop-in to be read by anything.
  const readOnly = !resolved.active
    ? resolved.installed
      ? "systemd-resolved is installed but not running, so a drop-in written here would be read by nothing."
      : `systemd-resolved is not running here; ${owner?.name ?? resolvConf.managedBy ?? "something else"} decides ${resolvConf.path}, so the upstreams are changed there rather than on this page. ${owner?.handoff ?? ""}`.trim()
    : undefined
  return (
    <SaveBarProvider>
      {/* The room at the foot is the save bar's, so it never lies over the last field. */}
      <div className="mx-auto w-full max-w-3xl min-w-0 space-y-8 pb-20">
        {outcome && <ResolverOutcomeNote outcome={outcome} />}
        {readOnly ? (
          <Notice title="These settings are read here, not written" tone="warning">
            {readOnly} {owner && <OwnerHandoff owner={owner} />}
          </Notice>
        ) : (
          owner &&
          !owner.dashboardWrites && (
            <Notice title={`${owner.name} writes ${resolvConf.path}`} tone="warning">
              What is saved here is resolved&rsquo;s own, and does not reach what programs ask.{" "}
              {owner.handoff} <OwnerHandoff owner={owner} />
            </Notice>
          )
        )}
        <FormSections>
          <ResolverSettings
            view={view}
            readOnly={readOnly}
            onChanged={onChanged}
            onOutcome={setOutcome}
          />
          {hosts.data ? (
            <HostRecordsEditor records={hosts.data} onSaved={hosts.refresh} />
          ) : (
            <FormSection
              aside
              title="Host records"
              className="py-8"
              hint={hosts.error ? undefined : <Bar className="w-40" />}
            >
              {hosts.error ? (
                <ErrorState error={hosts.error} onRetry={hosts.refresh} />
              ) : (
                <div
                  data-slot="skeleton"
                  className="h-9 w-full animate-pulse rounded-md bg-muted"
                />
              )}
            </FormSection>
          )}
        </FormSections>
      </div>
    </SaveBarProvider>
  )
}
