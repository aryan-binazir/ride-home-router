#!/usr/bin/env python3
"""Own one local development environment per canonical worktree path."""
import base64
import fcntl
import hashlib
import json
import os
from pathlib import Path
import secrets
import shutil
import signal
import subprocess
import sys
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
KEY = hashlib.sha256(str(ROOT).encode()).hexdigest()[:20]
STATE = Path(os.environ.get('XDG_STATE_HOME', Path.home() / '.local/state')) / 'ride-home-router' / KEY


def save(name, value):
    path = STATE / name
    temp = path.with_suffix('.tmp')
    temp.write_text(json.dumps(value, indent=2) + '\n')
    temp.chmod(0o600)
    temp.replace(path)


def read(name):
    path = STATE / name
    return json.loads(path.read_text()) if path.exists() else None


def run(args, **kwargs):
    return subprocess.run(args, check=True, text=True, **kwargs)


def process_start(pid):
    try:
        fields = Path(f'/proc/{pid}/stat').read_text().split(') ', 1)[1].split()
        return None if fields[0] == 'Z' else fields[19]
    except (FileNotFoundError, ProcessLookupError):
        return None


def alive(state):
    return bool(state.get('pid') and state.get('start') and process_start(state['pid']) == state['start'])


def container(state, *args, capture=False):
    return run([state['runtime'], *args], capture_output=capture)


def inspect(state):
    result = subprocess.run([state['runtime'], 'inspect', state['container']], text=True, capture_output=True)
    if result.returncode:
        if 'no such' in result.stderr.lower() or 'does not exist' in result.stderr.lower():
            return None
        raise RuntimeError(result.stderr.strip())
    info = json.loads(result.stdout)[0]
    if info['Config'].get('Labels', {}).get('rhr.dev.owner') != state['owner']:
        raise RuntimeError('Container ownership mismatch; refusing operation')
    return info


def stop(state):
    if alive(state):
        os.kill(state['pid'], signal.SIGTERM)
        deadline = time.monotonic() + 30
        while alive(state) and time.monotonic() < deadline:
            time.sleep(.1)
        if alive(state):
            raise RuntimeError('Dev runner did not stop; refusing to remove its database')
    info = inspect(state)
    if info and info['State']['Running']:
        container(state, 'stop', state['container'], capture=True)
    state.pop('pid', None)
    state.pop('start', None)
    save('state.json', state)
    (STATE / 'ready.json').unlink(missing_ok=True)


def status(state):
    ready = read('ready.json') if alive(state) else None
    print('Running' if ready else 'Stopped')
    if ready:
        print('URL:', ready['login'])
        print('Identities:', ready['login'] + '&choose=1', '(admin / member / denied)')
    print('Synthetic data and travel estimates only.')
    print('State:', STATE)
    print('Logs:', STATE / 'dev.log')
    print('Cleanup: make dev-stop (preserve data); make dev-reset (recreate owned data)')


def start(state):
    if alive(state):
        if read('ready.json'):
            status(state)
            return
        raise RuntimeError('Runner exists without readiness; run make dev-stop before retrying')
    (STATE / 'ready.json').unlink(missing_ok=True)
    build_env = dict(os.environ)
    build_env['GOTMPDIR'] = str(STATE / 'gotmp')
    (STATE / 'gotmp').mkdir(exist_ok=True)
    run(['go', 'build', '-o', str(STATE / 'runner.new'), './tools/dev'], cwd=ROOT, env=build_env)
    (STATE / 'runner.new').replace(STATE / 'runner')
    info = inspect(state)
    if not info:
        container(state, 'run', '-d', '--name', state['container'], '--label', 'rhr.dev.owner=' + state['owner'],
                  '-p', '127.0.0.1::5432', '-e', 'POSTGRES_PASSWORD=' + state['password'],
                  '-e', 'POSTGRES_DB=ride_home_router', 'docker.io/library/postgres:18', capture=True)
    elif not info['State']['Running']:
        container(state, 'start', state['container'], capture=True)
    for _ in range(60):
        result = subprocess.run([state['runtime'], 'exec', state['container'], 'pg_isready', '-U', 'postgres'], capture_output=True)
        if result.returncode == 0:
            break
        time.sleep(.5)
    else:
        raise RuntimeError('Owned Postgres did not become ready')
    ports = container(state, 'port', state['container'], '5432/tcp', capture=True).stdout.strip()
    if not ports.startswith('127.0.0.1:') or '\n' in ports:
        raise RuntimeError('Postgres is not exclusively loopback-bound')
    config = {'database_url': 'postgres://postgres:' + state['password'] + '@' + ports + '/ride_home_router?sslmode=disable',
              'encryption_key': state['encryption_key'], 'port': state.get('port', 0), 'cookie': 'rhr_dev_' + KEY, 'capability': state['owner']}
    save('config.json', config)
    with (STATE / 'dev.log').open('a') as log:
        proc = subprocess.Popen([str(STATE / 'runner'), '--state', str(STATE)], cwd=ROOT, stdout=log, stderr=log,
                                start_new_session=True, env={'PATH': os.environ['PATH'], 'HOME': str(Path.home())})
    state['pid'] = proc.pid
    state['start'] = process_start(proc.pid)
    save('state.json', state)
    for _ in range(120):
        if proc.poll() is not None:
            raise RuntimeError('Dev runner exited; see ' + str(STATE / 'dev.log'))
        ready = read('ready.json')
        if ready:
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            with opener.open(ready['url'] + '/healthz', timeout=5) as response:
                if response.status != 200:
                    raise RuntimeError('Application is not ready')
            state['port'] = int(ready['url'].rsplit(':', 1)[1])
            save('state.json', state)
            status(state)
            return
        time.sleep(.5)
    raise RuntimeError('Dev startup timed out; see ' + str(STATE / 'dev.log'))


def main():
    action = sys.argv[1] if len(sys.argv) == 2 else ''
    if action not in ('start', 'status', 'stop', 'reset'):
        raise RuntimeError('Expected start, status, stop, or reset')
    os.umask(0o077)
    STATE.mkdir(parents=True, exist_ok=True, mode=0o700)
    with (STATE / 'lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        state = read('state.json')
        if state is None:
            if action in ('status', 'stop'):
                print('No environment for this worktree')
                return
            runtime = os.environ.get('DEV_RUNTIME', 'podman')
            if not shutil.which(runtime):
                raise RuntimeError('Install Podman or set DEV_RUNTIME to a compatible runtime')
            owner = secrets.token_hex(16)
            state = {'root': str(ROOT), 'owner': owner, 'runtime': runtime, 'container': 'rhr-dev-' + owner,
                     'password': secrets.token_urlsafe(32), 'encryption_key': base64.b64encode(secrets.token_bytes(32)).decode()}
            save('state.json', state)
        if state['root'] != str(ROOT):
            raise RuntimeError('State ownership mismatch')
        if action == 'status':
            status(state)
        elif action == 'stop':
            stop(state)
            status(state)
        else:
            if action == 'reset':
                stop(state)
                if inspect(state):
                    container(state, 'rm', '-v', state['container'], capture=True)
            try:
                start(state)
            except Exception:
                stop(state)
                raise


if __name__ == '__main__':
    try:
        main()
    except (RuntimeError, subprocess.CalledProcessError, OSError) as error:
        print('dev:', error, file=sys.stderr)
        sys.exit(1)
