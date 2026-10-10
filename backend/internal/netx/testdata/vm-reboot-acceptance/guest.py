#!/usr/bin/env python3
"""Guest driver for the network reboot acceptance.

Runs as root inside the disposable guest, one phase per invocation:

    guest.py <phase> [arguments]

Each phase drives the real dashboard backend through its HTTP API (session
login, CSRF header, the pending-apply protocol and reconnection confirmation)
or reads the guest kernel and systemd directly, asserts what it observed, and
appends one JSON record to /var/lib/jd-vm-acceptance/results.jsonl. State that
must survive a guest reboot is kept beside it in state.json. The host side
(vm.sh) reboots the guest between phases; nothing here reboots anything.

Phases that verify a boot never start the backend before they have read the
kernel and the boot units: what they find was restored by the host's units
alone.
"""

import hashlib
import http.client
import json
import os
import subprocess
import sys
import time

STATE_DIR = "/var/lib/jd-vm-acceptance"
STATE = STATE_DIR + "/state.json"
RESULTS = STATE_DIR + "/results.jsonl"
NET = "/etc/just-dashboard/network"
UNIT = "just-dashboard-network.service"
RECOVERY_UNIT = "just-dashboard-network-recovery.service"
BACKEND = "jd-vm-backend.service"
FAULT = "/run/jd-vm-fault"


class Failed(Exception):
    pass


def sh(*argv, check=True, timeout=60):
    p = subprocess.run(argv, capture_output=True, text=True, timeout=timeout)
    if check and p.returncode != 0:
        raise Failed(f"{' '.join(argv)} exited {p.returncode}: {p.stdout}{p.stderr}")
    return p.stdout


def jcmd(*argv):
    out = sh(*argv)
    return json.loads(out) if out.strip() else []


def boot_id():
    with open("/proc/sys/kernel/random/boot_id") as f:
        return f.read().strip()


def load_state():
    try:
        with open(STATE) as f:
            return json.load(f)
    except FileNotFoundError:
        return {}


def save_state(state):
    os.makedirs(STATE_DIR, exist_ok=True)
    tmp = STATE + ".tmp"
    with open(tmp, "w") as f:
        json.dump(state, f, indent=2, sort_keys=True)
    os.replace(tmp, STATE)


class Check:
    """Collects assertions so one record shows every failed expectation."""

    def __init__(self, phase):
        self.phase = phase
        self.failures = []
        self.evidence = {}
        self.started = time.time()

    def expect(self, ok, message):
        if not ok:
            self.failures.append(message)
        return ok

    def note(self, key, value):
        self.evidence[key] = value

    def finish(self):
        record = {
            "phase": self.phase,
            "result": "pass" if not self.failures else "fail",
            "failures": self.failures,
            "bootId": boot_id(),
            "at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "seconds": round(time.time() - self.started, 1),
            "evidence": self.evidence,
        }
        os.makedirs(STATE_DIR, exist_ok=True)
        with open(RESULTS, "a") as f:
            f.write(json.dumps(record, sort_keys=True) + "\n")
        print(json.dumps(record, indent=2, sort_keys=True))
        if self.failures:
            sys.exit(1)


# ---- systemd, journal and backend


def unit_show(unit, *props):
    out = sh("systemctl", "show", unit, "--property=" + ",".join(props))
    values = {}
    for line in out.splitlines():
        key, _, value = line.partition("=")
        # systemctl prints one line per command for ExecStart and its kin.
        values[key] = values[key] + "\n" + value if key in values else value
    return values


def exec_outcomes(unit, prop="ExecStart"):
    """Each command's measured exit, as systemd records it."""
    raw = unit_show(unit, prop)[prop]
    out = []
    for part in raw.split("{ ")[1:]:
        fields = {}
        for field in part.split(" ; "):
            key, _, value = field.partition("=")
            fields[key.strip()] = value.strip().rstrip(" }")
        out.append({"path": fields.get("path"), "argv": fields.get("argv[]"), "status": fields.get("status"),
                    "code": fields.get("code"), "ignoreErrors": fields.get("ignore_errors")})
    return out


def unit_journal(unit, boot="0"):
    return sh("journalctl", "--no-pager", "-o", "short-iso-precise", "-b", boot, "-u", unit, check=False)


def change_journal():
    """The private recovery journal's public phase evidence, without its
    snapshots or recovery argv."""
    try:
        with open(NET + "/change.json") as f:
            j = json.load(f)
    except FileNotFoundError:
        return None
    keep = ("id", "phase", "generation", "watchdog", "runtime", "persistence", "boot", "recoveryErrors",
            "cleanup", "ownerUserId", "expiresAt", "appliedAt", "verifiedAt", "updatedAt")
    view = {k: j.get(k) for k in keep if k in j}
    view["commandTools"] = sorted({c.get("tool") for c in j.get("commands") or []})
    view["bootDependencies"] = len(j.get("bootDependencies") or [])
    view["files"] = [f.get("path") for f in j.get("files") or []]
    return view


def sha256_file(path):
    try:
        with open(path, "rb") as f:
            return hashlib.sha256(f.read()).hexdigest()
    except FileNotFoundError:
        return None


def backend_state():
    v = unit_show(BACKEND, "ActiveState", "SubState", "MainPID", "ExecMainStartTimestampMonotonic", "InvocationID")
    return {"active": v["ActiveState"], "sub": v["SubState"], "pid": v["MainPID"],
            "startedThisBoot": v["ExecMainStartTimestampMonotonic"] not in ("", "0")}


def start_backend():
    sh("systemctl", "start", BACKEND)
    deadline = time.time() + 60
    while time.time() < deadline:
        try:
            c = http.client.HTTPConnection("127.0.0.1", 8080, timeout=2)
            c.request("GET", "/healthz")
            if c.getresponse().status == 200:
                return
        except OSError:
            pass
        time.sleep(0.5)
    raise Failed("backend did not answer /healthz")


def stop_backend():
    sh("systemctl", "stop", BACKEND, check=False)


def kill_backend():
    sh("systemctl", "kill", "--kill-whom=main", "--signal=KILL", BACKEND)
    deadline = time.time() + 20
    while time.time() < deadline and unit_show(BACKEND, "ActiveState")["ActiveState"] not in ("inactive", "failed"):
        time.sleep(0.2)


# ---- the dashboard API


class API:
    def __init__(self):
        env = {}
        with open("/etc/jd-vm-acceptance/backend.env") as f:
            for line in f:
                key, _, value = line.strip().partition("=")
                env[key] = value
        self.cookie = None
        status, _, body = self.request("POST", "/auth/login",
                                       {"username": env["JD_BOOTSTRAP_USER"], "password": env["JD_BOOTSTRAP_PASSWORD"]})
        if status != 200 or not body.get("authenticated"):
            raise Failed(f"login failed: {status} {body}")

    def request(self, method, path, body=None, pending=False, timeout=90):
        headers = {"X-JD-CSRF": "1", "Accept": "application/json"}
        data = None
        if body is not None:
            data = json.dumps(body).encode()
            headers["Content-Type"] = "application/json"
        if pending:
            headers["X-JD-Network-Apply"] = "pending"
        if self.cookie:
            headers["Cookie"] = self.cookie
        c = http.client.HTTPConnection("127.0.0.1", 8080, timeout=timeout)
        c.request(method, "/api/v1" + path, body=data, headers=headers)
        r = c.getresponse()
        raw = r.read()
        for name, value in r.getheaders():
            if name.lower() == "set-cookie" and value.startswith("vpsd_session="):
                self.cookie = value.split(";", 1)[0]
        try:
            parsed = json.loads(raw) if raw else None
        except ValueError:
            parsed = raw.decode(errors="replace")
        return r.status, {k.lower(): v for k, v in r.getheaders()}, parsed

    def ok(self, method, path, body=None, pending=False, expect=(200, 201, 204)):
        status, headers, parsed = self.request(method, path, body, pending)
        if status not in expect:
            raise Failed(f"{method} {path} -> {status}: {parsed}")
        return headers, parsed

    def confirm(self, change_id):
        """The reconnection protocol: a fresh challenge bound to this session
        and source, returned within its window."""
        _, verified = self.ok("POST", f"/network/changes/{change_id}/verify", {})
        _, confirmed = self.ok("POST", f"/network/changes/{change_id}/confirm", {"challenge": verified["challenge"]})
        return confirmed

    def pending(self, method, path, body=None):
        headers, parsed = self.ok(method, path, body, pending=True)
        change = headers.get("x-jd-network-change")
        if not change:
            raise Failed(f"{method} {path} returned no pending change header")
        return change, parsed

    def drift(self):
        return self.ok("GET", "/network/drift")[1]


# ---- kernel readback


