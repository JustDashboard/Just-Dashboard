"use client"

import { useEffect } from "react"
import { RenderFailure } from "@/components/standalone-state"
import { sourceSans3 } from "./font"
import "./globals.css"

/**
 * What is left when the root layout itself throws: this replaces it, so it
 * brings the document, the stylesheet and the face the layout would have.
 *
 * It carries no inline script. A client component cannot read the request's
 * nonce, the policy would refuse a script without one, and the layout's theme
 * bootstrap has nothing to do here: the app has one mode, and it is written
 * into the markup below.
 */
export default function GlobalError({
  error,
  reset,
}: {
  error: Error & { digest?: string }
  reset: () => void
}) {
  useEffect(() => {
    console.error("root layout failed", error)
  }, [error])

  return (
    <html lang="en" className={`dark ${sourceSans3.variable}`} style={{ colorScheme: "dark" }}>
      <body className="antialiased">
        <RenderFailure error={error} reset={reset} />
      </body>
    </html>
  )
}
