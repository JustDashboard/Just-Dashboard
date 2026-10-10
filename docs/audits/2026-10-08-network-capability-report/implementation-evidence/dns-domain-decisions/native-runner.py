import hashlib
import json
import os
import pathlib
import re
import secrets
import stat
import subprocess
import sys
import tempfile
import time

ROOT = pathlib.Path("/home/ubuntu/Just-Dashboard-network-dns-decision-restart-endpoint")
ARTIFACTS = pathlib.Path("/home/ubuntu/Just-Dashboard-network-dns-decision-example-artifacts")
COMPILE = ARTIFACTS / "decision-example-compile-result.json"
EXPECTED_COMPILE = "23532af5fa4aa5ba2e5de90d9637fb908da63b9c619cbecc1a585d5eca21f02e"
EXPECTED_SOURCE = "8301171bc48c0ce9c3bae605ad72ed56e494bc7b"
DOCKER_ENDPOINT = "unix:///var/run/docker.sock"
TMP_PARENT = pathlib.Path("/home/ubuntu/jd-de-t")
ENGINE = sys.argv[1] if len(sys.argv) == 2 else ""
assert ENGINE in ("adguard", "pihole"), "one reviewed engine required"
PREFIX = ARTIFACTS / ("native-decision-example-" + ENGINE)
assert all(not PREFIX.with_suffix(s).exists() for s in (".log", ".source-binary.json", ".result.json")), "one attempt only"


def sha(data):
    return hashlib.sha256(data).hexdigest()


def directory_identity(observed):
    assert stat.S_ISDIR(observed.st_mode)
    return {"dev": observed.st_dev, "inode": observed.st_ino, "uid": observed.st_uid, "gid": observed.st_gid, "mode": stat.S_IMODE(observed.st_mode)}


def verify_tmp_scope(parent, name, parent_fd, task_fd, receipt):
    assert directory_identity(os.fstat(parent_fd)) == receipt["parent"]
    assert directory_identity(os.lstat(parent)) == receipt["parent"]
    assert directory_identity(os.fstat(task_fd)) == receipt["task"]
    assert directory_identity(os.stat(name, dir_fd=parent_fd, follow_symlinks=False)) == receipt["task"]


def open_tmp_scope(parent, engine):
    parent_fd = os.open(parent, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC)
    task_fd = None
    try:
        parent_identity = directory_identity(os.fstat(parent_fd))
        assert parent_identity["uid"] == 1000 and parent_identity["mode"] == 0o700
        assert directory_identity(os.lstat(parent)) == parent_identity and not os.listdir(parent_fd)
        name = "n-" + engine[:3] + "-" + secrets.token_hex(8)
        os.mkdir(name, 0o700, dir_fd=parent_fd)
        task_fd = os.open(name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC, dir_fd=parent_fd)
        receipt = {"parent": parent_identity, "task": directory_identity(os.fstat(task_fd)), "name": name}
        verify_tmp_scope(parent, name, parent_fd, task_fd, receipt)
        return parent / name, parent_fd, task_fd, receipt
    except BaseException:
        if task_fd is not None:
            os.close(task_fd)
        os.close(parent_fd)
        raise


def finish_tmp_scope(parent, parent_fd, task_fd, receipt):
    name = receipt["name"]
    verify_tmp_scope(parent, name, parent_fd, task_fd, receipt)
    remaining = sorted(os.listdir(task_fd))
    verify_tmp_scope(parent, name, parent_fd, task_fd, receipt)
    removed = False
    if not remaining:
        os.rmdir(name, dir_fd=parent_fd)
        assert os.fstat(task_fd).st_nlink == 0
        assert directory_identity(os.fstat(parent_fd)) == receipt["parent"]
        assert directory_identity(os.lstat(parent)) == receipt["parent"]
        try:
            os.stat(name, dir_fd=parent_fd, follow_symlinks=False)
        except FileNotFoundError:
            removed = True
        else:
            raise AssertionError("task name rebound after owned removal")
    return {"entries_remaining": remaining, "owned_directory_removed": removed, "directory_receipt": receipt}


