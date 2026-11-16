#!/usr/bin/env python3
import argparse
import json
import os
from pathlib import Path
import signal
import subprocess
import time


def memory(pid):
    processes = {}
    for entry in Path('/proc').iterdir():
        if not entry.name.isdigit():
            continue
        try:
            fields = (entry / 'stat').read_text().rsplit(')', 1)[1].split()
            processes[int(entry.name)] = (int(fields[1]), int(fields[21]) * os.sysconf('SC_PAGE_SIZE') // 1024,
                                         (entry / 'comm').read_text().strip())
        except (OSError, ValueError, IndexError):
            continue
    owned = {pid}
    while True:
        children = {child for child, (parent, _, _) in processes.items() if parent in owned}
        if children <= owned:
            break
        owned |= children
    return (sum(rss for child, (_, rss, _) in processes.items() if child in owned),
            sum(rss for child, (_, rss, name) in processes.items() if child in owned and name == 'java'))


def main():
    parser = argparse.ArgumentParser(description='Record command wall time, host load and sampled Linux process-tree RSS.')
    parser.add_argument('--output', type=Path, required=True, help='new evidence directory')
    parser.add_argument('command', nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command[1:] if args.command[:1] == ['--'] else args.command
    if not command:
        parser.error('a command is required after --')
    args.output.mkdir(parents=True)
    receipt = {'command': command, 'head': subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip(),
               'load_start': os.getloadavg(), 'started': time.time(), 'exit': None,
               'limits': {key: value for key, value in os.environ.items()
                          if key.startswith('TLA_') or key in ('VERIFICATION_JOBS', 'MAKEFLAGS')},
               'peak_rss_kib': 0, 'peak_java_rss_kib': 0, 'samples': []}
    path = args.output / 'timing.json'
    path.write_text(json.dumps(receipt, indent=2) + '\n')
    start = time.monotonic()
    process = None
    try:
        with (args.output / 'output.log').open('w') as output:
            process = subprocess.Popen(command, stdout=output, stderr=subprocess.STDOUT, start_new_session=True)
            while process.poll() is None:
                rss, java = memory(process.pid)
                receipt['peak_rss_kib'] = max(receipt['peak_rss_kib'], rss)
                receipt['peak_java_rss_kib'] = max(receipt['peak_java_rss_kib'], java)
                receipt['samples'].append([round(time.monotonic() - start, 3), *os.getloadavg(), rss, java])
                time.sleep(0.5)
            receipt['exit'] = process.returncode
    finally:
        if process is not None and process.poll() is None:
            os.killpg(process.pid, signal.SIGTERM)
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait()
        receipt['seconds'] = round(time.monotonic() - start, 3)
        receipt['load_end'] = os.getloadavg()
        path.write_text(json.dumps(receipt, indent=2) + '\n')
    print(json.dumps({key: value for key, value in receipt.items() if key != 'samples'}, indent=2))
    return receipt['exit'] if receipt['exit'] is not None else 1


if __name__ == '__main__':
    raise SystemExit(main())
