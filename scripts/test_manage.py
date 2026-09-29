import json
import os
import pathlib
import pty
import select
import shutil
import subprocess
import tempfile
import time
import unittest


REPO = pathlib.Path(__file__).resolve().parent.parent


class TerminalToolsTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name) / "checkout with spaces"
        self.scripts = self.root / "scripts"
        self.scripts.mkdir(parents=True)
        for name in ("manage.sh", "create-user.sh", "reset-password.sh", "dotenv.sh"):
            shutil.copy2(REPO / "scripts" / name, self.scripts / name)
        self.bin = pathlib.Path(self.temp.name) / "bin"
        self.bin.mkdir()
        self.calls = pathlib.Path(self.temp.name) / "calls.jsonl"
        self.env = {**os.environ, "PATH": f"{self.bin}:{os.environ['PATH']}",
                    "TEST_CALLS": str(self.calls), "NO_COLOR": "1"}
        self.tool("id", '#!/bin/bash\necho "${TEST_UID:-0}"\n')
        self.tool("docker", '''#!/usr/bin/env python3
import json, os, pathlib, sys
args = sys.argv[1:]
if args == ["compose", "version"]:
    sys.exit(int(os.environ.get("TEST_COMPOSE_FAIL", "0")))
if args == ["--version"]:
    print("Docker version 29.8.1")
    sys.exit(0)
entry = {"args": args, "cwd": os.getcwd()}
if "--admin" in args and any(x in args for x in ["create-user", "reset-password"]):
    entry["stdin"] = sys.stdin.read()
with pathlib.Path(os.environ["TEST_CALLS"]).open("a") as calls:
    calls.write(json.dumps(entry) + "\\n")
if args == ["compose", "build"]:
    sys.exit(int(os.environ.get("TEST_BUILD_FAIL", "0")))
''')
        # Sourcing this as shell code would execute the marker command.
        (self.root / ".env").write_text('JD_MASTER_KEY=fixture\nUNTRUSTED=$(touch sourced-env)\n')

    def tool(self, name, script):
        path = self.bin / name
        path.write_text(script)
        path.chmod(0o755)

    def run_tool(self, name, *args, input="", env=None):
        return subprocess.run([str(self.scripts / name), *args], input=input,
                              capture_output=True, text=True, cwd=self.temp.name,
                              env={**self.env, **(env or {})})

    def read_calls(self):
        return [json.loads(line) for line in self.calls.read_text().splitlines()] if self.calls.exists() else []

    def test_password_wrappers_preserve_stdin_without_secrets_in_argv(self):
        password = "Temporary password $'`\\42!"
        for script, args, command in (
                ("create-user.sh", ("Alice", "limited"), "create-user"),
                ("reset-password.sh", ("admin",), "reset-password")):
            result = self.run_tool(script, *args, "--password-stdin", input=password + "\n")
            self.assertEqual(result.returncode, 0, result.stderr)
            call = self.read_calls()[-1]
            self.assertEqual(call["args"], ["compose", "run", "--rm", "--no-deps", "-T", "backend", "--admin", command, *args])
            self.assertEqual(call["stdin"], password + "\n")
            self.assertEqual(call["cwd"], str(self.root))
            self.assertNotIn(password, result.stdout + result.stderr + " ".join(call["args"]))
            self.assertFalse((self.root / "sourced-env").exists())

    def test_stack_commands_use_current_environment_and_named_services(self):
        for args, expected in (
                (("status",), ["ps", "--all"]),
                (("logs",), ["logs", "--tail", "100", "--follow", "backend"]),
                (("logs", "proxy"), ["logs", "--tail", "100", "--follow", "proxy"]),
                (("restart",), ["up", "-d", "--force-recreate"])):
            result = self.run_tool("manage.sh", *args)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(self.read_calls()[-1]["args"], ["compose", *expected])

    def test_help_needs_no_root_configuration_or_docker(self):
        (self.root / ".env").unlink()
        for script in ("manage.sh", "create-user.sh", "reset-password.sh"):
            result = self.run_tool(script, "--help", env={"TEST_UID": "1000"})
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("Usage:", result.stdout)
        self.assertEqual(self.read_calls(), [])

    def test_bad_inputs_and_non_root_do_not_reach_docker(self):
        for args in (("create-user",), ("create-user", "user", "owner"),
                     ("reset-password", "user", "extra"), ("logs", "database"),
                     ("status", "extra"), ("unknown",), ("revoke-sessions",)):
            result = self.run_tool("manage.sh", *args)
            self.assertNotEqual(result.returncode, 0)
        result = self.run_tool("manage.sh", "users", env={"TEST_UID": "1000"})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("sudo", result.stderr)
        result = self.run_tool("reset-password.sh", "admin", input="secret\n")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("--password-stdin", result.stderr)
        self.assertEqual(self.read_calls(), [])

    def test_legacy_compose_fallback(self):
        self.tool("docker-compose", '#!/bin/bash\nexec docker compose "$@"\n')
        result = self.run_tool("manage.sh", "users", env={"TEST_COMPOSE_FAIL": "1"})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.read_calls()[-1]["args"][-2:], ["--admin", "users"])

    def test_interactive_password_is_hidden_and_confirmed(self):
        master, slave = pty.openpty()
        process = subprocess.Popen([str(self.scripts / "reset-password.sh"), "admin"],
                                   stdin=slave, stdout=slave, stderr=slave, env=self.env)
        os.close(slave)
        output = b""
        answers = 0
        deadline = time.monotonic() + 5
        try:
            while time.monotonic() < deadline:
                ready, _, _ = select.select([master], [], [], 0.1)
                if ready:
                    try:
                        output += os.read(master, 4096)
                    except OSError:
                        break
                if answers == 0 and b"Password: " in output:
                    os.write(master, b"Hidden-Password-42\n")
                    answers = 1
                if answers == 1 and b"Confirm password: " in output:
                    os.write(master, b"Hidden-Password-42\n")
                    answers = 2
            self.assertEqual(process.wait(timeout=1), 0, output.decode())
            self.assertEqual(answers, 2)
            self.assertNotIn(b"Hidden-Password-42", output)
            self.assertEqual(self.read_calls()[-1]["stdin"], "Hidden-Password-42\n")
        finally:
            os.close(master)
            if process.poll() is None:
                process.kill()
                process.wait()

    def run_installer(self, build_fail=False):
        script = (REPO / "install.sh").read_text()
        # Every writable absolute host path is confined to this disposable fixture.
        for prefix in ("/var/lib/", "/var/backups/", "/tmp/just-dashboard"):
            script = script.replace(prefix, str(pathlib.Path(self.temp.name) / "host") + prefix)
        installer = self.root / "install.sh"
        installer.write_text(script)
        installer.chmod(0o755)
        (self.root / "docker-compose.yml").write_text("services: {}\n")
        (self.root / ".env").write_text("""JD_MASTER_KEY=fixture
JD_SITE=localhost
JD_BIND=
JD_TLS=off
JD_REQUIRE_2FA=false
JD_TERMINAL_ENABLED=false
JD_PORT=8443
JD_BACKEND_PORT=42001
JD_FRONTEND_PORT=42002
""")
        version_dir = self.root / "backend/internal/version"
        version_dir.mkdir(parents=True)
        shutil.copy2(REPO / "backend/internal/version/version.go", version_dir / "version.go")
        (self.scripts / "install-dependencies.sh").write_text("jd_install_dependencies() { :; }\njd_install_host_tools() { :; }\n")
        self.tool("curl", '#!/bin/bash\n[[ "${@: -1}" == https://api.ipify.org ]] && echo TEST_SERVER\nexit 0\n')
        self.tool("dpkg", '#!/bin/bash\nexit 0\n')
        self.tool("chown", '#!/bin/bash\nexit 0\n')
        self.tool("logname", '#!/bin/bash\necho operator\n')
        (pathlib.Path(self.temp.name) / "host/tmp").mkdir(parents=True)
        return subprocess.run([str(installer)], input="\n", capture_output=True, text=True,
                              cwd=self.temp.name, env={**self.env, "TEST_BUILD_FAIL": "1" if build_fail else "0"})

    def test_installer_completion_shows_stages_access_and_terminal_commands(self):
        result = self.run_installer()
        self.assertEqual(result.returncode, 0, result.stderr)
        for text in ("[1/4] Prepare the host", "[2/4] Configure the dashboard",
                     "[3/4] Build and start", "[4/4] Verify startup", "Setup complete",
                     "http://localhost:8443", "scripts/reset-password.sh USER",
                     "scripts/create-user.sh USER limited", "scripts/manage.sh status",
                     "scripts/manage.sh --help"):
            self.assertIn(text, result.stdout)
        self.assertNotIn("\x1b", result.stdout)
        self.assertIn("./scripts/manage.sh restart", result.stdout)
        self.assertNotIn("compose restart", result.stdout)

    def test_installer_build_failure_never_claims_success(self):
        result = self.run_installer(build_fail=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("Setup complete", result.stdout)


if __name__ == "__main__":
    unittest.main()