def source():
    return {
        "commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT).decode().strip(),
        "clean": subprocess.check_output(["git", "status", "--porcelain"], cwd=ROOT) == b"",
        "go": {str(p.relative_to(ROOT)): sha(p.read_bytes()) for p in sorted((ROOT / "backend").rglob("*.go"))},
        "modules": {p: sha((ROOT / p).read_bytes()) for p in ("backend/go.mod", "backend/go.sum")},
    }


def asset(receipt):
    path = pathlib.Path(receipt["path"])
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        before = os.fstat(fd)
        assert stat.S_ISREG(before.st_mode) and before.st_size <= 64 << 20
        assert before.st_uid == 1000 and before.st_nlink == 1 and not before.st_mode & 0o022
        data = bytearray()
        while True:
            part = os.read(fd, 1 << 20)
            if not part:
                break
            data.extend(part)
            assert len(data) <= 64 << 20
        after = os.fstat(fd)
        current = os.lstat(path)
        keys = {"dev": "st_dev", "inode": "st_ino", "uid": "st_uid", "gid": "st_gid", "nlink": "st_nlink", "size": "st_size", "mtime_ns": "st_mtime_ns", "ctime_ns": "st_ctime_ns"}
        for observed in (before, after, current):
            assert stat.S_ISREG(observed.st_mode) and stat.S_IMODE(observed.st_mode) == receipt["mode"]
            assert all(getattr(observed, field) == receipt[key] for key, field in keys.items())
        assert sha(data) == receipt["sha256"]
        return receipt["sha256"]
    finally:
        os.close(fd)


prepared_bytes = COMPILE.read_bytes()
assert sha(prepared_bytes) == EXPECTED_COMPILE
prepared = json.loads(prepared_bytes)
assert prepared["source_commit"] == EXPECTED_SOURCE and prepared["backend_source_count"] == 1869
expected_source = {"commit": EXPECTED_SOURCE, "clean": True, "go": prepared["backend_source_sha256"], "modules": prepared["dependency_files_sha256"]}
assert source() == expected_source
BINARY = pathlib.Path(prepared["assets"]["native-domain-decisions-phase-race.test"]["path"])
HELPER = pathlib.Path(prepared["assets"]["native-dns-decisions-phase-helper"]["path"])
for receipt in prepared["assets"].values():
    asset(receipt)
for tool in ("compile", "link"):
    assert sha((pathlib.Path(prepared["compiler"]["tool_directory"]) / tool).read_bytes()) == prepared["compiler"][tool + "_sha256"]
runner_before = sha(pathlib.Path(__file__).read_bytes())

DOCKER_ENV = os.environ.copy()
for key in ("DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH", "DOCKER_API_VERSION"):
    DOCKER_ENV.pop(key, None)


def docker_socket():
    st = pathlib.Path("/var/run/docker.sock").stat()
    assert stat.S_ISSOCK(st.st_mode)
    return {"dev": st.st_dev, "inode": st.st_ino, "uid": st.st_uid, "gid": st.st_gid, "mode": st.st_mode}


def docker(args):
    return subprocess.check_output(["docker", "--host", DOCKER_ENDPOINT, *args], env=DOCKER_ENV, timeout=10).decode().splitlines()


def inventory():
    result = {}
    for label in ("io.justdashboard.dns.provision", "io.justdashboard.dns.decision-fixture"):
        for kind in ("container", "network", "volume"):
            result[label + ":" + kind] = sorted(docker([kind, "ls", "-q", "--filter", "label=" + label]))
    result["decision_images"] = sorted(set(docker(["image", "ls", "-a", "-q", "--no-trunc", "--filter", "label=io.justdashboard.dns.decision-fixture"])))
    return result


