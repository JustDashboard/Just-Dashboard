#!/bin/sh
# Mint's build image still says Ubuntu. Mint's own base-files and release
# information make it the release, as an installed Mint is: /etc/os-release
# says ID=linuxmint and /etc/upstream-release names the Ubuntu it is built on.
set -eu
apt-get update
mint="$(apt-cache madison base-files | awk '/mint/ {print $3; exit}')"
apt-get install -y --allow-downgrades "base-files=$mint" mint-info-cinnamon
apt-mark hold base-files
apt-get clean
rm -rf /var/lib/apt/lists/*
grep -q '^ID=linuxmint' /etc/os-release
