#!/usr/bin/env python3
"""Linux constrained-runner campaigns. No retries: every requested attempt counts."""
import argparse
import ctypes
import hashlib
import json
import math
import os
from pathlib import Path
import secrets
import signal
import stat
import subprocess
import sys
import tempfile
import time
from datetime import datetime, timezone


def child_env(inherited, state, caches):
    # Never inspect, print or propagate inherited omo credentials/socket overrides.
    env = {k: v for k, v in inherited.items()
           if not k.upper().startswith('OMO_')
           and (not k.upper().startswith('GIT_') or k.upper() == 'GIT_CONFIG_PARAMETERS')
           and not k.upper().startswith(('PLAYWRIGHT_', 'PW_TEST_', 'PWTEST', 'PWDEBUG'))}
    homes = {'HOME': 'home', 'USERPROFILE': 'home', 'APPDATA': 'config',
             'LOCALAPPDATA': 'data', 'XDG_CONFIG_HOME': 'config',
             'XDG_DATA_HOME': 'data', 'XDG_CACHE_HOME': 'cache',
             'XDG_STATE_HOME': 'state', 'XDG_RUNTIME_DIR': 'runtime', 'OMO_HOME': 'omo'}
    for key, folder in homes.items():
        path = state / folder
        path.mkdir(parents=True, exist_ok=True, mode=0o700)
        env[key] = str(path)
    env.update(caches)
    env.update(GIT_CONFIG_GLOBAL='/dev/null', GIT_CONFIG_NOSYSTEM='1',
               SHELL='/bin/sh', CI='true', GOFLAGS='')
    return env


def choose_cpus(allowed, requested):
    cpus = sorted(allowed)[:2] if requested is None else [int(c) for c in requested.split(',')]
    if len(cpus) != 2 or len(set(cpus)) != 2 or not set(cpus) <= set(allowed):
        raise ValueError('need two distinct CPUs from the actual allowed affinity')
    return cpus


def subreaper():
    # Adopt orphaned/double-forked children too (omo can start a detached server).
    if ctypes.CDLL(None, use_errno=True).prctl(36, 1, 0, 0, 0) != 0:  # PR_SET_CHILD_SUBREAPER
        raise OSError(ctypes.get_errno(), 'cannot become a child subreaper')


def descendants(pid):
    found = set()
    try:
        children = set()
        for task in Path(f'/proc/{pid}/task').iterdir():
            try:
                children.update((task / 'children').read_text().split())
            except FileNotFoundError:
                pass
    except FileNotFoundError:
        return found
    for child in children:
        child = int(child)
        found.add(child)
        found.update(descendants(child))
    return found


def reap_adopted(direct_pid):
    # Never consume Popen's child: doing so loses its true failure/signal status.
    for pid in descendants(os.getpid()):
        if pid == direct_pid:
            continue
        try:
            os.waitpid(pid, os.WNOHANG)
        except ChildProcessError:
            pass  # Still belongs to a living intermediate parent.


def wait_command(proc, timeout):
    deadline = time.monotonic() + timeout
    while True:
        reap_adopted(proc.pid)
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise subprocess.TimeoutExpired(proc.args, timeout)
        try:
            return proc.wait(timeout=min(0.05, remaining))
        except subprocess.TimeoutExpired:
            pass


def cleanup(proc):
    # Re-scan until reaped: TERM handlers can fork after a prior snapshot, and
    # detached descendants are adopted by this subreaper. Never block waitpid
    # on an unknown child or steal the direct command's true status from Popen.
    sent_term = set()
    for sig, grace in [(signal.SIGTERM, 3), (signal.SIGKILL, 5)]:
        deadline = time.monotonic() + grace
        while True:
            owned = descendants(os.getpid())
            targets = owned - sent_term if sig == signal.SIGTERM else owned
            for pid in targets:
                try:
                    os.kill(pid, sig)
                except ProcessLookupError:
                    pass
            sent_term.update(targets)
            proc.poll()
            reap_adopted(proc.pid)
            if not descendants(os.getpid()):
                return True
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                break
            time.sleep(min(0.02, remaining))  # Bounded cleanup condition polling.
    return False


