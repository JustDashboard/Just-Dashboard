import localFont from "next/font/local"

/* The UI face is bundled so builds and running servers need no font CDN. */
export const sourceSans3 = localFont({
  src: [
    { path: "./fonts/SourceSans3VF-Upright.woff2", weight: "200 900", style: "normal" },
    { path: "./fonts/SourceSans3VF-Italic.woff2", weight: "200 900", style: "italic" },
  ],
  variable: "--font-source-sans-3",
  display: "swap",
})
