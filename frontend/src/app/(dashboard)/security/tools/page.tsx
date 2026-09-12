"use client"

import { Page, PageHeader } from "@/components/page"
import { ToolsPanel } from "@/components/security/tools-panel"

export default function SecurityToolsPage() {
  return (
    <Page>
      <PageHeader
        eyebrow="Security"
        title="Tools"
      />
      <ToolsPanel />
    </Page>
  )
}
