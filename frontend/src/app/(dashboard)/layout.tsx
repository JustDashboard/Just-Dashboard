"use client"

import { useEffect, useRef } from "react"
import { usePathname, useRouter } from "next/navigation"
import { useAuth } from "@/hooks/use-auth"
import { MetricsStream, useHostName } from "@/hooks/use-metrics"
import { SelfUpdateProvider } from "@/hooks/use-self-update"
import { AppSidebar } from "@/components/app-sidebar"
import { Logo, LogoStacked } from "@/components/logo"
import { tabTitle } from "@/components/nav"
import { SidebarInset, SidebarProvider, SidebarTrigger } from "@/components/ui/sidebar"
import { CommandPaletteProvider, useCommandPalette } from "@/components/command-palette"
import { MagnifyingGlass } from "@/components/icons"
import { Button } from "@/components/ui/button"
import { NavScopeProvider } from "@/components/nav-scope"
import { SavedFolderColours } from "@/components/files/folder-colour"
import { WorkspaceCommandsProvider } from "@/components/workspace/commands"
import { NetworkChangeConfirmation } from "@/components/network/change-confirmation"
import { HeldSessionsHost, HeldSessionsProvider } from "@/components/remote-desktop/held-sessions"
import { AgentDock } from "@/components/assistant/agent/dock"
import { AskAboutPage } from "@/components/assistant/ask-about"
import { HubLinkProvider, useShellHub } from "@/hooks/use-hub-link"
import { signInAddress } from "@/lib/sign-in-return"
import { waitingReason } from "@/lib/session-probe"
import { ServerSwitcher } from "@/components/hub-link/server-switcher"

/**
 * The page's name in the tab is what tells a row of dashboard tabs apart. This
 * layout is a client component, so it cannot export `metadata`; React carries
 * the element to the head instead. The root layout declares no title for that
 * reason: Next would write its own over a `document.title`, and beside this one
 * would be the first of two.
 *
 * The hub link's provider sits above the title because a linked server names
 * itself in the tab, and above the shell because the rail, the phone's strip
 * and the palette all draw the hub from it. Unlinked it asks nothing and gives
 * nothing, and every one of them is what it was.
 *
 * The name in the tab is this server's own host name, the Overview's title,
 * never the name the hub lists it under: that one is the hub's text, and a hub
 * that is taken over could make it read "Session expired - sign in"
 * (hub-link-protocol §8.10). The hub only decides whether a name is shown.
 */
export default function DashboardLayout({ children }: { children: React.ReactNode }) {
  return (
    <HubLinkProvider>
      <TabTitle />
      <DashboardShell>{children}</DashboardShell>
    </HubLinkProvider>
  )
}

function TabTitle() {
  const pathname = usePathname()
  const hub = useShellHub()
  const host = useHostName()
  return <title>{tabTitle(pathname, hub ? (host ?? undefined) : undefined)}</title>
}

function DashboardShell({ children }: { children: React.ReactNode }) {
  const { status, loading, stalled, sessionEnded } = useAuth()
  const hub = useShellHub()
  const router = useRouter()

  // Redirecting here is a convenience, not a security control: every API call
  // behind this shell is independently authenticated by the server. An
  // account that has not been asked what the dashboard is for is asked first,
  // wherever it was headed; answering or skipping ends that for good.
  //
  // A browser that arrives signed out is sent to sign in with the page it
  // asked for carried along (`signInAddress`): a link from an alert, opened
  // after the session expired overnight, still ends on the page it named.
  // One that was signed in here and no longer is has signed out or been
  // signed out, and whoever signs in next starts from the Overview.
  const onboarding = Boolean(status?.user?.needsOnboarding)
  const signedInHere = useRef(false)
  useEffect(() => {
    if (status?.authenticated) signedInHere.current = true
    if (!loading && !status?.authenticated)
      router.replace(
        sessionEnded
          ? "/login?reason=session"
          : signedInHere.current
            ? "/login"
            : signInAddress(window.location.pathname + window.location.search),
      )
    else if (onboarding) router.replace("/welcome")
  }, [loading, status, sessionEnded, onboarding, router])

  if (loading || !status?.authenticated || onboarding)
    return <ShellSplash waiting={stalled === undefined ? undefined : waitingReason(stalled)} />

  // A recovery probe can discover another tab's sign-in without leaving this
  // route. Recreate mounted editors before the new account can reuse their buffers.
  return (
    <WorkspaceCommandsProvider key={status.user?.id}>
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
            {/* Remote Desktop's sessions outlive the page that shows them, so
              a look at another page does not hang up on the computer. */}
            <HeldSessionsProvider>
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
              product it belongs to — or, on a server linked to a hub, the
              server it is, which is the switcher to the others. */}
                  <header className="flex h-12 shrink-0 items-center gap-2 border-b px-3 md:hidden">
                    <SidebarTrigger className="-ml-0.5 size-8 text-muted-foreground" />
                    {hub ? <ServerSwitcher hub={hub} strip /> : <Logo />}
                    {/* A phone has no ⌘K and the rail's search field is inside
                      a closed sheet, so search had no way in at all here; nor
                      had asking the assistant about the page. */}
                    <div className="ml-auto flex items-center gap-1">
                      <AskAboutPage />
                      <MobileSearch />
                    </div>
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
                    <HeldSessionsHost />
                    {/* The way back to a conversation with the assistant, from
                      whatever page a shortcut of its led to. */}
                    <AgentDock />
                  </div>
                </SidebarInset>
              </SidebarProvider>
            </HeldSessionsProvider>
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
      className="text-muted-foreground"
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
 *
 * A probe that has failed and is being asked again (a drained limiter, a
 * backend restarting, no network yet) keeps this up rather than sending a
 * signed-in person to sign in, and `waiting` says why it is still here: a
 * splash with no word under it, for half a minute, is the broken page again.
 */
function ShellSplash({ waiting }: { waiting?: string }) {
  return (
    <div className="auth-backdrop flex min-h-svh flex-col items-center justify-center gap-4 bg-background">
      <div className="flex flex-col items-center gap-2.5">
        <LogoStacked className="h-18" />
        <div className="h-0.5 w-24 overflow-hidden rounded-full bg-muted">
          <div className="h-full w-1/3 animate-[loading_1.2s_ease-in-out_infinite] rounded-full bg-primary" />
        </div>
      </div>
      {waiting && (
        <p role="status" className="max-w-xs px-4 text-center text-sm text-muted-foreground">
          {waiting}
        </p>
      )}
      <style>{`@keyframes loading{0%{transform:translateX(-100%)}100%{transform:translateX(300%)}}`}</style>
    </div>
  )
}
