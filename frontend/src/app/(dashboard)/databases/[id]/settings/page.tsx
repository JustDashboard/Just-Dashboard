"use client"

import { Settings } from "@/components/database/ops"
import { SectionPage } from "@/components/database/shell/section-page"

export default function SettingsPage() {
  return <SectionPage section="settings" any={Settings} />
}
