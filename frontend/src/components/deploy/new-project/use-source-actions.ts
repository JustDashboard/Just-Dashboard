"use client"

import {
  loadDraft,
  redetectedConfiguration,
  reinspect,
  selectCandidate,
  type ConfigureFlow,
  type FlowUpdate,
  type InspectOutcome,
} from "@/components/deploy/new-project/draft"
import { composeSourceForCandidate } from "@/components/deploy/deployment-defaults"
import type {
  AssistantDraft,
  SourceAction,
} from "@/components/deploy/new-project/use-assistant-draft"
import type { DeploymentDraftSource } from "@/lib/types"

function asError(error: unknown) {
  return error instanceof Error ? error : new Error(String(error))
}

/**
 * The form's presses that ask the server to read the source again: a branch or a
 * clone option (saved as part of the source, then detected), a candidate picked from
 * several, deploying the repository as a Compose stack, and Review's "Inspect again".
 *
 * They are not fields the page can keep and save later: the candidate, the detection
 * and what the plan becomes are the server's answer. So while the assistant works
 * on the draft, or waits on an Allow, each press waits (`AssistantDraft.whenFree`)
 * until its last save is read back and the person's kept edits are saved, and then
 * runs at the draft's current revision, on the page as it is by then. A 409 then
 * reads the draft again and runs over that, never over the page's older copy.
 * While one waits the saved branch is not yet the one chosen last, so `Configure`
 * sends a branch chosen back again as a press of its own, rather than reading it
 * as no change.
 */
export function useSourceActions({
  assistant,
  onFlowChange,
  mutatingRef,
  setFailure,
  setBusy,
  setBranchBusy,
  onInspected,
}: {
  assistant: AssistantDraft
  onFlowChange: (next: FlowUpdate) => void
  mutatingRef: React.RefObject<boolean>
  setFailure: (error: Error | undefined) => void
  setBusy: (busy: string) => void
  setBranchBusy: (busy: boolean) => void
  /** Review's answer is about the commit it read, which a new read replaces. */
  onInspected: () => void
}) {
  /**
   * One read of the source: `run` against the page as it is, and what the flow
   * becomes from its answer. A detection that failed may have saved the source
   * first, so the draft is read again for its revision.
   */
  const detect =
    (
      branch: boolean,
      run: (now: ConfigureFlow) => Promise<InspectOutcome>,
      shape: (base: ConfigureFlow, result: InspectOutcome) => Partial<ConfigureFlow>,
      after?: () => void,
    ): SourceAction =>
    async (now) => {
      if (mutatingRef.current) return
      mutatingRef.current = true
      if (branch) setBranchBusy(true)
      else setBusy("detect")
      setFailure(undefined)
      try {
        const { result, base } = await assistant.atRevision(now, run)
        const changes: Partial<ConfigureFlow> = {
          draft: result.draft,
          candidate: result.candidate,
          detection: result.detection,
          ...shape(base, result),
        }
        // Read again after a 409, the page is the server's draft, not its own copy.
        onFlowChange((current) =>
          current ? { ...(base === now ? current : base), ...changes } : current,
        )
        after?.()
        return { ...base, ...changes }
      } catch (error) {
        try {
          const fresh = await loadDraft(now.draft.id)
          onFlowChange((current) => (current ? { ...current, draft: fresh } : current))
        } catch {
          /* The original inspection error is the useful one. */
        }
        setFailure(asError(error))
      } finally {
        mutatingRef.current = false
        if (branch) setBranchBusy(false)
        else setBusy("")
      }
    }

  // A branch or a clone option is part of the saved source, so changing one
  // saves it again and detects again.
  const resaveSource = (patch: Partial<DeploymentDraftSource>) =>
    assistant.whenFree(
      detect(
        true,
        (now) => reinspect(now.draft, now.profile, { ...now.source, ...patch }),
        (base, result) => {
          const profile =
            base.profile === base.candidate?.profile
              ? (result.candidate?.profile ?? base.profile)
              : base.profile
          return {
            source: { ...base.source, ...patch },
            profile,
            configuration: redetectedConfiguration(base, result, profile),
          }
        },
      ),
    )

  // `detection_ambiguous`'s only remedy — /detect has always accepted a
  // `selectedId`, but nothing sent one, so the finding blocked every ambiguous
  // plan with no way to resolve it from this screen.
  const pickCandidate = (id: string) =>
    assistant.whenFree(
      detect(
        false,
        (now) => selectCandidate(now.draft, now.profile, now.source, id),
        (base, result) => ({
          profile: result.candidate?.profile ?? base.profile,
          configuration: redetectedConfiguration(base, result),
        }),
      ),
    )

  // `compose_analysis_missing`'s remedy: the same repository, branch and
  // files, read as a Compose source so its services are analysed and built.
  const deployAsCompose = () =>
    assistant.whenFree(async (now) => {
      const nextSource = composeSourceForCandidate(now.source, now.candidate)
      if (!nextSource) return
      return detect(
        false,
        (at) => reinspect(at.draft, "compose", nextSource),
        (_base, result) => ({
          source: nextSource,
          profile: "compose",
          configuration: result.configuration,
        }),
      )(now)
    })

  /**
   * `source_moved`'s remedy: the branch has moved past the commit Review
   * checked, so detection reads the newer one — keeping the candidate the
   * reader chose where it still exists — and Review asks again. The plan may
   * come out the same, so the answer about the old commit is dropped rather
   * than left standing for an unchanged signature.
   */
  const inspectAgain = () =>
    assistant.whenFree(
      detect(
        false,
        (now) => {
          const selected = now.detection?.selectedId
          return selected
            ? selectCandidate(now.draft, now.profile, now.source, selected).catch(() =>
                reinspect(now.draft, now.profile, now.source),
              )
            : reinspect(now.draft, now.profile, now.source)
        },
        (base, result) => ({ configuration: redetectedConfiguration(base, result) }),
        onInspected,
      ),
    )

  return { resaveSource, pickCandidate, deployAsCompose, inspectAgain }
}
