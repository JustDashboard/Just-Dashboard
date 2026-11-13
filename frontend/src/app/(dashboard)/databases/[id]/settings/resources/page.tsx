"use client"

import { SettingsResources } from "@/components/database/ops/settings-resources-page"
import { SectionPage } from "@/components/database/shell/section-page"

export default function SettingsResourcesPage() {
  return <SectionPage section="settings" any={SettingsResources} />
}
