import type { Metadata, Viewport } from "next"
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

// The page is drawn edge to edge on a phone, so the safe-area insets are real
// numbers rather than always 0: the pane bar's `env(safe-area-inset-bottom)`
// and the `pb-safe` utilities keep a bottom layer clear of the home indicator,
// and the body keeps clear of the notch's sides (globals.css). The browser's
// own bar takes the page's ground (`--background`, #0d0d0d) rather than its
// white default, and its form controls and scrollbars are the dark ones before
// any stylesheet arrives. `resizes-content` shrinks the layout above an open
// keyboard on Android, so a composer or the terminal's key strip at the foot
// of the `h-svh` shell stays in view while typing; iOS ignores it.
export const viewport: Viewport = {
  viewportFit: "cover",
  themeColor: "#0d0d0d",
  colorScheme: "dark",
  interactiveWidget: "resizes-content",
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