def kernel():
    """What the guest kernel holds, read with the same tools an operator uses."""
    k = {
        "links": jcmd("ip", "-j", "-d", "link", "show"),
        "addresses": jcmd("ip", "-j", "addr", "show"),
        "routes4": jcmd("ip", "-j", "-4", "route", "show", "table", "all"),
        "routes6": jcmd("ip", "-j", "-6", "route", "show", "table", "all"),
        "rules4": jcmd("ip", "-j", "-4", "rule", "show"),
        "rules6": jcmd("ip", "-j", "-6", "rule", "show"),
        "netns": sh("ip", "netns", "list").split(),
        "qdiscs": jcmd("tc", "-j", "qdisc", "show"),
        "nft": sh("nft", "-j", "list", "table", "inet", "jd_gateway", check=False),
        "iptables": sh("iptables", "-S", check=False),
        "ip6tables": sh("ip6tables", "-S", check=False),
    }
    return k


def link(k, name):
    return next((l for l in k["links"] if l.get("ifname") == name), None)


def addrs(k, name):
    for a in k["addresses"]:
        if a.get("ifname") == name:
            return {f"{i['local']}/{i['prefixlen']}" for i in a.get("addr_info", [])}
    return set()


def route_present(k, family, dst, table=None, dev=None, rtype=None):
    for r in k["routes4" if family == "inet" else "routes6"]:
        if r.get("dst") != dst:
            continue
        t = r.get("table", "main")
        if table is not None and str(t) != str(table) and not (table == 254 and t == "main"):
            continue
        if dev and r.get("dev") != dev:
            continue
        if rtype and r.get("type", "unicast") != rtype:
            continue
        return True
    return False


def rule_present(k, family, priority):
    return any(r.get("priority") == priority for r in k["rules4" if family == "inet" else "rules6"])


def sysctl(key):
    return sh("sysctl", "-n", key, check=False).strip()


def spec():
    with open(NET + "/spec.json") as f:
        return json.load(f)


def check_spec_in_kernel(c, sp, k, prefix="kernel"):
    """Every object the saved spec holds, looked up in the kernel directly."""
    missing = []
    for ns in sp.get("namespaces") or []:
        if ns["name"] not in k["netns"]:
            missing.append(f"namespace {ns['name']}")
    peers = {}
    for l in sp.get("links") or []:
        if l.get("peerNamespace"):
            peers[l["peer"]] = l["peerNamespace"]
            peer = jcmd("ip", "-n", l["peerNamespace"], "-j", "link", "show", l["peer"]) if l["peerNamespace"] in k["netns"] else []
            if not peer:
                missing.append(f"veth peer {l['peerNamespace']}/{l['peer']}")
            elif l.get("up") and "UP" not in peer[0].get("flags", []):
                missing.append(f"veth peer {l['peer']} is not up")
        got = link(k, l["name"])
        if not got:
            missing.append(f"link {l['name']}")
            continue
        kind = (got.get("linkinfo") or {}).get("info_kind")
        if kind != l["kind"]:
            missing.append(f"link {l['name']} kind {kind} != {l['kind']}")
        if l.get("master") and got.get("master") != l["master"]:
            missing.append(f"link {l['name']} master {got.get('master')} != {l['master']}")
        if l.get("mtu") and got.get("mtu") != l["mtu"]:
            missing.append(f"link {l['name']} mtu {got.get('mtu')} != {l['mtu']}")
        if l.get("up") and "UP" not in got.get("flags", []):
            missing.append(f"link {l['name']} is not up")
        for cidr in l.get("addresses") or []:
            if cidr not in addrs(k, l["name"]):
                missing.append(f"address {cidr} on {l['name']}")
    for a in sp.get("addresses") or []:
        if a["link"] in peers:
            inside = jcmd("ip", "-n", peers[a["link"]], "-j", "addr", "show", "dev", a["link"]) if peers[a["link"]] in k["netns"] else []
            if a["cidr"] not in addrs({"addresses": inside}, a["link"]):
                missing.append(f"address {a['cidr']} on {peers[a['link']]}/{a['link']}")
            continue
        if a["cidr"] not in addrs(k, a["link"]):
            missing.append(f"address {a['cidr']} on {a['link']}")
    for r in sp.get("routes") or []:
        dst = r["destination"]
        if dst in ("0.0.0.0/0", "::/0"):
            dst = "default"
        if not route_present(k, r["family"], dst, r.get("table") or 254, r.get("device") or None,
                             None if r.get("type") in (None, "", "unicast") else r["type"]):
            missing.append(f"route {r['family']} {dst} table {r.get('table')}")
    for r in sp.get("rules") or []:
        if not rule_present(k, r["family"], r["priority"]):
            missing.append(f"rule {r['family']} priority {r['priority']}")
    qdiscs = {}
    for q in k["qdiscs"]:
        qdiscs.setdefault(q["dev"], []).append(q)
    for sh_ in sp.get("shaping") or []:
        dev = qdiscs.get(sh_["device"], [])
        roots = [q["kind"] for q in dev if q.get("root")]
        if sh_.get("egressKbit"):
            if "htb" not in roots and "cake" not in roots:
                missing.append(f"egress limit root on {sh_['device']} ({roots})")
        elif sh_.get("qdisc") and sh_["qdisc"] not in roots:
            missing.append(f"root {sh_['qdisc']} on {sh_['device']} ({roots})")
        if sh_.get("ingressKbit") and not any(q["kind"] in ("ingress", "clsact") for q in dev):
            missing.append(f"ingress hook on {sh_['device']}")
    gateway = bool((sp.get("forwards") or []) + (sp.get("nat") or []) + (sp.get("limits") or []) + (sp.get("blocklists") or []))
    if gateway:
        try:
            nft = json.loads(k["nft"])["nftables"]
        except (ValueError, KeyError):
            nft = []
            missing.append("nft table inet jd_gateway")
        comments = {e["rule"].get("comment") for e in nft if "rule" in e}
        for f in sp.get("forwards") or []:
            if f.get("enabled") and f"forward:{f['id']}" not in comments:
                missing.append(f"forward {f['id']} rule")
        for n in sp.get("nat") or []:
            if n.get("enabled") and f"nat:{n['id']}" not in comments:
                missing.append(f"nat {n['id']} rule")
        if (sp.get("forwards") or sp.get("nat")) and "0x4a000000" not in k["iptables"]:
            missing.append("iptables admission rule")
    for key, value in (sp.get("sysctls") or {}).items():
        got = sysctl(key)
        if got.split() != value.split():
            missing.append(f"sysctl {key}={got} != {value}")
    c.note(prefix + "Missing", missing)
    c.expect(not missing, f"{prefix}: saved objects missing from the kernel: {missing}")
    return missing


# Inspection documents three observations that stay unknown even when present:
# a veth's peer connectivity, nft rule-expression equality beyond the owned
# gateway structure, and the recovery helper's build identity, which a
# read-only inspection never executes (docs/internal/backend/network-drift.md).
DOCUMENTED_UNKNOWN = {
    "Some saved device attributes were not exposed by this kernel inventory.": "link:jdvm-veth",
    "Owned gateway structure is present; rule expression equality has not been established.": "gateway:jd_gateway",
    "The recovery executable exists; its build identity and ability to run were not established by this read-only inspection.":
        "recovery-helper:",
}


def accepted(o):
    if o["status"] in ("matching", "not_required"):
        return True
    prefix = DOCUMENTED_UNKNOWN.get(o.get("reason"))
    return o["status"] == "unknown" and prefix is not None and o["id"].startswith(prefix)


def drift_summary(report):
    bad = [o for o in report["runtime"] + report["files"] if not accepted(o)]
    return {
        "status": report["status"],
        "consistent": report["consistent"],
        "runtime": len(report["runtime"]),
        "files": len(report["files"]),
        "notMatching": [{"id": o["id"], "status": o["status"], "reason": o.get("reason")} for o in bad],
        "documentedUnknown": [o["id"] for o in report["runtime"] + report["files"] if o["status"] == "unknown" and accepted(o)],
        "boot": {k: report["boot"].get(k) for k in ("status", "reason", "unitFileState", "activeState", "needDaemonReload")},
        "execution": {k: report["boot"]["execution"].get(k) for k in
                      ("status", "reason", "bootId", "invocationId", "startedAt", "finishedAt", "result", "bootTrigger")},
        "commands": [(x["path"], x.get("exitStatus"), x.get("code")) for x in report["boot"]["execution"]["commands"]],
        "repairPlan": {"status": report["repairPlan"]["status"], "executable": report["repairPlan"]["executable"],
                       "items": [(i["id"], i["action"], i["executable"]) for i in report["repairPlan"]["items"]],
                       "blockers": report["repairPlan"]["blockers"]},
    }


