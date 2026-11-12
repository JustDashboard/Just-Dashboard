#!/usr/bin/env bash
# Sourced by install.sh; safe to source without changing the host. Nothing
# here changes anything: it reads the machine and says what it found.

# What building the stack needs, in megabytes. README.md and
# docs/guides/install.md state the same numbers under Requirements.
#
# Memory and swap together: the frontend's compiler peaks between 2.5 and 3 GB
# and the images are built one at a time, so that peak is the whole need. The
# line is a little under 3 GB because a server sold as "3 GB" reports less
# than that once the kernel has taken its share.
JD_NEED_MEMORY_MB=2800
# Free space where Docker keeps images: both images, their build layers and
# the previous pair an update keeps.
JD_NEED_DISK_MB=10000

# jd_preflight_arch [machine] prints Docker's name for the architecture and
# fails for one the images cannot be built for (backend/Dockerfile pins rclone
# for these two only).
jd_preflight_arch() {
 case "${1:-$(uname -m 2>/dev/null)}" in
  x86_64|amd64) echo amd64 ;;
  aarch64|arm64) echo arm64 ;;
  *) return 1 ;;
 esac
}

# jd_preflight_memory prints "<memory MB> <swap MB>", or fails when the kernel
# does not say.
jd_preflight_memory() {
 awk '/^MemTotal:/ {m=$2} /^SwapTotal:/ {s=$2}
  END {if (m == "") exit 1; printf "%d %d\n", m / 1024, s / 1024}' \
  "${JD_PREFLIGHT_MEMINFO:-/proc/meminfo}" 2>/dev/null
}

# jd_preflight_disk <directory> prints the megabytes free on the filesystem
# that holds the directory, or would hold it once it is created.
jd_preflight_disk() {
 local dir="$1"
 while [ ! -e "$dir" ] && [ "$dir" != / ] && [ "$dir" != . ]; do
  dir="$(dirname -- "$dir")"
 done
 df -Pm -- "$dir" 2>/dev/null | awk 'NR == 2 && $4 ~ /^[0-9]+$/ {print $4; found = 1} END {exit !found}'
}

jd_preflight_gb() { awk -v mb="$1" 'BEGIN {printf "%.1f GB", mb / 1024}'; }

# jd_preflight prints one line per thing it looked at, as "<kind><TAB><what>":
#
#   ok    the machine has what is needed
#   warn  setup can go on, and the build is likely to fail for this reason
#   stop  the dashboard cannot be installed on this machine at all
#
# The caller decides what to do with them; install.sh stops on any stop line
# before it has installed or written anything, and repeats the warn lines if
# the build then fails. A thing that cannot be measured is left out rather
# than guessed at.
jd_preflight() {
 local machine arch memory swap free docker_root
 machine="$(uname -m 2>/dev/null || true)"
 if arch="$(jd_preflight_arch "$machine")"; then
  printf 'ok\t%s (%s)\n' "$machine" "$arch"
 else
  printf 'stop\tThis server is %s. Just Dashboard builds for x86_64 (amd64) and aarch64 (arm64) only.\n' "${machine:-an architecture uname cannot name}"
 fi

 if [ -d "${JD_PREFLIGHT_SYSTEMD:-/run/systemd/system}" ]; then
  printf 'ok\tsystemd\n'
 else
  printf 'stop\tThis server does not run systemd (there is no /run/systemd/system). Just Dashboard manages services and timers through it and relies on it to uninstall itself, so it cannot be installed here.\n'
 fi

 if read -r memory swap < <(jd_preflight_memory); then
  if [ $((memory + swap)) -lt "$JD_NEED_MEMORY_MB" ]; then
   printf 'warn\t%s of memory and %s of swap. Building needs about 3 GB between the two, so the build is likely to be killed part-way. Add swap first: docs/guides/install.md shows how.\n' "$(jd_preflight_gb "$memory")" "$(jd_preflight_gb "$swap")"
  else
   printf 'ok\t%s of memory and %s of swap\n' "$(jd_preflight_gb "$memory")" "$(jd_preflight_gb "$swap")"
  fi
 fi

 docker_root=""
 if command -v docker >/dev/null 2>&1; then
  docker_root="$(docker info --format '{{.DockerRootDir}}' 2>/dev/null || true)"
 fi
 case "$docker_root" in /*) ;; *) docker_root=/var/lib/docker ;; esac
 if free="$(jd_preflight_disk "$docker_root")"; then
  if [ "$free" -lt "$JD_NEED_DISK_MB" ]; then
   printf 'warn\t%s free under %s. The images and their build need about 10 GB, so the build is likely to stop with "no space left on device".\n' "$(jd_preflight_gb "$free")" "$docker_root"
  else
   printf 'ok\t%s free under %s\n' "$(jd_preflight_gb "$free")" "$docker_root"
  fi
 fi
}
