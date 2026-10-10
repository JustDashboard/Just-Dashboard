"use client"

import { useEffect } from "react"
import { useRouter } from "next/navigation"
import { useAuth } from "@/hooks/use-auth"
import { MetricsStream } from "@/hooks/use-metrics"
import { SelfUpdateProvider } from "@/hooks/use-self-update"
import { AppSidebar } from "@/components/app-sidebar"
import { Logo } from "@/components/logo"
import { SidebarInset, SidebarProvider, SidebarTrigger } from "@/components/ui/sidebar"
import { CommandPaletteProvider, useCommandPalette } from "@/components/command-palette"
import { MagnifyingGlass } from "@/components/icons"
import { Button } from "@/components/ui/button"
import { NavScopeProvider } from "@/components/nav-scope"
import { SavedFolderColours } from "@/components/files/folder-colour"
import { WorkspaceCommandsProvider } from "@/components/workspace/commands"
import { NetworkChangeConfirmation } from "@/components/network/change-confirmation"

export default function DashboardLayout({ children }: { children: React.ReactNode }) {
  const { status, loading } = useAuth()
  const router = useRouter()

  // Redirecting here is a convenience, not a security control: every API call
  // behind this shell is independently authenticated by the server.
  useEffect(() => {
    if (!loading && !status?.authenticated) router.replace("/login")
  }, [loading, status, router])

  if (loading || !status?.authenticated) return <ShellSplash />

  return (
    <WorkspaceCommandsProvider>
      <CommandPaletteProvider>
        {/* One poll of the dashboard's own version for the whole shell: the
          sidebar notice and the Updates page are on screen together, and the
          provider is also what keeps the poll alive across the moment the
          backend restarts itself during an upgrade. */}
        <SelfUpdateProvider>
          {/* A section whose pages the route cannot name on its own — the
            databases, one deployment — hands the rail its own panel through
            here, so the rail never fetches a thing to draw a list the page
            below it already holds. */}
          <NavScopeProvider>
            <SidebarProvider
              desktopCollapsible={false}
              style={{ "--sidebar-width": "15.5rem" } as React.CSSProperties}
            >
              {/* Owns the metrics socket for the whole shell, so the Overview and
              Metrics charts keep filling while you are on another page. Renders
              nothing. */}
              <MetricsStream />
              <AppSidebar />
              <SidebarInset className="h-svh min-w-0 overflow-hidden">
                {/* The bar that used to run across the top of every page is gone —
              `components/top-bar.tsx` is still there if it has to come back.
              It carried a breadcrumb every page already states in its own
              header. Below `md` the rail is a sheet with nothing left to open
              it, so this one strip stays: the trigger, and the name of the
              product it belongs to. */}
                <header className="flex h-12 shrink-0 items-center gap-2 border-b px-3 md:hidden">
                  <SidebarTrigger className="-ml-0.5 size-8 text-muted-foreground" />
                  <Logo />
                  {/* A phone has no ⌘K and the rail's search field is inside
                      a closed sheet, so search had no way in at all here. */}
                  <MobileSearch />
                </header>
                {/* The scroll lives here rather than on the document, which lets a
              page ask for the remaining height (`<Page fill>`) instead of
              growing past the viewport. */}
                <div
                  data-workspace-shell-scroll
                  className="min-h-0 min-w-0 flex-1 overflow-x-hidden overflow-y-auto"
                >
                  {/* A folder coloured in Files is that colour on every page
                  that draws it, not only on the one that coloured it. */}
                  <NetworkChangeConfirmation />
                  <SavedFolderColours>{children}</SavedFolderColours>
                </div>
              </SidebarInset>
            </SidebarProvider>
          </NavScopeProvider>
        </SelfUpdateProvider>
      </CommandPaletteProvider>
    </WorkspaceCommandsProvider>
  )
}

function MobileSearch() {
  const palette = useCommandPalette()
  return (
    <Button
      variant="ghost"
      size="icon-sm"
      aria-label="Search"
      className="ml-auto text-muted-foreground"
      onClick={palette.open}
    >
      <MagnifyingGlass className="size-4" />
    </Button>
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
