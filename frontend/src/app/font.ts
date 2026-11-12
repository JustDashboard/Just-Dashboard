import localFont from "next/font/local"

/*
 * The UI face is bundled so builds and running servers need no font CDN.
 *
 * Only the upright file is declared here, because every file one `localFont`
 * call names is preloaded, and the italic (138 KB) then competed with the
 * page's JavaScript on every first visit to draw a face most screens never
 * use. The italic is `./font-italic.ts`, the same family without the preload.
 */
export const sourceSans3 = localFont({
  src: [{ path: "./fonts/SourceSans3VF-Upright.woff2", weight: "200 900", style: "normal" }],
  variable: "--font-source-sans-3",
  display: "swap",
})
