"""That the shipped stack puts nothing but Caddy on a routable address (invariant 7).

The backend refuses a routable JD_ADDR only when no allowlist is set, so what keeps the
stack itself to the rule is docker-compose.yml: the backend's JD_ADDR pinned to loopback,
every published port on loopback, and nothing on the host's network but the backend and
Caddy. Read with the standard library alone, as the other tests here are, which is enough
for the parts of the file this checks.
"""
import ipaddress
import pathlib
import re
import unittest

ROOT = pathlib.Path(__file__).resolve().parent.parent
COMPOSE = ROOT / "docker-compose.yml"

# The two services allowed the host's network: the backend, which manages the host and
# binds JD_ADDR, and the proxy, which is Caddy and the stack's one way in.
HOST_NETWORK = {"backend", "proxy"}

VARIABLE = re.compile(r"\$\{(\w+)(?::?-([^}]*))?\}")


def expand(value):
    """A value with each ${VAR:-default} replaced by its default, or by VAR when it has none."""
    return VARIABLE.sub(lambda m: m.group(2) if m.group(2) is not None else m.group(1), value)


def unquote(value):
    value = value.strip()
    if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
        return value[1:-1]
    return re.sub(r"\s+#.*$", "", value)


def services(text):
    """Each service's lines, by name, from the top-level services block."""
    out, current, inside = {}, None, False
    for line in text.splitlines():
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        indent = len(line) - len(line.lstrip(" "))
        if indent == 0:
            inside, current = line.rstrip() == "services:", None
        elif inside and indent == 2 and line.rstrip().endswith(":"):
            current = line.strip()[:-1]
            out[current] = []
        elif inside and current is not None:
            out[current].append(line)
    return out


def section(lines, key):
    """The value of a service's own key, and the lines nested under it."""
    if not lines:
        return None, []
    own = len(lines[0]) - len(lines[0].lstrip(" "))
    value, nested, found = None, [], False
    for line in lines:
        indent = len(line) - len(line.lstrip(" "))
        # A sequence may sit at its key's own indent.
        if indent == own and not line.strip().startswith("- "):
            name, _, rest = line.strip().partition(":")
            found = name == key
            if found:
                value = unquote(rest) or None
        elif found:
            nested.append(line)
    return value, nested


def items(nested):
    """A block sequence's items, each a string or, in the long syntax, a mapping."""
    out = []
    for line in nested:
        text = line.strip()
        if text.startswith("- "):
            entry = text[2:]
            if re.match(r"^[\w-]+:\s", entry + " ") and not entry.startswith(("'", '"')):
                name, _, rest = entry.partition(":")
                out.append({name.strip(): unquote(rest)})
            else:
                out.append(unquote(entry))
        elif out and isinstance(out[-1], dict):
            name, _, rest = text.partition(":")
            out[-1][name.strip()] = unquote(rest)
    return out


def environment(lines):
    """A service's environment map, KEY: value form."""
    pairs = (line.strip().partition(":") for line in section(lines, "environment")[1])
    return {key.strip(): unquote(value) for key, _, value in pairs}


def split_address(addr):
    """host and port of host:port, the host's brackets removed; the port may hold ${VAR:-x}."""
    match = re.match(r"^(\[[^\]]*\]|[^:\[]*):(.*)$", addr)
    return (match.group(1).strip("[]"), match.group(2)) if match else ("", addr)


def published_host(entry):
    """The host address a published port binds, or None for every address."""
    if isinstance(entry, dict):
        return entry.get("host_ip")
    spec = expand(entry).split("/")[0]
    if spec.startswith("["):
        return spec[1:spec.index("]")]
    parts = spec.split(":")
    return parts[0] if len(parts) == 3 else None


def loopback(address):
    try:
        return ipaddress.ip_address(address).is_loopback
    except (TypeError, ValueError):
        return False


def problems(text):
    """Each way the file puts a socket other than Caddy's on a routable address."""
    found = []
    stack = services(text)
    on_host = {name for name, lines in stack.items() if section(lines, "network_mode")[0] == "host"}
    for name in sorted(on_host - HOST_NETWORK):
        found.append(f"{name} is on the host's network, where it can bind any address")
    for name in sorted(HOST_NETWORK - on_host):
        found.append(f"{name} is expected on the host's network and is not")
    for name, lines in stack.items():
        for entry in items(section(lines, "ports")[1]):
            host = published_host(entry)
            if not loopback(host):
                found.append(f"{name} publishes {entry} on {host or 'every address'}, not loopback")
    addr = environment(stack.get("backend", [])).get("JD_ADDR")
    if addr is None:
        found.append("the backend's JD_ADDR is not set, so it is whatever .env says")
    else:
        host, port = split_address(addr)
        if not loopback(host):
            found.append(f"the backend binds JD_ADDR {addr}, not a loopback address")
        if "${JD_BACKEND_PORT" not in port:
            found.append(f"the backend's JD_ADDR port {port} is not JD_BACKEND_PORT, which the proxy forwards to")
    proxy_image = section(stack.get("proxy", []), "image")[0] or ""
    if not proxy_image.startswith("caddy:"):
        found.append(f"the proxy runs {proxy_image or 'no image'}, not Caddy")
    return found


class ComposeBindsTest(unittest.TestCase):
    def test_the_shipped_stack_binds_nothing_but_caddy_on_a_routable_address(self):
        self.assertEqual(problems(COMPOSE.read_text()), [])

    def test_the_backend_is_pinned_to_loopback_and_the_frontend_is_published_there(self):
        stack = services(COMPOSE.read_text())
        self.assertEqual(environment(stack["backend"])["JD_ADDR"], "127.0.0.1:${JD_BACKEND_PORT:-8080}")
        self.assertEqual(items(section(stack["frontend"], "ports")[1]), ["127.0.0.1:${JD_FRONTEND_PORT:-3000}:3000"])

    def test_each_way_out_of_loopback_is_named(self):
        # So the checks above cannot pass by reading nothing.
        bad = """name: x

services:
  backend:
    network_mode: host
    environment:
      JD_ADDR: 0.0.0.0:${JD_BACKEND_PORT:-8080}
  frontend:
    ports:
      - "${JD_FRONTEND_PORT:-3000}:3000"
      - "0.0.0.0:3001:3001"
      - "[::1]:3002:3002"
      - target: 3003
        published: 3003
      - target: 3004
        host_ip: 127.0.0.1
  sidecar:
    network_mode: host
  proxy:
    image: nginx:1
    network_mode: host

volumes:
  data:
"""
        self.assertEqual(
            problems(bad),
            [
                "sidecar is on the host's network, where it can bind any address",
                "frontend publishes ${JD_FRONTEND_PORT:-3000}:3000 on every address, not loopback",
                "frontend publishes 0.0.0.0:3001:3001 on 0.0.0.0, not loopback",
                "frontend publishes {'target': '3003', 'published': '3003'} on every address, not loopback",
                "the backend binds JD_ADDR 0.0.0.0:${JD_BACKEND_PORT:-8080}, not a loopback address",
                "the proxy runs nginx:1, not Caddy",
            ],
        )
        self.assertIn("the backend's JD_ADDR is not set, so it is whatever .env says", problems("services:\n  backend:\n    network_mode: host\n"))


if __name__ == "__main__":
    unittest.main()
