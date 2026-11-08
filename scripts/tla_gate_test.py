#!/usr/bin/env python3
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import time
import unittest

ROOT = Path(__file__).resolve().parents[1]


class GateCLI(unittest.TestCase):
    def setUp(self):
        import tempfile
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        (self.root / 'scripts').mkdir()
        for name in ('tla.sh', 'tla_gate.py', 'verification_context.py'):
            shutil.copy(ROOT / 'scripts' / name, self.root / 'scripts' / name)
        jar = self.root / 'pinned-fixture.jar'
        jar.write_bytes(b'test toolchain')
        runner = self.root / 'scripts/tla_gate.py'
        runner.write_text(runner.read_text().replace(
            "SHA = '936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88'",
            f"SHA = '{hashlib.sha256(jar.read_bytes()).hexdigest()}'"))
        self.configs = self.root / 'verification/tla'
        (self.configs / 'mutations').mkdir(parents=True)
        (self.configs / 'Tiny.tla').write_text('---- MODULE Tiny ----\n====\n')
        (self.configs / 'Tiny.cfg').write_text('SPECIFICATION Spec\nINVARIANTS\n Safe\n')
        (self.configs / 'mutations/Tiny.safety.cfg').write_text('\\* expect: invariant Safe\nINVARIANT Safe\n')
        (self.configs / 'mutations/Tiny.live.cfg').write_text('\\* expect: property Live\nPROPERTY\n Live\n')
        (self.root / '.gitignore').write_text('_scratch/\n__pycache__/\n')
        fake = self.root / 'java'
        fake.write_text('''#!/usr/bin/env python3
import os, pathlib, subprocess, sys, time
mode=os.environ.get('FAULT', '')
meta=pathlib.Path(sys.argv[sys.argv.index('-metadir')+1]);meta.mkdir(exist_ok=True);(meta/'owned-state').write_bytes(b'synthetic state bytes')
if mode=='timeout':
 p=pathlib.Path('_scratch/child-pid');p.parent.mkdir(exist_ok=True);p.write_text(str(os.getpid()));time.sleep(30)
if mode=='change-input': pathlib.Path('Tiny.tla').write_text('changed')
if mode=='change-head': subprocess.run(['git','-c','user.name=Test','-c','user.email=test@example.invalid','-c','commit.gpgsign=false','-c','core.hooksPath=/dev/null','commit','--allow-empty','-qm','changed'],check=True)
cfg=pathlib.Path(sys.argv[sys.argv.index('-config')+1]).name
if 'safety' in cfg:
 print('Error: Invariant Safe is violated.'); print('Error: The behavior up to this point is:'); code=12
elif 'live' in cfg:
 print('Error: Temporal properties were violated.'); print('Error: The following behavior constitutes a counter-example:'); code=13
else:
 print('Model checking completed. No error has been found.'); code=0
if mode!='missing-counts': print('10 states generated, '+('0' if mode=='zero-states' else '8')+' distinct states found, '+('1' if mode=='queued' else '0')+' states left on queue.')
if mode=='extra-error': print('Error: internal error')
if mode!='unfinished': print('Finished in 1s at now')
sys.exit(99 if mode=='exit99' else code)
''')
        fake.chmod(0o755)
        self.env = dict(os.environ, JAVA=str(fake), TLA2TOOLS=str(jar), TLA_TIMEOUT_SECONDS='2', GIT_CONFIG_GLOBAL='/dev/null', GIT_CONFIG_NOSYSTEM='1')
        self.git('init', '-q', str(self.root))
        self.assertEqual(self.run_gate('--refresh-manifest').returncode, 0)
        self.commit()

    def git(self, *args):
        return subprocess.run(['git', '-C', str(self.root), '-c', 'user.name=Test', '-c', 'user.email=test@example.invalid', '-c', 'commit.gpgsign=false', '-c', 'core.hooksPath=/dev/null', *args], env=self.env, check=True, stdout=subprocess.DEVNULL)

    def commit(self):
        self.git('add', '.')
        self.git('commit', '--allow-empty', '-qm', 'fixture')

    def run_gate(self, *args, fault=''):
        env = dict(self.env, FAULT=fault)
        if fault == 'timeout':
            env['TLA_TIMEOUT_SECONDS'] = '0.1'
        return subprocess.run(['bash', str(self.root / 'scripts/tla.sh'), *args], env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=20)

    def test_normal_and_partial(self):
        result = self.run_gate()
        self.assertEqual(result.returncode, 0, result.stdout)
        result = self.run_gate('Tiny.cfg')
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertIn('PARTIAL', result.stdout)
        receipt = json.loads(next((self.root / '_scratch/tla').glob('*/receipt.json')).read_text())
        self.assertIn('scripts/tla_gate.py', receipt['inputs'])
        self.assertEqual(receipt['runtime']['workers'], 2)

    def test_faults(self):
        for fault in ('exit99', 'extra-error', 'missing-counts', 'unfinished', 'timeout', 'zero-states', 'queued'):
            with self.subTest(fault=fault):
                result = self.run_gate(fault=fault)
                self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertTrue(list((self.root / '_scratch/tla').rglob('*.log')))
        self.assertFalse(list((self.root / '_scratch/tla').glob('*/states-*')))

    def test_config_changes(self):
        path = self.configs / 'Tiny.cfg'
        original = path.read_text()
        for text in ('', 'SPECIFICATION Spec\n', original + 'PROPERTY Other\n', original.replace('Safe', 'Other')):
            path.write_text(text)
            self.commit()
            self.assertNotEqual(self.run_gate().returncode, 0)
        path.unlink()
        self.commit()
        result = self.run_gate()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Tiny.cfg', result.stdout)

    def test_spec_changes_need_manifest_review(self):
        (self.configs / 'Tiny.tla').write_text('Safe == TRUE\n')
        self.commit()
        result = self.run_gate()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Tiny.tla', result.stdout)

    def test_isolated_multiline_property(self):
        path = self.configs / 'mutations/Tiny.live.cfg'
        path.write_text(path.read_text() + ' Other\n')
        self.assertNotEqual(self.run_gate('--refresh-manifest').returncode, 0)
        self.assertNotEqual(self.run_gate().returncode, 0)

    def test_dirty_full_gate_rejected(self):
        (self.root / 'uncommitted.txt').write_text('working change')
        result = self.run_gate()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('clean committed tree', result.stdout)
        self.assertFalse((self.root / '_scratch/tla').exists())
        result = self.run_gate('Tiny.cfg')
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertIn('PARTIAL', result.stdout)

    def test_changed_input_or_head_invalidates_run(self):
        for fault in ('change-head', 'change-input'):
            with self.subTest(fault=fault):
                result = self.run_gate('Tiny.cfg', fault=fault)
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertIn('verification invalidated: changed', result.stdout)

    def test_changed_content_with_same_dirty_status_invalidates(self):
        (self.configs / 'Tiny.tla').write_text('developer version')
        self.assertEqual(self.run_gate('--refresh-manifest').returncode, 0)
        self.git('add', 'verification/tla/checks.json')
        self.git('commit', '-qm', 'manifest')
        result = self.run_gate('Tiny.cfg', fault='change-input')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('verification invalidated: changed verification/tla/Tiny.tla', result.stdout)

    def test_wrong_jar(self):
        self.env['TLA2TOOLS'] = str(self.root / 'java')
        self.assertNotEqual(self.run_gate().returncode, 0)

    def test_status_gate_mutant_is_detected(self):
        runner = self.root / 'scripts/tla_gate.py'
        runner.write_text(runner.read_text().replace('code == 0', 'True').replace('code == 12', 'True').replace('code == 13', 'True'))
        self.commit()
        self.assertEqual(self.run_gate(fault='exit99').returncode, 0, 'fault oracle no longer distinguishes a bypassed status gate')

    def test_sigterm_reaps_owned_child(self):
        process = subprocess.Popen(['bash', str(self.root / 'scripts/tla.sh'), 'Tiny.cfg'], env=dict(self.env, FAULT='timeout', TLA_TIMEOUT_SECONDS='30'), stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        try:
            marker = self.configs / '_scratch/child-pid'
            deadline = time.monotonic() + 5
            while not marker.exists() and time.monotonic() < deadline:
                time.sleep(0.01)
            self.assertTrue(marker.exists())
            child = int(marker.read_text())
            process.send_signal(signal.SIGTERM)
            self.assertEqual(process.wait(timeout=10), 143)
            with self.assertRaises(ProcessLookupError):
                os.kill(child, 0)
        finally:
            if process.poll() is None:
                process.kill()
                process.wait()


if __name__ == '__main__':
    unittest.main()
