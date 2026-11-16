import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import tempfile
import time
import unittest
import verification_mutations as mutations

ROOT = Path(__file__).resolve().parents[1]


class MutationVerdictTest(unittest.TestCase):
    case = {'test': 'TestGuard/lease', 'diagnostic': 'expired claim wrote'}
    failure = [{'Action': 'output', 'Test': 'TestGuard/lease', 'Output': 'expired claim wrote'},
               {'Action': 'fail', 'Test': 'TestGuard/lease'}, {'Action': 'fail', 'Package': 'example'}]

    def test_only_completed_named_assertion_kills_mutant(self):
        self.assertTrue(mutations.killed(self.case, 1, self.failure))
        for status, events in [(0, self.failure), (-9, self.failure),
                               (1, self.failure[:-1]), (1, self.failure + [{'Action': 'build-fail'}]),
                               (1, self.failure[:1] + [{'Action': 'fail', 'Test': 'TestOther'}, self.failure[-1]]),
                               (1, self.failure[1:]),
                               (1, [{'Action': 'output', 'Output': 'panic: test timed out after 90s'}] + self.failure)]:
            self.assertFalse(mutations.killed(self.case, status, events))

    def test_skip_or_incomplete_control_is_not_a_pass(self):
        for events in [[{'Action': 'skip', 'Test': self.case['test']}], [{'Action': 'pass', 'Package': 'example'}],
                       [{'Action': 'pass', 'Test': self.case['test']}]]:
            self.assertFalse(mutations.passed(self.case, 0, events))
        self.assertTrue(mutations.passed(self.case, 0, [{'Action': 'pass', 'Test': self.case['test']},
                                                     {'Action': 'pass', 'Package': 'example'}]))


