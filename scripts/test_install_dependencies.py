import os
import pathlib
import subprocess
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).with_name("install-dependencies.sh").resolve()


class DependenciesTest(unittest.TestCase):
    def run_setup(self, present, manager=True, fail=False):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            tool_script = '#!/bin/bash\nif [[ "$1" == plugins ]]; then echo "standalone webroot"; fi\n'
            for name in present:
                path = root / name
                path.write_text(tool_script)
                path.chmod(0o755)
            if manager:
                path = root / 'apt-get'
                path.write_text('#!/bin/bash\nprintf "%s\\n" "$*" >> "$TEST_ROOT/calls"\n' + (
                    'exit 1\n' if fail else
                    '''if [[ "$1" == install ]]; then
for tool in curl openssl certbot; do
printf '#!/bin/bash\\nif [[ "$1" == plugins ]]; then echo "standalone webroot"; fi\\n' > "$TEST_ROOT/$tool"
/bin/chmod 755 "$TEST_ROOT/$tool"
done
fi
'''))
                path.chmod(0o755)
            result = subprocess.run(['/bin/bash', '-c', 'source "$1"; jd_install_dependencies', 'test', str(SCRIPT)],
                                    env={**os.environ, 'PATH': directory, 'TEST_ROOT': directory}, capture_output=True, text=True)
            calls = (root / 'calls').read_text() if (root / 'calls').exists() else ''
            return result, calls

    def test_installs_only_missing_tools(self):
        result, calls = self.run_setup(['curl', 'openssl'])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, 'update\ninstall -y --no-install-recommends certbot\n')

    def test_existing_install_does_not_reinstall(self):
        result, calls = self.run_setup(['curl', 'openssl', 'certbot'])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, '')

    def test_package_failure_stops_setup(self):
        result, calls = self.run_setup([], fail=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(calls, 'update\n')

    def test_unsupported_host_reports_missing_tools(self):
        result, _ = self.run_setup([], manager=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('No supported package manager', result.stderr)


if __name__ == '__main__':
    unittest.main()
