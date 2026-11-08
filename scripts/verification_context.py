import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]


class SignalInterrupted(Exception):
    def __init__(self, signum):
        super().__init__(f"interrupted by signal {signum}")
        self.signum = signum


def install_signal_handlers():
    def interrupt(signum, _frame):
        raise SignalInterrupted(signum)

    for signum in (signal.SIGTERM, signal.SIGHUP):
        signal.signal(signum, interrupt)


def run_command(command, *, cwd, stdout=None, timeout=None):
    process = subprocess.Popen(command, cwd=cwd, stdout=stdout, stderr=subprocess.STDOUT, start_new_session=True)
    try:
        return process.wait(timeout=timeout)
    except BaseException:
        try:
            os.killpg(process.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            process.poll()
            try:
                os.killpg(process.pid, 0)
            except ProcessLookupError:
                break
            time.sleep(0.01)
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.wait()
        raise


def git(root, *args):
    return subprocess.check_output(["git", "-C", str(root), *args], text=True).strip()


def digest(path):
    if path.is_symlink():
        return hashlib.sha256(os.readlink(path).encode()).hexdigest()
    return hashlib.sha256(path.read_bytes()).hexdigest()


class Snapshot:
    def __init__(self, root, partial=False, extra=()):
        self.root = root
        self.extra = list(extra)
        self.head = git(root, "rev-parse", "HEAD")
        self.dirty = git(root, "status", "--porcelain", "--untracked-files=all")
        if not partial and self.dirty:
            raise ValueError("full gate requires a clean committed tree; commit changes or use a partial developer run")
        self.inputs = self.fingerprints()
        self.receipt = {"head": self.head, "dirty": self.dirty, "partial": partial, "inputs": self.inputs}
        if git(root, "rev-parse", "HEAD") != self.head or git(root, "status", "--porcelain", "--untracked-files=all") != self.dirty:
            raise ValueError("HEAD or working tree changed while taking verification snapshot")

    def fingerprints(self):
        names = set(git(self.root, "ls-files", "-z").split("\0")) - {""}
        names.update(str(p.relative_to(self.root)) for p in self.extra)
        return {name: digest(self.root / name) if (self.root / name).exists() or (self.root / name).is_symlink() else None for name in sorted(names)}

    def finish(self):
        changed = []
        try:
            if git(self.root, "rev-parse", "HEAD") != self.head:
                changed.append("HEAD")
            if git(self.root, "status", "--porcelain", "--untracked-files=all") != self.dirty:
                changed.append("working tree")
            after = self.fingerprints()
            changed.extend(name for name in sorted(set(after) | set(self.inputs)) if after.get(name) != self.inputs.get(name))
        except (OSError, subprocess.SubprocessError) as error:
            changed.append(str(error))
        self.receipt["inputs_unchanged"] = not changed
        self.receipt["changed_inputs"] = changed
        if changed:
            print("verification invalidated: changed " + ", ".join(changed[:10]), file=sys.stderr)
        return not changed


def main():
    install_signal_handlers()
    snapshot = Snapshot(ROOT)
    directory = ROOT / "_scratch/branch-gates"
    directory.mkdir(parents=True, exist_ok=True)
    run = Path(tempfile.mkdtemp(prefix="run-", dir=directory))
    command = sys.argv[1:]
    if not command:
        raise ValueError("a gate command is required")
    print(f"FULL branch gate at {snapshot.head}; receipt: {run / 'receipt.json'}", flush=True)
    status = 1
    try:
        status = run_command(command, cwd=ROOT)
    finally:
        unchanged = snapshot.finish()
        snapshot.receipt["command"] = command
        snapshot.receipt["command_exit"] = status
        snapshot.receipt["passed"] = status == 0 and unchanged
        (run / "receipt.json").write_text(json.dumps(snapshot.receipt, indent=2) + "\n")
    return status if unchanged else 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except SignalInterrupted as error:
        print(f"branch gate: {error}", file=sys.stderr)
        sys.exit(128 + error.signum)
    except (OSError, ValueError, subprocess.SubprocessError, KeyboardInterrupt) as error:
        print(f"branch gate: {error}", file=sys.stderr)
        sys.exit(130 if isinstance(error, KeyboardInterrupt) else 1)
