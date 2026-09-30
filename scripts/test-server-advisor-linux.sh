#!/usr/bin/env bash
# Run the advisor's native reads and guarded file operations in several Linux
# userlands. Containers share the host kernel; kernel-feature fallbacks have
# separate fixture tests and this is not a claim of testing every kernel.
set -euo pipefail
repo=$(cd "$(dirname "$0")/.." && pwd)
artifacts=$(mktemp -d "${TMPDIR:-/tmp}/jd-advisor-linux.XXXXXXXX")
trap 'rm -rf "$artifacts"' EXIT
cd "$repo/backend"
CGO_ENABLED=0 go test -c ./internal/files -o "$artifacts/files.test"
CGO_ENABLED=0 go test -c ./internal/procs -o "$artifacts/procs.test"
images=(ubuntu:22.04 ubuntu:24.04 debian:12 alpine:3.20 fedora:42)
for image in "${images[@]}"; do
  printf '\nTesting local advisor on %s\n' "$image"
  docker run --rm --network none --read-only --tmpfs /tmp:rw,exec,nosuid,size=192m \
    --mount "type=bind,source=$artifacts,target=/tests,readonly" \
    "$image" /tests/files.test -test.run '^TestStorage' -test.v
  docker run --rm --network none --read-only --tmpfs /tmp:rw,exec,nosuid,size=32m \
    --mount "type=bind,source=$artifacts,target=/tests,readonly" \
    "$image" /tests/procs.test -test.run '^Test(CPUInterval|CPUIntervals|Workloads|ManagerFromCgroup)' -test.v
done
# Prove the production namespace boundary as well as native userland behavior.
mkdir "$artifacts/host-fixture"
printf 'advisor host fixture\n' > "$artifacts/host-fixture/marker"
printf '\nTesting verified host filesystem mapping\n'
docker run --rm --network none --read-only --pid host --cap-add SYS_PTRACE --security-opt apparmor=unconfined \
  --mount 'type=bind,source=/,target=/host,readonly' \
  --mount "type=bind,source=$artifacts,target=/tests,readonly" \
  -e JD_ADVISOR_HOST_MOUNT_LIVE=1 -e "JD_ADVISOR_HOST_FIXTURE=$artifacts/host-fixture" \
  ubuntu:24.04 /tests/files.test -test.run '^TestStorageMountedHostFilesystem$' -test.v