def boot_units_evidence(c, after_recovery=False):
    """The two host units' measured outcomes in this boot, from systemd. After
    a boot recovery replayed managed devices, the ordinary unit's creation
    lines report existing objects (documented); they are recorded, not failed."""
    net = unit_show(UNIT, "ActiveState", "Result", "UnitFileState", "ExecMainStatus", "InvocationID",
                    "ExecMainStartTimestampMonotonic", "ExecMainExitTimestamp")
    rec = unit_show(RECOVERY_UNIT, "ActiveState", "Result", "UnitFileState", "ExecMainStatus",
                    "ExecMainStartTimestampMonotonic", "ExecMainExitTimestamp")
    outcomes = exec_outcomes(UNIT)
    c.note("networkUnit", net)
    c.note("networkUnitCommands", outcomes)
    c.note("recoveryUnit", rec)
    c.note("networkUnitJournal", unit_journal(UNIT).splitlines()[-60:])
    c.note("recoveryUnitJournal", unit_journal(RECOVERY_UNIT).splitlines()[-30:])
    c.expect(net["UnitFileState"] == "enabled", f"{UNIT} is {net['UnitFileState']}, not enabled")
    c.expect(net["ExecMainStartTimestampMonotonic"] not in ("", "0"), f"{UNIT} did not run in this boot")
    c.expect(net["Result"] == "success", f"{UNIT} result {net['Result']}")
    failed = [o for o in outcomes if o["code"] != "exited" or o["status"] != "0"]
    c.note("networkUnitFailedCommands", failed)
    if after_recovery:
        c.expect(all(o["code"] == "exited" for o in outcomes), f"{UNIT} commands did not all run: {outcomes}")
    else:
        c.expect(not failed, f"{UNIT} commands failed (including ignored ones): {failed}")
    c.expect(rec["UnitFileState"] == "enabled", f"{RECOVERY_UNIT} is {rec['UnitFileState']}")
    c.expect(rec["ExecMainStartTimestampMonotonic"] not in ("", "0"), f"{RECOVERY_UNIT} did not run in this boot")
    c.expect(rec["Result"] == "success", f"{RECOVERY_UNIT} result {rec['Result']}")
    return net, outcomes


def versions():
    pkgs = ["linux-image-virtual", "systemd", "netplan.io", "network-manager", "iproute2", "nftables", "iptables",
            "linux-modules-extra-" + os.uname().release]
    out = {}
    for p in pkgs:
        out[p] = sh("dpkg-query", "-W", "-f=${Version}", p, check=False).strip() or None
    out["kernel"] = os.uname().release
    out["networkctl"] = sh("networkctl", "--version", check=False).splitlines()[0] if sh("networkctl", "--version", check=False) else None
    return out


# ---- phases


def phase_baseline(c, args):
    state = load_state()
    state.setdefault("boots", []).append({"bootId": boot_id(), "phase": "baseline"})
    c.note("versions", versions())
    c.note("modules", {m: os.path.isdir(f"/sys/module/{m}") for m in ("vrf", "ifb", "sch_cake", "tcp_bbr", "nf_tables")})
    nics = {}
    for l in kernel()["links"]:
        if l["ifname"].startswith("enp"):
            perm = sh("ethtool", "-P", l["ifname"], check=False).strip()
            nics[l["ifname"]] = {"mac": l.get("address"), "permanent": perm.split()[-1] if perm else None,
                                 "ifindex": l.get("ifindex"), "operstate": l.get("operstate")}
    c.note("nics", nics)
    c.note("owners", {"networkctl": sh("networkctl", "list", "--no-pager", check=False),
                      "nmcli": sh("nmcli", "-t", "device", check=False)})
    c.expect(not os.path.exists(NET + "/spec.json"), "the guest already has a managed spec")
    start_backend()
    api = API()
    _, caps = api.ok("GET", "/network/capabilities")
    c.note("capabilitiesSystemd", caps.get("systemd") if isinstance(caps, dict) else None)
    _, managers = api.ok("GET", "/network/native/managers")
    c.note("nativeManagers", managers)
    state["nics"] = nics
    save_state(state)


def c001_requests():
    """The owned object set: devices, a namespace with its veth, addresses,
    routes in two tables and both families, policy rules, forwarding, the
    gateway's NAT/forward/limit/blocklist, kernel protections and shaping."""
    return [
        ("POST", "/network/namespaces", {"name": "jdvmns", "veth": {"hostName": "jdvm-veth0", "peerName": "jdvm-peer0",
                                                                      "hostAddress": "10.235.0.1/30", "peerAddress": "10.235.0.2/30"}}),
        ("POST", "/network/links", {"name": "jdvmbr0", "kind": "bridge", "up": True,
                                     "addresses": ["10.231.0.1/24", "fd00:231::1/64"]}),
        ("POST", "/network/links", {"name": "jdvmdum0", "kind": "dummy", "master": "jdvmbr0", "up": True}),
        ("POST", "/network/links", {"name": "jdvmdum1", "kind": "dummy", "mtu": 1400, "up": True,
                                     "addresses": ["10.232.1.1/24"]}),
        ("POST", "/network/links", {"name": "jdvmvlan42", "kind": "vlan", "parent": "jdvmdum1", "vlanId": 42, "up": True,
                                     "addresses": ["10.232.42.1/24"]}),
        ("POST", "/network/links/jdvmdum1/addresses", {"cidr": "10.232.2.1/24"}),
        ("POST", "/network/routing/routes", {"destination": "10.233.0.0/24", "gateway": "10.231.0.2", "device": "jdvmbr0"}),
        ("POST", "/network/routing/routes", {"destination": "default", "gateway": "10.231.0.2", "device": "jdvmbr0", "table": 231}),
        ("POST", "/network/routing/routes", {"destination": "fd00:233::/64", "device": "jdvmbr0"}),
        ("POST", "/network/routing/routes", {"destination": "10.234.0.0/24", "type": "blackhole"}),
        ("POST", "/network/routing/rules", {"from": "10.231.0.0/24", "table": 231}),
        ("POST", "/network/routing/rules", {"family": "inet6", "from": "fd00:231::/64", "table": 231}),
        ("POST", "/network/routing/rules", {"fwmark": "0x231", "table": 231}),
        ("POST", "/network/forwarding/ipv4/on", None),
        ("POST", "/network/gateway/nat", {"name": "jdvm-nat", "source": "10.231.0.0/24", "interface": "enp0s3", "enabled": True}),
        ("POST", "/network/gateway/forwards", {"name": "jdvm-fwd", "protocol": "tcp", "interface": "enp0s3", "ports": "18080",
                                                "target": "10.231.0.2", "targetPort": "80", "sourceNat": "auto", "enabled": True}),
        ("POST", "/network/protection/limits", {"name": "jdvm-limit", "protocol": "tcp", "ports": "18081", "rate": 20,
                                                 "per": "second", "burst": 40, "perSource": True, "action": "drop", "enabled": True}),
        ("POST", "/network/protection/blocklists", {"name": "jdvm-block", "kind": "manual", "entries": ["203.0.113.0/24"],
                                                     "enabled": True}),
        ("POST", "/network/protection/settings", {"values": {"net.ipv4.tcp_syncookies": "1", "net.ipv4.tcp_rfc1337": "1",
                                                             "net.ipv4.conf.all.log_martians": "1",
                                                             "net.ipv4.conf.all.send_redirects": "0",
                                                             "net.ipv4.tcp_max_syn_backlog": "4096"}}),
        ("POST", "/network/shaping/jdvmdum1", {"qdisc": "fq_codel", "egressKbit": 20000, "ingressKbit": 10000}),
        ("POST", "/network/shaping/jdvmbr0", {"qdisc": "cake", "egressKbit": 0, "ingressKbit": 0}),
        ("POST", "/network/shaping/bbr", {"on": True}),
    ]


def phase_c001_apply(c, args):
    state = load_state()
    start_backend()
    api = API()
    # The product never loads kernel modules; the operator loads BBR's, as
    # its refusal asks. At boot, setting the saved sysctl loads it again.
    sh("modprobe", "tcp_bbr")
    applied = []
    for method, path, body in c001_requests():
        status, _, parsed = api.request(method, path, body)
        applied.append({"request": f"{method} {path}", "status": status})
        c.expect(status in (200, 201, 204), f"{method} {path} -> {status}: {parsed}")
    c.note("requests", applied)
    c.note("journal", change_journal())
    report = api.drift()
    summary = drift_summary(report)
    c.note("driftBeforeReboot", summary)
    c.expect(not summary["notMatching"], f"drift before reboot: {summary['notMatching']}")
    c.expect(summary["boot"]["status"] == "matching", f"boot unit before reboot: {summary['boot']}")
    sp = spec()
    k = kernel()
    check_spec_in_kernel(c, sp, k, "kernelBeforeReboot")
    c.note("unit", open("/etc/systemd/system/" + UNIT).read())
    c.note("specCounts", {key: len(v) for key, v in sp.items() if isinstance(v, (list, dict))})
    state["c001"] = {"bootId": boot_id(), "specSha256": sha256_file(NET + "/spec.json"), "generation": report.get("savedGeneration")}
    save_state(state)
    stop_backend()


