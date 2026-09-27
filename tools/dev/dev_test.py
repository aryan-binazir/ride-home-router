import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


class LifecycleStatusTest(unittest.TestCase):
    def test_missing_process_start_cannot_report_running(self):
        root = Path(__file__).resolve().parents[2]
        key = hashlib.sha256(str(root).encode()).hexdigest()[:20]
        with tempfile.TemporaryDirectory() as directory:
            state = Path(directory) / 'ride-home-router' / key
            state.mkdir(parents=True)
            (state / 'state.json').write_text(json.dumps({'root': str(root), 'pid': 999999999}))
            (state / 'ready.json').write_text(json.dumps({'url': 'http://127.0.0.1:1', 'login': 'http://127.0.0.1:1'}))
            result = subprocess.run([sys.executable, str(root / 'tools/dev/dev.py'), 'status'],
                                    env={**os.environ, 'XDG_STATE_HOME': directory},
                                    capture_output=True, text=True, check=True)
            self.assertEqual(result.stdout.splitlines()[0], 'Stopped')


if __name__ == '__main__':
    unittest.main()
