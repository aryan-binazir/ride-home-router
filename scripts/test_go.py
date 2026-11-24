"""Run the race suite with four isolated handler processes and merged coverage."""
from concurrent.futures import ThreadPoolExecutor
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time

HANDLERS = 'github.com/aryan-binazir/ride-home-router/internal/handlers'


def merge_coverage(paths, destination):
    blocks = {}
    for path in paths:
        lines = path.read_text().splitlines()
        if not lines or lines[0] != 'mode: atomic':
            raise ValueError(f'invalid atomic coverage: {path}')
        for line in lines[1:]:
            block, count = line.rsplit(' ', 1)
            blocks[block] = blocks.get(block, 0) + int(count)
    destination.write_text('mode: atomic\n' + ''.join(f'{block} {count}\n' for block, count in sorted(blocks.items())))


def run(command, log, cwd=None, deadline=None):
    with log.open('w') as stream:
        if deadline is not None:
            remaining_ns = int((deadline - time.monotonic()) * 1_000_000_000)
            if remaining_ns <= 0:
                stream.write('Handler package exceeded its 10m test timeout before this batch started\n')
                return 1
            command = [*command, f'-test.timeout={remaining_ns}ns']
        return subprocess.call(command, cwd=cwd, stdout=stream, stderr=subprocess.STDOUT)


def main():
    if not os.environ.get('TEST_DATABASE_URL', '').strip():
        raise ValueError('TEST_DATABASE_URL is required; database tests must run')
    start = time.monotonic()
    Path('coverage.out').write_text('mode: atomic\n')
    packages = subprocess.check_output(['go', 'list', '-race', './...'], text=True).splitlines()
    with tempfile.TemporaryDirectory(prefix='rhr-go-tests-') as temporary:
        directory = Path(temporary)
        binary = directory / 'handlers.test'
        subprocess.run(['go', 'test', '-c', '-race', '-cover', '-o', str(binary), HANDLERS], check=True)
        listing_coverage = directory / 'listing-coverage'
        listing_coverage.mkdir()
        listed = subprocess.check_output([str(binary), '-test.list', '.'], cwd='internal/handlers',
                                         env=dict(os.environ, GOCOVERDIR=str(listing_coverage)), text=True)
        names = [line for line in listed.splitlines() if re.fullmatch(r'(Test|Example|Fuzz)\w*', line)]
        if not names or HANDLERS not in packages:
            raise ValueError('handler test discovery returned no tests or package')
        jobs = []
        for index in range(16):
            selected = names[index::16]
            if selected:
                jobs.append((['go', 'tool', 'test2json', '-t', '-p', HANDLERS, str(binary),
                             '-test.v=test2json', '-test.count=1', '-test.paniconexit0',
                             f'-test.coverprofile={directory / str(len(jobs))}',
                             '-test.run=^(' + '|'.join(selected) + ')$'], set(selected)))
        other = len(jobs)
        other_command = ['go', 'test', '-race', '-count=1', '-json', '-p', '8',
                         f'-coverprofile={directory / str(other)}', *[p for p in packages if p != HANDLERS]]
        with ThreadPoolExecutor(max_workers=1) as rest:
            other_future = rest.submit(run, other_command, directory / f'{other}.log')
            deadline = time.monotonic() + 600
            with ThreadPoolExecutor(max_workers=4) as pool:
                futures = [pool.submit(run, command, directory / f'{index}.log', 'internal/handlers', deadline)
                           for index, (command, _) in enumerate(jobs)]
        jobs.append((other_command, None))
        futures.append(other_future)
        failed = False
        for index, (_, expected) in enumerate(jobs):
            passed = set()
            for line in (directory / f'{index}.log').read_text().splitlines():
                print(line, flush=True)
                try:
                    event = json.loads(line)
                except ValueError:
                    continue
                name = event.get('Test')
                if name and event.get('Action') == 'pass' and '/' not in name:
                    passed.add(name)
                if name and event.get('Action') == 'skip' and not (
                    event.get('Package') == 'github.com/aryan-binazir/ride-home-router/internal/routing/planeval' and name == 'TestPlannerEvaluation'
                ):
                    print(f'Unexpected skipped test: {event.get("Package")}/{name}', file=sys.stderr)
                    failed = True
            if expected is not None and passed != expected:
                print(f'Handler shard {index}: missing={sorted(expected-passed)}, unexpected={sorted(passed-expected)}', file=sys.stderr)
                failed = True
            failed |= futures[index].result() != 0
        profiles = [directory / str(index) for index in range(len(jobs))]
        present = [path for path in profiles if path.exists()]
        merge_coverage(present, Path('coverage.out'))
        if len(present) != len(profiles):
            print('Missing shard coverage profile', file=sys.stderr)
            failed = True
        print(f'Go race phase: {time.monotonic()-start:.3f}s', flush=True)
        return int(failed)


if __name__ == '__main__':
    sys.exit(main())
