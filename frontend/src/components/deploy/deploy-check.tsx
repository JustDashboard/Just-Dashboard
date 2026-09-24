"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { useRouter } from "next/navigation"
import { RefreshClockwise } from "@/components/icons"
import { post } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import type { DeploymentCheckResult, DeploymentPreflightFinding } from "@/lib/types"
import { FormFact, FormNote } from "@/components/form"
import { Modal } from "@/components/modal"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { ShortSha } from "@/components/git/marks"
import { FindingRow } from "@/components/deploy/deployment-findings"
import {
  attentionFindings,
  rememberDeploymentCheck,
  settingsPathForField,
  stopsDeployment,
} from "@/components/deploy/deploy-check-state"

/**
 * Preflight before Deploy is pressed.
 *
 * The backend evaluates the saved plan against the commit a deployment would
 * build now — the evaluation `analyze_plan` runs before every build — and
 * answers `POST …/check`. A project page asks when it opens and again after
 * each settings save while changes wait to go live; what it finds is drawn
 * on the Overview and in the pending-changes strip, and every way to start a
 * build asks "Ready to deploy?" first when something would stop it or should
 * be confirmed. It is advice, not the gate: a warning never stops the API,
 * and analyze_plan checks again on every trigger, pushes and schedules
 * included.
 */

/** Asks the check for one environment: when the page opens, and after each save that left changes waiting. */
export function useDeploymentCheck({
  projectId,
  environmentId,
  desiredRevision,
  pending,
  enabled,
}: {
  projectId: number
  environmentId: number
  desiredRevision?: number
  pending: boolean
  enabled: boolean
}) {
  const [state, setState] = useState<{ result?: DeploymentCheckResult; checking: boolean }>({
    checking: false,
  })
  const ticket = useRef(0)
  const check = useCallback(async () => {
    const mine = ++ticket.current
    setState((previous) => ({ ...previous, checking: true }))
    try {
      const result = await post<DeploymentCheckResult>(
        `/deploy/${projectId}/environments/${environmentId}/check`,
        {},
      )
      rememberDeploymentCheck(environmentId, result)
      if (mine === ticket.current) setState({ result, checking: false })
      return result
    } catch {
      // Advice that cannot be had leaves Deploy one press; the run checks
      // the same things itself before it builds.
      if (mine === ticket.current) setState((previous) => ({ ...previous, checking: false }))
      return undefined
    }
  }, [projectId, environmentId])
  const asked = useRef<string | undefined>(undefined)
  useEffect(() => {
    if (!enabled || environmentId <= 0 || desiredRevision === undefined) return
    const key = `${environmentId}:${desiredRevision}`
    if (asked.current === key) return
    // A save that left nothing waiting has nothing a deployment would take.
    if (asked.current !== undefined && !pending) {
      asked.current = key
      return
    }
    // Scheduled rather than called: the check raises its flag as its first
    // act, and a state write in an effect's own body is a cascading render.
    const timer = setTimeout(() => {
      asked.current = key
      void check()
    }, 0)
    return () => clearTimeout(timer)
  }, [enabled, environmentId, desiredRevision, pending, check])
  const accept = useCallback((result: DeploymentCheckResult) => {
    ticket.current++
    setState({ result, checking: false })
  }, [])
  // Only an answer about the revision now saved describes it.
  const current =
    state.result && state.result.planRevision === desiredRevision ? state.result : undefined
  return { result: current, checking: state.checking, recheck: check, accept }
}

/** The findings a check raised, each opening the settings page that holds its field. */
export function DeployCheckList({
  projectId,
  findings,
  limit,
}: {
  projectId: number
  findings: DeploymentPreflightFinding[]
  limit?: number
}) {
  const router = useRouter()
  const shown = limit ? findings.slice(0, limit) : findings
  return (
    <ul className="space-y-2">
      {shown.map((finding, index) => (
        <li key={`${finding.code}:${finding.fieldId ?? index}`}>
          <FindingRow
            finding={finding}
            index={index}
            canOpenRemedy={Boolean(settingsPathForField(finding.fieldId))}
            onOpenRemedy={(item) => {
              const path = settingsPathForField(item.fieldId)
              if (path) router.push(`/deploy/${projectId}${path}`)
            }}
          />
        </li>
      ))}
      {limit !== undefined && findings.length > limit && (
        <li className="text-hint text-muted-foreground">
          and {plural(findings.length - limit, "more finding")} on the Overview
        </li>
      )}
    </ul>
  )
}

