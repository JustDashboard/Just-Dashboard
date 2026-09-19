"use client"

import { useState } from "react"
import { useSessionState } from "@/lib/view-state"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { Archive, Trash } from "@/components/icons"
import { del, get, post } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { DeployProject } from "@/lib/types"
import { Page, PageHeader, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelToolbar } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { useConfirm } from "@/components/confirm-dialog"

/**
 * Deployments that have been taken out of the active fleet but keep their
 * history — a resting place before `Delete permanently` forgets them for good.
 */
export function ArchivedProjects() {
  const router = useRouter()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [query, setQuery] = useSessionState("deploy.archived.query", "")
  const [restoringId, setRestoringId] = useState<number>()
  const result = usePoll(
    (signal) => get<DeployProject[]>("/deploy/", { view: "archived" }, signal),
    10000,
  )
  const projects = (result.data ?? []).filter((project) =>
    project.name.toLowerCase().includes(query.trim().toLowerCase()),
  )

  // Unarchiving hands the name back to the pool it was reserved from, so the
  // one refusal worth a distinct read is another active project having taken
  // it in the meantime — the message is the server's own either way.
  const restore = async (project: DeployProject) => {
    setRestoringId(project.id)
    try {
      await post(`/deploy/${project.id}/unarchive`, {})
      notify.success("Project restored")
      router.push(`/deploy/${project.id}`)
    } catch (error) {
      notify.error("Could not restore the project", error)
    } finally {
      setRestoringId(undefined)
    }
  }

  const remove = (project: DeployProject) =>
    confirm({
      title: `Permanently delete ${project.name}?`,
      confirmLabel: "Delete permanently",
      description: (
        <div className="space-y-3">
          <p>
            Delete this deployment’s saved configuration, variables, release history, and deployment
            logs. This cannot be undone.
          </p>
          <p>
            Running containers, routes, images, files, and persistent data remain on the server. The
            dashboard will forget their deployment ownership. Remove managed resources from Settings
            → Danger zone first if you want them removed too.
          </p>
        </div>
      ),
      action: async () => {
        await del(`/deploy/${project.id}/permanent`)
      },
      onDone: () => result.refresh(),
    })

  return (
    <Page>
      <PageHeader
        eyebrow={
          <Link href="/deploy" className="rounded-sm focus-ring hover:underline">
            Deployments
          </Link>
        }
        title="Archived"
      />
      <Panel plain>
        <PanelToolbar>
          <SearchInput
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search archive"
            aria-label="Search archive"
          />
        </PanelToolbar>
        <PanelBody flush>
          {result.error ? (
            <ErrorState error={result.error} onRetry={result.refresh} />
          ) : result.loading && !result.data ? (
            <LoadingRows className="pt-4" />
          ) : projects.length === 0 ? (
            <EmptyState
              icon={Archive}
              className="mt-4"
              title={query ? "No matching deployments" : "No archived deployments"}
              description={
                query
                  ? "Try a different name."
                  : "Archived deployments appear here with their saved history."
              }
            />
          ) : (
            <RowList aria-label="Archived deployments" className="animate-rise">
              {projects.map((project) => (
                <Row
                  key={project.id}
                  title={
                    <Link
                      href={`/deploy/${project.id}`}
                      className="rounded-sm break-all focus-ring hover:underline"
                    >
                      {project.name}
                    </Link>
                  }
                  subtitle={`Archived ${relativeTime(project.archivedAt)}`}
                  trailing={
                    <>
                      {can("system.admin") && (
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={restoringId === project.id}
                          pending={restoringId === project.id}
                          onClick={() => void restore(project)}
                        >
                          Restore
                        </Button>
                      )}
                      {can("destructive") && (
                        <Button
                          variant="outline"
                          size="sm"
                          className="text-destructive"
                          onClick={() => remove(project)}
                        >
                          <Trash className="size-3.5" />
                          Delete permanently
                        </Button>
                      )}
                    </>
                  }
                />
              ))}
            </RowList>
          )}
        </PanelBody>
      </Panel>
      {dialog}
    </Page>
  )
}
