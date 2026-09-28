"""Deployment acceptance against a real backend and a real Docker daemon.

Builds (or reuses, JD_E2E_BACKEND) the backend, starts it on a loopback port
with a fresh data directory, starts a local webhook receiver, then drives the
public API exactly as the UI does: create a signed-webhook channel, deploy an
nginx image with resource limits, deploy a busybox image that exits before it
listens, restart the healthy release, and check what the product tells the
operator at each step — notifications, transcript, evidence and Docker state.

    python3 scripts/e2e-deployments.py

It needs Docker (busybox:1.36, nginx:1.27-alpine and postgres:16-alpine are pulled),
Go from go.mod, and free loopback ports 8090 and 18081 (JD_E2E_PORT,
JD_E2E_HOOK_PORT). It touches only its own data directory and the containers
of the environments it created; the running dashboard on the same host is not
involved. Exit status is non-zero when any check fails; report.json and
backend.log stay in the work directory (JD_E2E_DIR) for evidence.
"""
import json
import os
import re
import secrets
import signal
import sqlite3
import subprocess
import sys
import tempfile
import threading
import time
import urllib.request
from http.server import BaseHTTPRequestHandler, HTTPServer

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ROOT = os.environ.get("JD_E2E_DIR") or tempfile.mkdtemp(prefix="jd-e2e-")
DATA = os.path.join(ROOT, "data")
BACKEND = os.environ.get("JD_E2E_BACKEND") or os.path.join(ROOT, "just-dashboard")
PORT = int(os.environ.get("JD_E2E_PORT", "8090"))
API = f"http://127.0.0.1:{PORT}/api/v1"
HOOK_PORT = int(os.environ.get("JD_E2E_HOOK_PORT", "18081"))
PASSWORD = "E2e-" + secrets.token_hex(8)
RUN_SUFFIX = secrets.token_hex(6)
received = []


class Hook(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length)
        received.append({"headers": dict(self.headers), "body": json.loads(body or b"{}")})
        self.send_response(200)
        self.end_headers()

    def log_message(self, *_):
        return


def start_hook():
    server = HTTPServer(("127.0.0.1", HOOK_PORT), Hook)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server


def build_backend():
    if os.path.exists(BACKEND):
        return
    subprocess.run(["go", "build", "-o", BACKEND, "./cmd/server"], cwd=os.path.join(REPO, "backend"), check=True)


def start_backend():
    build_backend()
    os.makedirs(DATA, exist_ok=True)
    env = dict(os.environ)
    env.update({
        "JD_ADDR": f"127.0.0.1:{PORT}",
        "JD_DATA_DIR": DATA,
        "JD_MASTER_KEY": secrets.token_hex(32),
        "JD_ALLOWED_CIDRS": "127.0.0.1/32",
        "JD_BOOTSTRAP_USER": "e2e",
        "JD_BOOTSTRAP_PASSWORD": PASSWORD,
        "JD_DEPLOY_ROOTS": ROOT,
        "JD_BACKUP_DIR": os.path.join(ROOT, "backups"),
        "JD_UPDATE_CHECK": "false",
        "JD_TERMINAL_ENABLED": "false",
        "JD_SITE": "e2e.localhost",
        "JD_LOG_LEVEL": "info",
    })
    log = open(os.path.join(ROOT, "backend.log"), "w")
    proc = subprocess.Popen([BACKEND], env=env, stdout=log, stderr=subprocess.STDOUT)
    for _ in range(100):
        try:
            with urllib.request.urlopen(f"http://127.0.0.1:{PORT}/healthz", timeout=1) as res:
                if res.status == 200:
                    return proc
        except Exception:
            if proc.poll() is not None:
                break
            time.sleep(0.3)
    proc.kill()
    raise SystemExit("backend did not become healthy; see backend.log")