def processes():
    found = []
    for p in pathlib.Path("/proc").iterdir():
        if not p.name.isdigit():
            continue
        try:
            argv = (p / "cmdline").read_bytes().split(b"\0")
        except (FileNotFoundError, ProcessLookupError, PermissionError):
            continue
        if str(BINARY).encode() in argv or b"/dns-fixture" in argv:
            found.append({"pid": int(p.name), "argv": [a.decode(errors="replace") for a in argv if a]})
    return found


def host():
    proc = pathlib.Path("/proc/883")
    fields = {}
    for line in (proc / "status").read_text().splitlines():
        if line.split(":", 1)[0] in ("Name", "Uid", "Gid"):
            key, value = line.split(":", 1)
            fields[key] = value.split()
    start = (proc / "stat").read_text().rsplit(")", 1)[1].split()[19]
    comm = (proc / "comm").read_text().strip()
    assert start == "452" and comm == "systemd-network" and fields == {"Name": ["systemd-network"], "Uid": ["998"] * 4, "Gid": ["998"] * 4}
    return {"resolver_sha256": sha(pathlib.Path("/etc/resolv.conf").read_bytes()), "networkd883": {"start": start, "comm": comm, **fields}, "nm_paths": {p: os.path.lexists(p) for p in ("/etc/NetworkManager", "/var/lib/NetworkManager")}, "mountinfo_sha256": sha(pathlib.Path("/proc/self/mountinfo").read_bytes()), "namespaces": {n: os.readlink("/proc/self/ns/" + n) for n in ("net", "mnt", "pid")}}


before = host()
assert not any(before["nm_paths"].values())
socket_before = docker_socket()
prior = inventory()
assert not any(prior.values()) and not processes(), "foreign/prior fixture remains; refuse dispatch"
images_before = sorted(set(docker(["image", "ls", "-a", "-q", "--no-trunc"])))
task, tmp_parent_fd, tmp_task_fd, tmp_receipt = open_tmp_scope(TMP_PARENT, ENGINE)
env = DOCKER_ENV.copy()
for key in tuple(env):
    if key.startswith("JD_DNS_"):
        del env[key]
env.update({"JD_DNS_DOMAIN_DECISIONS_LIVE": "1", "JD_DNS_SERVICES_LIVE_ENGINE": ENGINE, "JD_DNS_SERVICES_DOCKER_HOST": DOCKER_ENDPOINT, "JD_DNS_DECISION_HELPER": str(HELPER), "JD_DNS_DECISION_HELPER_SHA256": prepared["assets"]["native-dns-decisions-phase-helper"]["sha256"], "TMPDIR": str(task), "GOMAXPROCS": "2", "GOFLAGS": "-p=2"})
argv = ["timeout", "--kill-after=5s", "240s", str(BINARY), "-test.run", "^TestDNSServiceNativeDomainDecisions$", "-test.count=1", "-test.timeout=235s", "-test.v"]
manifest = {"phase": "one_source_frozen_actual_decision_attempt", "source_commit": EXPECTED_SOURCE, "compile_receipt_sha256": EXPECTED_COMPILE, "runner_sha256": runner_before, "assets": prepared["assets"], "backend_source_sha256": prepared["backend_source_sha256"], "dependency_files_sha256": prepared["dependency_files_sha256"], "argv": argv, "cwd": str(ROOT / "backend/internal/dnsservice"), "environment": {k: env[k] for k in ("JD_DNS_DOMAIN_DECISIONS_LIVE", "JD_DNS_SERVICES_LIVE_ENGINE", "JD_DNS_SERVICES_DOCKER_HOST", "JD_DNS_DECISION_HELPER", "JD_DNS_DECISION_HELPER_SHA256", "TMPDIR", "GOMAXPROCS", "GOFLAGS")}, "host_before": before, "docker_socket_before": socket_before, "owned_docker_before": prior, "all_image_ids_before": images_before, "query_matrix_budget_sha256": sha((ARTIFACTS / "decision-phase-query-matrix-budget.json").read_bytes())}
with PREFIX.with_suffix(".source-binary.json").open("x") as f:
    manifest["tmp_directory_receipt"] = tmp_receipt
    json.dump(manifest, f, indent=2)
    f.write("\n")
