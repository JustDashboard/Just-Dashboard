"use client"

import { Suspense } from "react"
import { Page, PageHeader } from "@/components/page"
import { LoadingPanel } from "@/components/state"
import { ProjectsPage } from "@/components/deploy/projects-page"

export default function DeployPage() {
  return (
    <Suspense
      fallback={
        <Page>
          <PageHeader eyebrow="Apps" title="Deployments" />
          <LoadingPanel rows={5} />
        </Page>
      }
    >
      <ProjectsPage />
    </Suspense>
  )
}
