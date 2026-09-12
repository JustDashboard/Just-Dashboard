import { Suspense } from "react"
import { Page, PageHeader } from "@/components/page"
import { LoadingPanel } from "@/components/state"
import { DeploymentWizard } from "@/components/deploy/deployment-wizard"
import { QuickDeploy } from "@/components/deploy/quick-deploy"

/**
 * Quick deploy is the default and the wizard is the escape hatch.
 *
 * The wizard is reached by asking for it — `?mode=advanced` — or by already
 * being in the middle of one, which is what a `draft` in the URL means: a
 * reload, a bookmark, or quick deploy handing its own draft over for the
 * settings it does not show.
 */
export default async function NewDeploymentPage({
  searchParams,
}: {
  searchParams: Promise<{ mode?: string; draft?: string }>
}) {
  const { mode, draft } = await searchParams
  const advanced = mode === "advanced" || Boolean(draft)
  return (
    <Suspense
      fallback={
        <Page>
          <PageHeader eyebrow="Deployments" title="Deploy something" />
          <LoadingPanel rows={5} />
        </Page>
      }
    >
      {advanced ? <DeploymentWizard /> : <QuickDeploy />}
    </Suspense>
  )
}
