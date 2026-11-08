#!/usr/bin/env python3
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import tempfile


ROOT = Path(__file__).resolve().parents[1]


def run_tests(case, output, overlay=None):
    command = ["go", "test", "-json", "-race", "-count=1", "-timeout=90s"]
    if overlay:
        command.append("-overlay=" + str(overlay))
    command += [case["package"], "-run", case["run"]]
    with output.open("w") as log:
        process = subprocess.Popen(command, cwd=ROOT, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
        try:
            status = process.wait(timeout=120)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait()
            raise ValueError(f"test command timed out; see {output}") from None
    events = []
    for line in output.read_text().splitlines():
        try:
            events.append(json.loads(line))
        except json.JSONDecodeError:
            pass
    return status, events


def passed(case, status, events):
    return status == 0 and any(e.get("Action") == "pass" and e.get("Test") == case["test"] for e in events)


def killed(case, status, events):
    return (
        status == 1
        and any(e.get("Action") == "fail" and e.get("Test") == case["test"] for e in events)
        and any(e.get("Test") == case["test"] and case["diagnostic"] in e.get("Output", "") for e in events)
        and not any(e.get("Action") == "build-fail" for e in events)
    )


def main():
    parser = argparse.ArgumentParser(description="Kill real-code guard mutations through compiler overlays; leave source untouched.")
    parser.add_argument("filter", nargs="?", default="", help="developer subset; omit for the complete gate")
    args = parser.parse_args()
    cases = json.loads((ROOT / "verification/code-mutations.json").read_text())
    if not cases or len({c["name"] for c in cases}) != len(cases):
        raise ValueError("mutation inventory must be nonempty with unique names")
    selected = [c for c in cases if args.filter in c["name"]]
    if not selected:
        raise ValueError("no matching mutations")
    if not os.environ.get("TEST_DATABASE_URL"):
        raise ValueError("TEST_DATABASE_URL is required; skipped database tests cannot establish mutation coverage")
    scratch = ROOT / "_scratch/verification-mutations"
    scratch.mkdir(parents=True, exist_ok=True)
    work = Path(tempfile.mkdtemp(prefix="run-", dir=scratch))
    print(f"{'PARTIAL' if args.filter else 'FULL'} real-code mutation gate; evidence: {work}", flush=True)
    sources = {c["source"]: (ROOT / c["source"]).read_bytes() for c in selected}
    receipt = {"head": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
               "partial": bool(args.filter), "source_sha256": {p: hashlib.sha256(v).hexdigest() for p, v in sources.items()}, "results": []}
    try:
        for index, case in enumerate(selected):
            name = case["name"]
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
            control_log = work / f"{name}-control.jsonl"
            status, events = run_tests(case, control_log)
            if not passed(case, status, events):
                raise ValueError(f"{name}: clean test did not pass (failure, build error or skip); see {control_log}")
            mutant_log = work / f"{name}-mutant.jsonl"
            status, events = run_tests(case, mutant_log, overlay)
            if not killed(case, status, events):
                raise ValueError(f"{name}: mutant survived or failed for the wrong reason; see {mutant_log}")
            receipt["results"].append({"name": name, "status": "killed", "control": str(control_log), "mutant": str(mutant_log)})
            print(f"ok {name}: clean test passed; mutant failed its named assertion", flush=True)
    finally:
        (work / "receipt.json").write_text(json.dumps(receipt, indent=2) + "\n")
        for path, original in sources.items():
            if (ROOT / path).read_bytes() != original:
                raise ValueError(f"source changed during verification: {path}; rerun on a stable checkout")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        print(f"verification-mutations: {error}", file=sys.stderr)
        sys.exit(1)
