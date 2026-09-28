"use client"

import { useMemo, useState } from "react"
import { del, get, post, put } from "@/lib/api"
import { timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { ProxyFindingSnooze } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { FindingList, type Finding } from "@/components/finding-list"
import { Button } from "@/components/ui/button"
import { useNow } from "@/components/deploy/vocabulary"
import type { ProxyFinding, ProxyRemedy } from "@/components/proxy/findings/shared"
import {
  findingFingerprint,
  SNOOZE_LABEL,
  snoozeSpans,
  splitSnoozed,
  type SnoozeSpan,
} from "@/components/proxy/finding-snooze"

function remedyLabel(remedy: ProxyRemedy): string {
  return remedy.kind === "enable-site" ? "Enable and reload" : `Turn on ${remedy.unit}`
}

/**
 * The overview's findings with what an operator can do about each in place:
 * carry out its remedy, or put it aside for a day, a week or until it reads
 * differently. A snooze is kept on the server, so it holds for every account
 * and after a reload, and what it hides stays one tap away under the list.
 */
export function ProxyFindings({
  findings,
  emptyLabel,
  onRemedied,
}: {
  /** Each with its action already chosen by the page. */
  findings: ProxyFinding[]
  emptyLabel: string
  /** A remedy changed the host; the page reads again what it judged. */
  onRemedied: (remedy: ProxyRemedy) => void
}) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const now = useNow(60_000)
  const [showSnoozed, setShowSnoozed] = useState(false)
  const [busy, setBusy] = useState("")
  const snoozes = usePoll(
    (signal) => get<ProxyFindingSnooze[]>("/proxy/findings/snoozes", undefined, signal),
    60_000,
  )
  // Snoozes that cannot be read hide nothing: every finding is shown.
  const { shown, snoozed } = useMemo(
    () => splitSnoozed(findings, snoozes.error ? [] : (snoozes.data ?? []), now),
    [findings, snoozes.data, snoozes.error, now],
  )

  // The enabled switch is behind destructive because it also disables; the
  // timer is enabled by an operator and started by service control.
  const mayRemedy = (remedy: ProxyRemedy) =>
    admin && (remedy.kind === "enable-site" ? can("destructive") : can("service.control"))

  const remedy = async (id: string, fix: ProxyRemedy) => {
    setBusy(id)
    try {
      if (fix.kind === "enable-site") {
        await post(`/proxy/vhosts/${encodeURIComponent(fix.site)}/enabled`, {
          enabled: true,
          reload: true,
        })
        notify.success(`${fix.site} enabled`)
      } else {
        await post(`/systemd/${encodeURIComponent(fix.unit)}/enable`)
        await post(`/systemd/${encodeURIComponent(fix.unit)}/start`)
        notify.success(`${fix.unit} is running`, {
          description: "certbot checks twice a day and renews anything inside thirty days.",
        })
      }
    } catch (err) {
      notify.error("The fix did not go through", err)
    } finally {
      // A site's link is in place even when the reload after it failed, so
      // the page reads again either way.
      onRemedied(fix)
      setBusy("")
    }
  }

  const snooze = async (finding: ProxyFinding, span: SnoozeSpan) => {
    setBusy(finding.id)
    try {
      await put("/proxy/findings/snoozes", {
        findingId: finding.id,
        fingerprint: findingFingerprint(finding),
        level: finding.level,
        for: span,
      })
      snoozes.refresh()
    } catch (err) {
      notify.error("Couldn't snooze the finding", err)
    } finally {
      setBusy("")
    }
  }

  const unsnooze = async (finding: ProxyFinding) => {
    setBusy(finding.id)
    try {
      await del("/proxy/findings/snoozes", { query: { id: finding.id } })
      snoozes.refresh()
    } catch (err) {
      notify.error("Couldn't show the finding again", err)
    } finally {
      setBusy("")
    }
  }

  const triaged = shown.map((finding): Finding => {
    const fix = finding.remedy && mayRemedy(finding.remedy) ? finding.remedy : undefined
    const spans = admin ? snoozeSpans(finding) : []
    if (!fix && spans.length === 0) return finding
    return {
      ...finding,
      extra: (
        <div className="flex flex-wrap items-center gap-1.5 pt-0.5">
          {fix && (
            <Button
              size="xs"
              variant="outline"
              pending={busy === finding.id}
              disabled={busy !== ""}
              onClick={() => remedy(finding.id, fix)}
            >
              {remedyLabel(fix)}
            </Button>
          )}
          {spans.length > 0 && <span className="ml-1">Snooze</span>}
          {spans.map((span) => (
            <Button
              key={span}
              size="xs"
              variant="ghost"
              disabled={busy !== ""}
              onClick={() => snooze(finding, span)}
            >
              {SNOOZE_LABEL[span]}
            </Button>
          ))}
        </div>
      ),
    }
  })

  return (
    <div className="space-y-3">
      <FindingList
        findings={triaged}
        emptyLabel={
          snoozed.length > 0 ? "Nothing needs attention beyond what is snoozed" : emptyLabel
        }
      />
      {snoozed.length > 0 && (
        <Button size="xs" variant="ghost" onClick={() => setShowSnoozed((v) => !v)}>
          {showSnoozed ? "Hide snoozed" : `Show ${snoozed.length} snoozed`}
        </Button>
      )}
      {showSnoozed && snoozed.length > 0 && (
        <FindingList
          findings={snoozed.map(({ finding, snooze }) => ({
            ...finding,
            meta: "snoozed",
            extra: (
              <div className="flex flex-wrap items-center gap-2">
                <span>
                  Snoozed by {snooze.actor || "an operator"} on {timestamp(snooze.createdAt)},{" "}
                  {snooze.until ? `until ${timestamp(snooze.until)}` : "until it changes"}.
                  {snooze.note && ` ${snooze.note}`}
                </span>
                {admin && can("destructive") && (
                  <Button
                    size="xs"
                    variant="ghost"
                    pending={busy === finding.id}
                    disabled={busy !== ""}
                    onClick={() => unsnooze(finding)}
                  >
                    Show again
                  </Button>
                )}
              </div>
            ),
          }))}
        />
      )}
    </div>
  )
}
