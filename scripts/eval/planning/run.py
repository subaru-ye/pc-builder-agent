"""Offline evaluation in a dedicated container; never uses shared DB or .env."""
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

ROOT = Path(__file__).resolve().parents[3]

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--out', required=True)
    parser.add_argument('--suite', default='internal/planningeval/testdata/v2.0.json')
    parser.add_argument('--go-tests', nargs='+', help='Run existing Go tests in the same isolated database container')
    parser.add_argument('--intake-packet', help='Rehearse a reviewed intake using its real files in the isolated database')
    args = parser.parse_args()
    out = Path(args.out).resolve()
    if out.exists():
        raise SystemExit('Use a new output directory')
    suffix = uuid.uuid4().hex[:12]
    name, db = 'pcbuilder-planning-eval-' + suffix, 'peval_' + suffix
    env = dict(os.environ, POSTGRES_PASSWORD=secrets.token_hex(24), POSTGRES_DB=db)
    image = 'pgvector/pgvector:pg16'
    # Exact container is created by this run. No shared Compose operations.
    with tempfile.TemporaryDirectory(prefix='planning-eval-build-') as build:
        exe = Path(build) / 'evalplanning.exe'
        subprocess.run(['go', 'build', '-o', str(exe), './cmd/evalplanning'], cwd=ROOT, check=True)
        subprocess.run(['docker', 'run', '-d', '--name', name,
                        '-p', '127.0.0.1::5432', '--memory', '768m', '--cpus', '2',
                        '-e', 'POSTGRES_PASSWORD', '-e', 'POSTGRES_DB', image],
                       env=env, check=True, stdout=subprocess.DEVNULL)
        try:
            for _ in range(60):
                if subprocess.run(['docker', 'exec', name, 'pg_isready', '-h', '127.0.0.1', '-U', 'postgres', '-d', db], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
                    break
                time.sleep(.5)
            else:
                raise RuntimeError('isolated database did not become ready')
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
                result = subprocess.run(['go', 'test', '-count=1', '-p', '1', *args.go_tests], cwd=ROOT, env=env, capture_output=True, text=True, encoding='utf-8')
                (out / 'tests.log').write_bytes((result.stdout + result.stderr).encode('utf-8'))
                print(result.stdout + result.stderr)
            else:
                result = subprocess.run([str(exe), '-mode', 'replay', '-suite', args.suite, '-out', str(out)], cwd=ROOT, env=env)
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
                    'exit_code': result.returncode,
                    'go_tests': args.go_tests,
                    'intake_packet': args.intake_packet,
                }
                (out / 'manifest.json').write_text(json.dumps(manifest, ensure_ascii=False, indent=2), encoding='utf-8')
        finally:
            subprocess.run(['docker', 'rm', '-f', name], check=True, stdout=subprocess.DEVNULL)
    raise SystemExit(result.returncode)

if __name__ == '__main__':
    main()
