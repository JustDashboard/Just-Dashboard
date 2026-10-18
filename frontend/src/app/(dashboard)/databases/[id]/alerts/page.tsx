"use client"

import { Alerts } from "@/components/database/ops"
import { SectionPage } from "@/components/database/shell/section-page"

export default function AlertsPage() {
  return <SectionPage section="alerts" any={Alerts} />
}
