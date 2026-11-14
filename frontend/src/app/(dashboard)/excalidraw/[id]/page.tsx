"use client"

import dynamic from "next/dynamic"
import { useParams } from "next/navigation"
import { Page } from "@/components/page"
import { LoadingPanel } from "@/components/state"

// The workspace draws its own page: the editor is a workbench held to the
// window, and a drawing that is not there is an ordinary reading page.

const BoardWorkspace = dynamic(() => import("@/components/boards/board-workspace"), {
  ssr: false,
  loading: () => (
    <Page fill className="max-w-none p-2 lg:p-3">
      <LoadingPanel />
    </Page>
  ),
})

export default function BoardPage() {
  const { id } = useParams<{ id: string }>()
  return <BoardWorkspace id={Number(id)} />
}
