"use client"

import dynamic from "next/dynamic"
import { useParams, usePathname, useRouter, useSearchParams } from "next/navigation"
import { LoadingPanel } from "@/components/state"
import { sessionMode, type SessionMode } from "@/lib/remote-desktop"

// The Guacamole client draws into the page's own DOM and reads the window's
// size and keyboard; there is nothing of it to render on the server.
const RemoteSession = dynamic(
  () => import("@/components/remote-desktop/session").then((mod) => mod.RemoteSession),
  { ssr: false, loading: () => <LoadingPanel /> },
)

export default function RemoteSessionPage() {
  const { id } = useParams<{ id: string }>()
  const params = useSearchParams()
  const router = useRouter()
  const pathname = usePathname()
  // Which way the screen is drawn is in the address, so a stream can be
  // linked to and a reload stays in it.
  const mode = sessionMode(params.get("mode"))
  const onMode = (next: SessionMode) =>
    router.replace(next === "stream" ? `${pathname}?mode=stream` : pathname, { scroll: false })
  return <RemoteSession key={id} id={id} mode={mode} onMode={onMode} />
}
