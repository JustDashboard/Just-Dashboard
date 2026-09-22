"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import Link from "next/link"
import {
  ArrowLeft,
  Box,
  Database,
  GitHubMark,
  GridMasonry,
  Layers,
  Trash,
} from "@/components/icons"
import { ApiError, get } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useMemoryState, useSessionState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentDraftSummary } from "@/lib/types"
import { Page, PageHeader, PageState } from "@/components/page"
import { ChoiceList, ChoiceRow, FlowHeader, FlowSteps } from "@/components/flow"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { DimActions, IconAction } from "@/components/icon-action"
import { ErrorState } from "@/components/state"
import { Tag } from "@/components/tag"
import { tabClasses } from "@/components/tabs"
import { CREATION_STEPS, humanize } from "@/components/deploy/vocabulary"
import {
  creationStepIndex,
  discardAbandoned,
  discardDraft,
  forgetConfigure,
  landingStep,
  loadDraft,
  resumeFlow,
  persistableFlow,
  type ConfigureFlow,
  type ConfigureStepKey,
  type SourceTabKey,
  type FlowUpdate,
} from "@/components/deploy/new-project/draft"
import { SourceGit } from "@/components/deploy/new-project/source-git"
import { SourceImage } from "@/components/deploy/new-project/source-image"
import { SourceTemplate } from "@/components/deploy/new-project/source-template"
import { SourceDatabase } from "@/components/deploy/new-project/source-database"
import { SourceCompose } from "@/components/deploy/new-project/source-compose"
import { Configure } from "@/components/deploy/new-project/configure"

// The mark is wayfinding, not decoration: five words set in one line are five
// words to read, and the reader is choosing between five *kinds* of thing.
// §14 bans a glyph in front of a heading because you are already there; a
// chooser is the opposite case, and the same rule keeps the sidebar's and the
// overview tiles' marks. Every one is `aria-hidden`, so the button's
// accessible name stays exactly its label.
const TABS: {
  key: SourceTabKey
  label: string
  icon: React.ComponentType<{ className?: string }>
}[] = [
  { key: "git", label: "Git repository", icon: GitHubMark },
  { key: "image", label: "Docker image", icon: Box },
  { key: "template", label: "Template", icon: GridMasonry },
  { key: "database", label: "Database", icon: Database },
  { key: "compose", label: "Compose", icon: Layers },
]

// A profile from a link written before this page existed — the source tab
// that gets you the closest to what that outcome meant.
const LEGACY_PROFILE_TAB: Record<string, SourceTabKey> = {
  service: "template",
  game: "template",
  compose: "compose",
  image: "image",
  static: "git",
  worker: "git",
  web: "git",
}

/** The tab a link asked for, or nothing — in which case the last one used stands. */
function arrivalTab(source?: string, profile?: string): SourceTabKey | undefined {
  if (source && TABS.some((tab) => tab.key === source)) return source as SourceTabKey
  if (profile && LEGACY_PROFILE_TAB[profile]) return LEGACY_PROFILE_TAB[profile]
  return undefined
}

/**
 * What each screen of the sequence asks, at the page's own rank (§16).
 *
 * Configure used to ask one question — "How should it run?" — over a screen
 * that also settled what it was called, what it was built from, what it
 * needed, and whether it was right. Four screens, four questions, and the
 * spine under them saying which is which.
 */
const QUESTIONS: Record<ConfigureStepKey, string> = {
  project: "What are you building?",
  runtime: "How should it run?",
  variables: "What does it need to run?",
  review: "Ready to deploy?",
  // A statement, because the sequence is over: the outcome is not a question,
  // and this is the `h1` the surface under it no longer repeats.
  done: "Deployment created",
}

function asError(error: unknown) {
  return error instanceof Error ? error : new Error(String(error))
}

/** Within three days of lapsing — the point at which the date is worth saying. */
function expiringSoon(iso: string | undefined) {
  if (!iso) return false
  const at = new Date(iso).getTime()
  return !Number.isNaN(at) && at - Date.now() < 3 * 24 * 60 * 60 * 1000
}

const Eyebrow = (
  <Link
    href="/deploy"
    className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
  >
    <ArrowLeft className="size-3" /> Deployments
  </Link>
)

/**
 * One page, two states: choose a source, then configure it. The old
 * five-step wizard and quick deploy's separate escape hatch are gone —
 * every source drives the same `draft.ts` sequence and lands on the same
 * Configure screen, which is what let quick deploy and the wizard drift
 * apart in the first place.
 *
 * Both states are remembered for the tab. Importing a repository, changing
 * its settings and adding its environment is minutes of work, and walking to
 * another page to check something — a credential, a port, a container — used
 * to throw all of it away: the page mounts fresh on every visit, so the flow
 * lived and died with it. It now lives in the session store (the environment
 * in memory, since it holds secrets), and the page opens exactly where it was
 * left until the project is created or the source is changed.
 */
