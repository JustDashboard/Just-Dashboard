"use client"

import { useState } from "react"
import { useSessionState } from "@/lib/view-state"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { Archive, ArrowLeft, MagnifyingGlass, Trash } from "@/components/icons"
import { get, post } from "@/lib/api"
import { calendarDate, plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { usePoll } from "@/hooks/use-poll"
import type { ArchivedDeployment } from "@/lib/types"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { FormFact } from "@/components/form"
import { DimActions } from "@/components/icon-action"
import { Page, PageContext, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelToolbar } from "@/components/panel"
import { ProductGlyph } from "@/components/product-logo"
import { EmptyState, ErrorState } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { VerbMenu } from "@/components/verbs"
import { WorkloadMark, projectProduct, sourceProduct } from "@/components/deploy/vocabulary"
import { purgeRequest } from "@/components/deploy/project-verbs"

/**
 * Deployments that have been taken out of the active fleet but keep their
 * history — a resting place before `Delete permanently` forgets them for good.
 *
 * Each is still a destination — the archived workspace opens, read-only — so
 * each row is a choice with the lit edge, drawn as the product it deployed at
 * a step of opacity: a project at rest, the way Backups dims a paused job.
 * What it was built from and what deleting it takes with it are on the row,
 * because that is what the one decision made here turns on.
 */
export function ArchivedProjects() {
  const router = useRouter()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  // Restore and Delete sit in the row's own slot where there is room for
  // them, and on a line of their own under the name below that — chosen once,
  // because each is found by its name and a hidden twin is a second answer.
  // Room is `xl`: the source, the count and both buttons want about 520px
  // beside the name, and with the sidebar beside it anything narrower left
  // the name no width at all.
  const wide = useMediaQuery("(min-width: 1280px)")
  const [query, setQuery] = useSessionState("deploy.archived.query", "")
  const [restoringId, setRestoringId] = useState<number>()
  const result = usePoll(
    (signal) => get<ArchivedDeployment[]>("/deploy/", { view: "archived" }, signal),
    10000,
  )
  const projects = (result.data ?? []).filter((project) =>
    project.name.toLowerCase().includes(query.trim().toLowerCase()),
  )

  // Unarchiving hands the name back to the pool it was reserved from, so the
  // one refusal worth a distinct read is another active project having taken
  // it in the meantime — the message is the server's own either way.
  const restore = async (project: ArchivedDeployment) => {
    setRestoringId(project.id)
    try {
      await post(`/deploy/${project.id}/unarchive`, {})
      // What restoring does not bring back is the one thing to know next.
      notify.success(`Restored ${project.name}`, {
        description: "Automatic deployments and schedules stay off until you turn them back on.",
      })
      router.push(`/deploy/${project.id}`)
    } catch (error) {
      notify.error("Could not restore the project", error)
    } finally {
      setRestoringId(undefined)
    }
  }

  const remove = (project: ArchivedDeployment) =>
    confirm(
      purgeRequest(
        {
          id: project.id,
          name: project.name,
          mark: <ArchivedMark project={project} />,
          facts: (
            <>
              <FormFact label="Archived">{relativeTime(project.archivedAt)}</FormFact>
              {project.sourceRepository && (
                <FormFact label="Source" mono>
                  {project.sourceRepository}
                </FormFact>
              )}
            </>
          ),
        },
        { variables: project.envVarCount, onDone: () => result.refresh() },
      ),
    )

  // Restore is the row's one named act; deleting for good is in the menu
  // beside it, in the danger colour, as it is in the project's own header.
  const verbsFor = (project: ArchivedDeployment) => (
    <>
      {can("system.admin") && (
        <Button
          variant="outline"
          size="xs"
          disabled={restoringId === project.id}
          pending={restoringId === project.id}
          onClick={() => void restore(project)}
        >
          Restore
        </Button>
      )}
      {can("destructive") && (
        <VerbMenu
          label={`Actions for ${project.name}`}
          verbs={[
            {
              key: "purge",
              label: "Delete permanently",
              detail: "Forget this deployment's configuration, variables and history.",
              icon: Trash,
              danger: true,
              run: () => remove(project),
            },
          ]}
        />
      )}
    </>
  )

  return (
    <Page className="animate-rise">
      <PageContext eyebrow={BACK} title="Archived" />
      <Panel plain>
        <PanelToolbar>
          <SearchInput
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search archive"
            aria-label="Search archive"
          />
          {result.data && (
            <span className="numeric ml-auto text-hint text-muted-foreground">
              {plural(result.data.length, "project")}
            </span>
          )}
        </PanelToolbar>
        <PanelBody flush className="pt-3">
          {result.error ? (
            <ErrorState error={result.error} onRetry={result.refresh} />
          ) : result.loading && !result.data ? (
            <ArchivedRows />
          ) : projects.length === 0 ? (
            <EmptyState
              icon={query ? MagnifyingGlass : Archive}
              title={query ? "No matching deployments" : "No archived deployments"}
              description={
                query
                  ? "Try a different name."
                  : "Archived deployments appear here with their saved history."
              }
            />
          ) : (
            <ChoiceList aria-label="Archived deployments" className="animate-rise">
              {projects.map((project) => (
                <ChoiceRow
                  key={project.id}
                  href={`/deploy/${project.id}`}
                  verb={`Open ${project.name}`}
                  leading={<ArchivedMark project={project} />}
                  title={project.name}
                  description={
                    <>
                      Archived {relativeTime(project.archivedAt)} · created{" "}
                      {calendarDate(project.createdAt)}
                    </>
                  }
                  trailing={
                    wide && (
                      <>
                        <span className="flex w-48 min-w-0">
                          <ArchivedSource project={project} />
                        </span>
                        <span className="numeric w-24 text-right text-hint text-muted-foreground">
                          {plural(project.envVarCount, "variable")}
                        </span>
                      </>
                    )
                  }
                  actions={wide && <DimActions className="gap-1.5">{verbsFor(project)}</DimActions>}
                >
                  {!wide && (
                    <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2 text-hint text-muted-foreground">
                      <ArchivedSource project={project} />
                      <span className="numeric">{plural(project.envVarCount, "variable")}</span>
                      <span className="flex w-full items-center gap-1.5">{verbsFor(project)}</span>
                    </div>
                  )}
                </ChoiceRow>
              ))}
            </ChoiceList>
          )}
        </PanelBody>
      </Panel>
      {dialog}
    </Page>
  )
}

const BACK = (
  <Link
    href="/deploy"
    className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
  >
    <ArrowLeft className="size-3" /> Deployments
  </Link>
)

/** Three rows in the archive's shape, before its first answer. */
function ArchivedRows() {
  return (
    <ChoiceList aria-hidden>
      {[0, 1, 2].map((index) => (
        <li key={index}>
          <Skeleton className="h-14 rounded-xl" />
        </li>
      ))}
    </ChoiceList>
  )
}

/**
 * The archive before the route has rendered — the Suspense fallback of a hard
 * load of `?view=archived` — in the shape it arrives in: its header and its
 * search over the same rows the page draws while its list is read.
 */
export function ArchivedSkeleton() {
  return (
    <Page>
      <PageContext eyebrow={BACK} title="Archived" />
      <Panel plain>
        <PanelToolbar>
          <Skeleton aria-hidden className="h-10 w-full rounded-md sm:h-8 sm:w-72" />
        </PanelToolbar>
        <PanelBody flush className="pt-3">
          <ArchivedRows />
        </PanelBody>
      </Panel>
    </Page>
  )
}

/**
 * What the project's plan recorded, in the shape a summary reads, or nothing
 * for a project archived before those facts were kept.
 */
function recorded(project: ArchivedDeployment) {
  const { sourceKind, buildMethod } = project
  return sourceKind && buildMethod ? { ...project, sourceKind, buildMethod } : undefined
}

/**
 * The product the project deployed, at a step of opacity. A project with no
 * recorded facts draws its workload's glyph on the same tile.
 */
function ArchivedMark({ project }: { project: ArchivedDeployment }) {
  const facts = recorded(project)
  return (
    <WorkloadMark
      profile={project.profile}
      product={facts && projectProduct(facts)}
      size="sm"
      className="opacity-70"
    />
  )
}

/**
 * Where it was built from: the forge and repository, or the image, or the
 * template drawn as the fleet draws one — the word, and the reference as the
 * literal it is.
 */
function ArchivedSource({ project }: { project: ArchivedDeployment }) {
  const facts = recorded(project)
  if (!facts?.sourceRepository) return null
  if (facts.sourceKind === "blueprint")
    return (
      <span className="flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
        <span className="shrink-0">Template</span>
        <Tag mono className="min-w-0 shrink">
          <span className="truncate">{facts.sourceRepository}</span>
        </Tag>
      </span>
    )
  const product = sourceProduct(facts)
  return (
    <span className="flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
      {product && <ProductGlyph id={product} />}
      <span className="min-w-0 truncate font-mono">{facts.sourceRepository}</span>
    </span>
  )
}
