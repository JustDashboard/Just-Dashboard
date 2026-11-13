"use client"

import { useEffect, useState } from "react"
import { ArrowRight, Key, Location, Play, Plus, RefreshClockwise } from "@/components/icons"
import { Page, PageContext } from "@/components/page"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Status } from "@/components/status-dot"
import { EmptyState, ErrorState, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { VerbMenu, type Verb } from "@/components/verbs"
import { useConfirm } from "@/components/confirm-dialog"
import { IdentityFacts, IdentityStateLoading } from "@/components/network/identity-facts"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { del, errorMessage, get, post } from "@/lib/api"
import { plural } from "@/lib/format"
import {
  activeCheck,
  checkCounts,
  enrollmentRequest,
  newEnrollmentDraft,
  type ExternalCheck,
  type ExternalEnrollment,
  type ExternalVantage,
} from "@/lib/network-external"
import { ExternalPicture } from "@/components/network/external/external-picture"
import { ExternalSources } from "@/components/network/external/external-sources"
import { CheckReading, ExternalChecks } from "@/components/network/external/external-checks"
import {
  CompareSheet,
  EnrollSheet,
  RunCheckSheet,
  draftFrom,
  draftScope,
  type CheckDraft,
} from "@/components/network/external/external-sheets"
import { checkTarget, newestFirst, reachVerdict } from "@/components/network/external/reading"

type Sheet = "check" | "enroll" | "compare" | null

/**
 * Reading register: what enrolled machines elsewhere measured of this
 * server's approved services.
 *
 * Four tiles opened the page — enrolled sources, pending checks, retained
 * measurements, checks without evidence — over a paragraph, the check form,
 * a framed table, two compare pickers and the sources with the enrol form
 * folded under them. The counts are the identity line's facts and the lists'
 * own counts now; the page opens on the picture of sources, the internet and
 * the approved services with each line's last outcome; the forms are sheets
 * opened from the one command and the sections' verbs.
 */
export function ExternalChecksPage() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const destructive = can("destructive")
  const vantages = usePoll<ExternalVantage[]>(
    (signal) => get("/network/external/vantages", undefined, signal),
    10000,
    [admin],
    { enabled: admin },
  )
  const checks = usePoll<ExternalCheck[]>(
    (signal) => get("/network/external/checks", undefined, signal),
    5000,
    [admin],
    { enabled: admin },
  )
  const { confirm, dialog } = useConfirm()
  const [sheet, setSheet] = useState<Sheet>(null)
  const [draft, setDraft] = useState<CheckDraft>(() => draftFrom())
  const [enrollDraft, setEnrollDraft] = useState(newEnrollmentDraft)
  const [enrollment, setEnrollment] = useState<ExternalEnrollment>()
  const [selectedId, setSelectedId] = useState("")
  const [before, setBefore] = useState("")
  const [after, setAfter] = useState("")
  const [comparison, setComparison] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  const allVantages = vantages.data ?? []
  const rows = newestFirst(checks.data ?? [])
  const readError = vantages.error ?? checks.error
  const loading = admin && !readError && (!vantages.data || !checks.data)
  const enrolled = allVantages.filter((v) => v.enrolledAt && !v.revokedAt)
  const counts = checkCounts(allVantages, rows)
  const verdict = reachVerdict(allVantages, rows)
  const retained = rows.filter((row) => row.result)
  const selected = rows.find((row) => row.id === selectedId) ?? rows[0]
  const nameOf = (check: ExternalCheck) =>
    allVantages.find((v) => v.id === check.vantageId)?.name ?? check.vantageId
  const enrollInput = enrollmentRequest(enrollDraft)

  // The credential is shown once and forgotten when it can no longer be used.
  useEffect(() => {
    if (!enrollment) return
    const timer = setTimeout(
      () => setEnrollment(undefined),
      Math.max(0, new Date(enrollment.expiresAt).getTime() - Date.now()),
    )
    return () => clearTimeout(timer)
  }, [enrollment])

  const refresh = () => {
    vantages.refresh()
    checks.refresh()
  }
  const open = (next: Sheet) => {
    setError(undefined)
    setSheet(next)
  }
  const perform = async (action: () => Promise<void>) => {
    if (busy || !admin) return
    setBusy(true)
    setError(undefined)
    try {
      await action()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  const checkFrom = (vantage?: ExternalVantage) => {
    // The command opens on what was last chosen; a source opens on itself.
    const source = vantage ?? enrolled.find((v) => v.id === draft.vantageId) ?? enrolled[0]
    if (source?.id !== draft.vantageId) setDraft(draftFrom(source, draft.tls))
    open("check")
  }
  const queue = () => {
    if (!draftScope(draft, enrolled)) return
    void perform(async () => {
      const check = await post<ExternalCheck>("/network/external/checks", {
        vantageId: draft.vantageId,
        scopeId: draft.scopeId,
        family: draft.family,
        port: Number(draft.port),
        tls: draft.tls,
      })
      setSelectedId(check.id)
      setSheet(null)
      checks.refresh()
    })
  }
  const cancel = (check: ExternalCheck) =>
    void perform(async () => {
      await post(`/network/external/checks/${check.id}/cancel`)
      checks.refresh()
    })
  const compare = () =>
    void perform(async () => {
      const result = await get<{ comparison: string }>("/network/external/compare", {
        before,
        after,
      })
      setComparison(result.comparison)
    })
  const revoke = (v: ExternalVantage) =>
    confirm({
      title: "Revoke controlled source",
      description: `Revoke ${v.name}'s machine identity and reject all pending results. Its retained evidence remains for seven days.`,
      confirmLabel: "Revoke source",
      action: async () => {
        await del(`/network/external/vantages/${v.id}`)
        refresh()
      },
    })
  const waiting = rows.some((row) => row.vantageId === draft.vantageId && activeCheck(row))

  const menu: Verb[] = [
    { key: "enroll", label: "Enroll a source", icon: Plus, run: () => open("enroll") },
    ...(enrollment
      ? [{ key: "credential", label: "Show credential", icon: Key, run: () => open("enroll") }]
      : []),
    {
      key: "compare",
      label: "Compare measurements",
      icon: ArrowRight,
      disabled: retained.length < 2,
      run: () => open("compare"),
    },
    { key: "refresh", label: "Refresh", icon: RefreshClockwise, run: refresh },
  ]

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Network" title="External checks" />
      <HostIdentity
        fallback={Location}
        title="External checks"
        label="About external checks"
        state={
          !admin ? undefined : loading ? (
            <IdentityStateLoading />
          ) : (
            <Status
              tone={readError ? "warning" : verdict.tone}
              label={readError ? "Last known reach" : verdict.word}
            />
          )
        }
        facts={
          <IdentityFacts loading={loading}>
            {!admin ? (
              <span>needs system administrator access</span>
            ) : (
              <>
                <span className="numeric">{plural(counts.enrolled, "source")} enrolled</span>
                {counts.pending > 0 && (
                  <>
                    <FactDot />
                    <span className="numeric">{counts.pending} in flight</span>
                  </>
                )}
                <FactDot />
                <span className="numeric">{plural(counts.retained, "measurement")} kept</span>
                {counts.unknown > 0 && (
                  <>
                    <FactDot />
                    <span className="numeric" title="Expired or cancelled checks stay unknown">
                      {counts.unknown} without evidence
                    </span>
                  </>
                )}
              </>
            )}
          </IdentityFacts>
        }
        command={
          !admin ? undefined : !loading && enrolled.length === 0 ? (
            // Nothing to ask yet: the page's command is the step that makes a source.
            <Button size="sm" onClick={() => open("enroll")}>
              <Plus className="size-3.5" />
              Enroll a source
            </Button>
          ) : (
            <Button size="sm" disabled={loading} onClick={() => checkFrom()}>
              <Play className="size-3.5" />
              Run a check
            </Button>
          )
        }
        menu={admin ? <VerbMenu verbs={menu} label="More external check actions" /> : undefined}
      />
      {!admin ? (
        <EmptyState
          icon={Location}
          title="External checks need an administrator"
          description="External measurements and agent management require system administrator access."
        />
      ) : readError && !vantages.data && !checks.data ? (
        <ErrorState error={readError} onRetry={refresh} retryable />
      ) : (
        <>
          <NetworkReadWarning
            error={readError}
            refresh={refresh}
            lastSuccess={checks.lastSuccess}
          />
          {error && sheet === null && (
            <Notice tone="warning" title="That did not go through">
              <p role="alert">{error}</p>
            </Notice>
          )}
          <ExternalPicture
            vantages={allVantages}
            checks={rows}
            loading={loading}
            onSource={(v) => checkFrom(v)}
            onEnroll={() => open("enroll")}
          />
          <ExternalChecks
            checks={rows}
            vantages={allVantages}
            loading={loading}
            selected={selected}
            onSelect={(id) => {
              setSelectedId(id)
              // On a phone the list is as long as the week; the reading is under it.
              document
                .getElementById("external-check")
                ?.scrollIntoView({ block: "nearest", behavior: "smooth" })
            }}
            canAct={admin}
            busy={busy}
            onCancel={cancel}
            onCompare={retained.length > 1 ? () => open("compare") : undefined}
            onCompareFrom={(check) => {
              setBefore(check.id)
              setAfter("")
              setComparison(undefined)
              open("compare")
            }}
          />
          {selected && (
            <CheckReading
              check={selected}
              vantage={allVantages.find((v) => v.id === selected.vantageId)}
              target={checkTarget(selected, allVantages)}
            />
          )}
          <ExternalSources
            vantages={allVantages}
            loading={loading}
            canAdmin={admin}
            canRevoke={destructive}
            credentialFor={enrollment?.vantage.id}
            busy={busy}
            onEnroll={() => open("enroll")}
            onCheck={(v) => checkFrom(v)}
            onCredential={() => open("enroll")}
            onRevoke={revoke}
          />
        </>
      )}
      <RunCheckSheet
        open={sheet === "check"}
        onOpenChange={(next) => !next && setSheet(null)}
        enrolled={enrolled}
        draft={draft}
        onDraft={setDraft}
        waiting={waiting}
        busy={busy}
        error={sheet === "check" ? error : undefined}
        onQueue={queue}
      />
      <EnrollSheet
        open={sheet === "enroll"}
        onOpenChange={(next) => !next && setSheet(null)}
        draft={enrollDraft}
        onDraft={setEnrollDraft}
        ready={Boolean(enrollInput)}
        busy={busy}
        error={sheet === "enroll" ? error : undefined}
        enrollment={enrollment}
        onCreate={() => {
          if (!enrollInput) return
          void perform(async () => {
            setEnrollment(
              await post<ExternalEnrollment>("/network/external/enrollments", enrollInput),
            )
            setEnrollDraft(newEnrollmentDraft())
            vantages.refresh()
          })
        }}
        onDismiss={() => {
          setEnrollment(undefined)
          setSheet(null)
        }}
      />
      <CompareSheet
        open={sheet === "compare"}
        onOpenChange={(next) => !next && setSheet(null)}
        retained={retained}
        name={nameOf}
        before={before}
        after={after}
        onBefore={(id) => {
          setBefore(id)
          setComparison(undefined)
        }}
        onAfter={(id) => {
          setAfter(id)
          setComparison(undefined)
        }}
        busy={busy}
        error={sheet === "compare" ? error : undefined}
        comparison={comparison}
        onCompare={compare}
      />
      {dialog}
    </Page>
  )
}
