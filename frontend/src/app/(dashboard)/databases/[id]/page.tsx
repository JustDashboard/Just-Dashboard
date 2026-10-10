"use client"

import { DatabaseHome } from "@/components/database/home"
import { SectionPage } from "@/components/database/shell/section-page"

export default function DatabaseHomePage() {
  return <SectionPage section="home" any={DatabaseHome} />
}
