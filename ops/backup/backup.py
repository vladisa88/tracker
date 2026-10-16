"""PostgreSQL -> verified external S3 backups. CLI output contains only safe metadata."""
import datetime as dt
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time
import uuid
from urllib.parse import quote, urlparse


class Failure(Exception):
    pass


def now():
    return dt.datetime.now(dt.timezone.utc)


def run(args, stage, env=None):
    try:
        return subprocess.run(args, env=env, check=True, capture_output=True,
                              timeout=1800).stdout
    except (OSError, subprocess.SubprocessError):
        # Tool stderr can contain credentials, URLs, SQL and financial data.
        raise Failure(stage) from None


def pg(args, stage, target=False):
    dsn = os.environ.get('RESTORE_DATABASE_URL' if target else 'DATABASE_URL')
    if not dsn:
        raise Failure('configuration')
    return run(args + ['--dbname', dsn], stage, {**os.environ, 'PGCONNECT_TIMEOUT': '15'})


class Storage:
    def __init__(self):
        self.bucket = os.environ.get('BACKUP_BUCKET', '')
        self.prefix = os.environ.get('BACKUP_PREFIX', '')
        endpoint = os.environ.get('BACKUP_ENDPOINT_URL', '')
        if (not self.bucket or not self.prefix.endswith('/') or self.prefix.startswith('/')
                or not re.fullmatch(r'[A-Za-z0-9_/-]+/', self.prefix)):
            raise Failure('configuration')
        if endpoint:
            parsed = urlparse(endpoint)
            if (not parsed.hostname or parsed.username or parsed.password or parsed.query
                    or parsed.fragment or parsed.scheme not in ('http', 'https')
                    or (parsed.scheme == 'http' and os.getenv('BACKUP_ALLOW_HTTP_TEST_ONLY') != '1')):
                raise Failure('configuration')
        self.command = ['aws', '--no-cli-pager', '--cli-connect-timeout', '15',
                        '--cli-read-timeout', '120']
        if endpoint:
            self.command += ['--endpoint-url', endpoint]

    def call(self, operation, *args):
        output = run(self.command + ['s3api', operation, '--bucket', self.bucket,
                                    '--output', 'json', *args], 'storage')
        return json.loads(output or '{}')

    def valid(self, key):
        return key.startswith(self.prefix) and re.fullmatch(
            r'\d{8}T\d{12}Z-[0-9a-f]{32}\.dump', key[len(self.prefix):]) is not None

    def backups(self):
        # AWS CLI automatically follows all list-objects-v2 pages.
        entries = self.call('list-objects-v2', '--prefix', self.prefix).get('Contents', [])
        return sorted(item['Key'] for item in entries if self.valid(item['Key']))

    def download(self, key, path):
        metadata = self.call('get-object', '--key', key, str(path))
        checksum = metadata.get('Metadata', {}).get('sha256')
        if not checksum or checksum != digest(path):
            raise Failure('checksum')


def digest(path):
    checksum = hashlib.sha256()
    with path.open('rb') as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b''):
            checksum.update(chunk)
    return checksum.hexdigest()


def read_status(state):
    path = state / 'status.json'
    return json.loads(path.read_text()) if path.exists() else {'result': 'never', 'last_success': None}


def save_status(state, status):
    temporary = state / 'status.tmp'
    temporary.write_text(json.dumps(status) + '\n')
    temporary.replace(state / 'status.json')
    print(json.dumps(status), flush=True)


def backup(state):
    status = {'started_at': now().isoformat(), 'result': 'running',
              'last_success': read_status(state).get('last_success')}
    save_status(state, status)
    try:
        storage = Storage()
        key = storage.prefix + now().strftime('%Y%m%dT%H%M%S%fZ') + '-' + uuid.uuid4().hex + '.dump'
        staging = storage.prefix + 'staging/' + uuid.uuid4().hex
        with tempfile.TemporaryDirectory() as directory:
            archive = Path(directory) / 'database.dump'
            pg(['pg_dump', '--format=custom', '--no-owner', '--no-acl', '--file', str(archive)], 'dump')
            run(['pg_restore', '--list', str(archive)], 'archive')
            try:
                storage.call('put-object', '--key', staging, '--body', str(archive),
                             '--metadata', json.dumps({'sha256': digest(archive)}))
                storage.download(staging, Path(directory) / 'verified.dump')
                storage.call('copy-object', '--key', key, '--copy-source',
                             quote(storage.bucket + '/' + staging, safe='/'))
            finally:
                try:
                    storage.call('delete-object', '--key', staging)
                except Failure:
                    pass  # Staging is never counted as a successful backup.
        # Promotion is atomic; only verified archives enter the retention set.
        status.update(last_success=now().isoformat(), backup=key)
        for old in storage.backups()[:-7]:
            storage.call('delete-object', '--key', old)
        status.update(result='ok', stage='complete')
    except (Failure, OSError, ValueError) as error:
        status.update(result='error', stage=str(error) if isinstance(error, Failure) else 'local_io')
    status['finished_at'] = now().isoformat()
    save_status(state, status)
    return 0 if status['result'] == 'ok' else 1


def restore(key):
    storage = Storage()
    if not storage.valid(key):
        raise Failure('invalid_backup')
    with tempfile.TemporaryDirectory() as directory:
        archive = Path(directory) / 'database.dump'
        storage.download(key, archive)
        # Refuse user relations; restoration itself is transactional on conflicts.
        occupied = pg(['psql', '-XAt', '--set=ON_ERROR_STOP=1', '--command',
                       "SELECT count(*) FROM pg_class WHERE relnamespace IN "
                       "(SELECT oid FROM pg_namespace WHERE nspname !~ '^pg_' "
                       "AND nspname <> 'information_schema')"], 'target', target=True)
        if occupied.strip() != b'0':
            raise Failure('target_not_empty')
        pg(['pg_restore',
            '--single-transaction', '--exit-on-error', '--no-owner', '--no-acl',
            str(archive)], 'restore', target=True)
    print(json.dumps({'result': 'ok', 'stage': 'restored', 'backup': key}), flush=True)


def main():
    os.umask(0o077)
    state = Path(os.environ.get('BACKUP_STATE_DIR', '/state'))
    state.mkdir(parents=True, exist_ok=True)
    command = sys.argv[1] if len(sys.argv) == 2 else ''
    if len(sys.argv) == 3 and sys.argv[1] == 'restore':
        command = 'restore'
    if command == 'status':
        print(json.dumps(read_status(state)))
        return 0
    if command == 'list':
        print(json.dumps({'backups': Storage().backups()}))
        return 0
    # One scheduler/CLI writer per shared state volume and storage prefix.
    with (state / 'operation.lock').open('w') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise Failure('busy') from None
        if command == 'backup':
            return backup(state)
        if command == 'restore':
            restore(sys.argv[2])
            return 0
        if command != 'daemon':
            raise Failure('usage')
        while True:
            backup(state)
            # Immediate backup on startup, then daily at 02:00 UTC (05:00 Moscow).
            current = now()
            following = current.replace(hour=2, minute=0, second=0, microsecond=0)
            if following <= current:
                following += dt.timedelta(days=1)
            while True:
                remaining = (following - now()).total_seconds()
                if remaining <= 0:
                    break
                time.sleep(min(30, remaining))


if __name__ == '__main__':
    try:
        sys.exit(main())
    except (Failure, OSError, ValueError) as error:
        stage = str(error) if isinstance(error, Failure) else 'local_state'
        print(json.dumps({'result': 'error', 'stage': stage}), flush=True)
        sys.exit(1)
