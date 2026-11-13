"use client"

import { Key, Location, Play, Plus, Trash } from "@/components/icons"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { EmptyNote } from "@/components/state"
import { Skeleton } from "@/components/ui/skeleton"
import { Button } from "@/components/ui/button"
import { VerbActions, type Verb } from "@/components/verbs"
import { relativeTime } from "@/lib/format"
import type { ExternalVantage } from "@/lib/network-external"
import { sourceState } from "@/components/network/external/reading"

/**
 * The controlled sources, one card each: where the operator says it is, how
 * it is placed, whether it is enrolled, and when it last polled. Pressing an
 * enrolled source runs a check from it; one waiting on its credential shows
 * that credential while this page still holds it.
 */
export function ExternalSources({
  vantages,
  loading,
  canAdmin,
  canRevoke,
  credentialFor,
  busy,
  onEnroll,
  onCheck,
  onCredential,
  onRevoke,
}: {
  vantages: ExternalVantage[]
  loading: boolean
  canAdmin: boolean
  canRevoke: boolean
  /** The source whose one-use credential this page still holds. */
  credentialFor?: string
  busy: boolean
  onEnroll: () => void
  onCheck: (vantage: ExternalVantage) => void
  onCredential: () => void
  onRevoke: (vantage: ExternalVantage) => void
}) {
  return (
    <Panel plain aria-label="Sources">
      <PanelHeader
        title="Sources"
        actions={
          <span className="flex items-center gap-3">
            {vantages.length > 0 && (
              <span className="numeric text-hint text-muted-foreground">{vantages.length}</span>
            )}
            {canAdmin && (
              <Button size="sm" variant="outline" onClick={onEnroll}>
                <Plus className="size-3.5" />
                Enroll a source
              </Button>
            )}
          </span>
        }
      />
      <PanelBody>
        {loading ? (
          <div aria-hidden className="space-y-2">
            {[0, 1].map((i) => (
              <Skeleton key={i} className="h-16 rounded-xl" />
            ))}
          </div>
        ) : vantages.length === 0 ? (
          <EmptyNote>No sources enrolled</EmptyNote>
        ) : (
          <ChoiceList aria-label="Controlled sources">
            {vantages.map((v) => {
              const state = sourceState(v)
              const enrolled = Boolean(v.enrolledAt && !v.revokedAt)
              const holdsCredential = credentialFor === v.id
              const verbs: Verb[] = []
              if (canAdmin && enrolled)
                verbs.push({
                  key: "check",
                  label: "Check from here",
                  icon: Play,
                  run: () => onCheck(v),
                })
              if (holdsCredential)
                verbs.push({
                  key: "credential",
                  label: "Show credential",
                  icon: Key,
                  run: onCredential,
                })
              if (canRevoke && !v.revokedAt)
                verbs.push({
                  key: "revoke",
                  label: "Revoke",
                  icon: Trash,
                  danger: true,
                  disabled: busy,
                  run: () => onRevoke(v),
                })
              const press =
                canAdmin && enrolled ? () => onCheck(v) : holdsCredential ? onCredential : undefined
              return (
                <ChoiceRow
                  key={v.id}
                  leading={
                    <ProductLogo fallback={Location} className="size-9 rounded-lg [&_svg]:size-4" />
                  }
                  title={v.name}
                  verb={enrolled ? `Check from ${v.name}` : `Show ${v.name}`}
                  onSelect={press}
                  disabled={!press}
                  description={
                    <>
                      {v.location || "Location undeclared"} ·{" "}
                      {v.placement === "controlled_fixture"
                        ? "Controlled fixture"
                        : "External host (operator declared)"}
                    </>
                  }
                  trailing={
                    <span className="flex items-center gap-4 text-hint text-muted-foreground">
                      <span
                        className="max-sm:hidden"
                        title={`Poll source IP: ${v.lastIp || "Not recorded"}. This is not a probe source or geographic attestation.`}
                      >
                        {v.lastSeen ? `polled ${relativeTime(v.lastSeen)}` : "never polled"}
                      </span>
                      <Status tone={state.tone} label={state.word} />
                    </span>
                  }
                  actions={
                    verbs.length > 0 && (
                      <VerbActions dim verbs={verbs} menuLabel={`Actions for ${v.name}`} />
                    )
                  }
                />
              )
            })}
          </ChoiceList>
        )}
      </PanelBody>
    </Panel>
  )
}
