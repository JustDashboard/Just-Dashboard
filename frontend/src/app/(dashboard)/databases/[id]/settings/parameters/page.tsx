"use client"

import { SettingsParameters } from "@/components/database/ops"
import { SectionPage } from "@/components/database/shell/section-page"

export default function SettingsParametersPage() {
  return <SectionPage section="settings" any={SettingsParameters} />
}
