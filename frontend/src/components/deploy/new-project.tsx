"use client"

import { useEffect, useRef, useState } from "react"
import Link from "next/link"
import {
  ArrowLeft,
  Box,
  Database,
  GitHubMark,
  Inspect,
  Layers,
  Sparkles,
  Trash,
} from "@/components/icons"
import { ApiError, get } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useSessionState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentDraftSummary } from "@/lib/types"
import { Page, PageHeader, PageState } from "@/components/page"
import { FlowHeader, FlowSteps } from "@/components/flow"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ROW_BLEED, RowList } from "@/components/row-list"
import { IconAction } from "@/components/icon-action"
import { ErrorState } from "@/components/state"
import { Tag } from "@/components/tag"
import { tabClasses } from "@/components/tabs"
import { CREATION_STEPS, humanize } from "@/components/deploy/vocabulary"
import {
  discardAbandoned,
  discardDraft,
  forgetConfigure,
  loadDraft,
  resumeFlow,
  type ConfigureFlow,
  type SourceTabKey,
} from "@/components/deploy/new-project/draft"
import { SourceGit } from "@/components/deploy/new-project/source-git"
import { SourceImage } from "@/components/deploy/new-project/source-image"
import { SourceTemplate } from "@/components/deploy/new-project/source-template"
import { SourceDatabase } from "@/components/deploy/new-project/source-database"
import { SourceCompose } from "@/components/deploy/new-project/source-compose"
import { SourceExisting } from "@/components/deploy/new-project/source-existing"
import { Configure } from "@/components/deploy/new-project/configure"

// The mark is wayfinding, not decoration: six words set in one line are six
// words to read, and the reader is choosing between six *kinds* of thing.
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
  { key: "template", label: "Template", icon: Sparkles },
  { key: "database", label: "Database", icon: Database },
  { key: "compose", label: "Compose", icon: Layers },
  { key: "existing", label: "Existing workload", icon: Inspect },
]

// A profile from a link written before this page existed — the source tab
// that gets you the closest to what that outcome meant.
const LEGACY_PROFILE_TAB: Record<string, SourceTabKey> = {
  service: "template",
  game: "template",
  compose: "compose",
  imported: "existing",
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
  const [tab, setTab] = useSessionState<SourceTabKey>(
    "deploy.new.tab",
    "git",
    arrivalTab(source, profile),
  )
  const [flow, setFlow] = useSessionState<ConfigureFlow | null>("deploy.new.configure.flow", null)
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
  }
  const changeSource = () => {
    void discardAbandoned(flow?.draft.id)
    forgetConfigure()
    setFlow(null)
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
  }, [draftId, setFlow])

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
              <PanelBody flush>
                {/* Padded by the rows' own bleed, so a long list scrolls
                    without the wash overflowing sideways. */}
                <RowList
                  aria-label="Unfinished setups"
                  className="-mx-3 max-h-64 overflow-y-auto px-3"
                >
                  {drafts.data!.map((entry) => (
                    // Laid out by hand rather than as one `Row`: the whole row
                    // resumes the setup, and Discard is a second action, which
                    // cannot be a button nested inside that link.
                    <li key={entry.id} className="group/row relative min-w-0">
                      <Link
                        href={`/deploy/new?draft=${entry.id}`}
                        className={`flex min-w-0 items-center gap-3 py-2.5 pr-12 pl-5 text-left ${ROW_BLEED} focus-ring-inset transition-colors hover:bg-row-hover`}
                      >
                        <span className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-3">
                          <span className="truncate text-body font-medium">
                            {entry.name || "Untitled"}
                          </span>
                          <span className="min-w-0 flex-1 truncate text-hint text-muted-foreground">
                            {entry.source ?? "No source chosen yet"}
                          </span>
                        </span>
                        <span className="flex shrink-0 items-center gap-3">
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
                          <span className="numeric text-hint text-muted-foreground">
                            {relativeTime(entry.updatedAt)}
                          </span>
                        </span>
                      </Link>
                      <IconAction
                        label={`Discard ${entry.name || "this setup"}`}
                        reveal
                        revealGroup="row"
                        className="absolute top-1/2 right-2 -translate-y-1/2"
                        onClick={() => void discard(entry.id)}
                      >
                        <Trash />
                      </IconAction>
                    </li>
                  ))}
                </RowList>
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
          {tab === "existing" && <SourceExisting key="existing" onInspected={inspected} />}
        </>
      ) : (
        <>
          {/* The second step asks the second question. The spine is the only
              thing on either screen that says they are one sequence, which is
              why it is drawn here rather than inside each screen. */}
          <FlowHeader
            eyebrow={Eyebrow}
            question="How should it run?"
            steps={<FlowSteps steps={CREATION_STEPS} current={1} />}
          />
          <Configure
            flow={flow}
            onFlowChange={setFlow}
            onChangeSource={changeSource}
            initialAdvanced={mode === "advanced"}
          />
        </>
      )}
    </Page>
  )
}
