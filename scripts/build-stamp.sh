#!/usr/bin/env bash
# Sourced by install.sh; safe to source without changing the host.

# jd_build_commit [checkout] prints the commit a build of the checkout is
# stamped with (the JD_COMMIT build argument of backend/Dockerfile): the short
# commit when HEAD is not the release of the version the checkout carries, and
# nothing when it is. A release is the commit that its tag v<version> names,
# or the ref an update writes when it installs a tag it asked origin for
# (refs/just-dashboard/release/v<version>).
#
# Nothing is printed either when git is missing or cannot read the directory,
# so a build that cannot be described is built as it always was.
#
# The same rule as selfupdate.BuildCommit in the backend, which an update and
# a rebuild from Settings use. Change both together.
jd_build_commit() {
 local dir="${1:-.}" version head ref at safe
 command -v git >/dev/null 2>&1 || return 0
 # Setup runs as root in a checkout that is usually somebody else's, which git
 # refuses to read unless it is told the directory is safe.
 safe=$(cd -- "$dir" 2>/dev/null && pwd -P) || return 0
 set -- -c "safe.directory=$safe" -C "$dir"
 head=$(git "$@" rev-parse --verify --quiet 'HEAD^{commit}' 2>/dev/null) || return 0
 [ -n "$head" ] || return 0
 version=$(sed -n 's/^const Version = "\(.*\)"$/\1/p' "$dir/backend/internal/version/version.go" 2>/dev/null || true)
 if [ -n "$version" ]; then
  for ref in "refs/tags/v$version" "refs/just-dashboard/release/v$version"; do
   at=$(git "$@" rev-parse --verify --quiet "$ref^{commit}" 2>/dev/null) || continue
   if [ "$at" = "$head" ]; then
    return 0
   fi
  done
 fi
 git "$@" rev-parse --short HEAD 2>/dev/null || true
}
