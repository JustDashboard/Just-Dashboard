"use client"

import { useState } from "react"
import Link from "next/link"
import { Archive, Trash } from "@/components/icons"
import { useConfirm } from "@/components/confirm-dialog"
import { Page, PageHeader, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { del, get } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import type { DeployProject } from "@/lib/types"

export function DeleteArchivedDeployment({
  project,
  onDeleted,
}: {
  project: DeployProject
  onDeleted: () => void
}) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  if (!can("destructive") || !project.archivedAt) return null
  return (
    <>
      <Button
        variant="outline"
        size="sm"
        onClick={() =>
          confirm({
            title: `Permanently delete ${project.name}?`,
            confirmLabel: "Delete permanently",
            description: (
              <div className="space-y-3">
                <p>
                  Delete this deployment’s saved configuration, variables, release history, and
                  deployment logs. This cannot be undone.
                </p>
                <p>
                  Running containers, routes, images, files, and persistent data remain on the
                  server. The dashboard will forget their deployment ownership. Remove managed
                  resources from Settings → Lifecycle first if you want them removed too.
                </p>
              </div>
            ),
            action: async () => {
              await del(`/deploy/${project.id}/permanent`)
            },
            onDone: onDeleted,
          })
        }
      >
        <Trash className="size-3.5" />
        Delete permanently
      </Button>
      {dialog}
    </>
  )
}

export function ArchivedDeployments() {
  const [query, setQuery] = useState("")
  const result = usePoll(
    (signal) => get<DeployProject[]>("/deploy/", { view: "archived" }, signal),
    10000,
  )
  const projects = (result.data ?? []).filter((project) =>
    project.name.toLowerCase().includes(query.trim().toLowerCase()),
  )
  return (
    <Page>
      <PageHeader
        eyebrow={
          <Link href="/deploy" className="hover:underline">
            Deployments
          </Link>
        }
        title="Archived deployments"
        actions={
          <Button variant="ghost" size="sm" asChild>
            <Link href="/deploy">Active deployments</Link>
          </Button>
        }
      />
      <Panel plain>
        <PanelHeader
          title="Archive"
          actions={
            <span className="numeric text-hint text-muted-foreground">
              {result.data?.length ?? 0} deployments
            </span>
          }
        />
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
            <ErrorState error={result.error} />
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
            <RowList>
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
                  subtitle={`Archived ${relativeTime(project.archivedAt!)}`}
                  trailing={
                    <DeleteArchivedDeployment project={project} onDeleted={result.refresh} />
                  }
                />
              ))}
            </RowList>
          )}
        </PanelBody>
      </Panel>
    </Page>
  )
}
