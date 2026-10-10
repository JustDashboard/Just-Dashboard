import hashlib
import json
import os
from pathlib import Path
import subprocess
import time

ROOT = Path('/home/ubuntu/Just-Dashboard-network-dns-domain-filter-final')
ARTIFACTS = Path('/home/ubuntu/Just-Dashboard-network-dns-domain-filter-final-artifacts')
TMP = Path('/home/ubuntu/jd-dff-t')
SOURCE = 'b7038998c668fc915404b51a0b2236e68fe148fa'
BINARY = ARTIFACTS / 'dns-domain-filter-native.test'


def digest(data):
    return hashlib.sha256(data).hexdigest()


def git(*args):
    return subprocess.check_output(['git', *args], cwd=ROOT)


def frozen_source():
    assert git('rev-parse', 'HEAD').decode().strip() == SOURCE
    assert git('status', '--porcelain', '--untracked-files=all') == b''
    paths = sorted(p.decode() for p in git('ls-files', '-z', '--', 'backend').split(b'\0') if p)
    go_paths = [p for p in paths if p.endswith('.go')]
    assert len(go_paths) == 1863
    dependencies = ['backend/go.mod', 'backend/go.sum']
    paths = go_paths + dependencies
    actual_go = sorted(str(p.relative_to(ROOT)) for p in (ROOT / 'backend').rglob('*.go'))
    assert actual_go == go_paths
    batch = subprocess.run(['git', 'cat-file', '--batch'], cwd=ROOT,
                           input=''.join(f'{SOURCE}:{p}\n' for p in paths).encode(),
                           stdout=subprocess.PIPE, check=True).stdout
    hashes = {}
    offset = 0
    for name in paths:
        end = batch.index(b'\n', offset)
        header = batch[offset:end].split()
        assert len(header) == 3 and header[1] == b'blob', (name, header)
        length = int(header[2])
        data = batch[end + 1:end + 1 + length]
        assert len(data) == length and batch[end + 1 + length:end + 2 + length] == b'\n'
        offset = end + 2 + length
        path = ROOT / name
        assert not path.is_symlink() and path.is_file()
        hashes[name] = digest(path.read_bytes())
        assert hashes[name] == digest(data), name
    assert offset == len(batch)
    return ({p: hashes[p] for p in go_paths}, {p: hashes[p] for p in dependencies})


def main():
    assert TMP.is_dir() and not TMP.is_symlink()
    assert TMP.stat().st_uid == os.getuid() and TMP.stat().st_mode & 0o777 == 0o700
    assert not list(TMP.iterdir())
    assert not BINARY.exists()
    go_hashes, dependency_hashes = frozen_source()
    env = os.environ.copy()
    for key in tuple(env):
        if key.startswith('JD_DNS_SERVICES_'):
            del env[key]
    env.update(TMPDIR=str(TMP), GOMAXPROCS='2', GOFLAGS='-p=2', GOTOOLCHAIN='go1.26.8')
    version = subprocess.check_output(['go', 'version'], cwd=ROOT / 'backend', env=env).decode().strip()
    assert version == 'go version go1.26.8 linux/amd64', version
    go_environment = json.loads(subprocess.check_output(
        ['go', 'env', '-json', 'CGO_ENABLED', 'GOARCH', 'GOOS', 'GOVERSION', 'GOTOOLCHAIN'],
        cwd=ROOT / 'backend', env=env))
    assert go_environment['CGO_ENABLED'] == '1'
    argv = ['go', 'test', '-race', '-c', '-o', str(BINARY), './internal/dnsservice']
    log_path = ARTIFACTS / 'domain-native-compile.log'
    started = time.monotonic()
    with log_path.open('xb') as log:
        completed = subprocess.run(argv, cwd=ROOT / 'backend', env=env,
                                   stdout=log, stderr=subprocess.STDOUT, timeout=240)
    elapsed = time.monotonic() - started
    assert frozen_source() == (go_hashes, dependency_hashes)
    build_info = subprocess.check_output(['go', 'version', '-m', str(BINARY)],
                                        cwd=ROOT / 'backend', env=env).decode() if completed.returncode == 0 else None
    if completed.returncode == 0:
        assert 'go1.26.8' in build_info and '-race=true' in build_info and 'CGO_ENABLED=1' in build_info
    receipt = {
        'source': SOURCE, 'cwd': str(ROOT / 'backend'), 'argv': argv,
        'environment': {k: env[k] for k in ['TMPDIR', 'GOMAXPROCS', 'GOFLAGS', 'GOTOOLCHAIN']},
        'go_version': version, 'go_environment': go_environment,
        'exit_code': completed.returncode, 'wall_seconds': round(elapsed, 3),
        'backend_source_sha256': go_hashes, 'dependency_files_sha256': dependency_hashes,
        'source_matches_git_before_and_after': True, 'worktree_clean_before_and_after': True,
        'binary': str(BINARY), 'binary_sha256': digest(BINARY.read_bytes()) if BINARY.exists() else None,
        'compile_log_sha256': digest(log_path.read_bytes()), 'go_version_m': build_info,
        'preparer_sha256': digest(Path(__file__).read_bytes()),
        'runner_sha256': digest((ARTIFACTS / 'domain-native-runner-final.py').read_bytes()),
        'tmp_entries_remaining': [p.name for p in TMP.iterdir()],
    }
    (ARTIFACTS / 'domain-native-compile.json').write_text(json.dumps(receipt, indent=2) + '\n')
    print(json.dumps({k: receipt[k] for k in ['source', 'exit_code', 'wall_seconds', 'binary_sha256', 'compile_log_sha256', 'tmp_entries_remaining']}, indent=2), flush=True)
    assert not receipt['tmp_entries_remaining']
    raise SystemExit(completed.returncode)


if __name__ == '__main__':
    main()
