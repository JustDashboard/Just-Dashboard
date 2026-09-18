"""Read-only Compose interpolation regression; no daemon or containers required."""
import pathlib
import shutil
import subprocess
import tempfile
import unittest

HELPER = pathlib.Path(__file__).with_name("dotenv.sh").resolve()


class DotenvTest(unittest.TestCase):
    def literal(self, value):
        return subprocess.run(
            ["bash", "-c", 'source "$1"; jd_dotenv_literal "$2"', "test", str(HELPER), value],
            capture_output=True, text=True,
        )

    def test_rejects_extra_lines(self):
        for value in ["password\nOTHER=value", "password\rvalue"]:
            self.assertNotEqual(self.literal(value).returncode, 0)

    @unittest.skipUnless(shutil.which("docker"), "Docker Compose CLI unavailable")
    def test_compose_interpolation_preserves_literal_passwords(self):
        if subprocess.run(["docker", "compose", "version"], capture_output=True).returncode:
            self.skipTest("Docker Compose CLI unavailable")
        values = [
            "Abcd1234$JD_AUDIT_MISSING!", " trailing # value ", 'double"single\'quote',
            "back\\slash\\", "${JD_AUDIT_MISSING:-expanded}", "two$$dollars", "line\\ntext",
        ]
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            compose = root / "compose.yml"
            compose.write_text("services:\n  probe:\n    image: scratch\n    environment:\n      PASSWORD: ${PASSWORD}\n")
            for value in values:
                with self.subTest(value=value):
                    encoded = self.literal(value)
                    self.assertEqual(encoded.returncode, 0, encoded.stderr)
                    (root / ".env").write_text("PASSWORD=" + encoded.stdout + "\n")
                    # The config serializer re-escapes dollars for reuse as a
                    # Compose document. --environment gives the resolved value.
                    result = subprocess.run(
                        ["docker", "compose", "--project-directory", directory, "-f", str(compose), "config", "--environment"],
                        capture_output=True, text=True, check=True,
                    )
                    actual = next(line.removeprefix("PASSWORD=") for line in result.stdout.splitlines() if line.startswith("PASSWORD="))
                    self.assertEqual(actual, value)


if __name__ == "__main__":
    unittest.main()
