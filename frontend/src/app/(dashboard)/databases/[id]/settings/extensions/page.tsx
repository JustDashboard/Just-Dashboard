"use client"

import { SettingsExtensions } from "@/components/database/ops"
import { SectionPage } from "@/components/database/shell/section-page"

export default function SettingsExtensionsPage() {
  return <SectionPage section="settings" any={SettingsExtensions} />
}