export function NewProject({
  source,
  profile,
  mode,
  draftId,
  repo,
  repoRef,
}: {
  source?: string
  profile?: string
  mode?: string
  draftId?: string
  /** A clone URL to arrive with, from a deploy link outside the dashboard. */
  repo?: string
  repoRef?: string
}) {
  const [lastTab, setTab] = useSessionState<SourceTabKey>(
    "deploy.new.tab",
    "git",
    arrivalTab(source, profile),
  )
  // The store outlives the strip: a tab open when Existing workload was still
  // a source comes back naming it, and a key no button carries is a page with
  // nothing under the strip at all.
  const tab = TABS.some((option) => option.key === lastTab) ? lastTab : "git"
  const [savedFlow, setSavedFlow] = useSessionState<ConfigureFlow | null>(
    "deploy.new.configure.flow",
    null,
  )
  const [liveFlow, setLiveFlow] = useMemoryState<ConfigureFlow | null>(
    "deploy.new.configure.liveFlow",
    null,
  )
  const flow = liveFlow ?? savedFlow
  const setFlow = useCallback(
    (update: FlowUpdate) =>
      setSavedFlow((persisted) => {
        let next = persisted
        setLiveFlow((current) => {
          next = typeof update === "function" ? update(current ?? persisted) : update
          return next
        })
        return persistableFlow(next)
      }),
    [setSavedFlow, setLiveFlow],
  )
  // Which of the four configure screens is open. Under the `configure.` prefix
  // so that changing the source forgets it with everything else the setup knew.
  const [step, setStep] = useSessionState<ConfigureStepKey>("deploy.new.configure.step", "project")
  const [resuming, setResuming] = useState(Boolean(draftId))
  const [resumeError, setResumeError] = useState<Error>()
  // A link into the chooser — a README's deploy link, a `?source=` — asks for
  // a new setup: it opens the chooser over a remembered flow, which stays
  // remembered until something is inspected in its place.
  const [linkArrived, setLinkArrived] = useState(Boolean(source || profile || repo))
  // Inspecting a second source is a different project, not a revision of the
  // first: the setup being walked away from is thrown away rather than left
  // to expire, which is what turned three attempts at one repository into
  // three rows of unfinished work.
  const inspected = (next: ConfigureFlow) => {
    if (flow && flow.draft.id !== next.draft.id) void discardAbandoned(flow.draft.id)
    setLinkArrived(false)
    setFlow(next)
    // Arrival parameters choose a source once. Leaving them in the address
    // bar would reopen that chooser over the completed setup on every reload.
    const address = new URL(window.location.href)
    for (const key of ["source", "profile", "repo", "ref", "draft"])
      address.searchParams.delete(key)
    window.history.replaceState(
      window.history.state,
      "",
      `${address.pathname}${address.search}${address.hash}`,
    )
    // Everything detection answered is a screen the reader does not have to
    // visit: a repository it read completely opens on Review with the plan
    // read back and Deploy under it, which is the two-press import the page
    // had before it had steps at all.
    setStep(landingStep(next, mode === "advanced"))
    requestAnimationFrame(() => {
      const header = document.querySelector<HTMLElement>("[data-slot='flow-header']")
      const heading = header?.querySelector("h1")
      if (heading) {
        heading.tabIndex = -1
        heading.focus({ preventScroll: true })
      }
      header?.scrollIntoView({
        block: "start",
        behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches
          ? "instant"
          : "smooth",
      })
    })
  }
  const changeSource = () => {
    void discardAbandoned(flow?.draft.id)
    forgetConfigure()
    setFlow(null)
    setStep("project")
  }

  // Never blocks the chooser and never reports a failure of its own: a
  // draft is a convenience back to unfinished work, not something the page
  // depends on to function.
  const drafts = usePoll(
    (signal) => get<DeploymentDraftSummary[]>("/deploy/drafts", undefined, signal),
    0,
    [],
  )

  const discard = async (id: string) => {
    try {
      await discardDraft(id)
      if (flow?.draft.id === id) {
        forgetConfigure()
        setFlow(null)
      }
      drafts.refresh()
    } catch (error) {
      notify.error("Could not discard this setup", asError(error))
    }
  }

  useEffect(() => {
    if (!draftId) return
    let cancelled = false
    loadDraft(draftId)
      .then(resumeFlow)
      .then((resumed) => {
        if (cancelled) return
        // A draft chosen from the list replaces whatever was in progress,
        // environment included: the two are different setups.
        forgetConfigure()
        setFlow(resumed)
        setStep(landingStep(resumed, mode === "advanced"))
        setLinkArrived(false)
      })
      .catch((error) => {
        if (!cancelled) setResumeError(asError(error))
      })
      .finally(() => {
        if (!cancelled) setResuming(false)
      })
    return () => {
      cancelled = true
    }
  }, [draftId, mode, setFlow, setStep])

  // A remembered flow names a draft on the server, and the server may have
  // let it go: drafts expire, and another tab can finish one. Asked once per
  // draft on the way in, so a dead flow is dropped with a word now rather
  // than refused at Deploy after the form has been filled in again.
  const verified = useRef<string>(undefined)
  const rememberedDraftId = flow?.draft.id
  useEffect(() => {
    if (!rememberedDraftId || draftId || verified.current === rememberedDraftId) return
    verified.current = rememberedDraftId
    loadDraft(rememberedDraftId)
      .then((fresh) => {
        if (!fresh.committedProjectId) return
        setFlow((current) => (current?.draft.id === fresh.id ? null : current))
      })
      .catch((error) => {
        if (!(error instanceof ApiError) || (error.status !== 404 && error.status !== 410)) return
        setFlow((current) => (current?.draft.id === rememberedDraftId ? null : current))
        notify.error("Your unfinished project setup is no longer on the server", undefined, {
          description:
            "It expired or was finished elsewhere, so this starts again from the source.",
        })
      })
  }, [rememberedDraftId, draftId, setFlow])

  if (resuming) return <PageState eyebrow={Eyebrow} title="New project" />
  if (resumeError)
    return (
      <Page>
        <PageHeader eyebrow={Eyebrow} title="New project" />
        <ErrorState error={resumeError} />
      </Page>
    )

  return (
    <Page register="flow" className="animate-rise">
      {!flow || linkArrived ? (
        <>
          {/* The screen asks something, and the question is the page's own
              rank — not a sentence under a title, which is the caption §5
              removed from every page in the product. */}
          <FlowHeader
            eyebrow={Eyebrow}
            question="What are you deploying?"
            steps={<FlowSteps steps={CREATION_STEPS} current={0} />}
          />
          {(drafts.data?.length ?? 0) > 0 && (
            <Panel plain className="animate-rise">
              <PanelHeader
                title="Unfinished setups"
                // A count, not a caption (§4): how many there are is the one
                // thing the title cannot say, and it is what decides whether
                // this block is worth reading at all.
                actions={
                  <span className="numeric text-hint text-muted-foreground">
                    {drafts.data!.length}
                  </span>
                }
              />
              <PanelBody flush className="pt-3">
                <ChoiceList
                  aria-label="Unfinished setups"
                  className="max-h-72 overflow-y-auto pr-1"
                >
                  {drafts.data!.map((entry) => (
                    <ChoiceRow
                      key={entry.id}
                      href={`/deploy/new?draft=${entry.id}`}
                      // The same fallback the title uses: a control whose name
                      // is "this setup" while the row reads "Untitled" is two
                      // names for one row.
                      verb={`Resume ${entry.name || "Untitled"}`}
                      title={entry.name || "Untitled"}
                      description={entry.source ?? "No source chosen yet"}
                      trailing={
                        <>
                          {/* Only once it is nearly gone. Every draft expires,
                              so "29 days from now" on all four is a column of
                              the same word; the one about to lapse is the only
                              one that changes what you do next. */}
                          {expiringSoon(entry.expiresAt) && (
                            <span className="numeric text-hint text-warning">
                              expires {relativeTime(entry.expiresAt)}
                            </span>
                          )}
                          <Tag>{humanize(entry.currentStep)}</Tag>
                          <span className="numeric hidden text-hint text-muted-foreground sm:inline">
                            {relativeTime(entry.updatedAt)}
                          </span>
                        </>
                      }
                      actions={
                        <DimActions>
                          <IconAction
                            label={`Discard ${entry.name || "this setup"}`}
                            onClick={() => void discard(entry.id)}
                          >
                            <Trash />
                          </IconAction>
                        </DimActions>
                      }
                    />
                  ))}
                </ChoiceList>
              </PanelBody>
            </Panel>
          )}
          <div
            role="tablist"
            aria-label="Project source"
            className="flex gap-1 overflow-x-auto border-b border-hairline"
          >
            {TABS.map((option) => (
              <button
                key={option.key}
                type="button"
                aria-pressed={tab === option.key}
                onClick={() => setTab(option.key)}
                className={tabClasses(tab === option.key, "h-11")}
              >
                <option.icon
                  aria-hidden
                  className={
                    tab === option.key ? "size-3.5 text-brand" : "size-3.5 text-muted-foreground"
                  }
                />
                {option.label}
              </button>
            ))}
          </div>
          {tab === "git" && (
            <SourceGit key="git" onInspected={inspected} initialUrl={repo} initialRef={repoRef} />
          )}
          {tab === "image" && <SourceImage key="image" onInspected={inspected} />}
          {tab === "template" && <SourceTemplate key="template" onInspected={inspected} />}
          {tab === "database" && <SourceDatabase key="database" />}
          {tab === "compose" && <SourceCompose key="compose" onInspected={inspected} />}
        </>
      ) : (
        <>
          {/* Every screen past the chooser asks its own question. The spine is
              the only thing on any of them that says the five are one
              sequence, which is why it is drawn here rather than inside each
              screen — and why splitting Configure into four changed the spine
              rather than adding a second one inside it. */}
          <FlowHeader
            eyebrow={Eyebrow}
            question={QUESTIONS[step]}
            steps={<FlowSteps steps={CREATION_STEPS} current={creationStepIndex(step)} />}
          />
          <Configure
            flow={flow}
            onFlowChange={setFlow}
            onChangeSource={changeSource}
            initialAdvanced={mode === "advanced"}
            step={step}
            onStepChange={setStep}
          />
        </>
      )}
    </Page>
  )
}
