import { bytes, plural } from "@/lib/format"

/**
 * What a folder holds, as the sentence a person would say: "12 folders,
 * 40 files, 1.2 GB".
 *
 * A kind there is none of is left out rather than counted as zero, and the
 * size joins only once it has been measured: a folder's size is a walk of the
 * disk somebody asks for, not a figure the listing has. A folder with nothing
 * in it has no sentence, so the caller says "Empty" in its own words.
 */
export function folderSentence(
  folders: number,
  files: number,
  size?: number | null,
): string | undefined {
  const parts = [
    folders > 0 && plural(folders, "folder"),
    files > 0 && plural(files, "file"),
    size !== undefined && size !== null && (folders > 0 || files > 0 || size > 0) && bytes(size),
  ].filter(Boolean)
  return parts.length > 0 ? parts.join(", ") : undefined
}
