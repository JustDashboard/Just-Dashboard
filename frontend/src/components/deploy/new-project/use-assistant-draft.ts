"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { ApiError } from "@/lib/api"
import {
  agentBusy,
  dismissAgent,
  holdAgentTurns,
  useAgentSession,
} from "@/components/assistant/agent/sessions"
import { pendingSteps } from "@/components/assistant/agent/model"
import { draftScope } from "@/components/deploy/new-project/setup-assistant"
import {
  configurationForSave,
  loadDraft,
  resumeFlow,
  saveConfiguration,
  saveIntent,
  type ConfigureFlow,
  type FlowUpdate,
} from "@/components/deploy/new-project/draft"
import type { DeploymentDraft } from "@/lib/types"
import {
  NO_KEPT_EDITS,
  editedPaths,
  hasKeptEdits,
  keepEdits,
  withKeptEdits,
  withPaths,
  type KeptEdits,
} from "@/components/deploy/new-project/kept-edits"

/**
 * A press of the page's that asks the server to read the source again, run on the
 * page as it is when it runs; it answers what the flow became, when it changed it.
 */
export type SourceAction = (now: ConfigureFlow) => Promise<ConfigureFlow | undefined>

/** Why the page holds the chat's next turn: edits or presses waiting to be saved, or a save under way. */
const OWED = "draft-edits"
const SAVING = "draft-save"

/** Saves a flow's name, type and plan at the revision its draft holds. */
async function savePlan(flow: ConfigureFlow, text: string, retainedKeys: string[]) {
  let working = flow.draft
  if (working.data.intent?.name !== flow.name || working.data.intent?.profile !== flow.profile) {
    working = await saveIntent(working, { name: flow.name, profile: flow.profile })
  }
  return saveConfiguration(
    working,
    configurationForSave(flow.configuration, flow.source),
    text,
    retainedKeys,
  )
}

/**
 * The new-deployment page's side of the assistant working on its draft: reading
 * back what it saves, keeping what the person types meanwhile, and saving that once
 * it is done. `Configure` holds Review's check and its own saves on `working`.
 *
 * - **Read back here**, not in its panel, so a save made with the panel closed is
 *   read too; until the last one is in, the page is still behind it, and a check
 *   that went first would 409 and then save the page's older plan over its save.
 * - **The person's edits are kept.** The form stays editable while it works (Back
 *   and the step tabs too). Each edit made then is kept as the fields it touched
 *   (`kept-edits.ts`), laid over every read-back, and saved once nothing of the
 *   assistant's is under way or waiting on an Allow: at the draft's revision, and
 *   on a 409 over the draft read again with the edits laid over it, never over
 *   what it saved. `working` stays up while they wait to be saved, so Review checks
 *   the plan that has them.
 * - **A press that reads the source again waits too** (`whenFree`, for
 *   `use-source-actions.ts`): it cannot be kept as a field, since what it changes is
 *   the server's answer, so it runs once the edits are saved, at the current
 *   revision, and `atRevision` reads the draft again on a 409 and runs over that.
 * - **No turn starts over the page's own write.** From the first kept edit or
 *   waiting press until they are saved, and through any save of the page's
 *   (`holdTurns`), the chat's next turn is held (`holdAgentTurns`): what the person
 *   sends, and what was waiting in its queue, is asked once the write has answered
 *   and been folded into the page. A save starts only while no turn is under way.
 * - **It starts from what the person sees**: `prepare` saves the page as it is when
 *   the panel opens, read through a ref, not as it was when the draft was loaded.
 */
