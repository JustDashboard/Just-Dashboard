"use client"

import dynamic from "next/dynamic"
import { useParams } from "next/navigation"
import { LoadingPanel } from "@/components/state"

// The Guacamole client draws into the page's own DOM and reads the window's
// size and keyboard; there is nothing of it to render on the server.
const RemoteSession = dynamic(
  () => import("@/components/remote-desktop/session").then((mod) => mod.RemoteSession),
  { ssr: false, loading: () => <LoadingPanel /> },
)

export default function RemoteSessionPage() {
  const { id } = useParams<{ id: string }>()
  return <RemoteSession key={id} id={id} />
}
