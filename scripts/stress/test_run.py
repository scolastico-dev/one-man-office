"""Runner contract checks; no omo processes or real suites are launched."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock
import signal
import time

SPEC = importlib.util.spec_from_file_location('stress_run', Path(__file__).with_name('run.py'))
runner = importlib.util.module_from_spec(SPEC)
if SPEC.loader and Path(SPEC.origin).exists():
    SPEC.loader.exec_module(runner)


class Contracts(unittest.TestCase):
    def test_child_environment_drops_all_inherited_omo_and_isolates_state(self):
        with tempfile.TemporaryDirectory() as tmp:
            env = runner.child_env({'OMO_AGENT_ID': 'secret', 'omo_socket': 'live',
                                    'OmO_HOME': 'live', 'PATH': '/bin',
                                    'GIT_CONFIG_PARAMETERS': "'commit.gpgSign=false'"}, Path(tmp), {})
            self.assertEqual({k for k in env if k.upper().startswith('OMO_')}, {'OMO_HOME'})
            self.assertEqual(env['OMO_HOME'], str(Path(tmp) / 'omo'))
            self.assertEqual(env['HOME'], str(Path(tmp) / 'home'))
            self.assertEqual(env['GIT_CONFIG_GLOBAL'], '/dev/null')
            self.assertEqual(env['GIT_CONFIG_PARAMETERS'], "'commit.gpgSign=false'")

    def test_allowed_affinity_and_oversubscription_are_required(self):
        self.assertEqual(runner.choose_cpus({4, 9, 12}, None), [4, 9])
        self.assertEqual(runner.choose_cpus({4, 9, 12}, '12,4'), [12, 4])
        for requested in ['0,1', '4', '4,4']:
            with self.assertRaises(ValueError):
                runner.choose_cpus({4, 9}, requested)
        with self.assertRaises(ValueError):
            runner.choose_cpus({4}, None)

    def test_go_accounting_records_each_failure_and_skips_and_requires_completion(self):
        events = [
            {'Action': 'start', 'Package': 'p'},
            {'Action': 'run', 'Package': 'p', 'Test': 'TestA'},
            {'Action': 'fail', 'Package': 'p', 'Test': 'TestA'},
            {'Action': 'skip', 'Package': 'p', 'Test': 'TestB'},
            {'Action': 'fail', 'Package': 'p'},
        ]
        report = runner.parse_go('\n'.join(json.dumps(e) for e in events))
        self.assertEqual(report['tests'], {'p::TestA': 'failed', 'p::TestB': 'skipped'})
        self.assertTrue(report['errors'])
        with self.assertRaises(ValueError):
            runner.parse_go('{broken')
        for events in [[], [{'Action': 'run', 'Package': 'p', 'Test': 'A'}]]:
            with self.assertRaises(ValueError):
                runner.parse_go('\n'.join(json.dumps(e) for e in events))

    def test_browser_accounting_checks_expected_failure_and_infrastructure(self):
        data = {'suites': [{'title': 's', 'specs': [
            {'title': 'case', 'file': 'a.spec.ts', 'line': 3, 'tests': [
                {'projectName': 'chromium', 'expectedStatus': 'passed',
                 'results': [{'status': 'failed'}]},
            ]},
        ]}], 'errors': []}
        report = runner.parse_browser(data)
        self.assertEqual(list(report['tests'].values()), ['failed'])
        data['suites'][0]['specs'][0]['tests'][0]['results'] = []
        with self.assertRaises(ValueError):
            runner.parse_browser(data)
        with self.assertRaises(ValueError):
            runner.parse_browser({'suites': [], 'errors': []})

    def test_one_success_cannot_erase_failure(self):
        attempts = [
            {'suite': 'go', 'ok': False, 'tests': {'p::A': 'failed'}, 'errors': []},
            {'suite': 'go', 'ok': True, 'tests': {'p::A': 'passed'}, 'errors': []},
        ]
        summary = runner.summarize(attempts, {'go': 2})
        self.assertFalse(summary['ok'])
        self.assertEqual(summary['tests']['go::p::A'],
                         {'attempts': 2, 'passed': 1, 'failed': 1, 'skipped': 0})
        self.assertFalse(runner.summarize(attempts[1:], {'go': 2})['ok'])

    def test_full_suites_and_acceptance_counts_are_defaults(self):
        args = runner.arguments([])
        self.assertEqual((args.go_runs, args.browser_runs, args.packages), (20, 10, ['./...']))
        self.assertEqual(args.browser_specs, [])
        with tempfile.TemporaryDirectory() as tmp:
            cmd = runner.go_command(args, 17)
            self.assertIn('-race', cmd)
            self.assertIn('-count=1', cmd)
            self.assertIn('-shuffle=17', cmd)
            browser = runner.browser_command(args, Path(tmp))
            self.assertIn('--retries=0', browser)
            self.assertIn('--repeat-each=1', browser)
            self.assertNotIn('--grep', browser)

    def test_launch_constrains_descendants_and_preserves_raw_log(self):
        with tempfile.TemporaryDirectory() as tmp:
            folder = Path(tmp)
            cpus = sorted(os.sched_getaffinity(0))[:2]
            result = runner.execute([sys.executable, '-c',
                                    'import os; print(sorted(os.sched_getaffinity(0)))'],
                                   folder, runner.child_env(os.environ, folder / "state", {}), cpus, folder, 10)
            self.assertEqual(result['returncode'], 0)
            self.assertEqual(json.loads((folder / 'stdout.log').read_text()), cpus)

    def test_watchdog_failure_terminates_owned_children(self):
        with tempfile.TemporaryDirectory() as tmp:
            folder = Path(tmp)
            result = runner.execute([sys.executable, '-c',
                                     'import os, time; os.fork(); time.sleep(100)'],
                                    folder, runner.child_env(os.environ, folder / "state", {}), sorted(os.sched_getaffinity(0))[:2],
                                    folder, 0.2)
            self.assertTrue(result['timed_out'])
            self.assertTrue(result['cleaned'])


    def test_truncated_report_keeps_known_failures(self):
        raw = '\n'.join(json.dumps(e) for e in [
            {'Action': 'run', 'Package': 'p', 'Test': 'A'},
            {'Action': 'fail', 'Package': 'p', 'Test': 'A'},
            {'Action': 'run', 'Package': 'p', 'Test': 'B'},
        ])
        with self.assertRaises(ValueError) as caught:
            runner.parse_go(raw)
        self.assertEqual(caught.exception.tests, {'p::A': 'failed'})

    def test_campaign_exit_failure_and_reports_survive_later_success(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            tools = root / 'tools'
            tools.mkdir()
            counter = root / 'counter'
            fake = tools / 'go'
            fake.write_text('#!' + sys.executable + '\n' + f"""
