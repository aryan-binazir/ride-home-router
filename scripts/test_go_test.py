import tempfile
from pathlib import Path
import unittest

from test_go import merge_coverage


class CoverageTest(unittest.TestCase):
    def test_atomic_counts_add_and_disjoint_packages_survive(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            a, b, result = [root / name for name in ('a', 'b', 'result')]
            a.write_text('mode: atomic\na.go:1.1,2.2 1 3\nb.go:1.1,2.2 2 0\n')
            b.write_text('mode: atomic\na.go:1.1,2.2 1 4\nc.go:1.1,2.2 1 8\n')
            merge_coverage([a, b], result)
            self.assertEqual(result.read_text(), 'mode: atomic\na.go:1.1,2.2 1 7\nb.go:1.1,2.2 2 0\nc.go:1.1,2.2 1 8\n')

    def test_rejects_wrong_mode(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / 'profile'
            path.write_text('mode: set\n')
            with self.assertRaises(ValueError):
                merge_coverage([path], path)


class RunnerTest(unittest.TestCase):
    def test_real_runner_rejects_failure_skip_and_missing_test(self):
        import os
        import subprocess
        runner = Path(__file__).with_name('test_go.py').resolve()
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / 'internal/handlers').mkdir(parents=True)
            fake = root / 'go'
            fake.write_text('''#!/usr/bin/env python3
import json, os, pathlib, re, sys
args = sys.argv[1:]
pkg = 'ride-home-router/internal/handlers'
if args[0] == 'list':
    print(pkg+'\\nride-home-router/other')
elif '-c' in args:
    binary = pathlib.Path(args[args.index('-o')+1])
    binary.write_text("#!/bin/sh\\nprintf 'TestA\\nTestB\\nTestC\\nTestD\\nExample\\n'\\n")
    binary.chmod(0o755)
else:
    profile = next(a.split('=',1)[1] for a in args if a.startswith(('-coverprofile=', '-test.coverprofile=')))
    if os.environ.get('FAKE_ACTION') != 'profile_missing':
        pathlib.Path(profile).write_text('mode: atomic\\na.go:1.1,2.2 1 1\\n')
    selection = next((a for a in args if a.startswith('-test.run=')), None)
    names = re.findall(r'Test[A-D]|Example', selection) if selection else ['TestOther']
    package = pkg if selection else 'ride-home-router/other'
    for name in names:
        action = os.environ.get('FAKE_ACTION', 'pass') if name == 'TestC' else 'pass'
        if action == 'missing': continue
        if action == 'profile_missing': action = 'pass'
        for event in ['run', action]:
            print(json.dumps({'Package': package, 'Test': name, 'Action': event}))
        if action == 'fail': sys.exit(1)
''')
            fake.chmod(0o755)
            for action in ('pass', 'fail', 'skip', 'missing'):
                with self.subTest(action=action):
                    result = subprocess.run(['python3', str(runner)], cwd=root, env=dict(os.environ, PATH=str(root)+os.pathsep+os.environ['PATH'], TEST_DATABASE_URL='synthetic', FAKE_ACTION=action), capture_output=True, text=True)
                    self.assertEqual(result.returncode, 0 if action == 'pass' else 1, result.stdout+result.stderr)
                    if action != 'pass':
                        self.assertIn('TestC', result.stdout+result.stderr)
            self.assertEqual((root / 'coverage.out').read_text(), 'mode: atomic\na.go:1.1,2.2 1 6\n')

            (root / 'coverage.out').write_text('stale coverage')
            result = subprocess.run(['python3', str(runner)], cwd=root, env=dict(os.environ, PATH=str(root)+os.pathsep+os.environ['PATH'], TEST_DATABASE_URL='synthetic', FAKE_ACTION='profile_missing'), capture_output=True, text=True)
            self.assertEqual(result.returncode, 1)
            self.assertIn('Missing shard coverage profile', result.stderr)
            self.assertEqual((root / 'coverage.out').read_text(), 'mode: atomic\n')


class InventoryTest(unittest.TestCase):
    def test_subtest_difference_and_package_failure_are_rejected(self):
        import json
        import subprocess
        from compare_go_tests import inventory
        compare = Path(__file__).with_name('compare_go_tests.py').resolve()
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            before, after = root / 'before', root / 'after'
            events = [{'Package': 'example', 'Test': name, 'Action': action}
                      for name in ('TestParent', 'TestParent/child')
                      for action in ('run', 'pass')]
            before.write_text(''.join(json.dumps(event)+'\n' for event in events))
            after.write_text(before.read_text())
            missing = subprocess.run(['python3', str(compare), str(before)], capture_output=True, text=True)
            self.assertNotEqual(missing.returncode, 0)
            result = subprocess.run(['python3', str(compare), str(before), str(after)], capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            after.write_text(before.read_text().replace('/child', '/different'))
            result = subprocess.run(['python3', str(compare), str(before), str(after)], capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('TestParent/child', result.stderr)
            after.write_text(before.read_text()+json.dumps({'Package': 'broken', 'Action': 'fail'})+'\n')
            with self.assertRaises(ValueError):
                inventory(after)


class TimeoutTest(unittest.TestCase):
    def test_expired_package_budget_fails_before_starting_another_batch(self):
        from test_go import run
        with tempfile.TemporaryDirectory() as temporary:
            log = Path(temporary) / 'batch.log'
            status = run(['python3', '-c', 'raise AssertionError("batch started")'], log, deadline=0)
            self.assertEqual(status, 1)
            self.assertEqual(log.read_text(), 'Handler package exceeded its 10m test timeout before this batch started\n')
