"use client"

import { Advisor } from "@/components/database/ops"
import { SectionPage } from "@/components/database/shell/section-page"

export default function AdvisorPage() {
  return <SectionPage section="advisor" any={Advisor} />
}
