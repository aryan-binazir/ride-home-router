#!/usr/bin/env python3
"""Run fresh TLC checks. Manifest refresh requires review of model/source fidelity."""
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
DIR = ROOT / 'verification/tla'
MANIFEST = DIR / 'checks.json'
SHA = '936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88'
HEADERS = set('CONSTANT CONSTANTS INIT NEXT SPECIFICATION INVARIANT INVARIANTS PROPERTY PROPERTIES CONSTRAINT CONSTRAINTS ACTION_CONSTRAINT ACTION_CONSTRAINTS VIEW SYMMETRY CHECK_DEADLOCK ALIAS POSTCONDITION'.split())


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def describe(path):
    raw = path.read_text()
    clean = re.sub(r'\(\*.*?\*\)', '', raw, flags=re.S)
    clean = re.sub(r'\\\*[^\n]*', '', clean)
    tokens = re.findall(r'[A-Za-z_][A-Za-z_0-9]*|\S', clean)
    properties = {'invariant': [], 'property': []}
    section = None
    for token in tokens:
        if token in HEADERS:
            section = {'INVARIANT': 'invariant', 'INVARIANTS': 'invariant', 'PROPERTY': 'property', 'PROPERTIES': 'property'}.get(token)
        elif section:
            if not re.fullmatch(r'[A-Za-z_][A-Za-z_0-9]*', token):
                raise ValueError(f'{path}: unsupported property expression')
            properties[section].append(token)
    if not any(properties.values()):
        raise ValueError(f'{path}: no properties')
    expects = re.findall(r'^\s*\\\* expect: (?:(invariant|property) )?(\w+)\s*$', raw, re.M)
    mutation = path.parent.name == 'mutations'
    if len(expects) != int(mutation):
        raise ValueError(f'{path}: expected exactly {int(mutation)} expectation comments')
    expected = {'kind': expects[0][0] or 'invariant', 'name': expects[0][1]} if mutation else None
    if expected and (expected['name'] not in properties[expected['kind']] or (expected['kind'] == 'property' and (properties['property'] != [expected['name']] or properties['invariant']))):
        raise ValueError(f'{path}: negative check must isolate expected property')
    return {'sha256': digest(path), 'spec': path.name.split('.')[0] + '.tla', 'properties': properties, 'expected': expected}


def inventory():
    return {str(p.relative_to(DIR)): describe(p) for p in sorted(DIR.rglob('*.cfg'))}


def classify(code, output, expected):
    errors = re.findall(r'^Error: (.*)$', output, re.M)
    finished = bool(re.search(r'^Finished in .+', output, re.M))
    counts = re.findall(r'([\d,]+) states generated, ([\d,]+) distinct states found, ([\d,]+) states left on queue\.', output)
    if not finished or not counts or int(counts[-1][1].replace(',', '')) <= 0:
        return False
    if expected is None:
        return code == 0 and not errors and int(counts[-1][2].replace(',', '')) == 0 and output.count('Model checking completed. No error has been found.') == 1
    if expected['kind'] == 'invariant':
        return code == 12 and errors == [f"Invariant {expected['name']} is violated.", 'The behavior up to this point is:']
    return code == 13 and errors == ['Temporal properties were violated.', 'The following behavior constitutes a counter-example:']


def git(*args):
    return subprocess.check_output(['git', '-C', str(ROOT), *args], text=True).strip()


