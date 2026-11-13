/**
 * Where the runtimes copied into `public/` are served from.
 *
 * Monaco and Excalidraw's fonts are copied out of `node_modules` by
 * `scripts/sync-monaco.mjs` and `scripts/sync-excalidraw.mjs`, each under the
 * version of the package it came from, so the browser can keep the files for
 * good and an update asks under another path. `next.config.ts` reads the same
 * two versions from the installed packages and writes these addresses into the
 * build, so the copy, the address and the cache rule cannot disagree.
 */

/** Monaco's `vs` folder, as the loader's `paths.vs`: `/monaco/<version>/vs`. */
export const MONACO_VS = process.env.NEXT_PUBLIC_MONACO_VS as string

/** What Excalidraw puts before `fonts/…`: `/excalidraw/<version>/`. */
export const EXCALIDRAW_ASSETS = process.env.NEXT_PUBLIC_EXCALIDRAW_ASSETS as string
