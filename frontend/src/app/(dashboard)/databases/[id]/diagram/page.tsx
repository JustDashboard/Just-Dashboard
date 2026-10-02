"use client"

import { Diagram } from "@/components/database/diagram"
import { SectionPage } from "@/components/database/shell/section-page"

export default function DiagramPage() {
  return <SectionPage section="diagram" sql={Diagram} />
}
