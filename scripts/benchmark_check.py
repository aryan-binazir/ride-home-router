"""Record an uncached check, load samples, and every Go test event.

Usage: python3 scripts/benchmark_check.py OUTPUT_DIRECTORY [make -k check]
Run the same script against each checkout; compare events.jsonl by (Package, Test).
"""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import threading
import time


def main():
    output = Path(sys.argv[1]).resolve()
    output.mkdir(parents=True, exist_ok=False)
    real_go = shutil.which('go')
    wrapper = output / 'go'
    wrapper.write_text('''#!/usr/bin/env python3
import json, os, subprocess, sys, time
args = sys.argv[1:]
if args and args[0] == 'test' and '-json' not in args:
    args.insert(1, '-json')
start = time.monotonic()
status = subprocess.call([os.environ['BENCH_REAL_GO'], *args])
with open(os.environ['BENCH_GO_TIMES'], 'a') as f:
    f.write(json.dumps({'args': args, 'seconds': time.monotonic()-start, 'status': status})+'\\n')
sys.exit(status)
''')
    wrapper.chmod(0o755)
    env = dict(os.environ, BENCH_REAL_GO=real_go, BENCH_GO_TIMES=str(output / 'go-times.jsonl'))
    env['PATH'] = str(output) + os.pathsep + env['PATH']
    stop = threading.Event()
    def sample():
        with (output / 'load.jsonl').open('w') as stream:
            while not stop.is_set():
                stream.write(json.dumps({'time': time.time(), 'load': os.getloadavg()})+'\n')
                stream.flush()
                stop.wait(5)
    thread = threading.Thread(target=sample)
    thread.start()
    start = time.monotonic()
    try:
        with (output / 'output.log').open('w') as log, (output / 'events.jsonl').open('w') as events:
            process = subprocess.Popen(sys.argv[2:] or ['make', '-k', 'check'], env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
            for line in process.stdout:
                log.write(line)
                log.flush()
                if line.startswith('{'):
                    try:
                        event = json.loads(line)
                        if 'Action' in event:
                            events.write(line)
                    except ValueError:
                        pass
            status = process.wait()
        (output / 'result.json').write_text(json.dumps({'seconds': time.monotonic()-start, 'status': status}, indent=2)+'\n')
    finally:
        stop.set()
        thread.join()
    return status


if __name__ == '__main__':
    sys.exit(main())