def verify_boot_restored(c, label, require_backend_absent=True):
    """Checks a fresh boot before the backend runs, then the backend's own drift view."""
    state = load_state()
    b = backend_state()
    c.note("backendAtBoot", b)
    if require_backend_absent:
        c.expect(not b["startedThisBoot"], "the backend ran before the boot was inspected")
    boot_units_evidence(c)
    sp = spec()
    check_spec_in_kernel(c, sp, kernel(), "kernelAfterBoot")
    start_backend()
    api = API()
    report = api.drift()
    summary = drift_summary(report)
    c.note("driftAfterBoot", summary)
    c.expect(not summary["notMatching"], f"drift after boot: {summary['notMatching']}")
    c.expect(summary["boot"]["status"] == "matching", f"boot unit health after boot: {summary['boot']}")
    c.expect(summary["execution"]["status"] == "succeeded", f"measured activation after boot: {summary['execution']}")
    c.expect(summary["execution"]["bootId"] == boot_id(), "boot evidence names another boot")
    c.note("journal", change_journal())
    state.setdefault("boots", []).append({"bootId": boot_id(), "phase": label})
    save_state(state)
    return api, report


def phase_c001_verify(c, args):
    state = load_state()
    c.expect(state["c001"]["bootId"] != boot_id(), "no reboot since apply")
    c.expect(sha256_file(NET + "/spec.json") == state["c001"]["specSha256"], "the saved spec changed across the reboot")
    verify_boot_restored(c, "c001-verify")
    stop_backend()


# F4/C002: kill the backend between journal phases. The change is one added
# route; the prior saved generation is C001's.


def f4_change(index):
    return {"destination": f"10.240.{index}.0/24", "gateway": "10.231.0.2", "device": "jdvmbr0"}


def phase_f4_kill(c, args):
    kill_phase, index = args[0], int(args[1])
    state = load_state()
    start_backend()
    api = API()
    before = {"specSha256": sha256_file(NET + "/spec.json"),
              "links": sha256_file(NET + "/links.batch"), "journal": change_journal()}
    dst = f4_change(index)["destination"]
    os.makedirs(FAULT, exist_ok=True)
    for name in ("phase", "fired"):
        try:
            os.remove(f"{FAULT}/{name}")
        except FileNotFoundError:
            pass
    if kill_phase == "awaiting_confirmation":
        change, _ = api.pending("POST", "/network/routing/routes", f4_change(index))
        at_kill = change_journal()
        kill_backend()
    else:
        with open(f"{FAULT}/phase", "w") as f:
            f.write(kill_phase)
        try:
            status, _, parsed = api.request("POST", "/network/routing/routes", f4_change(index), timeout=60)
            c.expect(False, f"the request completed ({status} {parsed}) instead of the backend dying")
        except (OSError, http.client.HTTPException) as e:
            c.note("requestError", repr(e))
        deadline = time.time() + 20
        while time.time() < deadline and unit_show(BACKEND, "ActiveState")["ActiveState"] not in ("inactive", "failed"):
            time.sleep(0.2)
        at_kill = change_journal()
        change = at_kill["id"] if at_kill else None
        c.note("fired", open(f"{FAULT}/fired").read().strip() if os.path.exists(f"{FAULT}/fired") else None)
    c.note("backendAfterKill", backend_state())
    c.note("journalAtKill", at_kill)
    c.expect(at_kill is not None and at_kill["phase"] == kill_phase, f"journal at kill: {at_kill}")
    c.expect(at_kill is not None and at_kill["watchdog"] == "armed", "watchdog was not armed")
    c.expect(backend_state()["active"] in ("inactive", "failed"), "backend still running")
    k = kernel()
    c.expect(route_present(k, "inet", dst), f"candidate route {dst} not in the kernel at kill")
    timer = f"just-dashboard-network-recover-{change}.timer"
    c.note("timer", unit_show(timer, "ActiveState", "NextElapseUSecMonotonic", "LoadState"))
    state.setdefault("f4", {})[f"{kill_phase}-{index}"] = {
        "change": change, "dst": dst, "before": before, "bootId": boot_id(), "killedAt": time.time()}
    save_state(state)


def f4_verify_recovered(c, entry, rebooted):
    j = change_journal()
    c.note("journal", j)
    c.expect(j is not None and j["id"] == entry["change"], f"journal names another change: {j}")
    c.expect(j is not None and j["phase"] == "recovered", f"journal phase {j and j['phase']}")
    c.expect(j is not None and not j.get("recoveryErrors"), f"recovery errors: {j and j.get('recoveryErrors')}")
    c.expect(sha256_file(NET + "/spec.json") == entry["before"]["specSha256"], "spec was not restored to the prior generation")
    c.expect(sha256_file(NET + "/links.batch") == entry["before"]["links"], "links.batch was not restored")
    k = kernel()
    c.expect(not route_present(k, "inet", entry["dst"]), f"candidate route {entry['dst']} survived recovery")
    b = backend_state()
    c.note("backend", b)
    c.expect(not b["startedThisBoot"] if rebooted else b["active"] in ("inactive", "failed"),
             "the backend ran during recovery")


def phase_f4_timer(c, args):
    """No reboot: the transient systemd timer starts the installed helper."""
    key = args[0]
    entry = load_state()["f4"][key]
    deadline = entry["killedAt"] + 150
    while time.time() < deadline:
        j = change_journal()
        if j and j["phase"] in ("recovered", "degraded"):
            break
        time.sleep(2)
    unit = f"just-dashboard-network-recover-{entry['change']}.service"
    c.note("waitedSeconds", round(time.time() - entry["killedAt"], 1))
    c.note("helperJournal", unit_journal(unit).splitlines()[-20:])
    c.expect(entry["bootId"] == boot_id(), "the guest rebooted during the timer case")
    f4_verify_recovered(c, entry, rebooted=False)
    sp = spec()
    check_spec_in_kernel(c, sp, kernel(), "priorGenerationInKernel")


def phase_f4_boot(c, args):
    """After a reboot before the deadline: the boot recovery unit, no backend."""
    key = args[0]
    entry = load_state()["f4"][key]
    c.expect(entry["bootId"] != boot_id(), "no reboot since the kill")
    boot_units_evidence(c, after_recovery=True)
    c.note("recoveryUnitArgv", [o["argv"] for o in exec_outcomes(RECOVERY_UNIT)])
    f4_verify_recovered(c, entry, rebooted=True)
    sp = spec()
    check_spec_in_kernel(c, sp, kernel(), "priorGenerationInKernel")
    start_backend()
    summary = drift_summary(API().drift())
    c.note("driftAfterRecoveredBoot", summary)
    c.expect(not summary["notMatching"], f"drift after the recovered boot: {summary['notMatching']}")
    stop_backend()


# P7: drift and boot health. Runtime drift is introduced with the operator's
# own tools; boot-input drift by editing an owned render on disk.

P7_RUNTIME = [
    (["ip", "addr", "del", "10.232.2.1/24", "dev", "jdvmdum1"], "address"),
    (["ip", "route", "del", "10.233.0.0/24"], "route"),
    (["ip", "link", "del", "jdvmdum0"], "link"),
    (["tc", "qdisc", "del", "dev", "jdvmbr0", "root"], "shaping"),
]
P7_FILE_LINE = "addr add 10.232.2.1/24 dev jdvmdum1"


def not_matching(report):
    return {o["id"]: o["status"] for o in report["runtime"] + report["files"] if not accepted(o)}


def phase_p7_runtime_drift(c, args):
    state = load_state()
    start_backend()
    api = API()
    before = not_matching(api.drift())
    c.expect(not before, f"drift before the deletion: {before}")
    for argv, _ in P7_RUNTIME:
        sh(*argv)
    report = api.drift()
    found = not_matching(report)
    c.note("drift", drift_summary(report))
    c.note("notMatching", found)
    for _, domain in P7_RUNTIME:
        c.expect(any(i.split(":", 1)[0] == domain for i in found),
                 f"no {domain} drift reported: {found}")
    c.expect(all(s in ("missing", "drift") for s in found.values()), f"deleted objects reported as {found}")
    runtime_items = [i for i in report["repairPlan"]["items"] if i["domain"] != "render"]
    c.expect(not any(i["executable"] for i in runtime_items), "a runtime resource repair was offered as executable")
    state["p7runtime"] = {"bootId": boot_id(), "reported": found}
    save_state(state)
    stop_backend()


