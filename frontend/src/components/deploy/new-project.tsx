"use client"

import { useEffect, useState } from "react"
import Link from "next/link"
import { ArrowLeft } from "@/components/icons"
import { get } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentDraftSummary } from "@/lib/types"
import { Page, PageHeader, PageState } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { ErrorState } from "@/components/state"
import { Tag } from "@/components/tag"
import { tabClasses } from "@/components/tabs"
import { humanize } from "@/components/deploy/vocabulary"
import {
  loadDraft,
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

const TABS: { key: SourceTabKey; label: string }[] = [
  { key: "git", label: "Git repository" },
  { key: "image", label: "Docker image" },
  { key: "template", label: "Template" },
  { key: "database", label: "Database" },
  { key: "compose", label: "Compose" },
  { key: "existing", label: "Existing workload" },
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

function resolveInitialTab(source?: string, profile?: string): SourceTabKey {
  if (source && TABS.some((tab) => tab.key === source)) return source as SourceTabKey
  if (profile && LEGACY_PROFILE_TAB[profile]) return LEGACY_PROFILE_TAB[profile]
  return "git"
}

function sourceLabelFromDraft(source: NonNullable<ConfigureFlow["source"]>) {
  return (
    source.repository ||
    source.url ||
    source.image ||
    source.blueprintId ||
    source.resourceId ||
    source.localPath ||
    "Source"
  )
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
  const [tab, setTab] = useState<SourceTabKey>(() => resolveInitialTab(source, profile))
  const [flow, setFlow] = useState<ConfigureFlow>()
  const [resuming, setResuming] = useState(Boolean(draftId))
  const [resumeError, setResumeError] = useState<Error>()

  // Never blocks the chooser and never reports a failure of its own: a
  // draft is a convenience back to unfinished work, not something the page
  // depends on to function.
  const drafts = usePoll(
    (signal) => get<DeploymentDraftSummary[]>("/deploy/drafts", undefined, signal),
    0,
    [],
  )

  useEffect(() => {
    if (!draftId) return
    let cancelled = false
    loadDraft(draftId)
      .then((draft) => {
        if (cancelled) return
        const { intent, source: draftSource, configuration, detection } = draft.data
        if (!intent || !draftSource || !configuration) {
          setResumeError(new Error("This draft has not reached configuration yet."))
          return
        }
        setFlow({
          name: intent.name,
          profile: intent.profile,
          source: draftSource,
          draft,
          detection,
          candidate: detection?.candidates.find((entry) => entry.id === detection.selectedId),
          configuration,
          sourceLabel: sourceLabelFromDraft(draftSource),
        })
      })
      .catch((error) => {
        if (!cancelled) setResumeError(error instanceof Error ? error : new Error(String(error)))
      })
      .finally(() => {
        if (!cancelled) setResuming(false)
      })
    return () => {
      cancelled = true
    }
  }, [draftId])

  if (resuming) return <PageState eyebrow={Eyebrow} title="New project" />
  if (resumeError)
    return (
      <Page>
        <PageHeader eyebrow={Eyebrow} title="New project" />
        <ErrorState error={resumeError} />
      </Page>
    )

  return (
    <Page className="animate-rise">
      <PageHeader eyebrow={Eyebrow} title="New project" />
      {!flow ? (
        <>
          {(drafts.data?.length ?? 0) > 0 && (
            <Panel plain>
              <PanelHeader title="Unfinished setups" />
              <PanelBody flush>
                <RowList aria-label="Unfinished setups">
                  {drafts.data!.map((entry) => (
                    <Row
                      key={entry.id}
                      href={`/deploy/new?draft=${entry.id}`}
                      title={entry.name || "Untitled"}
                      subtitle={entry.source ?? "No source chosen yet"}
                      trailing={
                        <>
                          <Tag>{humanize(entry.currentStep)}</Tag>
                          <span className="numeric text-hint text-muted-foreground">
                            {relativeTime(entry.updatedAt)}
                          </span>
                        </>
                      }
                    />
                  ))}
                </RowList>
              </PanelBody>
            </Panel>
          )}
          <div
            role="tablist"
            aria-label="Project source"
            className="-mt-2 flex gap-1 overflow-x-auto border-b border-hairline"
          >
            {TABS.map((option) => (
              <button
                key={option.key}
                type="button"
                aria-pressed={tab === option.key}
                onClick={() => setTab(option.key)}
                className={tabClasses(tab === option.key, "h-10")}
              >
                {option.label}
              </button>
            ))}
          </div>
          {tab === "git" && (
            <SourceGit
              key="git"
              onInspected={setFlow}
              onSwitchTab={setTab}
              initialUrl={repo}
              initialRef={repoRef}
            />
          )}
          {tab === "image" && <SourceImage key="image" onInspected={setFlow} />}
          {tab === "template" && <SourceTemplate key="template" onInspected={setFlow} />}
          {tab === "database" && <SourceDatabase key="database" />}
          {tab === "compose" && <SourceCompose key="compose" onInspected={setFlow} />}
          {tab === "existing" && <SourceExisting key="existing" onInspected={setFlow} />}
        </>
      ) : (
        <Configure
          flow={flow}
          onFlowChange={setFlow}
          onChangeSource={() => setFlow(undefined)}
          initialAdvanced={mode === "advanced"}
        />
      )}
    </Page>
  )
}
