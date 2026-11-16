"use client"

import { useEffect } from "react"
import { useRouter } from "next/navigation"
import { useAuth } from "@/hooks/use-auth"
import { WelcomeFlow } from "@/components/onboarding/welcome-flow"
// For what it does on load: a browser that paused the product's animations is
// paused here too, outside the shell that holds the switch.
import "@/components/shell/motion-pause"

/**
 * The first-run question, outside the shell: the rail is what it decides, so
 * it is not drawn yet. The shell sends a new account here after sign-in; an
 * account that has answered can come back from Account → Workspace.
 *
 * Its own scroll container, as the shell's `SidebarInset` is for every other
 * page, so a flow screen held to the window (`<Page fill="xl">`) has a
 * definite height to hold to. It stands on sign-in's ground, the screen it
 * follows. It is the window's `<main>`, as sign-in's panel is: with no shell
 * there is no other landmark, and the flow was content in none.
 */
export default function WelcomePage() {
  const { status, loading } = useAuth()
  const router = useRouter()

  // A convenience, as in the shell: the save behind this page is
  // authenticated by the server whatever the browser was shown.
  useEffect(() => {
    if (!loading && !status?.authenticated) router.replace("/login")
  }, [loading, status, router])

  return (
    <main className="auth-backdrop h-svh overflow-y-auto bg-background">
      {!loading && status?.authenticated && <WelcomeFlow />}
    </main>
  )
}
