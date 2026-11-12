import localFont from "next/font/local"

/*
 * The italic of the UI face: the same family as `./font.ts`, fetched when a
 * page first draws italic text rather than preloaded on every page.
 *
 * **The constant must keep the name `sourceSans3`.** The build names a font's
 * family after the constant it is assigned to, so the same name here is what
 * makes this an italic face *of the upright's family*, picked by the browser
 * for `font-style: italic` with nothing else to say. Under another name it
 * would be a second family nobody asks for, and italics would be the upright
 * slanted by the browser. `app-states.spec.ts` checks that the page's family
 * has both faces and that only the upright is preloaded.
 *
 * No fallback face of its own: the family's fallback metrics are the
 * upright's, and a second set under the same name would replace them.
 */
export const sourceSans3 = localFont({
  src: [{ path: "./fonts/SourceSans3VF-Italic.woff2", weight: "200 900", style: "italic" }],
  variable: "--font-source-sans-3-italic",
  display: "swap",
  preload: false,
  adjustFontFallback: false,
})
