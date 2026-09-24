import { Suspense } from "react"
import { Page, PageHeader } from "@/components/page"
import { LoadingPanel } from "@/components/state"
import { NewProject } from "@/components/deploy/new-project"

function first(value: string | string[] | undefined) {
  return typeof value === "string" ? value : undefined
}

/**
 * One page now, not a quick path and a five-step escape hatch: `NewProject`
 * draws both of its states itself. `?mode=advanced` opens Configure with
 * Advanced expanded and `?draft=` resumes a server draft into it; `?source=`
 * and the legacy `?profile=` preselect a tab; `?repo=` (with an optional `?ref=`)
 * arrives on the Git tab with that clone URL filled in, which is what a
 * "deploy to your server" link in a README points at. `?template=` and
 * `?image=` arrive on those tabs with that blueprint or image chosen, which is
 * where detection sends the upstream repository of an application the
 * catalogue already packages.
 */
export default async function NewProjectPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>
}) {
  const params = await searchParams
  return (
    <Suspense
      fallback={
        <Page>
          <PageHeader eyebrow="Deployments" title="New project" />
          <LoadingPanel rows={5} />
        </Page>
      }
    >
      <NewProject
        source={first(params.source)}
        profile={first(params.profile)}
        mode={first(params.mode)}
        draftId={first(params.draft)}
        repo={first(params.repo)}
        repoRef={first(params.ref)}
        template={first(params.template)}
        image={first(params.image)}
      />
    </Suspense>
  )
}