export function useAssistantDraft({
  flow,
  text,
  retainedKeys,
  onFlowChange,
  created,
  idle,
  mutatingRef,
}: {
  flow: ConfigureFlow
  /** The environment as the page would save it. */
  text: string
  retainedKeys: string[]
  onFlowChange: (next: FlowUpdate) => void
  created: boolean
  /** No save or detection of the page's own is under way. */
  idle: boolean
  /** The page's own save in flight; one of these at a time. */
  mutatingRef: React.RefObject<boolean>
}) {
  const draftId = flow.draft.id
  const session = useAgentSession(draftScope(flow))
  const changes = session.changes
  const [readBack, setReadBack] = useState(changes)
  const answering = agentBusy(session) || changes !== readBack
  const waiting = pendingSteps(session.transcript).length > 0
  const engaged = answering || waiting

  const [kept, setKeptState] = useState<{ draftId: string; edits: KeptEdits }>({
    draftId,
    edits: NO_KEPT_EDITS,
  })
  const keptRef = useRef(kept)
  const setKept = (next: { draftId: string; edits: KeptEdits }) => {
    keptRef.current = next
    setKeptState(next)
  }
  const editsFor = (id: string) =>
    keptRef.current.draftId === id ? keptRef.current.edits : NO_KEPT_EDITS
  const ownEdits = kept.draftId === draftId ? kept.edits : NO_KEPT_EDITS
  const [saving, setSaving] = useState(false)

  const latest = useRef({ flow, text, retainedKeys })
  useEffect(() => {
    latest.current = { flow, text, retainedKeys }
  })

  useEffect(() => {
    if (changes === readBack) return
    let live = true
    void loadDraft(draftId)
      .then(resumeFlow)
      .then((next) => {
        if (!live) return
        const edits = keptRef.current.draftId === draftId ? keptRef.current.edits : NO_KEPT_EDITS
        onFlowChange((current) =>
          current?.draft.id === draftId
            ? withKeptEdits(
                { ...next, sourceLabel: current.sourceLabel, githubRepo: current.githubRepo },
                edits,
              )
            : current,
        )
      })
      .catch(() => undefined)
      .finally(() => {
        if (live) setReadBack(changes)
      })
    return () => {
      live = false
    }
  }, [changes, readBack, draftId, onFlowChange])

  /**
   * The form's own edits. While the assistant works, only the fields an edit
   * changed are written, over the page as it is by then, and they are kept; at any
   * other time it is the page's setter.
   */
  const editFlow = (update: FlowUpdate) => {
    if (!engaged) return onFlowChange(update)
    const next = typeof update === "function" ? update(flow) : update
    if (!next) return
    const paths = editedPaths(flow, next)
    if (paths.length === 0) return
    setKept({ draftId, edits: keepEdits(editsFor(draftId), paths, next) })
    holdAgentTurns(session, OWED, true)
    onFlowChange((current) => (current ? withPaths(current, paths, next) : current))
  }

  // Once it is done, what the person typed meanwhile is saved, once.
  const pending = hasKeptEdits(ownEdits)
  const saveKept = () => {
    // A turn begun since this was scheduled goes first; this waits for its end.
    if (mutatingRef.current || agentBusy(session)) return
    holdAgentTurns(session, SAVING, true)
    mutatingRef.current = true
    setSaving(true)
    const edits = editsFor(draftId)
    const { flow: now, text: dotenvText, retainedKeys: keys } = latest.current
    void (async () => {
      let saved: DeploymentDraft
      try {
        saved = await savePlan(now, dotenvText, keys)
      } catch (error) {
        if (!(error instanceof ApiError) || error.code !== "draft_revision_conflict") throw error
        // Something saved the draft since the page read it: the edits go over the
        // draft as it is now, never the page's older copy over it.
        const fresh = withKeptEdits(
          {
            ...(await resumeFlow(await loadDraft(now.draft.id))),
            sourceLabel: now.sourceLabel,
            githubRepo: now.githubRepo,
          },
          edits,
        )
        // What was typed while this was out stays as it was typed.
        onFlowChange((current) =>
          current?.draft.id === now.draft.id
            ? withPaths(fresh, editedPaths(now, current), current)
            : current,
        )
        saved = await savePlan(fresh, dotenvText, keys)
      }
      onFlowChange((current) =>
        current?.draft.id === saved.id ? { ...current, draft: saved } : current,
      )
    })()
      // A save that fails leaves the edits in the page, where Review's check or
      // Deploy saves them; holding them here would hold those too, for good.
      .catch(() => undefined)
      .finally(() => {
        // Edits made while this saved were the page's alone, and are in it.
        if (keptRef.current.edits === edits) setKept({ draftId, edits: NO_KEPT_EDITS })
        mutatingRef.current = false
        setSaving(false)
        holdAgentTurns(session, SAVING, false)
      })
  }

  useEffect(() => {
    if (engaged || !pending || created || !idle) return
    // Scheduled rather than run: raising `saving` in the effect's own body is a
    // cascading render. The cleanup drops a save nobody is waiting for any more.
    const timer = setTimeout(saveKept, 0)
    return () => clearTimeout(timer)
    // `saveKept` reads the page through refs when it runs.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [engaged, pending, created, idle, draftId])

  // Presses that read the source again, waiting for the assistant to be done.
  const deferredRef = useRef<SourceAction[]>([])
  const [deferred, setDeferred] = useState(0)
  const [running, setRunning] = useState(false)
  /** Around a save of the page's own: a turn begun over it would race it for the revision. */
  const holdTurns = (on: boolean) => holdAgentTurns(session, SAVING, on)
  const run = async (actions: SourceAction[]) => {
    holdTurns(true)
    setRunning(true)
    try {
      // Each runs on the page as the one before left it.
      let now = latest.current.flow
      for (const action of actions) now = (await action(now)) ?? now
    } finally {
      setRunning(false)
      holdTurns(false)
    }
  }
  /** A press that reads the source: now, or once the assistant is done and its edits saved. */
  const whenFree = (action: SourceAction) => {
    const free =
      !engaged && !pending && !saving && !running && idle && deferredRef.current.length === 0
    if (free && !agentBusy(session)) return void run([action])
    deferredRef.current = [...deferredRef.current, action]
    setDeferred(deferredRef.current.length)
    holdAgentTurns(session, OWED, true)
  }
  useEffect(() => {
    if (engaged || pending || saving || running || !idle || deferred === 0 || created) return
    const timer = setTimeout(() => {
      if (mutatingRef.current || agentBusy(session)) return
      const actions = deferredRef.current
      deferredRef.current = []
      setDeferred(0)
      void run(actions)
    }, 0)
    return () => clearTimeout(timer)
    // `run` reads the page through refs when it runs.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [engaged, pending, saving, running, idle, deferred, created])

  /**
   * `run` on the page as it is, at its draft's revision; a 409 means something
   * saved the draft since, so it is read again and `run` goes over that. `base` is
   * the page it ran on.
   */
  const atRevision = async <T>(now: ConfigureFlow, step: (flow: ConfigureFlow) => Promise<T>) => {
    try {
      return { result: await step(now), base: now }
    } catch (error) {
      if (!(error instanceof ApiError) || error.code !== "draft_revision_conflict") throw error
      const fresh = await resumeFlow(await loadDraft(now.draft.id))
      const base = { ...fresh, sourceLabel: now.sourceLabel, githubRepo: now.githubRepo }
      return { result: await step(base), base }
    }
  }

  // Nothing owed any more lets the chat's next turn go; neither does a page left.
  const owed = pending || saving || running || deferred > 0
  useEffect(() => {
    if (!owed) holdAgentTurns(session, OWED, false)
  }, [owed, session])
  useEffect(
    () => () => {
      holdAgentTurns(session, OWED, false)
      holdAgentTurns(session, SAVING, false)
    },
    [session],
  )

  // Once it is deployed there is no setup to go back to, so the dock lets it go.
  useEffect(() => {
    if (created) dismissAgent(session)
  }, [created, session])

  // It starts from what the person sees, so what the page holds is saved first. A
  // plan that does not save yet is no reason to refuse: it reads the detection.
  // It reads the flow as it is when the panel opens, not as it was when the draft
  // was first loaded: Review's check has moved the revision on by then, and a save
  // at the first one was refused 409 and saved nothing the person had typed since.
  const prepare = useCallback(async () => {
    const { flow: now, text: dotenvText, retainedKeys: keys } = latest.current
    try {
      const saved = await savePlan(now, dotenvText, keys)
      onFlowChange((current) => (current ? { ...current, draft: saved } : current))
    } catch {
      // Told to the assistant by what it reads; the form keeps what was typed.
    }
  }, [onFlowChange])

  return {
    session,
    /** Answering, a save of its not yet read back, or the person's edits and presses not yet saved. */
    working: answering || owed,
    /** A press that reads the source waits for the assistant to be done. */
    deferred: deferred > 0,
    /** An answer waits on the person's Allow, holding the revision it read. */
    waiting,
    editFlow,
    prepare,
    whenFree,
    atRevision,
    holdTurns,
  }
}

export type AssistantDraft = ReturnType<typeof useAssistantDraft>
