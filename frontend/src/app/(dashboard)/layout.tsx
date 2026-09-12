"use client"

import { useEffect } from "react"
import { useViewState } from "@/lib/view-state"
import { useRouter } from "next/navigation"
import { useAuth } from "@/hooks/use-auth"
import { MetricsStream } from "@/hooks/use-metrics"
import { SelfUpdateProvider } from "@/hooks/use-self-update"
import { AppSidebar } from "@/components/app-sidebar"
import { Logo } from "@/components/logo"
import { TopBar } from "@/components/top-bar"
import { CommandPaletteProvider } from "@/components/command-palette"
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar"

export default function DashboardLayout({ children }: { children: React.ReactNode }) {
  const { status, loading } = useAuth()
  const router = useRouter()
  const [sidebarOpen, setSidebarOpen] = useViewState("shell.sidebar", true)

  // Redirecting here is a convenience, not a security control: every API call
  // behind this shell is independently authenticated by the server.
  useEffect(() => {
    if (!loading && !status?.authenticated) router.replace("/login")
  }, [loading, status, router])

  if (loading || !status?.authenticated) return <ShellSplash />

  return (
    <CommandPaletteProvider>
      {/* One poll of the dashboard's own version for the whole shell: the
          sidebar notice and the Updates page are on screen together, and the
          provider is also what keeps the poll alive across the moment the
          backend restarts itself during an upgrade. */}
      <SelfUpdateProvider>
        {/* The rail's collapsed state is controlled from here rather than left
            to the provider's own `useState`, which starts expanded on every
            load: a rail collapsed for the width it gives back is collapsed for
            the same reason on the next visit. */}
        <SidebarProvider
          open={sidebarOpen}
          onOpenChange={setSidebarOpen}
          style={{ "--sidebar-width": "15.5rem" } as React.CSSProperties}
        >
          {/* Owns the metrics socket for the whole shell, so the Overview charts
            and the top bar's vitals keep filling while you are on another
            page. Renders nothing. */}
          <MetricsStream />
          <AppSidebar />
          <SidebarInset className="h-svh min-w-0 overflow-hidden">
            <TopBar />
            {/* The scroll lives here rather than on the document, which is what
              keeps the top bar pinned and lets a page ask for the remaining
              height (`<Page fill>`) instead of growing past the viewport. */}
            <div className="min-h-0 min-w-0 flex-1 overflow-x-hidden overflow-y-auto">
              {children}
            </div>
          </SidebarInset>
        </SidebarProvider>
      </SelfUpdateProvider>
    </CommandPaletteProvider>
  )
}

/**
 * What fills the window while the session probe is in flight.
 *
 * A bare spinner on an empty background reads as a broken page; the name says
 * the app is starting, which is what is actually happening — and it is one
 * paint, not a layout that then reflows into the shell.
 */
function ShellSplash() {
  return (
    <div className="auth-backdrop flex min-h-svh flex-col items-center justify-center gap-4 bg-background">
      <div className="flex flex-col items-center gap-2.5">
        <Logo size="lg" />
        <div className="h-0.5 w-24 overflow-hidden rounded-full bg-muted">
          <div className="h-full w-1/3 animate-[loading_1.2s_ease-in-out_infinite] rounded-full bg-primary" />
        </div>
      </div>
      <style>{`@keyframes loading{0%{transform:translateX(-100%)}100%{transform:translateX(300%)}}`}</style>
    </div>
  )
}
