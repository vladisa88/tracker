"""CLI integration checks, run inside the backup image against test services."""
import json
import os
import subprocess
import unittest


class BackupCLI(unittest.TestCase):
    def cli(self, *args, success=True, **env):
        result = subprocess.run(['python3', '/backup.py', *args], env={**os.environ, **env},
                                capture_output=True, text=True)
        self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
        self.assertNotIn('test-secret-only', result.stdout + result.stderr)
        self.assertNotIn('test-only@', result.stdout + result.stderr)
        return json.loads(result.stdout.splitlines()[-1])

    def test_retention_and_failure_preserve_last_good_backup(self):
        # Each successful backup round-trips through S3 before retention.
        for _ in range(8):
            latest = self.cli('backup')
        objects = self.cli('list')['backups']
        self.assertEqual(len(objects), 7)
        self.assertEqual(objects[-1], latest['backup'])
        self.assertEqual(self.cli('status')['result'], 'ok')
        failure = self.cli('backup', success=False,
                           DATABASE_URL='postgres://tracking:test-only@test-db:5432/missing')
        self.assertEqual(failure['stage'], 'dump')
        self.assertEqual(self.cli('list')['backups'], objects)
        self.assertEqual(self.cli('status')['last_success'], latest['last_success'])
        self.cli('backup', success=False, BACKUP_BUCKET='missing-bucket')
        self.assertEqual(self.cli('list')['backups'], objects)
        self.assertEqual(self.cli('status')['result'], 'error')


if __name__ == '__main__':
    unittest.main()
