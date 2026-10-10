import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time

from importlib.machinery import SourceFileLoader

prepare = SourceFileLoader('native_prepare', str(Path(__file__).with_name('prepare-native.py'))).load_module()
ROOT, ARTIFACTS, TMP, SOURCE, BINARY = prepare.ROOT, prepare.ARTIFACTS, prepare.TMP, prepare.SOURCE, prepare.BINARY


def digest(data):
    return hashlib.sha256(data).hexdigest()


def inventory():
    return {kind: subprocess.check_output(
        ['docker', kind, 'ls', '-q', '--filter', 'label=io.justdashboard.dns.provision'],
        timeout=15).decode().splitlines() for kind in ('container', 'network', 'volume')}


def host():
    networkd_stat = Path('/proc/883/stat').read_text()
    status = {line.split(':', 1)[0]: line.split(':', 1)[1].strip()
              for line in Path('/proc/883/status').read_text().splitlines()}
    identity = {'comm': Path('/proc/883/comm').read_text().strip(),
                'name': status['Name'],
                'uids': [int(value) for value in status['Uid'].split()],
                'gids': [int(value) for value in status['Gid'].split()]}
    start = networkd_stat.rsplit(')', 1)[1].split()[19]
    assert networkd_stat.split(' ', 1)[0] == '883' and start == '452'
    assert identity == {'comm': 'systemd-network', 'name': 'systemd-network',
                        'uids': [998] * 4, 'gids': [998] * 4}, 'production networkd witness changed'
    return {
        'resolver_sha256': digest(Path('/etc/resolv.conf').read_bytes()),
        'networkd883_start': start,
        'networkd883_identity': identity,
        'nm_paths': {p: os.path.lexists(p) for p in ['/etc/NetworkManager', '/var/lib/NetworkManager']},
    }


def processes():
    found = []
    for entry in Path('/proc').iterdir():
        if not entry.name.isdigit():
            continue
        try:
            args = (entry / 'cmdline').read_bytes().split(b'\0')
        except (FileNotFoundError, PermissionError, ProcessLookupError):
            continue
        if str(BINARY).encode() in args:
            found.append({'pid': int(entry.name), 'argv': [a.decode(errors='replace') for a in args if a]})
    return found


def main():
    assert len(sys.argv) == 2 and sys.argv[1] in ('adguard', 'pihole')
    engine = sys.argv[1]
    prefix = ARTIFACTS / ('native-' + engine + '-final')
    assert not any(prefix.with_suffix(suffix).exists() for suffix in ('.log', '.source-binary.json', '.result.json'))
    receipt_bytes = (ARTIFACTS / 'domain-native-compile.json').read_bytes()
    receipt = json.loads(receipt_bytes)
    wrapper_receipt_path = ARTIFACTS / 'domain-native-runner-corrected.receipt.json'
    wrapper_receipt = json.loads(wrapper_receipt_path.read_bytes())
    assert digest(receipt_bytes) == wrapper_receipt['original_compile_receipt_sha256']
    assert wrapper_receipt['source'] == SOURCE
    assert wrapper_receipt['binary_sha256'] == receipt['binary_sha256']
    assert wrapper_receipt['original_runner_sha256'] == receipt['runner_sha256']
    hashes, dependencies = prepare.frozen_source()
    assert receipt['source'] == SOURCE and receipt['exit_code'] == 0
    assert hashes == receipt['backend_source_sha256'] and dependencies == receipt['dependency_files_sha256']
    assert digest(BINARY.read_bytes()) == receipt['binary_sha256']
    assert digest(Path(__file__).read_bytes()) == wrapper_receipt['corrected_runner_sha256']
    assert digest(Path(prepare.__file__).read_bytes()) == receipt['preparer_sha256']
    assert TMP.is_dir() and not TMP.is_symlink() and TMP.stat().st_uid == os.getuid()
    assert TMP.stat().st_mode & 0o777 == 0o700 and not list(TMP.iterdir())
    before, prior, prior_processes = host(), inventory(), processes()
    assert not any(prior.values()) and not prior_processes, (prior, prior_processes)
    task = Path(tempfile.mkdtemp(prefix='n-' + engine[:3] + '-', dir=TMP))
    assert task.stat().st_mode & 0o777 == 0o700
    env = os.environ.copy()
    for key in tuple(env):
        if key.startswith('JD_DNS_SERVICES_'):
            del env[key]
    env.update(JD_DNS_SERVICES_LIVE_ENGINE=engine, TMPDIR=str(task), GOMAXPROCS='2', GOFLAGS='-p=2', GOTOOLCHAIN='go1.26.8')
    argv = ['timeout', '--kill-after=5s', '240s', str(BINARY), '-test.run', '^TestDNSServiceNativeOwnedEngine$', '-test.count=1', '-test.timeout=235s', '-test.v']
    manifest = {
        'source': SOURCE, 'binary_sha256': receipt['binary_sha256'],
        'runner_sha256': wrapper_receipt['corrected_runner_sha256'],
        'wrapper_receipt_sha256': digest(wrapper_receipt_path.read_bytes()),
        'compiler_receipt_sha256': digest((ARTIFACTS / 'domain-native-compile.json').read_bytes()),
        'backend_source_sha256': hashes, 'dependency_files_sha256': dependencies,
        'worktree_clean_and_source_matches_git_before': True,
        'argv': argv, 'cwd': str(ROOT / 'backend/internal/dnsservice'),
        'environment': {k: env[k] for k in ['JD_DNS_SERVICES_LIVE_ENGINE', 'TMPDIR', 'GOMAXPROCS', 'GOFLAGS', 'GOTOOLCHAIN']},
        'host_before': before, 'owned_docker_before': prior, 'owned_processes_before': prior_processes,
    }
    prefix.with_suffix('.source-binary.json').write_text(json.dumps(manifest, indent=2) + '\n')
    started = time.monotonic()
    with prefix.with_suffix('.log').open('xb') as log:
        completed = subprocess.run(argv, cwd=ROOT / 'backend/internal/dnsservice', env=env,
                                   stdout=log, stderr=subprocess.STDOUT)
    wall = time.monotonic() - started
    raw = prefix.with_suffix('.log').read_bytes()
    after, owned, remaining_processes = host(), inventory(), processes()
    remaining = [p.name for p in task.iterdir()]
    if not remaining:
        task.rmdir()
    unchanged_source = prepare.frozen_source() == (hashes, dependencies)
    result = {
        'source': SOURCE, 'engine': engine, 'exit_code': completed.returncode,
        'wall_seconds': round(wall, 3), 'raw_sha256': digest(raw),
        'skipped': b'--- SKIP:' in raw, 'terminal_pass': b'--- PASS: TestDNSServiceNativeOwnedEngine' in raw,
        'owned_processes_remaining': remaining_processes, 'owned_docker_remaining': owned,
        'owned_tmp_entries_remaining': remaining, 'owned_tmp_directory_removed': not task.exists(),
        'host_after': after, 'host_unchanged': before == after,
        'source_matches_compile_and_git_after': unchanged_source,
    }
    prefix.with_suffix('.result.json').write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps(result, indent=2), flush=True)
    print(raw.decode(errors='replace')[-7000:], flush=True)
    if (completed.returncode or remaining_processes or any(owned.values()) or remaining
            or before != after or result['skipped'] or not result['terminal_pass'] or not unchanged_source):
        raise SystemExit(1)


if __name__ == '__main__':
    main()