def phase_p7_file_drift(c, args):
    state = load_state()
    stop_backend()
    path = NET + "/links.batch"
    with open(path) as f:
        lines = f.read().split("\n")
    c.expect(P7_FILE_LINE in lines, f"{P7_FILE_LINE!r} not in links.batch")
    with open(path, "w") as f:
        f.write("\n".join(l for l in lines if l != P7_FILE_LINE))
    state["p7file"] = {"bootId": boot_id(), "sha256": sha256_file(path)}
    save_state(state)
    c.note("editedSha256", state["p7file"]["sha256"])


def phase_p7_file_verify(c, args):
    """After a reboot from the edited render: report it accurately, then repair
    it through the reviewed, pending, confirmed transaction."""
    state = load_state()
    c.expect(state["p7file"]["bootId"] != boot_id(), "no reboot since the edit")
    c.expect(not backend_state()["startedThisBoot"], "the backend ran before inspection")
    boot_units_evidence(c)
    k = kernel()
    c.expect("10.232.2.1/24" not in addrs(k, "jdvmdum1"), "the edited render still restored the omitted address")
    start_backend()
    api = API()
    report = api.drift()
    found = not_matching(report)
    c.note("driftAfterBoot", drift_summary(report))
    file_ids = [o["id"] for o in report["files"] if o["status"] == "drift" and o["resource"].endswith("links.batch")]
    c.expect(len(file_ids) == 1, f"links.batch drift not reported exactly: {found}")
    c.expect(any(s == "missing" for i, s in found.items() if "10.232.2.1" in i), f"omitted address not reported missing: {found}")
    c.expect(report["boot"]["execution"]["status"] == "succeeded", f"measured activation: {report['boot']['execution']}")
    item = next((i for i in report["repairPlan"]["items"] if file_ids and i["observationId"] == file_ids[0]), None)
    c.expect(item is not None and item["executable"], f"no executable repair for links.batch: {report['repairPlan']}")
    if c.failures:
        return
    c.note("repairItem", {k_: item[k_] for k_ in ("id", "action", "effect", "preconditions")})
    change, result = api.pending("POST", "/network/drift/repairs",
                                 {"generation": report["repairPlan"]["generation"],
                                  "selections": [{"id": item["id"], "reviewToken": item["reviewToken"]}]})
    c.note("repairResult", result)
    c.note("journalPending", change_journal())
    confirmed = api.confirm(change)
    c.note("confirmed", confirmed)
    after = api.drift()
    found_after = not_matching(after)
    c.note("driftAfterRepair", drift_summary(after))
    c.expect(not [i for i in found_after if i == file_ids[0]], "links.batch still drifts after repair")
    c.expect(any("10.232.2.1" in i for i in found_after), "the file repair replayed the kernel address (it must not)")
    j = change_journal()
    c.note("journal", j)
    c.expect(j["phase"] == "confirmed", f"repair journal phase {j['phase']}")
    state["p7file"]["repairedBootId"] = boot_id()
    save_state(state)
    stop_backend()


def phase_p7_daemon_restart(c, args):
    """systemd-networkd removes foreign routes when it restarts; the managed
    unit is PartOf it and must put every object back."""
    before = unit_show(UNIT, "InvocationID")["InvocationID"]
    restarts = unit_show("systemd-networkd.service", "NRestarts")["NRestarts"]
    sh("systemctl", "restart", "systemd-networkd.service")
    # Settled: the unit has restarted and kept one invocation, with networkd
    # active, for five seconds. networkd may restart itself meanwhile.
    deadline, stable, last = time.time() + 90, 0, None
    while time.time() < deadline and stable < 5:
        v = unit_show(UNIT, "InvocationID", "ActiveState")
        settled = v["InvocationID"] != before and v["ActiveState"] == "active" and \
            unit_show("systemd-networkd.service", "ActiveState")["ActiveState"] == "active"
        stable = stable + 1 if settled and v["InvocationID"] == last else 0
        last = v["InvocationID"]
        time.sleep(1)
    after = unit_show(UNIT, "InvocationID", "ActiveState", "Result")
    crashes = int(unit_show("systemd-networkd.service", "NRestarts")["NRestarts"]) - int(restarts)
    c.note("networkdAutomaticRestarts", crashes)
    c.note("networkdAborts", [l for l in sh("journalctl", "-u", "systemd-networkd", "--since", "-2min", "--no-pager",
                                             check=False).splitlines() if "Assertion" in l or "dumped" in l])
    c.note("invocations", {"before": before, "after": after})
    c.expect(after["InvocationID"] != before, f"{UNIT} did not restart with systemd-networkd")
    c.note("networkUnitCommands", exec_outcomes(UNIT))
    check_spec_in_kernel(c, spec(), kernel(), "kernelAfterNetworkdRestart")
    start_backend()
    report = API().drift()
    summary = drift_summary(report)
    c.note("drift", summary)
    c.expect(not summary["notMatching"], f"drift after networkd restart: {summary['notMatching']}")
    c.expect(summary["execution"]["invocationId"] == after["InvocationID"].replace("-", ""),
             f"measured activation is not the restart's: {summary['execution']}")
    # The devices and addresses survive a restart, so the creation lines of
    # links.batch report them; nothing else may fail (documented limit).
    failed = [cmd for cmd in summary["commands"] if cmd[1] != 0]
    c.note("restartFailedCommands", failed)
    c.expect(all(cmd[0] == "ip" for cmd in failed) and len(failed) <= 1,
             f"restart commands beyond links.batch failed: {failed}")
    stop_backend()


# P14: explicit download SQM on a virtio NIC whose permanent MAC is fixed by
# the VM definition, so the saved source identity survives the reboot.

SQM_DEVICE = "enp0s6"
SQM_BODY = {"qdisc": "", "egressKbit": 0, "ingressKbit": 9000,
            "sqm": {"diffserv": "diffserv4", "flowMode": "triple-isolate", "nat": False, "preserveDscp": False,
                    "overhead": 22, "mpu": 64, "linkLayer": "noatm", "rttMillis": 80}}