# The session cookie is marked Secure because the dashboard is normally served
# over TLS; a cookie jar would drop it over loopback HTTP, so it is carried by
# hand exactly as the browser would carry it.
session_cookie = {"value": ""}
opener = urllib.request.build_opener()


def call(method, path, body=None, expect=(200, 201, 202)):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(API + path, data=data, method=method)
    req.add_header("X-JD-CSRF", "1")
    if session_cookie["value"]:
        req.add_header("Cookie", "vpsd_session=" + session_cookie["value"])
    if data is not None:
        req.add_header("Content-Type", "application/json")
    try:
        with opener.open(req, timeout=30) as res:
            raw = res.read()
            status = res.status
            for header in res.headers.get_all("Set-Cookie") or []:
                match = re.match(r"vpsd_session=([^;]+)", header)
                if match:
                    session_cookie["value"] = match.group(1)
    except urllib.error.HTTPError as err:
        raw = err.read()
        status = err.code
    parsed = json.loads(raw) if raw else None
    if status not in expect:
        raise SystemExit(f"{method} {path} -> {status}: {raw[:800]!r}")
    return parsed


def plan_project(name, image, command, port, checks, profile="web", limits=None, environment=None):
    draft = call("POST", "/deploy/drafts", {})
    draft = call("PUT", f"/deploy/drafts/{draft['id']}", {
        "revision": draft["revision"], "step": "intent", "intent": {"name": name, "profile": profile}})
    draft = call("PUT", f"/deploy/drafts/{draft['id']}", {
        "revision": draft["revision"], "step": "source",
        "source": {"kind": "image", "mode": "image_reference", "image": image}})
    draft = call("POST", f"/deploy/drafts/{draft['id']}/detect", {"revision": draft["revision"]})
    runtime = {
        "image": image, "command": command, "internalPort": port, "hostPort": 0,
        "bindAddress": "127.0.0.1", "strategy": "stop_first", "privileged": False,
        "hostNetwork": False, "capabilities": [], "devices": [], "mounts": [],
    }
    runtime.update(limits or {})
    configuration = {
        "build": {"method": "image", "noCache": False, "secrets": [], "releaseTasks": []},
        "runtime": runtime, "variables": [], "dependencies": [], "checks": checks, "domains": [],
    }
    for key in environment or {}:
        configuration["variables"].append({
            "name": key, "sensitivity": "secret", "scopes": ["runtime"], "required": True})
    draft = call("PUT", f"/deploy/drafts/{draft['id']}", {
        "revision": draft["revision"], "step": "configuration", "configuration": configuration,
        "dotenv": "\n".join(f"{key}={json.dumps(value)}" for key, value in (environment or {}).items())})
    if environment:
        resumed = call("GET", f"/deploy/drafts/{draft['id']}")
        if set(resumed.get("environmentKeys", [])) != set(environment) or any(
                value in json.dumps(resumed) for value in environment.values()):
            raise SystemExit("staged values were missing or exposed by the draft response")
    preflight = call("POST", f"/deploy/drafts/{draft['id']}/preflight", {"revision": draft["revision"]})
    draft = preflight["draft"]
    blockers = [f for f in preflight["preflight"]["findings"] if f["severity"] in ("blocked", "decision")]
    if blockers:
        raise SystemExit("preflight blocked: " + json.dumps(blockers, indent=1))
    committed = call("POST", f"/deploy/drafts/{draft['id']}/commit", {"revision": draft["revision"]})
    return committed["projectId"], committed["environmentId"]


