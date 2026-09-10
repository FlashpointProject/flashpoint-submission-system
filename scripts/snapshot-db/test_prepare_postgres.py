import subprocess
import sys
import unittest
from pathlib import Path


class PreparePostgresTests(unittest.TestCase):
    def test_cluster_setup_scrub_preserves_copy_data(self):
        source = (b"DROP ROLE fpfss;\nDROP DATABASE IF EXISTS fpfss;\n"
                  b"ALTER ROLE fpfss WITH LOGIN PASSWORD 'synthetic-only';\n"
                  b"COPY public.example (value) FROM stdin;\n"
                  b"ALTER ROLE row_content PASSWORD 'preserve-row';\n\\.\n")
        result = subprocess.run([sys.executable, str(Path(__file__).with_name('prepare_postgres.py'))], input=source, capture_output=True, check=True)
        self.assertIn(b'DROP ROLE IF EXISTS fpfss;', result.stdout)
        self.assertNotIn(b'synthetic-only', result.stdout)
        self.assertIn(b"ALTER ROLE row_content PASSWORD 'preserve-row';", result.stdout)
        self.assertIn(b'PASSWORD NULL', result.stdout)
        self.assertIn(b'clauses_removed=1', result.stderr)

    def test_unsupported_role_clause_fails_without_echoing_value(self):
        result = subprocess.run([sys.executable, str(Path(__file__).with_name('prepare_postgres.py'))], input=b'ALTER ROLE fpfss PASSWORD unsupported_dummy;\n', capture_output=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn(b'unsupported_dummy', result.stdout + result.stderr)


if __name__ == '__main__':
    unittest.main()
