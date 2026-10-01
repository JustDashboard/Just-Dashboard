"use client"

import { Backups } from "@/components/database/ops"
import { SectionPage } from "@/components/database/shell/section-page"

export default function BackupsPage() {
  return <SectionPage section="backups" any={Backups} />
}