def plan_blueprint(name, blueprint_id, inputs, profile="service"):
    """Drive the wizard's blueprint journey: the server renders the plan when the
    source is inspected; the client only reviews it and acknowledges warnings."""
    draft = call("POST", "/deploy/drafts", {})
    draft = call("PUT", f"/deploy/drafts/{draft['id']}", {
        "revision": draft["revision"], "step": "intent", "intent": {"name": name, "profile": profile}})
    draft = call("PUT", f"/deploy/drafts/{draft['id']}", {
        "revision": draft["revision"], "step": "source",
        "source": {"kind": "blueprint", "mode": "blueprint", "blueprintId": blueprint_id,
                   "blueprintVersion": "1.0.0", "blueprintInputs": inputs}})
    draft = call("POST", f"/deploy/drafts/{draft['id']}/detect", {"revision": draft["revision"]})
    rendered = draft["data"]["configuration"]
    draft = call("PUT", f"/deploy/drafts/{draft['id']}", {
        "revision": draft["revision"], "step": "configuration", "configuration": rendered})
    preflight = call("POST", f"/deploy/drafts/{draft['id']}/preflight", {"revision": draft["revision"]})
    draft = preflight["draft"]
    findings = preflight["preflight"]["findings"]
    blockers = [f for f in findings if f["severity"] in ("blocked", "decision")]
    if blockers:
        raise SystemExit("blueprint preflight blocked: " + json.dumps(blockers, indent=1))
    warnings = [f["code"] for f in findings if f["severity"] == "warning"]
    committed = call("POST", f"/deploy/drafts/{draft['id']}/commit",
                     {"revision": draft["revision"], "acknowledgedWarnings": warnings})
    return committed["projectId"], committed["environmentId"], draft["data"]["detection"], rendered, warnings


def run_and_wait(project, environment, timeout=300):
    run = call("POST", f"/deploy/{project}/environments/{environment}/runs", {"operation": "deploy"})
    deadline = time.time() + timeout
    while time.time() < deadline:
        snapshot = call("GET", f"/deploy/{project}/runs/{run['id']}")
        state = snapshot["run"]["state"]
        if state in ("succeeded", "failed", "cancelled", "rolled_back", "superseded"):
            return snapshot
        time.sleep(2)
    raise SystemExit(f"run {run['id']} did not finish: {state}")


def docker(*args):
    return subprocess.run(["docker", *args], capture_output=True, text=True, check=True).stdout


def wait_for_events(run_id, wanted, timeout=30):
    deadline = time.time() + timeout
    while time.time() < deadline:
        got = {r["body"]["event"] for r in received if r["body"].get("runId") == run_id}
        if wanted <= got:
            return
        time.sleep(0.5)
    raise SystemExit(f"webhook events for run {run_id}: {got}, wanted {wanted}")


