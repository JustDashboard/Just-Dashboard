"use client"

import { Page, PageContext } from "@/components/page"
import { EventsTab } from "@/components/docker/events-tab"

export default function DockerEventsPage() {
  return (
    <Page>
      <PageContext eyebrow="Docker" title="Events" />
      <EventsTab />
    </Page>
  )
}