def execute(command, cwd, env, cpus, folder, timeout):
    subreaper()
    started = time.monotonic()
    result = {'command': command, 'cwd': str(cwd), 'timed_out': False, 'cleaned': False}
    write_json(folder / 'command.json', result)
    with (folder / 'stdout.log').open('wb') as stdout, (folder / 'stderr.log').open('wb') as stderr:
        proc = subprocess.Popen(command, cwd=cwd, env=env, stdout=stdout, stderr=stderr,
                                start_new_session=True,
                                preexec_fn=lambda: os.sched_setaffinity(0, cpus))
        try:
            try:
                result['returncode'] = wait_command(proc, timeout)
            except subprocess.TimeoutExpired:
                result.update(returncode=-signal.SIGKILL, timed_out=True)
        finally:
            handlers = {sig: signal.signal(sig, signal.SIG_IGN) for sig in (signal.SIGINT, signal.SIGTERM)}
            try:
                result['cleaned'] = cleanup(proc)
                result['actual_returncode'] = proc.returncode
            finally:
                for sig, handler in handlers.items():
                    signal.signal(sig, handler)
            result['seconds'] = time.monotonic() - started
            write_json(folder / 'command.json', result)
    return result


class ReportError(ValueError):
    """Keep already observed test results when a report is truncated/corrupt."""
    def __init__(self, message, tests):
        super().__init__(message)
        self.tests = dict(tests)


def parse_go(raw):
    tests, running, packages, completed, errors = {}, set(), set(), set(), []
    for line in raw.splitlines():
        if not line.strip():
            continue
        try:
            event = json.loads(line)
        except json.JSONDecodeError as error:
            raise ReportError(str(error), tests) from error
        if not isinstance(event, dict):
            raise ReportError('malformed Go event', tests)
        package, name, action = event.get('Package', ''), event.get('Test'), event.get('Action')
        if not package:
            raise ReportError('Go event lacks Package', tests)
        packages.add(package)
        if name:
            key = package + '::' + name
            if action == 'run':
                running.add(key)
            if action in ('pass', 'fail', 'skip'):
                if key in tests:
                    raise ReportError('duplicate test result: ' + key, tests)
                tests[key] = {'pass': 'passed', 'fail': 'failed', 'skip': 'skipped'}[action]
        elif action in ('pass', 'fail', 'skip'):
            completed.add(package)
            if action == 'fail':
                errors.append('package failed: ' + package)
        if action == 'build-fail':
            errors.append('build failed: ' + package)
    if not tests or not any(s != 'skipped' for s in tests.values()):
        raise ReportError('Go report contains no executed tests', tests)
    if packages != completed or running - tests.keys():
        raise ReportError('Go report incomplete; packages/tests did not finish', tests)
    return {'tests': tests, 'errors': errors}


def browser_cases(data):
    if not isinstance(data, dict) or not isinstance(data.get('suites'), list):
        raise ReportError('malformed browser report', {})
    seen = set()

    def visit(suites, parents):
        if not isinstance(suites, list):
            raise ReportError('malformed browser suites', {})
        for suite in suites:
            if not isinstance(suite, dict) or not isinstance(suite.get('specs', []), list):
                raise ReportError('malformed browser suite', {})
            path = parents + [suite.get('title', '')]
            yield from visit(suite.get('suites', []), path)
            for spec in suite.get('specs', []):
                if not isinstance(spec, dict) or not isinstance(spec.get('tests'), list) or not spec['tests']:
                    raise ReportError('browser spec has no test results', {})
                for test in spec['tests']:
                    key = (f"{spec.get('file')}:{spec.get('line')}::" + ' / '.join(path + [spec['title']])
                           + '::' + test.get('projectName', ''))
                    if key in seen:
                        raise ReportError('duplicate browser test result: ' + key, {})
                    seen.add(key)
                    yield key, test
    yield from visit(data['suites'], [])
    if not seen:
        raise ReportError('browser report contains no tests', {})


def inventory_errors(expected, observed):
    missing, extra = set(expected) - observed.keys(), observed.keys() - set(expected)
    return ([f'test inventory differs: missing={sorted(missing)}, extra={sorted(extra)}']
            if missing or extra else [])