def sqm_readback(c, label):
    """The IFB, its CAKE parameters and the source's redirect, from tc and ip."""
    entry = next(s for s in spec()["shaping"] if s["device"] == SQM_DEVICE)
    sqm = entry["sqm"]
    ifb = link(kernel(), sqm["ifb"])
    cake = [q for q in jcmd("tc", "-j", "qdisc", "show", "dev", sqm["ifb"]) if q["kind"] == "cake"]
    filters = jcmd("tc", "-j", "filter", "show", "dev", SQM_DEVICE, "ingress")
    source = link(kernel(), SQM_DEVICE)
    got = {
        "ifb": sqm["ifb"],
        "ifbExists": ifb is not None,
        "ifbUp": bool(ifb and "UP" in ifb.get("flags", [])),
        "ifbAlias": ifb and ifb.get("ifalias"),
        "ifbMac": ifb and ifb.get("address"),
        "cake": cake[0].get("options") if cake else None,
        "redirect": [f for f in filters if "options" in f],
        "sourceMac": source and source.get("address"),
        "savedSourceMac": sqm.get("sourceMac"),
    }
    c.note(label, got)
    o = got["cake"] or {}
    c.expect(got["ifbExists"] and got["ifbUp"], f"{label}: IFB {sqm['ifb']} missing or down")
    c.expect((got["ifbAlias"] or "").startswith("jd-sqm:"), f"{label}: IFB alias {got['ifbAlias']}")
    c.expect(got["sourceMac"] == got["savedSourceMac"], f"{label}: source MAC changed")
    c.expect(o.get("bandwidth") == 9000 * 1000 // 8, f"{label}: CAKE bandwidth {o.get('bandwidth')}")
    for key, want in (("diffserv", "diffserv4"), ("flowmode", "triple-isolate"), ("nat", False), ("wash", True),
                      ("ingress", True), ("overhead", 22), ("mpu", 64), ("atm", "noatm"), ("rtt", 80000),
                      ("split_gso", True)):
        c.expect(o.get(key) == want, f"{label}: CAKE {key}={o.get(key)!r}, want {want!r}")
    c.expect(len(got["redirect"]) == 1, f"{label}: ingress redirect filters {got['redirect']}")
    return got


def phase_p14_apply(c, args):
    state = load_state()
    start_backend()
    api = API()
    change, _ = api.pending("POST", f"/network/shaping/{SQM_DEVICE}", SQM_BODY)
    c.note("change", change)
    c.note("journalPending", change_journal())
    c.note("confirmed", api.confirm(change))
    c.note("journal", change_journal())
    sqm_readback(c, "readbackBeforeReboot")
    _, view = api.ok("GET", "/network/shaping")
    dev = next(d for d in view["devices"] if d["name"] == SQM_DEVICE)
    c.note("shapingView", dev.get("sqm"))
    c.expect(dev.get("verification", {}).get("status") == "verified", f"shaping verification {dev.get('verification')}")
    c.note("unitSqmLine", [l for l in open("/etc/systemd/system/" + UNIT).read().splitlines() if "sqm" in l])
    state["p14"] = {"bootId": boot_id()}
    save_state(state)
    stop_backend()


def phase_p14_verify(c, args):
    state = load_state()
    c.expect(state["p14"]["bootId"] != boot_id(), "no reboot since apply")
    c.expect(not backend_state()["startedThisBoot"], "the backend ran before inspection")
    boot_units_evidence(c)
    sqm_lines = [o for o in exec_outcomes(UNIT) if o["argv"] and "--network-sqm-restore" in o["argv"]]
    c.note("sqmRestoreCommand", sqm_lines)
    c.expect(len(sqm_lines) == 1 and sqm_lines[0]["status"] == "0" and sqm_lines[0]["ignoreErrors"] == "no",
             f"packaged SQM restore: {sqm_lines}")
    sqm_readback(c, "readbackAfterBoot")
    start_backend()
    api = API()
    _, view = api.ok("GET", "/network/shaping")
    dev = next(d for d in view["devices"] if d["name"] == SQM_DEVICE)
    c.note("shapingView", {"verification": dev.get("verification"), "sqm": dev.get("sqm")})
    c.expect(dev.get("verification", {}).get("status") == "verified", f"shaping verification {dev.get('verification')}")
    for key in ("helper", "boot"):
        v = (dev.get("sqm") or {}).get(key) or {}
        c.expect(v.get("status") in ("verified", "matching"), f"SQM {key} evidence {v}")
    summary = drift_summary(api.drift())
    c.note("drift", summary)
    c.expect(not summary["notMatching"], f"drift after boot: {summary['notMatching']}")
    stop_backend()


# P11: existing native owner profiles. Three real owners run in the guest: an
# authored Netplan origin rendered by systemd-networkd, an authored Netplan
# origin rendered by NetworkManager 1.46 (Ubuntu's Netplan writer), and a
# direct systemd-networkd profile.

NATIVE = {
    "netplan-networkd": {"device": "enp0s4", "owner": "netplan", "renderer": "networkd",
                         "file": "/etc/netplan/60-jdvm-networkd.yaml", "net": "10.0.4", "gateway": "10.0.4.2"},
    "netplan-nm": {"device": "jdnm0", "owner": "netplan", "renderer": "NetworkManager",
                   "file": "/etc/netplan/62-jdvm-nm-dummy.yaml", "net": "10.0.7", "gateway": "10.0.7.2"},
    "networkd": {"device": "enp0s6", "owner": "networkd", "renderer": "networkd",
                 "file": "/etc/systemd/network/30-jdvm-direct.network", "net": "10.0.6", "gateway": "10.0.6.2"},
}


def settle_networkd(c):
    """networkd's reload reconfigures every link whose file changed since its
    last load. On Ubuntu, NetworkManager's start reloads systemd after networkd
    has loaded, and Netplan's generator rewrites every artifact, so the first
    reload after a boot also reconfigures the uplink (DHCP and RA restart) and
    the dashboard's IPv6 anchor guard refuses that first edit. That refusal is
    recorded separately; the transaction cases start from a settled networkd."""
    sh("networkctl", "reload")
    deadline = time.time() + 20
    while time.time() < deadline:
        if sh("ip", "-6", "route", "get", "2606:4700:4700::1111", check=False).strip() and \
                "routable" in sh("networkctl", "--no-pager", "list", "enp0s3", check=False):
            break
        time.sleep(0.5)
    time.sleep(2)
    c.note("networkdSettled", True)


def native_profile(api, case):
    _, view = api.ok("GET", f"/network/native/profiles/{NATIVE[case]['device']}")
    return view


def native_view_summary(view):
    return {k: view.get(k) for k in ("device", "owner", "renderer", "version", "editable", "refusal", "configured",
                                     "runtime", "boot", "intent", "contract")}


def native_file(case):
    path = NATIVE[case]["file"]
    st = os.stat(path)
    return {"path": path, "sha256": sha256_file(path), "inode": st.st_ino, "mode": oct(st.st_mode & 0o7777)}


def native_candidate(view, case, host, route_octet):
    """The current intent plus one IPv4 address and one explicit route."""
    intent = json.loads(json.dumps(view["intent"]))
    cfg = NATIVE[case]
    intent["ipv4"]["addresses"] = intent["ipv4"]["addresses"] + [f"{cfg['net']}.{host}/24"]
    intent["ipv4"]["routes"] = (intent["ipv4"].get("routes") or []) + [
        {"destination": f"10.{route_octet}.{cfg['net'].split('.')[2]}.0/24", "gateway": cfg["gateway"], "metric": 100, "table": 254}]
    return intent


def addr_on(device, cidr):
    return cidr in addrs(kernel(), device)


def wait_journal(predicate, seconds):
    deadline = time.time() + seconds
    while time.time() < deadline:
        j = change_journal()
        if j and predicate(j):
            return j
        time.sleep(1)
    return change_journal()


def phase_p11_read(c, args):
    start_backend()
    api = API()
    _, managers = api.ok("GET", "/network/native/managers")
    c.note("managers", managers.get("capabilities") if isinstance(managers, dict) else managers)
    # Netplan renders [ethernet] wake-on-lan=0 for every NetworkManager
    # ethernet; the adapter refuses that unverified property before effects.
    _, ethernet = api.ok("GET", "/network/native/profiles/enp0s5")
    c.note("netplan-nm-ethernet", native_view_summary(ethernet))
    c.expect(not ethernet.get("editable") and "not yet verified" in (ethernet.get("refusal") or ""),
             f"NetworkManager ethernet profile: {ethernet.get('editable')} {ethernet.get('refusal')}")
    for case in args or NATIVE:
        view = native_profile(api, case)
        c.note(case, native_view_summary(view))
        c.expect(view.get("editable"), f"{case}: not editable: {view.get('refusal')}")
        c.expect(view.get("owner") == NATIVE[case]["owner"] and view.get("renderer") == NATIVE[case]["renderer"],
                 f"{case}: owner {view.get('owner')}/{view.get('renderer')}")


def phase_p11_first_edit(c, args):
    """The first owner-path edit of a Netplan/networkd link after a boot,
    without settling networkd: records what the anchor guard does."""
    case = "netplan-networkd"
    cfg = NATIVE[case]
    start_backend()
    api = API()
    view = native_profile(api, case)
    before = native_file(case)
    status, _, body = api.request("PUT", f"/network/native/profiles/{cfg['device']}",
                                  {"generation": view["generation"], "intent": native_candidate(view, case, 19, 93)},
                                  pending=True)
    c.note("response", {"status": status, "body": body})
    c.note("networkdJournal", sh("journalctl", "-u", "systemd-networkd", "--since", "-30s", "--no-pager",
                                 "-o", "short-precise", check=False).splitlines()[-12:])
    j = change_journal()
    c.note("journal", j)
    if status == 200:
        c.note("outcome", "accepted: networkd had nothing stale to reconfigure")
        api.confirm(j["id"])
        wait_journal(lambda j: j.get("cleanup") == "complete", 30)
    else:
        c.expect(status == 409 and "IPv6" in json.dumps(body), f"unexpected first-edit outcome {status} {body}")
        c.expect(native_file(case)["sha256"] == before["sha256"], "a refused edit left the profile changed")
        c.expect(j["phase"] == "recovered" and not j.get("recoveryErrors"), f"refused edit journal {j}")
    stop_backend()


def phase_p11_confirm(c, args):
    """Pending owner-path edit, reconnection confirmation and cleanup."""
    case = args[0]
    cfg = NATIVE[case]
    state = load_state()
    settle_networkd(c)
    start_backend()
    api = API()
    view = native_profile(api, case)
    c.expect(view.get("editable"), f"not editable: {view.get('refusal')}")
    before = native_file(case)
    intent = native_candidate(view, case, 16, 91)
    change, edited = api.pending("PUT", f"/network/native/profiles/{cfg['device']}",
                                 {"generation": view["generation"], "intent": intent})
    pending = change_journal()
    c.note("journalPending", pending)
    c.expect(addr_on(cfg["device"], f"{cfg['net']}.16/24"), "candidate address not applied by the owner")
    c.note("confirmed", api.confirm(change))
    j = wait_journal(lambda j: j.get("cleanup") == "complete", 30)
    c.note("journal", j)
    c.expect(j["phase"] == "confirmed" and j.get("cleanup") == "complete", f"journal {j}")
    after = native_profile(api, case)
    c.note("profileAfter", native_view_summary(after))
    c.expect(after["intent"]["ipv4"]["addresses"] == intent["ipv4"]["addresses"], "saved intent differs from the edit")
    c.note("fileBefore", before)
    c.note("fileAfter", native_file(case))
    state.setdefault("p11", {})[case] = {"confirmedBootId": boot_id(), "intent": after["intent"], "file": native_file(case)}
    save_state(state)
    stop_backend()


def phase_p11_persist(c, args):
    """After a reboot: the owner restored the confirmed profile by itself."""
    case = args[0]
    cfg = NATIVE[case]
    entry = load_state()["p11"][case]
    c.expect(entry["confirmedBootId"] != boot_id(), "no reboot since confirmation")
    c.expect(not backend_state()["startedThisBoot"], "the backend ran before inspection")
    c.expect(addr_on(cfg["device"], f"{cfg['net']}.16/24"), "the owner did not restore the confirmed address at boot")
    k = kernel()
    c.expect(route_present(k, "inet", f"10.91.{cfg['net'].split('.')[2]}.0/24"), "confirmed route not restored at boot")
    f = native_file(case)
    c.note("file", f)
    c.expect(f["sha256"] == entry["file"]["sha256"], "the selected profile bytes changed across the reboot")
    start_backend()
    api = API()
    view = native_profile(api, case)
    c.note("profile", native_view_summary(view))
    c.expect(view.get("editable"), f"not editable after reboot: {view.get('refusal')}")
    c.expect(view["intent"] == entry["intent"], "the profile intent after reboot differs from the confirmed intent")
    c.note("journal", change_journal())
    stop_backend()


def phase_p11_kill(c, args):
    """Pending owner-path edit, then backend death before confirmation."""
    case, host = args[0], int(args[1])
    cfg = NATIVE[case]
    state = load_state()
    settle_networkd(c)
    start_backend()
    api = API()
    view = native_profile(api, case)
    c.expect(view.get("editable"), f"not editable: {view.get('refusal')}")
    before = native_file(case)
    intent = native_candidate(view, case, host, 92)
    change, _ = api.pending("PUT", f"/network/native/profiles/{cfg['device']}",
                            {"generation": view["generation"], "intent": intent})
    candidate = f"{cfg['net']}.{host}/24"
    c.expect(addr_on(cfg["device"], candidate), "candidate address not applied by the owner")
    at_kill = change_journal()
    kill_backend()
    c.note("journalAtKill", at_kill)
    c.expect(at_kill["phase"] == "awaiting_confirmation" and at_kill["watchdog"] == "armed", f"journal at kill {at_kill}")
    c.note("candidateFile", native_file(case))
    state.setdefault("p11kill", {})[f"{case}-{host}"] = {
        "change": change, "bootId": boot_id(), "killedAt": time.time(), "candidate": candidate,
        "before": before, "intent": view["intent"]}
    save_state(state)


def p11_verify_recovered(c, entry, case, rebooted):
    cfg = NATIVE[case]
    j = change_journal()
    c.note("journal", j)
    c.expect(j and j["id"] == entry["change"] and j["phase"] == "recovered", f"journal {j}")
    c.expect(j and not j.get("recoveryErrors"), f"recovery errors {j and j.get('recoveryErrors')}")
    c.expect(not addr_on(cfg["device"], entry["candidate"]), f"candidate {entry['candidate']} survived recovery")
    f = native_file(case)
    c.note("file", f)
    c.expect(f["sha256"] == entry["before"]["sha256"], "the selected profile bytes were not restored")
    b = backend_state()
    c.note("backend", b)
    c.expect(not b["startedThisBoot"] if rebooted else b["active"] in ("inactive", "failed"), "the backend ran during recovery")


def phase_p11_timer(c, args):
    key = args[0]
    case = key.rsplit("-", 1)[0]
    entry = load_state()["p11kill"][key]
    j = wait_journal(lambda j: j["phase"] in ("recovered", "degraded"), entry["killedAt"] + 150 - time.time())
    c.note("waitedSeconds", round(time.time() - entry["killedAt"], 1))
    c.note("helperJournal", unit_journal(f"just-dashboard-network-recover-{entry['change']}.service").splitlines()[-30:])
    c.expect(entry["bootId"] == boot_id(), "the guest rebooted during the timer case")
    p11_verify_recovered(c, entry, case, rebooted=False)
    start_backend()
    view = native_profile(API(), case)
    c.note("profile", native_view_summary(view))
    c.expect(view["intent"] == entry["intent"], "the restored intent differs from the prior intent")
    stop_backend()


def phase_p11_boot(c, args):
    key = args[0]
    case = key.rsplit("-", 1)[0]
    entry = load_state()["p11kill"][key]
    c.expect(entry["bootId"] != boot_id(), "no reboot since the kill")
    boot_units_evidence(c, after_recovery=True)
    p11_verify_recovered(c, entry, case, rebooted=True)
    start_backend()
    view = native_profile(API(), case)
    c.note("profile", native_view_summary(view))
    c.expect(view.get("editable"), f"not editable after boot recovery: {view.get('refusal')}")
    c.expect(view["intent"] == entry["intent"], "the restored intent differs from the prior intent")
    stop_backend()


def phase_c002_cancel(c, args):
    """A client that disconnects as soon as its mutation is sent. The apply runs
    on with its own bounded context; the journal must end terminal and the
    kernel must equal whatever the saved spec then says."""
    start_backend()
    api = API()
    body = json.dumps(f4_change(int(args[0]))).encode()
    import socket
    s = socket.create_connection(("127.0.0.1", 8080))
    s.sendall(b"POST /api/v1/network/routing/routes HTTP/1.1\r\nHost: 127.0.0.1:8080\r\nContent-Type: application/json\r\n"
              b"X-JD-CSRF: 1\r\nCookie: " + api.cookie.encode() + b"\r\nContent-Length: " + str(len(body)).encode() +
              b"\r\nConnection: close\r\n\r\n" + body)
    s.close()
    j = wait_journal(lambda j: j["phase"] in ("saved", "recovered", "degraded", "confirmed"), 30)
    time.sleep(1)
    c.note("journal", j)
    c.expect(j["phase"] in ("saved", "recovered"), f"journal after a canceled request: {j}")
    sp = spec()
    in_spec = any(r["destination"] == f4_change(int(args[0]))["destination"] for r in sp["routes"])
    in_kernel = route_present(kernel(), "inet", f4_change(int(args[0]))["destination"])
    c.note("routeInSpec", in_spec)
    c.note("routeInKernel", in_kernel)
    c.expect(in_spec == in_kernel, "spec and kernel disagree after a canceled request")
    summary = drift_summary(api.drift())
    c.note("drift", summary)
    c.expect(not summary["notMatching"], f"drift after a canceled request: {summary['notMatching']}")
    if in_spec:
        rid = next(r["id"] for r in sp["routes"] if r["destination"] == f4_change(int(args[0]))["destination"])
        api.ok("DELETE", f"/network/routing/routes/{rid}")
    stop_backend()


def phase_c004_ownership(c, args):
    """Native NICs keep their owner; dependent managed devices are protected."""
    start_backend()
    api = API()
    _, links = api.ok("GET", "/network/links")
    rows = links["links"] if isinstance(links, dict) and "links" in links else links
    view = {l["name"]: {k: l.get(k) for k in ("kind", "role", "owner", "managed", "guard")} for l in rows
            if l["name"].startswith(("enp", "jdvm"))}
    c.note("links", view)
    for nic in ("enp0s3", "enp0s4", "enp0s5", "enp0s6"):
        c.expect(view.get(nic) and not view[nic].get("managed"), f"{nic} read as dashboard-managed: {view.get(nic)}")
    status, _, body = api.request("DELETE", "/network/links/enp0s4")
    c.note("deleteNative", {"status": status, "body": body})
    c.expect(status in (400, 403, 409), f"deleting a native NIC answered {status}")
    status, _, body = api.request("DELETE", "/network/links/jdvmbr0")
    c.note("deleteManagedWithDependents", {"status": status, "body": body})
    c.expect(status in (400, 409), f"deleting a bridge carrying routes, rules and NAT answered {status}")
    k = kernel()
    c.expect(link(k, "enp0s4") is not None and link(k, "jdvmbr0") is not None, "a refused deletion removed a device")
    stop_backend()


# C027 VRF: the guest kernel has the vrf module the production host lacks.
# The routing package with l3mdev rules and VRF listing is a separate branch;
# its server and test binary are staged beside this one (ROUTING) and run last.

ROUTING = "/var/tmp/jd-vm-acceptance/routing"


def phase_vrf_kernel(c, args):
    """Traffic through a real VRF in two disposable namespaces."""
    a, b = "jdvrf-a", "jdvrf-b"
    for ns in (a, b):
        sh("ip", "netns", "del", ns, check=False)
        sh("ip", "netns", "add", ns)
    try:
        for argv in (["-n", a, "link", "add", "blue", "type", "vrf", "table", "1100"], ["-n", a, "link", "set", "blue", "up"],
                     ["-n", a, "link", "add", "va", "type", "veth", "peer", "name", "vb", "netns", b],
                     ["-n", a, "link", "set", "va", "master", "blue"], ["-n", a, "addr", "add", "10.244.0.1/30", "dev", "va"],
                     ["-n", a, "link", "set", "va", "up"], ["-n", b, "addr", "add", "10.244.0.2/30", "dev", "vb"],
                     ["-n", b, "link", "set", "vb", "up"], ["-n", b, "link", "set", "lo", "up"]):
            sh("ip", *argv)
        rules = sh("ip", "-n", a, "rule", "show")
        in_vrf = sh("ip", "-n", a, "route", "get", "10.244.0.2", "vrf", "blue", check=False)
        outside = subprocess.run(["ip", "-n", a, "route", "get", "10.244.0.2"], capture_output=True, text=True)
        ping_vrf = subprocess.run(["ip", "netns", "exec", a, "ping", "-c", "2", "-W", "2", "-I", "blue", "10.244.0.2"],
                                  capture_output=True, text=True)
        ping_main = subprocess.run(["ip", "netns", "exec", a, "ping", "-c", "1", "-W", "1", "10.244.0.2"],
                                   capture_output=True, text=True)
        c.note("module", os.path.isdir("/sys/module/vrf"))
        c.note("rules", rules.splitlines())
        c.note("routeGetInVRF", in_vrf.strip())
        c.note("routeGetOutside", (outside.stdout + outside.stderr).strip())
        c.note("pingThroughVRF", ping_vrf.stdout.strip().splitlines()[-2:])
        c.note("pingOutsideVRF", (ping_main.stdout + ping_main.stderr).strip().splitlines()[-2:])
        c.expect("l3mdev" in rules, "the kernel added no l3mdev rule for the VRF")
        c.expect("table 1100" in in_vrf and "dev va" in in_vrf, f"VRF lookup: {in_vrf}")
        c.expect(ping_vrf.returncode == 0, "no traffic through the VRF")
        c.expect(ping_main.returncode != 0, "the VRF-only peer answered outside the VRF")
    finally:
        for ns in (a, b):
            sh("ip", "netns", "del", ns, check=False)


def phase_vrf_routing(c, args):
    """The routing package's l3mdev policy rule and VRF reading against a real
    VRF device, through its own server, then its live routing test."""
    state = load_state()
    live = subprocess.run(["env", "JD_NETNS_LIVE=1", "TMPDIR=" + ROUTING + "/tmp", ROUTING + "/netx-routing.test",
                           "-test.run", "^TestLiveRoutingMaturity$", "-test.count=1", "-test.v"],
                          capture_output=True, text=True, cwd=ROUTING, timeout=300)
    c.note("liveRoutingMaturity", (live.stdout + live.stderr).strip().splitlines()[-6:])
    c.expect(live.returncode == 0, "TestLiveRoutingMaturity failed in the guest")
    stop_backend()
    if not os.path.exists("/usr/local/bin/just-dashboard.main"):
        os.replace("/usr/local/bin/just-dashboard", "/usr/local/bin/just-dashboard.main")
    sh("install", "-m", "0755", ROUTING + "/just-dashboard-routing", "/usr/local/bin/just-dashboard")
    for argv in (["link", "add", "jdvrf0", "type", "vrf", "table", "1100"], ["link", "set", "jdvrf0", "up"],
                 ["link", "add", "jdvrfm0", "type", "dummy"], ["link", "set", "jdvrfm0", "master", "jdvrf0"],
                 ["addr", "add", "10.245.0.1/24", "dev", "jdvrfm0"], ["link", "set", "jdvrfm0", "up"]):
        sh("ip", *argv, check=False)
    start_backend()
    api = API()
    status, _, rule = api.request("POST", "/network/routing/rules", {"l3mdev": True, "from": "10.245.0.0/24"})
    c.note("addRule", {"status": status, "body": rule})
    c.expect(status in (200, 201), f"l3mdev rule add answered {status}: {rule}")
    _, view = api.ok("GET", "/network/routing")
    vrfs = view.get("vrfs")
    managed = [r for r in view["rules"] if r.get("l3mdev")]
    c.note("vrfs", vrfs)
    c.note("l3mdevRules", managed)
    c.note("clientDecision", view.get("clientDecision"))
    c.expect({"name": "jdvrf0", "table": 1100} in [{"name": v.get("name"), "table": v.get("table")} for v in vrfs or []],
             f"VRF device not read: {vrfs}")
    c.expect(any(r.get("managed") and r.get("from") == "10.245.0.0/24" for r in managed), f"l3mdev rule not read back as managed: {managed}")
    status, _, preview = api.request("POST", "/network/routing/rules/preview",
                                     {"l3mdev": True, "from": "10.245.0.0/24", "probe": {"target": "10.245.0.9", "source": "10.245.0.1"}})
    c.note("preview", {"status": status, "probe": (preview or {}).get("probe") if isinstance(preview, dict) else preview})
    c.note("kernelRules", sh("ip", "rule", "show").splitlines())
    state["vrf"] = {"bootId": boot_id(), "rule": rule}
    save_state(state)
    stop_backend()


def phase_vrf_routing_verify(c, args):
    """After a reboot the routing package's boot unit restored its l3mdev rule;
    the unmanaged VRF device itself is gone, as it was never persisted."""
    state = load_state()
    c.expect(state["vrf"]["bootId"] != boot_id(), "no reboot since the rule was added")
    rules = sh("ip", "rule", "show")
    c.note("rules", rules.splitlines())
    c.note("networkUnitCommands", exec_outcomes(UNIT))
    c.expect(any("from 10.245.0.0/24" in l and "l3mdev" in l for l in rules.splitlines()), "l3mdev rule not restored at boot")
    stop_backend()
    if os.path.exists("/usr/local/bin/just-dashboard.main"):
        os.replace("/usr/local/bin/just-dashboard.main", "/usr/local/bin/just-dashboard")


def phase_drift(c, args):
    """Read-only drift inspection, for evidence between steps."""
    start_backend()
    api = API()
    c.note("drift", drift_summary(api.drift()))


def phase_state(c, args):
    c.note("journal", change_journal())
    c.note("backend", backend_state())
    c.note("units", {u: unit_show(u, "ActiveState", "Result", "UnitFileState") for u in (UNIT, RECOVERY_UNIT, BACKEND)})


PHASES = {
    "baseline": phase_baseline,
    "c001-apply": phase_c001_apply,
    "c001-verify": phase_c001_verify,
    "f4-kill": phase_f4_kill,
    "f4-timer": phase_f4_timer,
    "f4-boot": phase_f4_boot,
    "c002-cancel": phase_c002_cancel,
    "c004-ownership": phase_c004_ownership,
    "p7-runtime-drift": phase_p7_runtime_drift,
    "p7-runtime-reboot": lambda c, a: (c.expect(load_state()["p7runtime"]["bootId"] != boot_id(), "no reboot"),
                                       verify_boot_restored(c, "p7-runtime-reboot"), stop_backend()),
    "p7-file-drift": phase_p7_file_drift,
    "p7-file-verify": phase_p7_file_verify,
    "p7-repaired-verify": lambda c, a: (c.expect(load_state()["p7file"]["repairedBootId"] != boot_id(), "no reboot"),
                                        verify_boot_restored(c, "p7-repaired-verify"), stop_backend()),
    "p7-daemon-restart": phase_p7_daemon_restart,
    "p11-read": phase_p11_read,
    "p11-first-edit": phase_p11_first_edit,
    "p11-confirm": phase_p11_confirm,
    "p11-persist": phase_p11_persist,
    "p11-kill": phase_p11_kill,
    "p11-timer": phase_p11_timer,
    "p11-boot": phase_p11_boot,
    "p14-apply": phase_p14_apply,
    "p14-verify": phase_p14_verify,
    "vrf-kernel": phase_vrf_kernel,
    "vrf-routing": phase_vrf_routing,
    "vrf-routing-verify": phase_vrf_routing_verify,
    "drift": phase_drift,
    "state": phase_state,
}


def main():
    if len(sys.argv) < 2 or sys.argv[1] not in PHASES:
        print(__doc__)
        print("phases:", " ".join(PHASES))
        sys.exit(2)
    name = sys.argv[1]
    c = Check(" ".join(sys.argv[1:]))
    try:
        PHASES[name](c, sys.argv[2:])
    except Failed as e:
        c.expect(False, str(e))
    c.finish()


if __name__ == "__main__":
    main()
