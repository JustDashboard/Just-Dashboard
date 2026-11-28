#!/bin/sh
# Tumbleweed's repositories redirect each file to a mirror picked at random,
# and zypper waits without end on one that never answers (it stalled two runs
# out of three here). openSUSE's own CDN serves the same repositories.
set -eu
sed -i 's|download\.opensuse\.org|cdn.opensuse.org|g' /etc/zypp/repos.d/*.repo
grep -q cdn.opensuse.org /etc/zypp/repos.d/*.repo
