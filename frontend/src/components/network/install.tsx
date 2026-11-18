/**
 * @deprecated import from `@/components/install-handoff`. The hand-off moved
 * there and draws through `MissingThing`; this name stays until the importers
 * outside Network move and WP-39 deletes it. One module, so a package
 * installed from either import is one `recent` store.
 */
export {
  InstallHandoff,
  InstallFollowUp,
  HandoffPhases,
  usePackageKnown,
  type PackageKnown,
} from "@/components/install-handoff"