def parse_browser(data):
    tests, workers = {}, set()
    if not isinstance(data, dict) or not isinstance(data.get('errors', []), list):
        raise ReportError('malformed browser report', tests)
    errors = [json.dumps(e) for e in data.get('errors', [])]
    try:
        for key, test in browser_cases(data):
            results = test.get('results', [])
            if len(results) != 1:
                raise ReportError('browser test has missing results or retries', tests)
            result = results[0]
            status = result.get('status')
            if status not in ('passed', 'failed', 'timedOut', 'skipped', 'interrupted'):
                raise ReportError('unknown browser status: ' + str(status), tests)
            if result.get('workerIndex', -1) >= 0:
                workers.add(result['workerIndex'])
            # Expected failures remain failures: they cannot become acceptance.
            tests[key] = ('skipped' if status == 'skipped' else
                          'passed' if status == 'passed' and test.get('expectedStatus') == 'passed'
                          else 'failed')
    except ReportError as error:
        error.tests = dict(tests)
        raise
    if not tests or not any(s != 'skipped' for s in tests.values()):
        raise ReportError('browser report contains no executed tests', tests)
    if 'stats' in data:
        stats = data['stats']
        fields = ['expected', 'unexpected', 'flaky', 'skipped']
        if not isinstance(stats, dict) or any(type(stats.get(k)) is not int or stats[k] < 0 for k in fields):
            raise ReportError('malformed browser report totals', tests)
        if sum(stats[k] for k in fields) != len(tests) or stats['skipped'] != list(tests.values()).count('skipped'):
            raise ReportError('browser report totals do not match observed results', tests)
        if stats['flaky'] or stats['unexpected']:
            errors.append('browser report contains unexpected or retried/flaky tests')
    return {'tests': tests, 'errors': errors, 'workers_used': len(workers)}


def summarize(attempts, requested):
    counts, tests = {}, {}
    for attempt in attempts:
        suite = attempt['suite']
        counts[suite] = counts.get(suite, 0) + 1
        for name, status in attempt['tests'].items():
            key = suite + '::' + name
            row = tests.setdefault(key, {'attempts': 0, 'passed': 0, 'failed': 0, 'skipped': 0})
            row['attempts'] += 1
            row[status] += 1
    return {'ok': bool(attempts) and counts == requested and all(a['ok'] for a in attempts),
            'requested': requested, 'completed': counts, 'attempts': attempts, 'tests': tests}


def write_json(path, data):
    temporary = path.with_suffix(path.suffix + '.tmp')
    temporary.write_text(json.dumps(data, indent=2, sort_keys=True) + '\n')
    temporary.replace(path)


def write_summary(folder, attempts, requested):
    summary = summarize(attempts, requested)
    metadata_path = folder / 'metadata.json'
    meta = json.loads(metadata_path.read_text()) if metadata_path.exists() else {}
    summary['campaign_errors'] = meta.get('errors', [])
    summary['ok'] = summary['ok'] and not summary['campaign_errors']
    write_json(folder / 'summary.json', summary)
    lines = ['# Constrained stress results', '',
             f"Result: {'PASS' if summary['ok'] else 'FAIL / INCOMPLETE'}", '',
             '| Suite | Attempt | Result | Executed | Skipped |', '|---|---:|---|---:|---:|']
    for a in attempts:
        skipped = sum(s == 'skipped' for s in a['tests'].values())
        lines.append(f"| {a['suite']} | {a['attempt']} | {'PASS' if a['ok'] else 'FAIL'} | "
                     f"{len(a['tests']) - skipped} | {skipped} |")
        for error in a['errors']:
            lines.extend(['', f"{a['suite']} attempt {a['attempt']}: {error}"])
    lines.extend(['', '## Failure frequencies', '', '| Test | Failed / attempts |', '|---|---:|'])
    for key, count in sorted(summary['tests'].items()):
        if count['failed']:
            lines.append(f"| {key.replace('|', '/')} | {count['failed']} / {count['attempts']} |")
    for error in summary['campaign_errors']:
        lines.extend(['', error])
    if 'settings' in meta:
        settings = meta['settings']
        lines.extend(['', '## Conditions', '',
                      f"Source commit `{meta['source']['commit']}`, tree `{meta['source']['tree']}`; dirty={meta['source']['dirty']}; manifest `{meta['source']['manifest_sha256']}`.",
                      f"Affinity {meta['affinity']} from allowed {meta['allowed_affinity']}; Go workers/GOMAXPROCS {settings['go_workers']}, race=true, count=1, shuffle base {meta['base_seed']}; browser workers {settings['browser_workers']}, Chromium throttle {settings['throttle']}, retries=0.",
                      f"Full-suite selectors: {meta['full_suites']}. {meta['throttle_limit']}."])
    lines.extend(['', 'All test counts and explicit skips are in summary.json. See metadata.json and each attempt folder.'])
    (folder / 'summary.md').write_text('\n'.join(lines) + '\n')
    return summary


