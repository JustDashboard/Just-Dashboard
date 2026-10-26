"use client"

import { useState } from "react"
import { Logs, RefreshClockwise, Sparkles } from "@/components/icons"
import { ApiError, errorMessage, get, post } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { transcriptLine } from "@/lib/transcript"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentRunDiagnosis } from "@/lib/types"
import { useConfirm } from "@/components/confirm-dialog"
import { Disclosure, FormNote } from "@/components/form"
import { Markdown } from "@/components/git/markdown"
import { ProductGlyph } from "@/components/product-logo"
import { Notice } from "@/components/state"
import { TranscriptRow } from "@/components/transcript-line"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { AssistantSettingsSheet } from "@/components/database/query/assistant/settings-sheet"
import { useAssistantSettings } from "@/components/database/query/assistant/use-assistant"
import type { ConsoleRow } from "@/components/deploy/build-console"
import {
  diagnosisGlyph,
  diagnosisProvenance,
  diagnosisSends,
} from "@/components/deploy/run-diagnosis-model"

/**
 * A failed run asked about: the assistant's provider reads what the run's
 * page shows — the failed step, the cause the dashboard's own rules named,
 * the settings the run used, its variables' names and the end of its
 * transcript, masked on the server first — and answers with a likely cause,
 * the lines that show it and what to do.
 *
 * It sits under the failure and is quieter than it: the failure's own fix,
 * when its evidence names one, stays the command, and nothing here applies
 * anything. Nothing is sent until somebody presses for it; the answer is kept
 * with the run, so opening the run again reads it rather than asking again.
 * Each quoted line is one the run printed — the server leaves out any it
 * cannot find — and opens the build logs at that line.
 */
