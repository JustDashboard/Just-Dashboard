"use client"

import { SettingsVersion } from "@/components/database/ops"
import { SectionPage } from "@/components/database/shell/section-page"

export default function SettingsVersionPage() {
  return <SectionPage section="settings" any={SettingsVersion} />
}
