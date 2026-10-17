"use client"

import { SettingsPooling } from "@/components/database/ops"
import { SectionPage } from "@/components/database/shell/section-page"

export default function SettingsPoolingPage() {
  return <SectionPage section="settings" any={SettingsPooling} />
}
