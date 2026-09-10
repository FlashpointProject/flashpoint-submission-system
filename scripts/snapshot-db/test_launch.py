import shutil
import subprocess
import tempfile
import time
import unittest
from pathlib import Path


class LauncherTests(unittest.TestCase):
    def test_running_script_is_not_changed_by_source_edit(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            shutil.copy(Path(__file__).with_name('launch.sh'), root / 'launch.sh')
            runner = root / 'run.sh'
            runner.write_text('touch "$1"\nwhile [[ ! -f "$2" ]]; do sleep 0.01; done\nprintf "original\\n"\n')
            ready, resume = root / 'ready', root / 'resume'
            process = subprocess.Popen(['bash', str(root / 'launch.sh'), str(ready), str(resume)], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            try:
                deadline = time.monotonic() + 5
                while not ready.exists() and time.monotonic() < deadline:
                    time.sleep(0.01)
                self.assertTrue(ready.exists(), 'runner did not start')
                runner.write_text('exit 99\n' * 30)
                resume.touch()
                stdout, stderr = process.communicate(timeout=5)
                self.assertEqual(process.returncode, 0, stderr)
                self.assertEqual(stdout, b'original\n')
            finally:
                if process.poll() is None:
                    process.kill()
                    process.communicate()


if __name__ == '__main__':
    unittest.main()
