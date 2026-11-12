import os
import pathlib
import subprocess
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).with_name("preflight.sh").resolve()
GB = 1024 * 1024  # kilobytes, as /proc/meminfo counts


class PreflightTest(unittest.TestCase):
    """jd_preflight against a machine described by stubs; it only ever reads."""

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        (self.root / "systemd").mkdir()

    def tool(self, name, script):
        path = self.bin / name
        path.write_text(script)
        path.chmod(0o755)

    def machine(self, arch="x86_64", memory=4 * GB, swap=0, free_mb=40000, systemd=True, docker_root=None,
                meminfo=True):
        self.tool("uname", f'#!/bin/bash\n[ "$1" = -m ] && echo {arch}\n')
        self.tool("df", '#!/bin/bash\nprintf "%s\\n" "${@: -1}" > "$TEST_ROOT/df-asked"\n'
                        'echo "Filesystem 1048576-blocks Used Available Capacity Mounted on"\n'
                        f'echo "/dev/vda1 80000 1 {free_mb} 1% /"\n')
        if docker_root is not None:
            self.tool("docker", f'#!/bin/bash\n[ "$1" = info ] && echo "{docker_root}"\n')
        if meminfo:
            (self.root / "meminfo").write_text(
                f"MemTotal:       {memory} kB\nMemFree:          1000 kB\nSwapTotal:      {swap} kB\nSwapFree: 0 kB\n")
        result = subprocess.run(
            ["/bin/bash", "-c", 'set -euo pipefail; source "$1"; jd_preflight', "test", str(SCRIPT)],
            env={"PATH": f"{self.bin}:/usr/bin:/bin", "TEST_ROOT": str(self.root),
                 "JD_PREFLIGHT_MEMINFO": str(self.root / "meminfo"),
                 "JD_PREFLIGHT_SYSTEMD": str(self.root / ("systemd" if systemd else "no-systemd"))},
            capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stderr, "")
        lines = [line.split("\t", 1) for line in result.stdout.splitlines()]
        self.assertTrue(all(len(line) == 2 and line[0] in ("ok", "warn", "stop") for line in lines), result.stdout)
        return lines

    def kinds(self, lines):
        return [kind for kind, _ in lines]

    def test_a_server_with_enough_of_everything_passes(self):
        lines = self.machine()
        self.assertEqual(self.kinds(lines), ["ok", "ok", "ok", "ok"])
        self.assertEqual([what for _, what in lines],
                         ["x86_64 (amd64)", "systemd", "4.0 GB of memory and 0.0 GB of swap",
                          "39.1 GB free under /var/lib/docker"])

    def test_arm64_is_supported_and_other_architectures_stop(self):
        self.assertEqual(self.machine(arch="aarch64")[0], ["ok", "aarch64 (arm64)"])
        for arch in ("armv7l", "i686", "riscv64"):
            kind, what = self.machine(arch=arch)[0]
            self.assertEqual(kind, "stop")
            self.assertIn(f"This server is {arch}.", what)
            self.assertIn("x86_64 (amd64) and aarch64 (arm64) only", what)

    def test_a_server_without_systemd_stops(self):
        kind, what = self.machine(systemd=False)[1]
        self.assertEqual(kind, "stop")
        self.assertIn("does not run systemd", what)

    def test_too_little_memory_warns_with_the_numbers(self):
        lines = self.machine(memory=int(0.95 * GB), swap=0)
        self.assertEqual(self.kinds(lines), ["ok", "ok", "warn", "ok"])
        self.assertIn("0.9 GB of memory and 0.0 GB of swap", lines[2][1])
        self.assertIn("about 3 GB", lines[2][1])
        self.assertIn("docs/guides/install.md", lines[2][1])
        # A 2 GB server is short by itself, and swap makes up the difference.
        self.assertEqual(self.machine(memory=int(1.9 * GB), swap=0)[2][0], "warn")
        self.assertEqual(self.machine(memory=int(1.9 * GB), swap=2 * GB)[2],
                         ["ok", "1.9 GB of memory and 2.0 GB of swap"])
        # A server sold as 3 GB reports a little less, and is enough.
        self.assertEqual(self.machine(memory=int(2.85 * GB), swap=0)[2][0], "ok")

    def test_too_little_disk_warns_with_the_number_and_the_place(self):
        lines = self.machine(free_mb=4200, docker_root="/srv/docker")
        self.assertEqual(self.kinds(lines), ["ok", "ok", "ok", "warn"])
        self.assertIn("4.1 GB free under /srv/docker", lines[3][1])
        self.assertIn("about 10 GB", lines[3][1])

    def test_disk_is_measured_where_docker_will_keep_its_images(self):
        # Before Docker is installed that directory does not exist yet; the
        # filesystem that will hold it is the one measured.
        self.machine()
        asked = (self.root / "df-asked").read_text().strip()
        self.assertTrue("/var/lib/docker".startswith(asked.rstrip("/")) or asked == "/", asked)
        self.machine(docker_root=str(self.root / "docker-data" / "root"))
        self.assertEqual((self.root / "df-asked").read_text().strip(), str(self.root))

    def test_what_cannot_be_measured_is_left_out(self):
        lines = self.machine(meminfo=False)
        self.assertEqual(self.kinds(lines), ["ok", "ok", "ok"])
        self.tool("df", "#!/bin/bash\nexit 1\n")
        result = subprocess.run(
            ["/bin/bash", "-c", 'set -euo pipefail; source "$1"; jd_preflight', "test", str(SCRIPT)],
            env={"PATH": f"{self.bin}:/usr/bin:/bin", "JD_PREFLIGHT_MEMINFO": str(self.root / "meminfo"),
                 "JD_PREFLIGHT_SYSTEMD": str(self.root / "systemd")},
            capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual([line.split("\t")[0] for line in result.stdout.splitlines()], ["ok", "ok"])

    def test_the_documents_state_the_numbers_the_check_uses(self):
        script = SCRIPT.read_text()
        self.assertIn("JD_NEED_MEMORY_MB=2800", script)
        self.assertIn("JD_NEED_DISK_MB=10000", script)
        repo = SCRIPT.parent.parent
        for name in ("README.md", "docs/guides/install.md"):
            text = (repo / name).read_text()
            for phrase in ("x86_64", "arm64", "systemd", "2 GB of swap", "10 GB"):
                self.assertTrue(phrase in text, f"{name} does not state {phrase!r} under Requirements")


if __name__ == "__main__":
    unittest.main()