export function RunDiagnosis({
  projectId,
  runId,
  canAsk,
  canSetUp,
  rows,
  onShowLine,
}: {
  projectId: number
  runId: number
  /** The account may spend the install's model budget (`service.control`). */
  canAsk: boolean
  /** The account may choose the provider (`system.admin`). */
  canSetUp: boolean
  rows: ConsoleRow[]
  onShowLine: (seq: number) => void
}) {
  const path = `/deploy/${projectId}/runs/${runId}/diagnosis`
  const kept = usePoll(
    (signal) =>
      get<{ diagnosis: DeploymentRunDiagnosis | null }>(path, undefined, signal).then(
        (response) => response.diagnosis,
      ),
    0,
    [projectId, runId],
  )
  const settings = useAssistantSettings(canAsk)
  const { confirm, dialog } = useConfirm()
  const [asked, setAsked] = useState<DeploymentRunDiagnosis>()
  const [asking, setAsking] = useState(false)
  const [failure, setFailure] = useState<string>()
  const [settingUp, setSettingUp] = useState(false)

  const diagnosis = asked ?? kept.data ?? undefined
  const view = settings.data
  const configured = view?.configured === true

  const ask = async (again: boolean) => {
    if (asking) return
    setAsking(true)
    setFailure(undefined)
    try {
      const response = await post<{ diagnosis: DeploymentRunDiagnosis }>(path, { again })
      setAsked(response.diagnosis)
    } catch (error) {
      if (error instanceof ApiError && error.code === "assistant_not_configured") settings.refresh()
      setFailure(errorMessage(error))
    } finally {
      setAsking(false)
    }
  }

  // Nothing to show an account that may not ask, until somebody has.
  if (!diagnosis && (!canAsk || kept.data === undefined)) return null

  const sheet = canSetUp && (
    <>
      <AssistantSettingsSheet
        open={settingUp}
        settings={view}
        onClose={() => setSettingUp(false)}
        onSaved={() => {
          settings.refresh()
          setFailure(undefined)
        }}
        confirm={confirm}
      />
      {dialog}
    </>
  )

  if (!diagnosis) {
    return (
      <section aria-label="Ask why it failed" className="min-w-0 space-y-2">
        <div className="flex min-w-0 flex-col gap-3 rounded-lg border border-hairline p-3.5 sm:flex-row sm:items-center">
          <span
            aria-hidden
            className="flex size-8 shrink-0 items-center justify-center rounded-md bg-plot-neutral max-sm:hidden"
          >
            <Sparkles className="size-4 text-muted-foreground" />
          </span>
          <div className="min-w-0 flex-1 space-y-1">
            {configured ? (
              <>
                <p className="flex min-w-0 flex-wrap items-center gap-x-1.5 text-body font-medium">
                  Ask{" "}
                  <span className="inline-flex min-w-0 items-center gap-1.5">
                    <ProductGlyph id={diagnosisGlyph(view.provider)} />
                    <span className="truncate">{view.modelLabel}</span>
                  </span>{" "}
                  why this run failed
                </p>
                {asking ? (
                  <TextShimmer className="text-hint">
                    {`Reading the run with ${view.modelLabel ?? "the model"}… a model on this machine can take a minute or two`}
                  </TextShimmer>
                ) : (
                  <FormNote>{diagnosisSends(view)}</FormNote>
                )}
              </>
            ) : (
              <>
                <p className="text-body font-medium">Ask a model why this run failed</p>
                <FormNote>
                  {view?.problem
                    ? view.problem
                    : canSetUp
                      ? "The assistant is off. Choose who answers it — Claude with your Anthropic key, or a model on a server of your own — and it reads this run's record, masked, when you ask."
                      : "The assistant is off. An administrator turns it on by choosing who answers it."}
                </FormNote>
              </>
            )}
          </div>
          <div className="flex shrink-0 gap-2">
            {configured ? (
              <Button variant="outline" size="sm" disabled={asking} onClick={() => void ask(false)}>
                <Sparkles className="size-3.5" />
                {asking ? "Asking…" : "Ask why it failed"}
              </Button>
            ) : (
              canSetUp &&
              view && (
                <Button variant="outline" size="sm" onClick={() => setSettingUp(true)}>
                  Set up the assistant
                </Button>
              )
            )}
          </div>
        </div>
        {failure && (
          <Notice tone="danger" title="No answer about this run">
            {failure}
          </Notice>
        )}
        {sheet}
      </section>
    )
  }

  const { answer } = diagnosis
  const shaped = Boolean(answer.cause)
  return (
    <section
      aria-label="What the assistant read in this run"
      className="min-w-0 animate-rise overflow-hidden rounded-lg border border-hairline"
    >
      <header className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2 border-b border-hairline bg-surface-header px-4 py-2.5">
        <span className="flex min-w-0 items-center gap-2">
          <Sparkles aria-hidden className="size-4 shrink-0 text-muted-foreground" />
          <h2 className="text-body font-medium">Likely cause</h2>
        </span>
        <span className="flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
          <ProductGlyph id={diagnosisGlyph(diagnosis.provider)} />
          <span className="truncate">{diagnosis.modelLabel || diagnosis.model}</span>
          <span aria-hidden>·</span>
          <span className="shrink-0">
            asked {relativeTime(diagnosis.createdAt)} by {diagnosis.createdBy}
          </span>
        </span>
        {canAsk && configured && (
          <Button
            variant="ghost"
            size="sm"
            className="ml-auto"
            disabled={asking}
            onClick={() => void ask(true)}
          >
            <RefreshClockwise className="size-3.5" />
            {asking ? "Asking…" : "Ask again"}
          </Button>
        )}
      </header>
      <div className="space-y-4 p-4">
        {shaped ? (
          <Markdown source={answer.cause ?? ""} className="text-foreground" />
        ) : (
          <Markdown source={answer.text} />
        )}

        {answer.evidence.length > 0 && (
          <div className="space-y-2">
            <h3 className="text-xs font-medium text-muted-foreground">
              From this run&apos;s transcript
            </h3>
            <ol
              aria-label="Lines the answer quoted"
              className="overflow-hidden rounded-md border border-hairline bg-surface-sunken py-1.5 font-mono text-xs leading-6"
            >
              {answer.evidence.map((quote) => {
                const row = rows.find((candidate) => candidate.id.startsWith(`${quote.seq}:`))
                return (
                  <TranscriptRow
                    key={quote.seq}
                    line={row?.line ?? transcriptLine(quote.seq, quote.text)}
                    wrap
                    needle=""
                    tokens
                    trailing={
                      <button
                        type="button"
                        className="ml-2 inline-flex items-center gap-1 rounded-sm font-sans text-hint text-muted-foreground focus-ring hover:text-foreground"
                        onClick={() => onShowLine(quote.seq)}
                      >
                        <Logs aria-hidden className="size-3" />
                        Show the line
                      </button>
                    }
                  />
                )
              })}
            </ol>
          </div>
        )}

        {shaped && answer.steps.length > 0 && (
          <div className="space-y-2">
            <h3 className="text-xs font-medium text-muted-foreground">What to try</h3>
            <ol className="list-decimal space-y-1.5 pl-5 text-body marker:text-muted-foreground">
              {answer.steps.map((step, index) => (
                <li key={index} className="pl-1">
                  <Markdown source={step} />
                </li>
              ))}
            </ol>
          </div>
        )}

        {failure && (
          <Notice tone="danger" title="No new answer">
            {failure}
          </Notice>
        )}

        <Disclosure
          quiet
          summary="What was sent"
          facts={
            <span className="text-hint text-muted-foreground">
              {diagnosisProvenance(diagnosis)} · nothing was changed
            </span>
          }
        >
          <pre className="max-h-96 overflow-auto rounded-md border border-hairline bg-surface-sunken p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap">
            {diagnosis.sent}
          </pre>
        </Disclosure>
      </div>
      {sheet}
    </section>
  )
}
