"use client"

import { SettingsDanger } from "@/components/database/ops"
import { SectionPage } from "@/components/database/shell/section-page"

export default function SettingsDangerPage() {
  return <SectionPage section="settings" any={SettingsDanger} />
}
