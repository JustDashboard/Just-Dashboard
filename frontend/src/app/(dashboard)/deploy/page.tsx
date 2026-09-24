import { Suspense } from "react"
import { Page, PageHeader } from "@/components/page"
import { ArchivedSkeleton } from "@/components/deploy/archived-projects"
import { FleetSkeleton, ProjectsPage } from "@/components/deploy/projects-page"

export default async function DeployPage({
  searchParams,
}: {
  searchParams: Promise<{ view?: string | string[] }>
}) {
  const { view } = await searchParams
  return (
    <Suspense
      fallback={
        // The placeholder of the page the address names, so it arrives in
        // the shape it was drawn in: the fleet's readings over its cards, or
        // the archive's rows — never the one and then the other.
        view === "archived" ? (
          <ArchivedSkeleton />
        ) : (
          <Page>
            <PageHeader eyebrow="Apps" title="Deployments" />
            <FleetSkeleton />
          </Page>
        )
      }
    >
      <ProjectsPage />
    </Suspense>
  )
}