/** What the check read, as a line: the commit it judged and when. */
function CheckedLine({ result }: { result: DeploymentCheckResult }) {
  return (
    <span className="inline-flex min-w-0 flex-wrap items-center gap-1.5">
      {result.sourceRevision && <ShortSha sha={result.sourceRevision} />}
      <span>
        revision {result.planRevision} · checked {relativeTime(result.checkedAt)}
      </span>
    </span>
  )
}

/**
 * "Before you deploy", on the Overview: what a deployment of the saved plan
 * would stop on or should be confirmed, with the way to each field.
 */
export function BeforeYouDeploy({
  projectId,
  result,
  checking,
  onRecheck,
}: {
  projectId: number
  result: DeploymentCheckResult
  checking: boolean
  onRecheck: () => void
}) {
  const findings = attentionFindings(result.findings)
  if (findings.length === 0) return null
  const stops = findings.filter(stopsDeployment).length
  return (
    <Panel plain id="before-you-deploy" className="scroll-mt-6">
      <PanelHeader
        title="Before you deploy"
        actions={
          <Button variant="outline" size="sm" onClick={onRecheck} pending={checking}>
            <RefreshClockwise className="size-3.5" /> Check again
          </Button>
        }
      />
      <PanelBody className="space-y-3">
        <p className="text-hint text-muted-foreground">
          {stops > 0
            ? `A deployment would stop before it builds: ${plural(stops, "finding")} to fix. `
            : "A deployment can go ahead; these are worth a look first. "}
          <CheckedLine result={result} />
        </p>
        <DeployCheckList projectId={projectId} findings={findings} />
      </PanelBody>
    </Panel>
  )
}

/**
 * "Ready to deploy?" — what the check found, before a build is started.
 *
 * Something that would stop the deployment keeps the command disabled: the
 * run would refuse at analyze_plan, before a build slot, and the dialog says
 * so now instead. Warnings are confirmed by pressing the command anyway,
 * which the caller remembers for the tab so the same warnings are asked once.
 */
export function DeployCheckDialog({
  open,
  onOpenChange,
  projectId,
  subject,
  result,
  command,
  checking,
  busy,
  onRecheck,
  onConfirm,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  projectId: number
  subject?: { mark: React.ReactNode; name: React.ReactNode }
  result: DeploymentCheckResult
  /** The command being confirmed, as its button reads: "Deploy", "Rebuild without cache". */
  command: string
  checking?: boolean
  busy?: boolean
  onRecheck?: () => void
  onConfirm: () => void
}) {
  const findings = attentionFindings(result.findings)
  const stops = findings.some(stopsDeployment)
  return (
    <Modal
      open={open}
      onOpenChange={(next) => !busy && onOpenChange(next)}
      title="Ready to deploy?"
      size="lg"
      footer={
        <>
          <FormNote className="mr-auto">
            {stops
              ? "The deployment would stop before building until these are fixed."
              : "The deployment checks again before it builds."}
          </FormNote>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          {onRecheck && (
            <Button variant="outline" onClick={onRecheck} pending={checking} disabled={busy}>
              Check again
            </Button>
          )}
          <Button onClick={onConfirm} disabled={stops || checking} pending={busy}>
            {stops || findings.length === 0 ? command : `${command} anyway`}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        {subject && (
          <div className="flex min-w-0 items-center gap-3">
            {subject.mark}
            <div className="min-w-0 space-y-0.5">
              <p className="truncate text-body font-medium">{subject.name}</p>
              <p className="text-hint text-muted-foreground">
                <CheckedLine result={result} />
              </p>
            </div>
          </div>
        )}
        {!subject && (
          <FormFact label="Checked">
            <CheckedLine result={result} />
          </FormFact>
        )}
        <p className="text-body leading-relaxed">
          {findings.length === 0
            ? "Nothing in the saved plan or the commit it builds needs a look any more."
            : stops
              ? "Preflight read the saved plan against the commit this deployment builds and found what would stop it."
              : "Preflight read the saved plan against the commit this deployment builds. It can go ahead; confirm these first."}
        </p>
        <DeployCheckList projectId={projectId} findings={findings} />
      </div>
    </Modal>
  )
}
