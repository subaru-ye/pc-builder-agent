"""Dedicated evaluation database; offline by default, explicit pinned live models."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import secrets
import subprocess
import tempfile
import time
import uuid
from urllib.parse import urlsplit, urlunsplit

ROOT = Path(__file__).resolve().parents[3]

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--out', required=True)
    parser.add_argument('--suite', default='internal/planningeval/testdata/current-178-20260917/mechanisms/suite.json')
    parser.add_argument('--go-tests', nargs='+', help='Run existing Go tests in the same isolated database container')
    parser.add_argument('--test-timeout', default='10m', help='Go test timeout; offline interactive browser runs may explicitly use 30m')
    parser.add_argument('--test-run', default='.', help='Existing go test -run filter; only used with --go-tests')
    parser.add_argument('--local-postgres', action='store_true', help='Create only a fresh peval_ database in the existing local PostgreSQL container; requires PLANNING_EVAL_ADMIN_DSN')
    parser.add_argument('--intake-packet', help='Rehearse a reviewed intake using its real files in the isolated database')
    parser.add_argument('--live', action='store_true', help='Explicit real chat models; web remains offline')
    parser.add_argument('--plan-live', action='store_true', help='Validate pinned model settings against the environment with zero model and database calls')
    parser.add_argument('--max-calls', type=int, default=0, help='Positive shared cap required with --live')
    parser.add_argument('--model-pin', default='docs/eval/planning-v2/baseline-20260915.json')
    args = parser.parse_args()
    if (args.live or args.plan_live) and (args.max_calls <= 0 or args.intake_packet or args.go_tests):
        parser.error('--live and --plan-live require a positive --max-calls and cannot run intake or Go tests')
    out = Path(args.out).resolve()
    if out.exists():
        raise SystemExit('Use a new output directory')
    suffix = uuid.uuid4().hex[:12]
    name, db = 'pcbuilder-planning-eval-' + suffix, 'peval_' + suffix
    env = dict(os.environ, POSTGRES_PASSWORD=secrets.token_hex(24), POSTGRES_DB=db,
               # Evaluation pins forbid model retries; force the harness-side zero
               # so a deployment .env that enables transient retries cannot make
               # a live batch non-reproducible.
               MODEL_MAX_RETRIES='0')
    image = 'pgvector/pgvector:pg16'
    # Default creates a dedicated container; explicit local mode only creates
    # a uniquely named database. Neither mode performs shared Compose changes.
    with tempfile.TemporaryDirectory(prefix='planning-eval-build-') as build:
        exe = Path(build) / 'evalplanning.exe'
        subprocess.run(['go', 'build', '-o', str(exe), './cmd/evalplanning'], cwd=ROOT, check=True)
        if args.local_postgres:
            admin = urlsplit(os.environ.get('PLANNING_EVAL_ADMIN_DSN', ''))
            if admin.hostname not in {'localhost', '127.0.0.1'} or not admin.username or args.intake_packet:
                raise ValueError('Explicit local PostgreSQL administrator DSN required; intake uses its original launcher')
            name = 'pc-builder-agent-postgres-1'
            subprocess.run(['docker', 'exec', name, 'createdb', '-U', admin.username, db], check=True)
        else:
            subprocess.run(['docker', 'run', '-d', '--name', name,
                            '-p', '127.0.0.1::5432', '--memory', '768m', '--cpus', '2',
                            '-e', 'POSTGRES_PASSWORD', '-e', 'POSTGRES_DB', image],
                           env=env, check=True, stdout=subprocess.DEVNULL)
        try:
            for _ in range(60):
                if subprocess.run(['docker', 'exec', name, 'pg_isready', '-h', '127.0.0.1', '-U', admin.username if args.local_postgres else 'postgres', '-d', db], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
                    break
                time.sleep(.5)
            else:
                raise RuntimeError('isolated database did not become ready')
            if args.local_postgres:
                env['PLANNING_EVAL_DSN'] = urlunsplit(admin._replace(path='/' + db))
            else:
                binding = json.loads(subprocess.check_output(['docker', 'inspect', '--format', '{{json .NetworkSettings.Ports}}', name]))
                port = binding['5432/tcp'][0]['HostPort']
                env['PLANNING_EVAL_DSN'] = f"postgres://postgres:{env['POSTGRES_PASSWORD']}@127.0.0.1:{port}/{db}?sslmode=disable"
            if args.intake_packet:
                env['PG_DSN'] = env['PLANNING_EVAL_DSN']
                result = subprocess.run(['python', 'scripts/data/collection-tools/2026-09-14/publish_intake.py',
                    '--packet', args.intake_packet, '--out', str(out), '--container', name,
                    '--database', db, '--db-user', 'postgres', '--rehearsal'], cwd=ROOT, env=env)
            elif args.go_tests:
                if any(not p.startswith('./') for p in args.go_tests):
                    raise ValueError('Go test packages must be relative package paths')
                env['PG_TEST_DSN'] = env['PLANNING_EVAL_DSN']
                out.mkdir(parents=True)
                result = subprocess.run(['go', 'test', '-count=1', '-p', '1', '-timeout', args.test_timeout, '-run', args.test_run, *args.go_tests], cwd=ROOT, env=env, capture_output=True, text=True, encoding='utf-8')
                (out / 'tests.log').write_bytes((result.stdout + result.stderr).encode('utf-8'))
                print(result.stdout + result.stderr)
            else:
                mode = 'plan-live' if args.plan_live else 'live' if args.live else 'replay'
                command = [str(exe), '-mode', mode, '-suite', args.suite, '-out', str(out)]
                if args.live or args.plan_live:
                    command += ['-max-calls', str(args.max_calls), '-model-pin', args.model_pin]
                result = subprocess.run(command, cwd=ROOT, env=env)
            if out.is_dir():
                manifest = {
                    'head': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip(),
                    'image_id': subprocess.check_output(['docker', 'image', 'inspect', '--format', '{{.Id}}', image], text=True).strip(),
                    'executable_sha256': hashlib.sha256(exe.read_bytes()).hexdigest(),
                    'sources': {str(p.relative_to(ROOT)): hashlib.sha256(p.read_bytes()).hexdigest()
                                for folder in ['internal/planning', 'internal/planningeval', 'internal/agents/pipeline', 'internal/agents/validate', 'internal/product', 'internal/store', 'internal/schemas', 'cmd/evalplanning', 'db', 'scripts/eval/planning']
                                for p in (ROOT / folder).rglob('*') if p.is_file() and p.suffix in {'.go', '.sql', '.py', '.json'}},
                    'shared_services_restarted': False,
                    'external_web_requests': 0,
                    'live_models': args.live,
                    'run_mode': 'plan-live' if args.plan_live else 'live' if args.live else 'go-tests' if args.go_tests else 'intake' if args.intake_packet else 'replay',
                    'max_model_requests': args.max_calls,
                    'exit_code': result.returncode,
                    'go_tests': args.go_tests,
                    'test_timeout': args.test_timeout if args.go_tests else None,
                    'test_run': args.test_run if args.go_tests else None,
                    'database_isolation': 'dedicated_database_existing_local_server' if args.local_postgres else 'dedicated_container',
                    'intake_packet': args.intake_packet,
                }
                (out / 'manifest.json').write_text(json.dumps(manifest, ensure_ascii=False, indent=2), encoding='utf-8')
        finally:
            if args.local_postgres:
                subprocess.run(['docker', 'exec', name, 'dropdb', '-U', admin.username, db], check=True)
            else:
                subprocess.run(['docker', 'rm', '-f', name], check=True, stdout=subprocess.DEVNULL)
    raise SystemExit(result.returncode)

if __name__ == '__main__':
    main()