def main():
    hook = start_hook()
    backend = start_backend()
    report = {"checks": []}
    created_environments = []
    created_volumes = []

    def check(name, ok, detail=""):
        report["checks"].append({"name": name, "ok": bool(ok), "detail": detail})
        print(("PASS " if ok else "FAIL ") + name + (f" — {detail}" if detail else ""))

    try:
        # Docker names are derived from environment IDs, while this fixture's
        # store is fresh. Allocate a unique range before its first deployment
        # so it cannot claim the live dashboard's low-numbered environments.
        with sqlite3.connect(os.path.join(DATA, "vpsd.db")) as db:
            if db.execute("SELECT COUNT(*) FROM deploy_environments").fetchone()[0]:
                raise SystemExit("deployment acceptance requires a fresh data directory")
            db.execute("DELETE FROM sqlite_sequence WHERE name='deploy_environments'")
            db.execute("INSERT INTO sqlite_sequence(name, seq) VALUES('deploy_environments', ?)",
                       (time.time_ns() // 1000 + secrets.randbelow(1000),))
        call("POST", "/auth/login", {"username": "e2e", "password": PASSWORD})
        # A bootstrap account must replace its temporary password before it may
        # do anything else; the change ends the session, so sign in again.
        final_password = PASSWORD + "-rotated"
        call("POST", "/account/password", {"currentPassword": PASSWORD, "newPassword": final_password}, expect=(200, 204))
        session_cookie["value"] = ""
        call("POST", "/auth/login", {"username": "e2e", "password": final_password})
        channel = call("POST", "/deploy/notifications", {
            "name": "e2e webhook", "kind": "webhook", "url": f"http://127.0.0.1:{HOOK_PORT}/hook",
            "events": [], "enabled": True})
        check("signed webhook channel created", channel["channel"]["kind"] == "webhook" and channel["secret"])
        options = call("GET", "/deploy/notifications/options")
        check("notification options list five kinds and four events",
              len(options["kinds"]) == 5 and len(options["events"]) == 4)

        # 1. A healthy image with resource limits.
        staged_secret = secrets.token_hex(24)
        project, environment = plan_project(
            "e2e-nginx", "nginx:1.27-alpine", [], 80,
            [{"name": "HTTP readiness", "kind": "http", "phase": "readiness", "required": True,
              "config": {"path": "/", "attempts": 20, "timeoutSeconds": 5, "intervalSeconds": 2}}],
            limits={"memoryMb": 128, "cpus": 0.5, "pidsLimit": 64, "restartPolicy": "on-failure"},
            environment={"JD_E2E_SECRET": staged_secret})
        created_environments.append(environment)
        snapshot = run_and_wait(project, environment)
        run_id = snapshot["run"]["id"]
        check("nginx deployment succeeded", snapshot["run"]["state"] == "succeeded",
              f"state={snapshot['run']['state']} code={snapshot['run'].get('terminalCode')} reason={snapshot['run'].get('terminalReason')}")
        wait_for_events(run_id, {"run.started", "run.succeeded"})
        started = [r for r in received if r["body"].get("runId") == run_id and r["body"]["event"] == "run.started"]
        succeeded = [r for r in received if r["body"].get("runId") == run_id and r["body"]["event"] == "run.succeeded"]
        check("webhook received run.started and run.succeeded exactly once", len(started) == 1 and len(succeeded) == 1,
              f"started={len(started)} succeeded={len(succeeded)}")
        env = succeeded[0]["body"]
        check("envelope names project, environment, run number, duration and run URL",
              env.get("projectName") == "e2e-nginx" and env.get("environmentName") and env.get("runNumber") == 1
              and env.get("durationSeconds", 0) >= 0 and str(env.get("url", "")).endswith(f"/deploy/{project}/runs/{run_id}"),
              json.dumps({k: env.get(k) for k in ("projectName", "environmentName", "runNumber", "durationSeconds", "url")}))
        signature = next((v for k, v in succeeded[0]["headers"].items() if k.lower() == "x-jd-signature-256"), "")
        check("webhook body is signed", signature.startswith("sha256="), signature[:16])
        container = f"jd-e{environment}-r1"
        observed = json.loads(docker("inspect", container))[0]
        inspect = observed["HostConfig"]
        check("required encrypted draft input reaches the first runtime",
              "JD_E2E_SECRET=" + staged_secret in observed["Config"]["Env"])
        check("Docker applied memory 128 MiB, 0.5 CPU, 64 pids and on-failure restart",
              inspect["Memory"] == 128 << 20 and inspect["NanoCpus"] == 500_000_000 and inspect["PidsLimit"] == 64
              and inspect["RestartPolicy"]["Name"] == "on-failure",
              f"Memory={inspect['Memory']} NanoCpus={inspect['NanoCpus']} Pids={inspect['PidsLimit']} Restart={inspect['RestartPolicy']['Name']}")
        comparison_env = call("GET", f"/deploy/{project}/environments/{environment}/releases")
        release_id = comparison_env[0]["id"]
        comparison = call("GET", f"/deploy/{project}/environments/{environment}/releases/{release_id}/comparison")
        # A first release has no predecessor: the API answers with artifacts and
        # update evidence and an explicit null comparison rather than inventing one.
        check("release comparison answers for the first release with no predecessor to diff against",
              comparison.get("comparison") is None and "artifacts" in comparison and "update" in comparison,
              json.dumps({k: (v if k != "artifacts" else "…") for k, v in comparison.items()})[:160])

        # 2. An image whose process exits before answering readiness.
        project2, environment2 = plan_project(
            "e2e-crash", "busybox:1.36",
            ["sh", "-c", "echo booting e2e-crash; echo 'fatal: DATABASE_URL is not set (postgres://app:hunter2@db/app)' >&2; sleep 1; exit 7"],
            8080,
            [{"name": "HTTP readiness", "kind": "http", "phase": "readiness", "required": True,
              "config": {"path": "/", "attempts": 3, "timeoutSeconds": 2, "intervalSeconds": 1}}])
        created_environments.append(environment2)
        snapshot2 = run_and_wait(project2, environment2)
        run2 = snapshot2["run"]["id"]
        check("crashing deployment failed at the health gate",
              snapshot2["run"]["state"] == "failed" and snapshot2["run"].get("terminalCode") == "health_gate_failed",
              f"state={snapshot2['run']['state']} code={snapshot2['run'].get('terminalCode')}")
        check("failure message points at the transcript", "last output is in the build log" in snapshot2["run"].get("terminalReason", ""),
              snapshot2["run"].get("terminalReason", ""))
        readiness = [s for s in snapshot2["steps"] if s["key"] == "verify_readiness"][0]
        diagnostics = readiness["evidence"].get("diagnostics") or {}
        container_state = diagnostics.get("containers", [{}])[0]
        # Docker's unless-stopped default restarts the crashing process, so the
        # diagnosis may catch it running again with a restart count, or exited 7.
        crash_observed = (container_state.get("state") == "exited" and container_state.get("exitCode") == 7) or container_state.get("restartCount", 0) >= 1
        check("readiness evidence records the container's crash state without log text",
              diagnostics.get("available") and crash_observed and diagnostics.get("lines", 0) >= 2
              and "DATABASE_URL" not in json.dumps(readiness["evidence"]),
              json.dumps(diagnostics))
        notify_step = [s for s in snapshot2["steps"] if s["key"] == "notify"][0]
        check("notify step never ran on the failed path (observers deliver instead)", notify_step["state"] == "pending", notify_step["state"])
        wait_for_events(run2, {"run.started", "run.failed"})
        failed = [r for r in received if r["body"].get("runId") == run2 and r["body"]["event"] == "run.failed"]
        check("webhook received run.failed with the reason", len(failed) == 1 and "readiness checks failed" in failed[0]["body"].get("terminalReason", ""),
              failed[0]["body"].get("terminalReason", "") if failed else "none")
        db = sqlite3.connect(f"file:{os.path.join(DATA, 'vpsd.db')}?mode=ro", uri=True)
        lines = [row[0] for row in db.execute("SELECT text FROM deploy_log_chunks WHERE run_id=? ORDER BY id", (run2,))]
        transcript = "\n".join(lines)
        check("transcript carries the application's own output", "booting e2e-crash" in transcript and "fatal: DATABASE_URL is not set" in transcript
              and "Application output from jd-e" in transcript and ("exit code 7" in transcript or "restarted" in transcript),
              [line for line in lines if "Application output" in line][:1])
        deliveries = call("GET", f"/deploy/notifications/{channel['channel']['id']}/deliveries")
        check("delivery history records four delivered events", len(deliveries) == 4 and all(d["status"] == "delivered" for d in deliveries),
              f"{len(deliveries)} rows: {[d['event'] for d in deliveries]}")
        crashed_left = docker("ps", "-a", "--filter", f"name=jd-e{environment2}-", "--format", "{{.Names}}").strip()
        check("failed candidate was removed by compensation after diagnosis", crashed_left == "", crashed_left)

        # 3. Pause the channel and confirm nothing else is delivered.
        toggled = call("PUT", f"/deploy/notifications/{channel['channel']['id']}/enabled", {"enabled": False})
        check("channel paused through its own route", toggled["enabled"] is False)
        before = len(received)
        restart = call("POST", f"/deploy/{project}/environments/{environment}/runs", {"operation": "restart"})
        deadline = time.time() + 120
        while time.time() < deadline:
            state = call("GET", f"/deploy/{project}/runs/{restart['id']}")["run"]["state"]
            if state in ("succeeded", "failed", "cancelled"):
                break
            time.sleep(2)
        time.sleep(3)
        check("paused channel received nothing for the restart", len(received) == before and state == "succeeded", f"state={state} new={len(received) - before}")

        # 4. A one-click service from the reviewed catalogue.
        catalogue = {entry["id"]: entry for entry in call("GET", "/deploy/blueprints/")}
        check("catalogue offers Redis and PostgreSQL and explains why Minecraft is preview-only",
              catalogue["redis"]["deploymentSupported"] and catalogue["postgresql"]["deploymentSupported"]
              and not catalogue["minecraft-java"]["deploymentSupported"] and "Game servers" in catalogue["minecraft-java"]["unavailableReason"],
              json.dumps({k: (v["deploymentSupported"], v.get("unavailableReason", "")[:40]) for k, v in catalogue.items() if k in ("redis", "postgresql", "prometheus", "minecraft-java")}))
        project3, environment3, detection, rendered, warnings = plan_blueprint("e2e-postgres-" + RUN_SUFFIX, "postgresql", {})
        created_environments.append(environment3)
        created_volumes.extend(mount["source"] for mount in rendered["runtime"].get("mounts", [])
                               if mount.get("ownership") == "managed")
        check("blueprint inspection resolved an immutable image digest and a render digest",
              detection["source"]["kind"] == "blueprint" and detection["source"]["digest"].startswith("sha256:")
              and detection["source"]["revision"].startswith("sha256:") and detection["source"]["ref"] == "postgresql@1.0.0",
              json.dumps({k: detection["source"].get(k) for k in ("repository", "ref", "digest")}))
        password = next((v for v in rendered["variables"] if v["name"] == "POSTGRES_PASSWORD"), {})
        check("rendered plan carries the reviewed defaults: image, managed volume, command check, memory limit, generated secret",
              rendered["runtime"]["image"] == "postgres:16-alpine" and rendered["runtime"]["strategy"] == "stop_first"
              and rendered["runtime"]["mounts"][0]["target"] == "/var/lib/postgresql/data" and rendered["runtime"]["mounts"][0]["ownership"] == "managed"
              and rendered["checks"][0]["kind"] == "command" and rendered["runtime"].get("memoryMb") == 512
              and password.get("generate") == 40 and password.get("sensitivity") == "secret" and "value" not in password,
              json.dumps({"image": rendered["runtime"]["image"], "memoryMb": rendered["runtime"].get("memoryMb"), "password": password}))
        check("preflight only warned about the missing backup policy", warnings == ["backup_policy_missing"], json.dumps(warnings))
        snapshot3 = run_and_wait(project3, environment3)
        check("PostgreSQL blueprint deployment succeeded", snapshot3["run"]["state"] == "succeeded",
              f"state={snapshot3['run']['state']} code={snapshot3['run'].get('terminalCode')} reason={snapshot3['run'].get('terminalReason')}")
        postgres_container = f"jd-e{environment3}-r1"
        postgres_inspect = json.loads(docker("inspect", postgres_container))[0]
        mounts = [m for m in postgres_inspect["Mounts"] if m["Destination"] == "/var/lib/postgresql/data"]
        check("PostgreSQL has the blueprint's memory limit and a managed data volume",
              postgres_inspect["HostConfig"]["Memory"] == 512 << 20 and mounts and mounts[0]["Type"] == "volume"
              and mounts[0]["Name"].startswith("e2e-postgres-") and mounts[0]["Name"].endswith("-data")
              and postgres_inspect["Config"]["Image"].startswith("sha256:"),
              f"memory={postgres_inspect['HostConfig']['Memory']} volume={mounts[0]['Name'] if mounts else None} image={postgres_inspect['Config']['Image'][:20]}")
        env_values = {e.split("=", 1)[0]: e.split("=", 1)[1] for e in postgres_inspect["Config"]["Env"] if "=" in e}
        variables = call("GET", f"/deploy/{project3}/environments/{environment3}/variables")
        listed = next((v for v in variables if v.get("name") == "POSTGRES_PASSWORD"), None) if isinstance(variables, list) else None
        revealed = call("GET", f"/deploy/{project3}/environments/{environment3}/variables/POSTGRES_PASSWORD/reveal")
        revealed_value = revealed.get("value") if isinstance(revealed, dict) else revealed
        check("the generated password is listed by name only", listed is not None and listed.get("sensitivity") == "secret",
              json.dumps(listed)[:160])
        check("generated secret reached the container and the reveal route agrees",
              isinstance(revealed_value, str) and re.fullmatch(r"[A-Za-z0-9]{40}", revealed_value or "") is not None
              and env_values.get("POSTGRES_PASSWORD") == revealed_value and revealed_value not in json.dumps(variables),
              f"len={len(revealed_value or '')} masked_list={revealed_value not in json.dumps(variables)}")
        # The image trusts local sockets and loopback. Use its Docker address
        # to exercise the password rule that another container will reach.
        postgres_address = next(network["IPAddress"] for network in
                                postgres_inspect["NetworkSettings"]["Networks"].values()
                                if network.get("IPAddress"))
        psql = ["docker", "exec", "-e", "PGPASSWORD", postgres_container,
                "psql", "-h", postgres_address, "-U", "app", "-d", "app", "-w", "-Atc", "SELECT 1"]
        authenticated = subprocess.run(psql, capture_output=True, text=True,
                                       env={**os.environ, "PGPASSWORD": revealed_value or ""})
        refused = subprocess.run(psql, capture_output=True, text=True,
                                 env={**os.environ, "PGPASSWORD": "invalid-" + secrets.token_hex(12)})
        check("PostgreSQL accepts the generated password over TCP and refuses an incorrect password",
              authenticated.returncode == 0 and authenticated.stdout.strip() == "1"
              and refused.returncode != 0 and "password authentication failed" in refused.stderr,
              f"authenticated={authenticated.returncode} incorrect_password={refused.returncode}")
        schedules = call("GET", f"/deploy/{project3}/environments/{environment3}/schedules")
        check("the default nightly backup stays paused until a backup policy is linked",
              len(schedules) == 1 and schedules[0].get("name") == "Nightly backup"
              and schedules[0].get("enabled") is False and schedules[0].get("expression") == "0 3 * * *"
              and [step["action"] for step in schedules[0].get("steps", [])] == ["backup"],
              json.dumps(schedules)[:200])
    finally:
        # Only this instance's environments. The live dashboard on this host owns
        # other jd-e* containers and they must not be touched.
        for environment_id in created_environments:
            try:
                names = docker("ps", "-a", "--filter", f"name=^jd-e{environment_id}-r", "--format", "{{.Names}}").split()
            except Exception:
                names = []
            for name in names:
                if re.fullmatch(rf"jd-e{environment_id}-r\d+", name):
                    subprocess.run(["docker", "rm", "-f", name], capture_output=True)
        for volume in created_volumes:
            subprocess.run(["docker", "volume", "rm", "-f", volume], capture_output=True)
        backend.send_signal(signal.SIGTERM)
        try:
            backend.wait(timeout=30)
        except subprocess.TimeoutExpired:
            backend.kill()
        hook.shutdown()
        with open(os.path.join(ROOT, "report.json"), "w") as handle:
            json.dump({"checks": report["checks"], "received": [r["body"] for r in received]}, handle, indent=1)
    failed = [c for c in report["checks"] if not c["ok"]]
    print(f"\n{len(report['checks']) - len(failed)} of {len(report['checks'])} checks passed")
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