import json, os, sys
from pathlib import Path
if sys.argv[1] == 'env':
 print(json.dumps({{'GOCACHE': '/tmp/stress-contract-cache', 'GOPATH': '/tmp/stress-contract-path', 'GOMODCACHE': '/tmp/stress-contract-mod'}}))
 sys.exit(0)
if sys.argv[1] == 'version':
 print('go version contract')
 sys.exit(0)
counter = Path({str(counter)!r})
n = int(counter.read_text()) + 1 if counter.exists() else 1
counter.write_text(str(n))
assert not any(k.upper().startswith('OMO_') and k != 'OMO_HOME' for k in os.environ)
assert os.environ['HOME'].startswith('/tmp/ost-')
assert len(os.sched_getaffinity(0)) == 2
assert os.environ['GOFLAGS'] == ''
for action in ['start', 'run', 'fail' if n == 1 else 'pass', 'fail' if n == 1 else 'pass']:
 event = {{'Action': action, 'Package': 'contract'}}
 if action == 'run' or action in ('fail', 'pass') and 'done' not in locals():
  event['Test'] = 'TestContract'
  if action != 'run': done = True
 print(json.dumps(event))
sys.exit(1 if n == 1 else 0)
""")
            fake.chmod(0o755)
            env = {k: v for k, v in os.environ.items() if not k.upper().startswith('OMO_')}
            env['PATH'] = str(tools) + os.pathsep + os.environ['PATH']
            env['GOFLAGS'] = '-run=WrongSubset -short'
            command = [sys.executable, str(Path(SPEC.origin)), '--suite', 'go',
                       '--go-runs', '2', '--output', str(root / 'reports')]
            completed = subprocess.run(command, env=env, capture_output=True, text=True, timeout=30)
            self.assertEqual(completed.returncode, 1, completed.stdout + completed.stderr)
            campaign = next((root / 'reports').iterdir())
            summary = json.loads((campaign / 'summary.json').read_text())
            self.assertEqual(summary['completed'], {'go': 2})
            self.assertEqual(summary['tests']['go::contract::TestContract']['failed'], 1)
            self.assertEqual(summary['tests']['go::contract::TestContract']['passed'], 1)
            # Running the same output destination creates a distinct campaign.
            again = subprocess.run(command, env=env, capture_output=True, text=True, timeout=30)
            self.assertEqual(again.returncode, 0, again.stdout + again.stderr)
            self.assertEqual(len(list((root / 'reports').iterdir())), 2)
            self.assertFalse(json.loads((campaign / 'summary.json').read_text())['ok'])

    def test_signal_stops_adopted_detached_child_and_reports_interruption(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            ready = root / 'ready'
            wrapper = root / 'wrapper.py'
            wrapper.write_text(f"""
