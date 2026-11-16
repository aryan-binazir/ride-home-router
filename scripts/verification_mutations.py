#!/usr/bin/env python3
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time
from verification_context import Snapshot, SignalInterrupted, concurrent_checks, install_signal_handlers, run_command


ROOT = Path(__file__).resolve().parents[1]


def run_tests(case, output, overlay=None, cancelled=None):
    command = ["go", "test", "-json", "-race", "-count=1", "-timeout=90s"]
    if overlay:
        command.append("-overlay=" + str(overlay))
    command += [case["package"], "-run", case["run"]]
    with output.open("w") as log:
        try:
            status = run_command(command, cwd=ROOT, stdout=log, timeout=120, cancelled=cancelled)
        except subprocess.TimeoutExpired:
            raise ValueError(f"test command timed out; see {output}") from None
    text = output.read_text()
    if not text.endswith('\n'):
        raise ValueError(f"missing or truncated test output; see {output}")
    try:
        events = [json.loads(line) for line in text.splitlines()]
    except json.JSONDecodeError:
        raise ValueError(f"invalid test output; see {output}") from None
    if not all(isinstance(event, dict) and isinstance(event.get('Action'), str)
               and all(isinstance(event[key], str) for key in ('Package', 'Test', 'Output') if key in event)
               for event in events):
        raise ValueError(f"invalid test events; see {output}")
    return status, events


def passed(case, status, events):
    return (status == 0 and completed(events, 'pass')
            and any(e.get("Action") == "pass" and e.get("Test") == case["test"] for e in events)
            and not any(e.get('Action') in ('fail', 'skip', 'build-fail') for e in events))


def completed(events, action):
    terminals = [e for e in events if e.get('Action') in ('pass', 'fail', 'skip') and not e.get('Test') and e.get('Package')]
    started = {(e.get('Package'), e['Test']) for e in events if e.get('Action') == 'run' and e.get('Test')}
    ended = {(e.get('Package'), e['Test']) for e in events if e.get('Action') in ('pass', 'fail', 'skip') and e.get('Test')}
    return len(terminals) == 1 and terminals[0] is events[-1] and events[-1].get('Action') == action and started <= ended


def killed(case, status, events):
    return (
        status == 1
        and completed(events, 'fail')
        and any(e.get("Action") == "fail" and e.get("Test") == case["test"] for e in events)
        and any(e.get("Test") == case["test"] and case["diagnostic"] in e.get("Output", "") for e in events)
        and not any(e.get("Action") in ('build-fail', 'skip') for e in events)
        and not any(e.get('Action') == 'fail' and e.get('Test') and not (case['test'] == e['Test'] or case['test'].startswith(e['Test'] + '/')) for e in events)
        and not any(re.search(r'panic:|fatal error:|signal:|\bSIG[A-Z]+:|out of memory|WARNING: DATA RACE', e.get('Output', '')) for e in events)
    )


def main():
    parser = argparse.ArgumentParser(description="Kill real-code guard mutations through compiler overlays; leave source untouched.")
    parser.add_argument("filter", nargs="?", default="", help="developer subset; omit for the complete gate")
    args = parser.parse_args()
    install_signal_handlers()
    inventory = ROOT / "verification/code-mutations.json"
    snapshot = Snapshot(ROOT, partial=bool(args.filter), extra=[inventory, Path(__file__)])
    cases = json.loads(inventory.read_text())
    if not cases or len({c["name"] for c in cases}) != len(cases):
        raise ValueError("mutation inventory must be nonempty with unique names")
    selected = [c for c in cases if args.filter in c["name"]]
    if not selected:
        raise ValueError("no matching mutations")
    if not os.environ.get("TEST_DATABASE_URL"):
        raise ValueError("TEST_DATABASE_URL is required; skipped database tests cannot establish mutation coverage")
    jobs = int(os.environ.get('VERIFICATION_JOBS', '2'))
    if not 1 <= jobs <= 4:
        raise ValueError('limits: VERIFICATION_JOBS 1..4')
    scratch = ROOT / "_scratch/verification-mutations"
    scratch.mkdir(parents=True, exist_ok=True)
    work = Path(tempfile.mkdtemp(prefix="run-", dir=scratch))
    print(f"{'PARTIAL' if args.filter else 'FULL'} real-code mutation gate; evidence: {work}", flush=True)
    sources = {c["source"]: (ROOT / c["source"]).read_bytes() for c in selected}
    receipt = snapshot.receipt
    receipt["results"] = []
    receipt['failures'] = []
    receipt['jobs'] = jobs
    receipt['complete'] = False
    (work / "receipt.json").write_text(json.dumps(receipt, indent=2) + "\n")

    def check(item, cancelled):
        index, case = item
        name = case['name']
        start = time.monotonic()
        control_log = work / f"{name}-control.jsonl"
        mutant_log = work / f"{name}-mutant.jsonl"
        try:
            if not re.fullmatch(r"[a-z0-9-]+", name):
                raise ValueError(f"invalid mutation name: {name}")
            original = sources[case["source"]].decode()
            changed = original
            for old, new in case["replace"]:
                if changed.count(old) != 1:
                    raise ValueError(f"{name}: mutation site changed; reassess the code, test and model")
                changed = changed.replace(old, new, 1)
            mutant = work / f"{index}-{name}.go"
            mutant.write_text(changed)
            overlay = work / f"{index}-{name}.json"
            overlay.write_text(json.dumps({"Replace": {str(ROOT / case["source"]): str(mutant)}}))
            status, events = run_tests(case, control_log, cancelled=cancelled)
            if not passed(case, status, events):
                raise ValueError(f"{name}: clean test did not pass (failure, build error or skip); see {control_log}")
            status, events = run_tests(case, mutant_log, overlay, cancelled=cancelled)
            if not killed(case, status, events):
                raise ValueError(f"{name}: mutant survived or failed for the wrong reason; see {mutant_log}")
            return {"name": name, "status": "killed", "control": str(control_log), "mutant": str(mutant_log), 'seconds': round(time.monotonic() - start, 3)}
        except (OSError, ValueError, subprocess.SubprocessError) as error:
            return {'name': name, 'status': 'failed', 'error': str(error)}

    try:
        with concurrent_checks(enumerate(selected), check, jobs) as results:
            for result in results:
                if result['status'] == 'killed':
                    receipt['results'].append(result)
                    print(f"ok {result['name']}: clean test passed; mutant failed its named assertion", flush=True)
                else:
                    receipt['failures'].append(result)
                    print(f"verification-mutations: {result['error']}", file=sys.stderr, flush=True)
    finally:
        unchanged = snapshot.finish()
        receipt["complete"] = len(receipt["results"]) == len(selected)
        (work / "receipt.json").write_text(json.dumps(receipt, indent=2) + "\n")
        if not unchanged:
            raise ValueError("verification inputs changed; rerun on a stable checkout")
    return int(not receipt['complete'])


if __name__ == "__main__":
    try:
        sys.exit(main())
    except SignalInterrupted as error:
        print(f"verification-mutations: {error}", file=sys.stderr)
        sys.exit(128 + error.signum)
    except (OSError, ValueError, subprocess.SubprocessError, KeyboardInterrupt) as error:
        print(f"verification-mutations: {error}", file=sys.stderr)
        sys.exit(130 if isinstance(error, KeyboardInterrupt) else 1)