def main():
    if sys.argv[1:] == ['--refresh-manifest']:
        checks = inventory()
        if not checks:
            raise ValueError('empty inventory')
        MANIFEST.write_text(json.dumps({'review': 'Refresh only after reviewing model/source fidelity, guards, bounds and property coverage. Receipts are never cached proof.', 'checks': checks}, indent=2) + '\n')
        print(f'refreshed {len(checks)} configs; source fidelity review required')
        return 0
    if len(sys.argv) > 2:
        raise ValueError('usage: tla.sh [name-filter | --refresh-manifest]')
    selected = sys.argv[1] if len(sys.argv) == 2 else ''
    checks = inventory()
    if not checks or checks != json.loads(MANIFEST.read_text())['checks']:
        raise ValueError('config inventory/content differs from checks.json; review fidelity before refreshing manifest')
    jar = Path(os.environ.get('TLA2TOOLS', str(Path(os.environ.get('XDG_CACHE_HOME', Path.home() / '.cache')) / 'ride-home-router/tla2tools-1.7.4.jar')))
    if not jar.exists():
        jar.parent.mkdir(parents=True, exist_ok=True)
        with tempfile.NamedTemporaryFile(dir=jar.parent, delete=False) as tmp:
            download = Path(tmp.name)
        try:
            with urllib.request.urlopen('https://github.com/tlaplus/tlaplus/releases/download/v1.7.4/tla2tools.jar', timeout=30) as response, download.open('wb') as target:
                shutil.copyfileobj(response, target)
            if digest(download) != SHA:
                raise ValueError('download checksum mismatch')
            download.replace(jar)
        finally:
            download.unlink(missing_ok=True)
    if digest(jar) != SHA:
        raise ValueError('jar checksum mismatch')
    java = os.environ.get('JAVA')
    if not java and shutil.which('java'):
        if subprocess.run(['java', '-version'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=10).returncode == 0:
            java = shutil.which('java')
    if not java:
        java = subprocess.check_output(['mise', 'which', 'java', '--tool', 'java@temurin-25'], text=True).strip()
    workers = int(os.environ.get('TLA_WORKERS', '2'))
    heap = int(os.environ.get('TLA_HEAP_MB', '2048'))
    timeout = float(os.environ.get('TLA_TIMEOUT_SECONDS', '300'))
    if not (1 <= workers <= 4 and 128 <= heap <= 8192 and 0 < timeout <= 3600):
        raise ValueError('limits: workers 1..4, heap 128..8192 MB, timeout >0..3600 seconds')
    names = [n for n in checks if selected in Path(n).name]
    if not names:
        raise ValueError('no matching configs')
    logs = ROOT / '_scratch/tla'
    logs.mkdir(parents=True, exist_ok=True)
    run = Path(tempfile.mkdtemp(prefix='run-', dir=logs))
    receipt = {'head': git('rev-parse', 'HEAD'), 'dirty': git('status', '--porcelain'), 'partial': bool(selected), 'jar_sha256': SHA, 'java': str(Path(shutil.which(java) or java).resolve()), 'java_sha256': digest(Path(shutil.which(java) or java).resolve()), 'inputs': {str(p.relative_to(ROOT)): digest(p) for p in [*DIR.rglob('*.tla'), *DIR.rglob('*.cfg'), MANIFEST, Path(__file__), ROOT / 'scripts/tla.sh']}, 'checks': []}
    print(f'{"PARTIAL developer run" if selected else "FULL gate"}; logs: {run}', flush=True)
    try:
        for index, name in enumerate(names):
            log = run / (name.replace('/', '__') + '.log')
            command = [java, f'-Xmx{heap}m', '-XX:+UseParallelGC', f'-Djava.io.tmpdir={run}', '-cp', str(jar.resolve()), 'tlc2.TLC', '-workers', str(workers), '-cleanup', '-metadir', str(run / f'states-{index}'), '-config', name, checks[name]['spec']]
            start = time.monotonic()
            with log.open('w') as stream:
                process = subprocess.Popen(command, cwd=DIR, stdout=stream, stderr=subprocess.STDOUT, start_new_session=True)
                try:
                    code = process.wait(timeout=timeout)
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait()
                    code = -signal.SIGKILL
                except KeyboardInterrupt:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait()
                    raise
            ok = classify(code, log.read_text(), checks[name]['expected'])
            receipt['checks'].append({'config': name, 'exit': code, 'ok': ok, 'seconds': round(time.monotonic() - start, 3), 'log': str(log)})
            print(f'{"ok" if ok else "FAIL"} {name} (exit {code})', flush=True)
            if not ok:
                print('\n'.join(log.read_text().splitlines()[-15:]))
    finally:
        receipt['inputs_unchanged'] = (
            all((ROOT / name).is_file() and digest(ROOT / name) == sha for name, sha in receipt['inputs'].items())
            and inventory() == checks and git('rev-parse', 'HEAD') == receipt['head']
        )
        (run / 'receipt.json').write_text(json.dumps(receipt, indent=2) + '\n')
    return int(not receipt['inputs_unchanged'] or len(receipt['checks']) != len(names) or not all(c['ok'] for c in receipt['checks']))


if __name__ == '__main__':
    try:
        sys.exit(main())
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        print(f'tla gate: {error}', file=sys.stderr)
        sys.exit(1)