def arguments(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source', type=Path, default=Path(__file__).resolve().parents[2])
    parser.add_argument('--output', type=Path, default=Path('bin/stress'))
    parser.add_argument('--suite', choices=['both', 'go', 'browser'], default='both')
    parser.add_argument('--cpus', help='two CPU IDs from allowed affinity (default: first two allowed)')
    parser.add_argument('--go-runs', type=int, default=20)
    parser.add_argument('--browser-runs', type=int, default=10)
    parser.add_argument('--go-workers', type=int, default=4)
    parser.add_argument('--browser-workers', type=int, default=4)
    parser.add_argument('--throttle', type=float, default=4)
    parser.add_argument('--seed', type=int, help='Go base seed (attempt n uses seed+n-1)')
    parser.add_argument('--go-timeout', default='60m', help='per-package aggregate Go watchdog')
    parser.add_argument('--attempt-timeout', type=float, default=10800, help='whole-command watchdog seconds')
    parser.add_argument('--packages', nargs='+', default=['./...'], help='Go subset; calibration only')
    parser.add_argument('--go-test', help='Go -run selector; calibration only')
    parser.add_argument('--browser-specs', nargs='*', default=[], help='Playwright subset; calibration only')
    parser.add_argument('--browser-grep', help='Playwright title selector; calibration only')
    args = parser.parse_args(argv)
    if min(args.go_runs, args.browser_runs) < 1:
        parser.error('run counts must be positive; select a suite with --suite')
    if min(args.go_workers, args.browser_workers) <= 2:
        parser.error('workers must exceed the two constrained CPUs')
    if not math.isfinite(args.throttle) or args.throttle <= 1:
        parser.error('stress throttle must be finite and >1')
    if not math.isfinite(args.attempt_timeout) or args.attempt_timeout <= 0:
        parser.error('attempt timeout must be positive and finite')
    if args.seed is not None and not 0 <= args.seed <= 2**63 - 1 - args.go_runs:
        parser.error('seed must fit a signed 64-bit Go shuffle seed for every attempt')
    if any(p.startswith('-') for p in args.packages + args.browser_specs):
        parser.error('subset paths cannot be flags')
    return args


def go_command(args, seed):
    command = ['go', 'test', '-json', '-race', '-count=1', f'-shuffle={seed}',
               f'-p={args.go_workers}', f'-parallel={args.go_workers}', f'-timeout={args.go_timeout}',
               '-short=false', '-failfast=false']
    command += ['-run', args.go_test or '.', '-skip=']
    return command + args.packages


def browser_preflight(source):
    if (source / 'web/node_modules').is_symlink():
        raise ValueError('browser node_modules is a symlink; install private dependencies before building')
    hook = (source / 'web/e2e/support/omo.ts').read_text()
    if 'E2E_CPU_THROTTLE_RATE' not in hook:
        raise ValueError('source lacks agreed E2E_CPU_THROTTLE_RATE hook; refusing an unthrottled run')


def browser_command(args, folder):
    command = ['node', str(args.source.resolve() / 'web/node_modules/@playwright/test/cli.js'),
               'test', '--config=' + str(args.source.resolve() / 'web/playwright.config.ts'), '--project=chromium', f'--workers={args.browser_workers}',
               '--retries=0', '--repeat-each=1', '--max-failures=0', '--forbid-only', '--reporter=list,json',
               '--output=' + str(folder / 'artifacts')]
    if args.browser_grep:
        command += ['--grep', args.browser_grep]
    return command + args.browser_specs


def source_metadata(source, env):
    def git(*args):
        return subprocess.check_output(['git', '-C', str(source), *args], env=env).decode().strip()
    if Path(git('rev-parse', '--show-toplevel')).resolve() != source:
        raise ValueError('--source must be a Git checkout, including historical snapshots')
    files = subprocess.check_output(['git', '-C', str(source), 'ls-files', '-co', '--exclude-standard', '-z'], env=env)
    manifest = {}
    for name in sorted(set(files.decode().split('\0')) - {''}):
        path = source / name
        if path.is_symlink():
            manifest[name] = 'symlink:' + os.readlink(path)
        elif path.is_file():
            manifest[name] = f'{stat.S_IMODE(path.stat().st_mode):o}:' + hashlib.sha256(path.read_bytes()).hexdigest()
        else:
            manifest[name] = 'deleted'
    return {'commit': git('rev-parse', 'HEAD'), 'tree': git('rev-parse', 'HEAD^{tree}'),
            'dirty': bool(git('status', '--porcelain')), 'files_sha256': manifest,
            'manifest_sha256': hashlib.sha256(json.dumps(manifest, sort_keys=True).encode()).hexdigest()}


def interrupted(signum, _frame):
    raise InterruptedError(f'interrupted by signal {signum}')


def main(argv=None):
    args = arguments(argv)
    if sys.platform != 'linux':
        raise ValueError('this harness requires Linux affinity, /proc and a child subreaper')
    allowed = sorted(os.sched_getaffinity(0))
    cpus = choose_cpus(allowed, args.cpus)
    args.source = args.source.resolve()
    # Never overwrite an earlier campaign, even when --output is reused.
    args.output.mkdir(parents=True, exist_ok=True)
    folder = Path(tempfile.mkdtemp(prefix=datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%SZ-'),
                                   dir=args.output.resolve()))
    print('Stress artifacts: ' + str(folder), flush=True)
    requested = {}
    if args.suite in ('go', 'both'):
        requested['go'] = args.go_runs
    if args.suite in ('browser', 'both'):
        requested['browser'] = args.browser_runs
    attempts = []
    write_summary(folder, attempts, requested)
    for sig in (signal.SIGINT, signal.SIGTERM):
        signal.signal(sig, interrupted)
    # Share dependency/compiler caches, never state or omo session variables.
    clean = {k: v for k, v in os.environ.items() if not k.upper().startswith('OMO_')}
    clean['GOFLAGS'] = ''
    caches = json.loads(subprocess.check_output(['go', 'env', '-json', 'GOCACHE', 'GOPATH', 'GOMODCACHE'], env=clean))
    if 'PLAYWRIGHT_BROWSERS_PATH' in clean:
        browser_cache = clean['PLAYWRIGHT_BROWSERS_PATH']
    else:
        browser_cache = str(Path(clean.get('XDG_CACHE_HOME', str(Path.home() / '.cache'))) / 'ms-playwright')
    caches['PLAYWRIGHT_BROWSERS_PATH'] = browser_cache
    with tempfile.TemporaryDirectory(prefix='ost-') as temporary:
        env = child_env(clean, Path(temporary), caches)
        env.update(GOMAXPROCS=str(args.go_workers), CGO_ENABLED='0')
        metadata = {'source': source_metadata(args.source, env), 'allowed_affinity': allowed, 'affinity': cpus,
                    'settings': {k: str(v) if isinstance(v, Path) else v for k, v in vars(args).items()},
                    'runner_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                    'race': True, 'count': 1, 'retries': 0, 'browser_repeat_each': 1,
                    'full_suites': args.packages == ['./...'] and not args.go_test
                                   and not args.browser_specs and not args.browser_grep,
                    'throttle_limit': 'newPage before navigation; popup external resources gated; initial inline popup JS not guaranteed',
                    'browser_order': 'Playwright declaration/scheduler order; no browser shuffle',
                    'go_version': subprocess.check_output(['go', 'version'], env=env).decode().strip()}
        seed = args.seed if args.seed is not None else secrets.randbits(62)
        metadata['base_seed'] = seed
        write_json(folder / 'metadata.json', metadata)
        inventories = {}
        try:
            for suite, count in requested.items():
                if suite == 'browser':
                    browser_preflight(args.source)
                    build_folder = folder / 'browser-build'
                    build_folder.mkdir()
                    build = execute(['make', 'build'], args.source, env, cpus, build_folder, args.attempt_timeout)
                    metadata['browser_build'] = build
                    write_json(folder / 'metadata.json', metadata)
                    if build['returncode'] or build['timed_out'] or not build['cleaned']:
                        raise ValueError('browser source build failed; see browser-build logs')
                    metadata['browser_binary_sha256'] = hashlib.sha256((args.source / 'bin/omo').read_bytes()).hexdigest()
                    write_json(folder / 'metadata.json', metadata)
                    discovery = folder / 'browser-list'
                    discovery.mkdir()
                    discovery_env = dict(env, PLAYWRIGHT_JSON_OUTPUT_FILE=str(discovery / 'results.json'),
                                         E2E_CPU_THROTTLE_RATE=str(args.throttle))
                    listed = execute(browser_command(args, discovery) + ['--list'], args.source / 'web',
                                     discovery_env, cpus, discovery, args.attempt_timeout)
                    if listed['returncode'] or listed['timed_out'] or not listed['cleaned']:
                        raise ValueError('browser collection failed; see browser-list logs')
                    collected = json.loads((discovery / 'results.json').read_text())
                    inventories['browser'] = {key for key, _ in browser_cases(collected)}
                    metadata['browser_collected'] = len(inventories['browser'])
                    write_json(folder / 'metadata.json', metadata)
                for number in range(1, count + 1):
                    attempt_folder = folder / f'{suite}-{number:03d}'
                    attempt_folder.mkdir()
                    attempt = {'suite': suite, 'attempt': number, 'ok': False, 'tests': {}, 'errors': []}
                    attempts.append(attempt)
                    write_summary(folder, attempts, requested)
                    with tempfile.TemporaryDirectory(prefix='ost-') as state:
                        child = child_env(clean, Path(state), caches)
                        child.update(GOMAXPROCS=str(args.go_workers), CGO_ENABLED='1' if suite == 'go' else '0',
                                     E2E_CPU_THROTTLE_RATE=str(args.throttle),
                                     PLAYWRIGHT_JSON_OUTPUT_FILE=str(attempt_folder / 'results.json'))
                        command = (go_command(args, seed + number - 1) if suite == 'go'
                                   else browser_command(args, attempt_folder))
                        attempt['seed'] = seed + number - 1 if suite == 'go' else None
                        print(f"{suite} attempt {number}/{count}: {' '.join(command)}", flush=True)
                        try:
                            outcome = execute(command, args.source if suite == 'go' else args.source / 'web',
                                              child, cpus, attempt_folder, args.attempt_timeout)
                            attempt.update(outcome)
                            report = (parse_go((attempt_folder / 'stdout.log').read_text()) if suite == 'go'
                                      else parse_browser(json.loads((attempt_folder / 'results.json').read_text())))
                            attempt.update(report)
                            attempt['reported_tests'] = len(attempt['tests'])
                            attempt['skipped_tests'] = list(attempt['tests'].values()).count('skipped')
                            attempt['executed_tests'] = attempt['reported_tests'] - attempt['skipped_tests']
                            observed = (attempt['tests'] if suite == 'browser' else
                                        {key.split('::')[0] + '::' + key.split('::')[1].split('/')[0]: status
                                         for key, status in attempt['tests'].items()})
                            if suite in inventories:
                                attempt['errors'].extend(inventory_errors(inventories[suite], observed))
                            else:
                                inventories[suite] = set(observed)
                            if outcome['returncode'] != 0:
                                attempt['errors'].append(f"command exit {outcome['returncode']}")
                            if outcome['timed_out'] or not outcome['cleaned']:
                                attempt['errors'].append('watchdog expired or descendant cleanup failed')
                            attempt['ok'] = not attempt['errors'] and 'failed' not in attempt['tests'].values()
                        except (ValueError, OSError, TypeError, KeyError, AttributeError, InterruptedError) as error:
                            if isinstance(error, ReportError):
                                attempt['tests'] = error.tests
                            attempt['errors'].append(str(error))
                            if isinstance(error, InterruptedError):
                                raise
                        finally:
                            write_json(attempt_folder / 'attempt.json', attempt)
                            write_summary(folder, attempts, requested)
                    print(f"{suite} attempt {number}: {'PASS' if attempt['ok'] else 'FAIL'}", flush=True)
        except (ValueError, OSError, InterruptedError) as error:
            metadata.setdefault('errors', []).append(str(error))
            raise
        finally:
            metadata['source_after'] = source_metadata(args.source, env)
            if metadata['source_after'] != metadata['source']:
                metadata.setdefault('errors', []).append('source changed during campaign; provenance is not stable')
            write_json(folder / 'metadata.json', metadata)
            summary = write_summary(folder, attempts, requested)
    return 0 if summary['ok'] else 1


if __name__ == '__main__':
    try:
        sys.exit(main())
    except (ValueError, OSError, InterruptedError) as error:
        print('stress: ' + str(error), file=sys.stderr)
        sys.exit(1)