import importlib.util, os, signal, sys
from pathlib import Path
spec=importlib.util.spec_from_file_location('runner', {str(Path(SPEC.origin))!r})
r=importlib.util.module_from_spec(spec); spec.loader.exec_module(r)
signal.signal(signal.SIGTERM, r.interrupted)
try:
 r.execute([sys.executable, '-c', {('import os, time; from pathlib import Path; '
  'pid=os.fork(); '
  f'Path({str(ready)!r}).write_text(str(pid)) if pid else os.setsid(); '
  'time.sleep(100)')!r}], Path({str(root)!r}), os.environ.copy(),
  sorted(os.sched_getaffinity(0))[:2], Path({str(root)!r}), 30)
except InterruptedError:
 sys.exit(7)
""")
            proc = subprocess.Popen([sys.executable, str(wrapper)], env=runner.child_env(os.environ, root / 'state', {}))
            try:
                deadline = time.monotonic() + 10
                while not ready.exists():
                    if time.monotonic() > deadline or proc.poll() is not None:
                        self.fail('child did not acknowledge readiness')
                    time.sleep(0.01)  # Bounded condition polling, no synchronization guess.
                child = int(ready.read_text())
                proc.send_signal(signal.SIGTERM)
                self.assertEqual(proc.wait(timeout=10), 7)
                self.assertFalse(Path(f'/proc/{child}').exists())
                outcome = json.loads((root / 'command.json').read_text())
                self.assertTrue(outcome['cleaned'])
            finally:
                if proc.poll() is None:
                    proc.kill()
                    proc.wait()


    def test_adopted_zombies_are_reaped_while_direct_command_still_runs(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            code = """
import os, sys, time
from pathlib import Path
child = os.fork()
if child == 0:
    orphan = os.fork()
    if orphan == 0:
        os._exit(0)
    Path('orphan').write_text(str(orphan))
    os._exit(0)
os.waitpid(child, 0)
pid = int(Path('orphan').read_text())
deadline = time.monotonic() + 2
while Path(f'/proc/{pid}').exists():
    if time.monotonic() > deadline:
        sys.exit(9)
    time.sleep(0.01)
