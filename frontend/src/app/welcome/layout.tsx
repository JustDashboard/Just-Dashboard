import type { Metadata } from "next"

export const metadata: Metadata = { title: "Welcome · Just Dashboard" }

export default function Layout({ children }: { children: React.ReactNode }) {
  return children
}
