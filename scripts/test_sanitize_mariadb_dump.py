import io
import unittest
import shutil
import subprocess
import tempfile
from pathlib import Path
from sanitize_mariadb_dump import filter_dump, sanitize


def section(table, data):
    return (f'-- Table structure for table `{table}`\nCREATE TABLE `{table}` (\n);\n'
            f'-- Dumping data for table `{table}`\n').encode() + data


class SanitizerTests(unittest.TestCase):
    def test_only_auth_inserts_removed(self):
        session = b"INSERT INTO `session` VALUES (1,'synthetic;quoted\\\'value');\n"
        oauth = b"INSERT INTO `oauth_client` VALUES ('synthetic','dummy');\n"
        other = section('comment', b"INSERT INTO `comment` VALUES ('INSERT INTO `session` VALUES (dummy);');\n")
        source = section('session', session) + section('oauth_client', oauth) + other
        output = io.BytesIO()
        result = filter_dump(io.BytesIO(source), output)
        self.assertEqual(output.getvalue(), source.replace(session, b'').replace(oauth, b''))
        self.assertEqual(result['removed_insert_statements'], {'oauth_client': 1, 'session': 1})

    @unittest.skipUnless(shutil.which('zstd'), 'zstd required')
    def test_compressed_publication_and_failure_cleanup(self):
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / 'source.sql.zst'
            output = Path(directory) / 'sanitized.sql.zst'
            raw = section('session', b"INSERT INTO `session` VALUES (1,'dummy');\n") + section('oauth_client', b'')
            original = subprocess.check_output(['zstd', '-q', '-c'], input=raw)
            source.write_bytes(original)
            result = sanitize(source, output)
            self.assertTrue(result['verified'])
            self.assertEqual(source.read_bytes(), original)
            self.assertEqual(output.stat().st_mode & 0o777, 0o600)
            with self.assertRaises(ValueError):
                sanitize(source, output)
            broken = Path(directory) / 'broken.sql.zst'
            source.write_bytes(subprocess.check_output(['zstd', '-q', '-c'], input=b'unsupported'))
            with self.assertRaises(ValueError):
                sanitize(source, broken)
            self.assertFalse(broken.exists())
            self.assertFalse(list(Path(directory).glob('.sanitizing-*')))

    def test_multiline_auth_with_quoted_delimiters(self):
        payload = b"INSERT INTO `session` VALUES\n(1,'semi;colon'),\n(2,'doubled''quote;'),\n(3,'embedded\nnewline;');\n"
        source = section('session', payload) + section('oauth_client', b'')
        output = io.BytesIO()
        result = filter_dump(io.BytesIO(source), output)
        self.assertEqual(result['removed_insert_statements']['session'], 1)
        self.assertEqual(output.getvalue(), source.replace(payload, b''))
        with self.assertRaises(ValueError):
            filter_dump(io.BytesIO(section('session', b"INSERT INTO `session` VALUES\n(1,'unfinished")), io.BytesIO())
        with self.assertRaises(ValueError):
            filter_dump(io.BytesIO(section('session', b"INSERT INTO `session` VALUES (1); SELECT 1;\n")), io.BytesIO())

    def test_empty_auth_tables(self):
        source = section('session', b'') + section('oauth_client', b'')
        output = io.BytesIO()
        self.assertEqual(filter_dump(io.BytesIO(source), output)['removed_insert_statements'], {'oauth_client': 0, 'session': 0})
        self.assertEqual(output.getvalue(), source)

    def test_rejects_unknown_multiline_or_missing_sections(self):
        for payload in [b"REPLACE INTO `session` VALUES (1,'dummy');\n", b"COPY session FROM stdin;\n"]:
            with self.subTest(payload=payload), self.assertRaises(ValueError):
                filter_dump(io.BytesIO(section('session', payload) + section('oauth_client', b'')), io.BytesIO())
        with self.assertRaises(ValueError):
            filter_dump(io.BytesIO(b''), io.BytesIO())


if __name__ == '__main__':
    unittest.main()
