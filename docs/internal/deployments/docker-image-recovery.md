# Recovering an existing container whose image was deleted

Managed adoption first pins each existing service's exact local image ID. When an original image was
force-deleted but its container still exists, `dockerx.RecoverAdoptionImage` can preserve that container's
root filesystem as a new local recovery image. This path needs an authoritative Linux amd64 or arm64
platform descriptor retained by Docker's container inspection; it never guesses architecture from the
host or a binary. Missing descriptors, unsupported platforms, and meaningful writable-layer changes
block recovery with a reason to restore the original image or supply its source.

The adapter uses Docker's read-only container export API. It does not pause, stop, restart or signal the
application, execute a command inside it, or use `docker commit`. Docker export excludes mounted volume
contents. The existing mounts are recovered separately as external volumes, contained bind paths and
existing network identities. An export is not a database backup. Review and verify a persistent-data
backup before Deploy changes because an image/configuration rollback cannot undo database, schema or
file changes in those existing mounts.

Only the already verified regenerable Python bytecode additions are removed from the captured archive.
Unknown additions, source modifications, unverified directory changes and writable-layer data still
block adoption. The archive preserves rootfs metadata and is capped at 2 GiB and 100,000 entries. It is
staged in a private cache directory, using regular files with mode 0600 and rejecting symlink artifacts.
No archive is returned to the browser.

Image import sets only the dashboard's source-identity label. Captured environment variables,
entrypoint, command and original labels stay in encrypted deployment inputs, outside the imported image
configuration. The imported tag is `just-dashboard/adoption-recovery:<source-hash>`. A private manifest
records its exact image ID, source hash, archive hash and platform. The source hash fences container ID,
original image identity, effective configuration, resolved mounts/networks, process PID/start time,
platform descriptor and writable-layer diff. Recovery checks the fence before and after export;
ordinary health polling does not change the identity. Retrying recovery reuses the exact owned image
after checking its ID, controlled label and absence of environment/command configuration. The adoption
API additionally checks a fresh captured baseline before committing ownership.

A declared Compose service without an existing container cannot use this fallback. If its image is
also missing, recovery blocks with `inactive_service_image_missing`; the dashboard cannot invent that
service's source or filesystem. Adoption preserves stopped containers and absent services exactly.
Deploy changes explicitly applies the reviewed recipe. Replica-specific storage or network identities
that cannot be faithfully expressed as one Compose service also block recovery.

Keep the recovery image and private source cache available for future deployments and rollback.
Isolated proof tooling may clean up only the exact images recorded in its own cache manifests, after
checking `io.just-dashboard.adoption-source` matches each manifest. It must not remove an operator's
original image, prune the image store, or infer ownership from an arbitrary image name.

The opt-in tests create and clean up only their own loopback HTTP applications, volumes, networks and
unique images. `TestLiveDeletedImageExportDoesNotChangeOriginalRuntime` checks export continuity and
secret-free raw import. `TestLiveManagedDeletedImageContainerAdoptionAndRollback` exercises the
production cached fallback, repeated recovery, nonmutating adoption, failed Deploy restoration,
successful managed deployment and rollback while preserving HTTP, persistent data and network aliases:

```bash
cd backend
JD_DOCKER_ADOPTION_LIVE=1 JD_ADOPTION_EVIDENCE_DIR=/tmp/jd-managed-adoption-evidence \
  go test ./internal/deploy -run '^TestLiveManagedDeletedImageContainerAdoptionAndRollback$' -count=1 -v
```

Docker documents [export's volume exclusion](https://docs.docker.com/reference/cli/docker/container/export/),
[import's supported configuration changes](https://docs.docker.com/reference/cli/docker/image/import/),
and [commit's default pause behavior](https://docs.docker.com/reference/cli/docker/container/commit/).