class MutationCLI(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        (self.root / 'scripts').mkdir()
        (self.root / 'verification').mkdir()
        (self.root / '_scratch').mkdir()
        for name in ('verification_mutations.py', 'verification_context.py'):
            shutil.copy(ROOT / 'scripts' / name, self.root / 'scripts' / name)
        runner = self.root / 'scripts/verification_mutations.py'
        runner.write_text(runner.read_text().replace('timeout=120', "timeout=(0.2 if os.environ.get('FAULT') == 'timeout' else 10)"))
        (self.root / 'guard.go').write_text('original')
        cases = [{'name': name, 'source': 'guard.go', 'replace': [['original', name]],
                  'package': './example', 'run': '^TestGuard$', 'test': 'TestGuard', 'diagnostic': 'guard missing'}
                 for name in ('first', 'second', 'third', 'fourth')]
        (self.root / 'verification/code-mutations.json').write_text(json.dumps(cases))
        (self.root / '.gitignore').write_text('_scratch/\n__pycache__/\n')
        fake = self.root / 'go'
        fake.write_text('''#!/usr/bin/env python3
import json, os, pathlib, resource, signal, subprocess, sys, time
resource.setrlimit(resource.RLIMIT_CORE,(0,0))
root=pathlib.Path(__file__).parent
assert '-count=1' in sys.argv and '-timeout=90s' in sys.argv
assert (root/'guard.go').read_text()=='original'
overlay=next((a.split('=',1)[1] for a in sys.argv if a.startswith('-overlay=')),None)
mode=os.environ.get('FAULT','') if overlay or os.environ.get('FAULT','').startswith('control-') else ''
if overlay:
 replacements=json.loads(pathlib.Path(overlay).read_text())['Replace']
 source,copy=next(iter(replacements.items()))
 assert pathlib.Path(source)==root/'guard.go'
 assert pathlib.Path(copy).read_text() in ('first','second','third','fourth')
with (root/'_scratch/started.jsonl').open('a') as log:
 log.write(json.dumps([os.getpid(),overlay,time.monotonic()])+'\\n')
if mode=='timeout':
 (root/'_scratch/child').write_text(str(os.getpid()));time.sleep(30)
if mode=='descendant':
 child=subprocess.Popen([sys.executable,'-c','import time;time.sleep(30)'])
 (root/'_scratch/child').write_text(str(os.getpid()))
 (root/'_scratch/grandchild').write_text(str(child.pid))
 time.sleep(30)
if mode=='change-input': (root/'guard.go').write_text('changed')
if mode=='missing-output': sys.exit(1)
time.sleep(0.02)
action='fail' if overlay else 'pass'
if mode=='survives': action='pass'
if mode=='control-skip': action='skip'
events=[]
if overlay:
 events.append({'Action':'output','Package':'example','Test':'TestGuard','Output':'wrong reason' if mode=='wrong-diagnostic' else 'guard missing'})
if mode=='go-timeout': events.append({'Action':'output','Output':'panic: test timed out after 90s'})
if mode=='compile': events.append({'Action':'build-fail'})
if mode=='garbled-json': events.append({'Action':'output','Test':'TestGuard','Output':{}})
events.append({'Action':action,'Package':'example','Test':'Other' if mode=='wrong-test' else 'TestGuard'})
if mode!='unfinished': events.append({'Action':'fail' if action=='fail' else 'pass','Package':'example'})
for event in events: print(json.dumps(event))
with (root/'_scratch/finished.jsonl').open('a') as log:
 log.write(json.dumps([os.getpid(),time.monotonic()])+'\\n')
if mode=='garbled': print('not json')
if mode=='truncated': print('{"Action":',end='')
sys.stdout.flush()
if mode=='oom-kill': os.kill(os.getpid(),signal.SIGKILL)
if mode=='crash': os.kill(os.getpid(),signal.SIGSEGV)
sys.exit(1 if action=='fail' else 0)
''')
        fake.chmod(0o755)
        self.env = dict(os.environ, PATH=str(self.root) + os.pathsep + os.environ['PATH'],
                        TEST_DATABASE_URL='synthetic', GIT_CONFIG_GLOBAL='/dev/null', GIT_CONFIG_NOSYSTEM='1')
        self.git('init', '-q')
        self.git('add', '.')
        self.git('commit', '-qm', 'fixture')

    def git(self, *args):
        return subprocess.check_output(['git', '-C', str(self.root), '-c', 'user.name=Test', '-c',
                                        'user.email=test@example.invalid', '-c', 'commit.gpgsign=false',
                                        '-c', 'core.hooksPath=/dev/null', *args], env=self.env, text=True)

    def command(self):
        return ['python3', str(self.root / 'scripts/verification_mutations.py')]

    def run_gate(self, jobs, fault=''):
        before = set((self.root / '_scratch').glob('verification-mutations/*/receipt.json'))
        result = subprocess.run(self.command(), env=dict(self.env, VERIFICATION_JOBS=str(jobs), FAULT=fault),
                                text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=20)
        path, = set((self.root / '_scratch').glob('verification-mutations/*/receipt.json')) - before
        return result, json.loads(path.read_text())

    def test_serial_and_concurrent_classifications_match(self):
        verdicts = []
        for jobs in (1, 3):
            result, receipt = self.run_gate(jobs)
            self.assertEqual(result.returncode, 0, result.stdout)
            self.assertTrue(receipt['complete'])
            verdicts.append([(r['name'], r['status']) for r in receipt['results']])
        self.assertEqual(verdicts[0], verdicts[1])
        self.assertEqual(self.git('status', '--porcelain'), '')

    def test_failure_injections_fail_serial_and_concurrent(self):
        inventory = self.root / 'verification/code-mutations.json'
        inventory.write_text(json.dumps(json.loads(inventory.read_text())[:1]))
        self.git('add', '.')
        self.git('commit', '-qm', 'one failure fixture')
        for fault in ('survives', 'wrong-diagnostic', 'wrong-test', 'compile', 'control-skip', 'unfinished',
                      'missing-output', 'garbled', 'garbled-json', 'truncated', 'oom-kill', 'crash', 'timeout', 'go-timeout'):
            for jobs in (1, 3):
                with self.subTest(fault=fault, jobs=jobs):
                    result, receipt = self.run_gate(jobs, fault)
                    self.assertNotEqual(result.returncode, 0, result.stdout)
                    self.assertFalse(receipt['complete'])
                    self.assertEqual(len(receipt['failures']), 1)
                    self.assertEqual(self.git('status', '--porcelain'), '')

    def test_failure_does_not_skip_remaining_cases(self):
        result, receipt = self.run_gate(3, 'wrong-diagnostic')
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertFalse(receipt['complete'])
        self.assertEqual([failure['name'] for failure in receipt['failures']], ['first', 'second', 'third', 'fourth'])

    def test_simultaneous_runs_own_distinct_overlays(self):
        processes = [subprocess.Popen(self.command(), env=dict(self.env, VERIFICATION_JOBS='3'),
                                      stdout=subprocess.PIPE, stderr=subprocess.STDOUT) for _ in range(2)]
        outputs = [process.communicate(timeout=20)[0] for process in processes]
        for process, output in zip(processes, outputs):
            self.assertEqual(process.returncode, 0, output)
        records = [json.loads(line) for line in (self.root / '_scratch/started.jsonl').read_text().splitlines()]
        overlays = [overlay for _, overlay, _ in records if overlay]
        self.assertEqual(len(overlays), 8)
        self.assertEqual(len(set(overlays)), 8)
        self.assertEqual(self.git('status', '--porcelain'), '')

    def test_concurrency_is_bounded(self):
        result, _ = self.run_gate(3)
        self.assertEqual(result.returncode, 0, result.stdout)
        started = [json.loads(line) for line in (self.root / '_scratch/started.jsonl').read_text().splitlines()]
        finished = [json.loads(line) for line in (self.root / '_scratch/finished.jsonl').read_text().splitlines()]
        timeline = [(at, 1) for _, _, at in started] + [(at, -1) for _, at in finished]
        active = peak = 0
        for _, delta in sorted(timeline):
            active += delta
            peak = max(peak, active)
        self.assertEqual(active, 0)
        self.assertGreater(peak, 1)
        self.assertLessEqual(peak, 3)

    def test_changed_source_invalidates_whole_run(self):
        result, receipt = self.run_gate(3, 'change-input')
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertFalse(receipt['inputs_unchanged'])

    def test_interruption_reaps_running_children_and_leaves_incomplete_clean_tree(self):
        process = subprocess.Popen(self.command(), env=dict(self.env, VERIFICATION_JOBS='3', FAULT='descendant'),
                                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        try:
            deadline = time.monotonic() + 10
            marker = self.root / '_scratch/grandchild'
            while not marker.exists() and time.monotonic() < deadline:
                time.sleep(0.01)
            self.assertTrue(marker.exists())
            child = int((self.root / '_scratch/child').read_text())
            grandchild = int(marker.read_text())
            process.send_signal(signal.SIGTERM)
            self.assertEqual(process.wait(timeout=15), 143)
            with self.assertRaises(ProcessLookupError):
                os.kill(child, 0)
            stat = Path(f'/proc/{grandchild}/stat')
            self.assertTrue(not stat.exists() or stat.read_text().rsplit(')', 1)[1].split()[0] == 'Z')
            path, = (self.root / '_scratch/verification-mutations').glob('*/receipt.json')
            self.assertFalse(json.loads(path.read_text())['complete'])
            self.assertEqual(self.git('status', '--porcelain'), '')
        finally:
            if process.poll() is None:
                process.kill()
                process.wait()


if __name__ == '__main__':
    unittest.main()