# A nonzero status must survive concurrent orphan reaping.
sys.exit(3)
"""
            result = runner.execute([sys.executable, '-c', code], root,
                                    runner.child_env(os.environ, root / 'state', {}),
                                    sorted(os.sched_getaffinity(0))[:2], root, 10)
            self.assertEqual(result['returncode'], 3)
            self.assertTrue(result['cleaned'])


    def test_browser_preflight_refuses_shared_mutable_dependencies(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            support = root / 'web/e2e/support'
            support.mkdir(parents=True)
            (support / 'omo.ts').write_text('E2E_CPU_THROTTLE_RATE')
            dependencies = root / 'dependencies'
            dependencies.mkdir()
            (root / 'web/node_modules').symlink_to(dependencies, target_is_directory=True)
            with self.assertRaisesRegex(ValueError, 'symlink'):
                runner.browser_preflight(root)

    def test_direct_signal_status_survives_orphan_reaping(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            result = runner.execute([sys.executable, '-c',
                                     'import os, signal; os.kill(os.getpid(), signal.SIGTERM)'],
                                    root, runner.child_env(os.environ, root / 'state', {}),
                                    sorted(os.sched_getaffinity(0))[:2], root, 10)
            self.assertEqual(result['returncode'], -signal.SIGTERM)
            self.assertTrue(result['cleaned'])


    def test_browser_report_rejects_malformed_structure(self):
        for data in [[], {'suites': None}, {'suites': [{'specs': None}]}]:
            with self.assertRaises(ValueError):
                runner.parse_browser(data)

    def test_campaign_preflight_failure_is_explained_in_summary(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            runner.write_json(root / 'metadata.json', {'errors': ['build failed']})
            summary = runner.write_summary(root, [], {'browser': 2})
            self.assertFalse(summary['ok'])
            self.assertEqual(summary['campaign_errors'], ['build failed'])
            self.assertIn('build failed', (root / 'summary.md').read_text())


    def test_inherited_go_selectors_and_git_repository_overrides_are_removed(self):
        with tempfile.TemporaryDirectory() as tmp:
            env = runner.child_env({'GOFLAGS': '-run=OnlyOne -short', 'GIT_DIR': '/live/repo',
                                    'GIT_WORK_TREE': '/live', 'GIT_CONFIG_COUNT': '1',
                                    'GIT_CONFIG_KEY_0': 'core.hooksPath', 'GIT_CONFIG_VALUE_0': '/live/hooks',
                                    'GIT_CONFIG_PARAMETERS': "'commit.gpgSign=false'"}, Path(tmp), {})
            self.assertEqual(env['GOFLAGS'], '')
            for key in ['GIT_DIR', 'GIT_WORK_TREE', 'GIT_CONFIG_COUNT', 'GIT_CONFIG_KEY_0', 'GIT_CONFIG_VALUE_0']:
                self.assertNotIn(key, env)
            self.assertEqual(env['GIT_CONFIG_PARAMETERS'], "'commit.gpgSign=false'")

    def test_empty_browser_spec_and_mismatched_totals_cannot_hide_tests(self):
        spec = {'title': 'case', 'file': 'a.spec.ts', 'line': 1, 'tests': [
            {'projectName': 'chromium', 'expectedStatus': 'passed', 'results': [{'status': 'passed'}]}]}
        for empty in [dict(spec, title='empty', tests=[]), {'title': 'missing tests'}]:
            with self.assertRaises(ValueError):
                runner.parse_browser({'suites': [{'specs': [spec, empty]}]})
        with self.assertRaises(ValueError):
            runner.parse_browser({'suites': [{'specs': [spec]}],
                                  'stats': {'expected': 2, 'unexpected': 0, 'flaky': 0, 'skipped': 0}})


    def test_cleanup_rechecks_descendants_created_during_termination(self):
        # Model a child born after the first post-TERM snapshot. An unbounded
        # generic waitpid here would wait forever; only fresh ownership scans
        # can find and terminate the newly adopted descendant.
        proc = mock.Mock()
        proc.pid = 101
        proc.returncode = 0
        scans = iter([{101}, {101}, {101, 202}, {202}, set(), set()])
        killed = []
        def observed(_pid):
            return next(scans, set())
        with mock.patch.object(runner, 'descendants', side_effect=observed), \
             mock.patch.object(runner.os, 'kill', side_effect=lambda pid, sig: killed.append((pid, sig))), \
             mock.patch.object(runner, 'reap_adopted'), \
             mock.patch.object(runner.os, 'waitpid', side_effect=AssertionError('blocking generic waitpid forbidden')):
            self.assertTrue(runner.cleanup(proc))
        self.assertTrue(any(pid == 202 for pid, _ in killed))


    def test_discovered_browser_inventory_cannot_silently_disappear(self):
        tests = [{'title': 'one', 'file': 'a.spec.ts', 'line': 1, 'tests': [
            {'projectName': 'chromium', 'expectedStatus': 'passed', 'results': []}]},
                 {'title': 'two', 'file': 'a.spec.ts', 'line': 2, 'tests': [
            {'projectName': 'chromium', 'expectedStatus': 'passed', 'results': []}]}]
        data = {'suites': [{'title': 'file', 'specs': tests}]}
        inventory = set(key for key, _ in runner.browser_cases(data))
        self.assertEqual(len(inventory), 2)
        self.assertTrue(runner.inventory_errors(inventory, {next(iter(inventory)): 'passed'}))
        self.assertFalse(runner.inventory_errors(inventory, dict.fromkeys(inventory, 'passed')))

    def test_inherited_playwright_runtime_and_report_overrides_are_removed(self):
        with tempfile.TemporaryDirectory() as tmp:
            env = runner.child_env({'PW_TEST_REPORTER': '/live/reporter.js',
                                    'PLAYWRIGHT_JSON_OUTPUT_FILE': '/live/result.json',
                                    'PWTEST_DEBUG': '1', 'PWDEBUG': '1'}, Path(tmp), {})
            self.assertFalse(any(k.startswith(('PW_TEST_', 'PLAYWRIGHT_', 'PWTEST', 'PWDEBUG')) for k in env))


    def test_browser_preparation_rebuilds_stale_assets_despite_inherited_build_controls(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'bin').mkdir()
            (root / 'web/dist').mkdir(parents=True)
            (root / 'web/package.json').write_text(json.dumps({'scripts': {
                'build': 'node -e "require(\'fs\').writeFileSync(\'dist/index.html\', \'current-web\')"'}}))
            (root / 'Makefile').write_text(
                '.PHONY: build web\nWEB_DEPS := $(if $(NO_WEB),,web)\n'
                'build: $(WEB_DEPS)\n\t@printf current-binary > bin/omo\n'
                'web:\n\t@npm --prefix web run build\n')
            (root / 'override.mk').write_text('override NO_WEB = 1\n')
            (root / 'injected.npmrc').write_text('script-shell=/bin/true\n')
            controls = [
                {'NO_WEB': '1'}, {'MAKEFLAGS': 'n'}, {'GNUMAKEFLAGS': 'n'},
                {'MAKEFLAGS': 't'}, {'MAKEFLAGS': 'e', 'WEB_DEPS': ''},
                {'MAKEFILES': str(root / 'override.mk')},
                {'npm_config_script_shell': '/bin/true'},
                {'NPM_CONFIG_SCRIPT_SHELL': '/bin/true'},
                {'NPM_CONFIG_USERCONFIG': str(root / 'injected.npmrc')},
            ]
            for number, override in enumerate(controls):
                with self.subTest(override=override):
                    (root / 'bin/omo').write_text('stale-binary')
                    (root / 'web/dist/index.html').write_text('stale-web')
                    folder = root / str(number)
                    folder.mkdir()
                    env = runner.child_env(dict(os.environ, **override), folder / 'state', {})
                    result = runner.execute(['make', 'build'], root, env,
                                            sorted(os.sched_getaffinity(0))[:2], folder, 20)
                    self.assertEqual(result['returncode'], 0, (folder / 'stderr.log').read_text())
                    self.assertEqual((root / 'web/dist/index.html').read_text(), 'current-web')
                    self.assertEqual((root / 'bin/omo').read_text(), 'current-binary')

    def test_make_dry_run_cannot_hide_failed_preparation_with_stale_artifacts(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'Makefile').write_text('.PHONY: build\nbuild:\n\tfalse\n')
            (root / 'bin').mkdir()
            (root / 'bin/omo').write_text('stale-binary')
            for number, variable in enumerate(['MAKEFLAGS', 'GNUMAKEFLAGS']):
                with self.subTest(variable=variable):
                    folder = root / str(number)
                    folder.mkdir()
                    env = runner.child_env(dict(os.environ, **{variable: 'n'}), folder / 'state', {})
                    result = runner.execute(['make', 'build'], root, env,
                                            sorted(os.sched_getaffinity(0))[:2], folder, 10)
                    self.assertNotEqual(result['returncode'], 0)
                    self.assertEqual((root / 'bin/omo').read_text(), 'stale-binary')


if __name__ == '__main__':
    unittest.main()
