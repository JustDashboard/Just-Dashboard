import type { Metadata } from "next"
import { headers } from "next/headers"
import { Toaster } from "@/components/ui/sonner"
import { AuthProvider } from "@/hooks/use-auth"
import { themeBootstrapScript } from "@/lib/themes"
import { sourceSans3 } from "./font"
import "./globals.css"

// No title here: each of the pages below names its own tab (`login/layout.tsx`,
// `welcome/layout.tsx`, and the dashboard's layout from the address).
export const metadata: Metadata = {
  description: "Self-hosted server management",
  robots: { index: false, follow: false },
}

export default async function RootLayout({ children }: { children: React.ReactNode }) {
  const nonce = (await headers()).get("x-nonce") ?? undefined
  // The server has no way to know which mode this browser chose, so it always
  // renders the default (dark) and the inline script below corrects the class
  // before the first paint. suppressHydrationWarning covers exactly that
  // divergence, which is confined to <html>.
  return (
    <html lang="en" className={`dark ${sourceSans3.variable}`} suppressHydrationWarning>
      <head>
        <script nonce={nonce} dangerouslySetInnerHTML={{ __html: themeBootstrapScript() }} />
      </head>
      <body className="antialiased">
        <AuthProvider>
          {children}
          {/* Position and surface are sonner's defaults — bottom right, one
              neutral popover for every type. `closeButton` is the one addition:
              a failure carries its reason underneath it and stays up for
              twelve seconds or until dismissed, which is too long to wait out. */}
          <Toaster closeButton />
        </AuthProvider>
      </body>
    </html>
  )
}