started = time.monotonic()
with PREFIX.with_suffix(".log").open("xb") as log:
    completed = subprocess.run(argv, cwd=ROOT / "backend/internal/dnsservice", env=env, stdout=log, stderr=subprocess.STDOUT)
wall = time.monotonic() - started
raw = PREFIX.with_suffix(".log").read_bytes()
errors = []
result = {"source_commit": EXPECTED_SOURCE, "engine": ENGINE, "exit_code": completed.returncode, "wall_seconds": round(wall, 3), "raw_sha256": sha(raw), "skipped": b"--- SKIP:" in raw, "owned_processes_remaining": processes()}
for key, read in (("host_after", host), ("docker_socket_after", docker_socket), ("owned_docker_remaining", inventory), ("all_image_ids_after", lambda: sorted(set(docker(["image", "ls", "-a", "-q", "--no-trunc"]))))):
    try:
        result[key] = read()
    except Exception as e:
        errors.append({"reading": key, "error_class": type(e).__name__})
remaining = ["unknown"]
try:
    terminal_tmp = finish_tmp_scope(TMP_PARENT, tmp_parent_fd, tmp_task_fd, tmp_receipt)
    result["owned_tmp_scope"] = terminal_tmp
    remaining = terminal_tmp["entries_remaining"]
except Exception as e:
    errors.append({"reading": "tmp_scope_reverification", "error_class": type(e).__name__})
finally:
    os.close(tmp_task_fd)
    os.close(tmp_parent_fd)
result["owned_tmp_entries_remaining"] = remaining
try:
    frozen = source() == expected_source and sha(COMPILE.read_bytes()) == EXPECTED_COMPILE and sha(pathlib.Path(__file__).read_bytes()) == runner_before
    frozen = frozen and all(asset(r) == r["sha256"] for r in prepared["assets"].values())
    result["frozen_source_assets_runner_unchanged"] = frozen
except Exception as e:
    errors.append({"reading": "source_asset_reverification", "error_class": type(e).__name__})
    result["frozen_source_assets_runner_unchanged"] = False
result["host_unchanged"] = result.get("host_after") == before
result["docker_socket_unchanged"] = result.get("docker_socket_after") == socket_before
result["all_image_ids_unchanged"] = result.get("all_image_ids_after") == images_before
result["errors"] = errors
sequences = [int(x) for x in re.findall(rb"decision sequence=(\d+) ", raw)]
summary = re.findall(rb"queries=(\d+) budget=256 rate<=8/s scope=IPv4-client-A-AAAA-UDP-TCP", raw)
result["complete_bounded_question_receipt"] = len(summary) == 1 and 184 <= int(summary[0]) <= 256 and sequences == list(range(1, int(summary[0]) + 1))
result["question_count"] = int(summary[0]) if len(summary) == 1 else None
result["terminal_pass"] = b"--- PASS: TestDNSServiceNativeDomainDecisions (" in raw and raw.endswith(b"PASS\n")
result["fresh_history_phase_receipts"] = len(re.findall(rb"native query history corroborated engine=.+ phaseNotBefore=.+ readNotAfter=.+ transportBasis=", raw)) == 2
result["accepted"] = result["exit_code"] == 0 and result["terminal_pass"] and not result["skipped"] and not result["owned_processes_remaining"] and not remaining and not any(result.get("owned_docker_remaining", {"unknown": [1]}).values()) and result["host_unchanged"] and result["docker_socket_unchanged"] and result["all_image_ids_unchanged"] and result["frozen_source_assets_runner_unchanged"] and result["complete_bounded_question_receipt"] and result["fresh_history_phase_receipts"] and not errors
with PREFIX.with_suffix(".result.json").open("x") as f:
    json.dump(result, f, indent=2)
    f.write("\n")
print(json.dumps(result, indent=2), flush=True)
print(raw.decode(errors="replace")[-6000:], flush=True)
if not result["accepted"]:
    raise SystemExit(1)
