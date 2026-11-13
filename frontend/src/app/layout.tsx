import type { Metadata } from "next"
import { headers } from "next/headers"
import { Toaster } from "@/components/ui/sonner"
import { AuthProvider } from "@/hooks/use-auth"
import { API_BASE } from "@/lib/api"
import { sessionProbeScript } from "@/lib/session-probe"
import { themeBootstrapScript } from "@/lib/themes"
import { sourceSans3 } from "./font"
import { sourceSans3 as sourceSans3Italic } from "./font-italic"
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
    <html
      lang="en"
      className={`dark ${sourceSans3.variable} ${sourceSans3Italic.variable}`}
      suppressHydrationWarning
    >
      <head>
        <script nonce={nonce} dangerouslySetInnerHTML={{ __html: themeBootstrapScript() }} />
        {/* Every page below is a splash until the session is known, so the
            question is asked from here, while the JavaScript that will read
            the answer is still downloading (`AuthProvider` takes it). */}
        <script nonce={nonce} dangerouslySetInnerHTML={{ __html: sessionProbeScript(API_BASE) }} />
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
