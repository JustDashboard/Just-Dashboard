import type { Metadata } from "next"
import { TooltipProvider } from "@/components/ui/tooltip"

export const metadata: Metadata = { title: "Sign in · Just Dashboard" }

// Sign-in stands outside the dashboard's shell, whose sidebar gives every page its tooltips, and
// its copy controls are IconActions, whose name is also their tooltip.
export default function Layout({ children }: { children: React.ReactNode }) {
  return <TooltipProvider>{children}</TooltipProvider>
}
