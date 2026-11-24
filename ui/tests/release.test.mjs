import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, writeFile, readFile, readdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createHash } from 'node:crypto';

// These exercise the release boundary: wrong tag, identity or bundle selection
// must fail before a release upload can publish an unusable installer.
import * as release from '../scripts/release.mjs';

test('release tag supplies the installer version and unsigned desktop config', () => {
  for (const tag of ['0.9.2', 'v0.9.2']) {
    const config = release.releaseConfig(tag);
    assert.equal(config.version, '0.9.2');
    assert.equal(config.bundle.android.versionCode, 9002);
    assert.equal(config.bundle.android.autoIncrementVersionCode, false);
    assert.equal(config.bundle.macOS.signingIdentity, null);
    assert.equal(config.bundle.windows.certificateThumbprint, null);
    assert.equal(config.bundle.windows.signCommand, null);
    assert.equal(config.bundle.createUpdaterArtifacts, false);
  }
  for (const tag of ['', 'latest', 'v1.2', '1.2.3/evil', '01.2.3', '1.1000.0', '0.0.0', '2100.0.1']) {
    assert.throws(() => release.releaseConfig(tag), /release tag/i, tag);
  }
});

test('Android signing requires every persistent identity input without leaking values', () => {
  const env = {
    OMO_UI_ANDROID_KEYSTORE_BASE64: Buffer.from('test keystore').toString('base64'),
    OMO_UI_ANDROID_KEYSTORE_PASSWORD: 'private-store-password',
    OMO_UI_ANDROID_KEY_ALIAS: 'private-alias',
    OMO_UI_ANDROID_KEY_PASSWORD: 'private-key-password',
    OMO_UI_ANDROID_CERT_SHA256: 'AB:'.repeat(31) + 'AB',
  };
  assert.equal(release.signingInputs(env).fingerprint, 'ab'.repeat(32));
  for (const name of Object.keys(env)) {
    const missing = { ...env, [name]: '' };
    assert.throws(() => release.signingInputs(missing), error => {
      assert.match(error.message, new RegExp(name));
      assert.doesNotMatch(error.message, /private-|test keystore/);
      return true;
    });
  }
  assert.throws(() => release.signingInputs({ ...env, OMO_UI_ANDROID_KEYSTORE_BASE64: '!bad' }), /base64/i);
  assert.throws(() => release.signingInputs({ ...env, OMO_UI_ANDROID_CERT_SHA256: 'abcd' }), /SHA256/);
});

test('desktop collection selects one installer and writes an independent verifiable checksum', async () => {
  const root = await mkdtemp(join(tmpdir(), 'omo-ui-release-test-'));
  try {
    const bundle = join(root, 'bundle');
    const out = join(root, 'dist');
    await mkdir(join(bundle, 'deb'), { recursive: true });
    await writeFile(join(bundle, 'deb', 'omo ui_0.9.2_amd64.deb'), 'installer');
    await writeFile(join(bundle, 'ignored.key'), 'private');
    await release.stageDesktop(bundle, out, 'linux', 'amd64', 'v0.9.2');
    const name = 'omo-ui-0.9.2-linux-amd64.deb';
    assert.equal(await readFile(join(out, name), 'utf8'), 'installer');
    const digest = createHash('sha256').update('installer').digest('hex');
    assert.equal(await readFile(join(out, 'omo-ui-linux-amd64-SHA256SUMS'), 'utf8'), `${digest}  ${name}\n`);
    assert.deepEqual((await readdir(out)).sort(), [name, 'omo-ui-linux-amd64-SHA256SUMS'].sort());
    await writeFile(join(bundle, 'deb', 'stale.deb'), 'old');
    await assert.rejects(release.stageDesktop(bundle, out, 'linux', 'amd64', '0.9.2'), /exactly one/i);
    await assert.rejects(release.stageDesktop(bundle, out, '../bad', 'amd64', '0.9.2'), /platform/i);
    await assert.rejects(release.stageDesktop(bundle, out, 'linux', '../bad', '0.9.2'), /architecture/i);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('APK verification must report the pinned single signer before staging', () => {
  const pin = 'ab'.repeat(32);
  assert.doesNotThrow(() => release.verifyCertificate(`Signer #1 certificate SHA-256 digest: ${pin}\n`, pin));
  assert.throws(() => release.verifyCertificate('', pin), /certificate/i);
  assert.throws(() => release.verifyCertificate(`Signer #1 certificate SHA-256 digest: ${'cd'.repeat(32)}`, pin), /certificate/i);
  assert.throws(() => release.verifyCertificate(`Signer #1 certificate SHA-256 digest: ${pin}\nSigner #2 certificate SHA-256 digest: ${pin}`, pin), /certificate/i);
});

test('each desktop architecture stages the documented installer format', async () => {
  const root = await mkdtemp(join(tmpdir(), 'omo-ui-release-formats-'));
  try {
    const cases = [
      ['linux', 'arm64', 'deb', 'arm64.deb', 'omo-ui-0.9.2-linux-arm64.deb'],
      ['windows', 'amd64', 'nsis', 'x64-setup.exe', 'omo-ui-0.9.2-windows-amd64.exe'],
      ['windows', 'arm64', 'nsis', 'arm64-setup.exe', 'omo-ui-0.9.2-windows-arm64.exe'],
      ['darwin', 'amd64', 'dmg', 'x64.dmg', 'omo-ui-0.9.2-darwin-amd64.dmg'],
      ['darwin', 'arm64', 'dmg', 'aarch64.dmg', 'omo-ui-0.9.2-darwin-arm64.dmg'],
    ];
    for (const [platform, arch, folder, source, expected] of cases) {
      const bundle = join(root, platform, arch, 'bundle');
      const out = join(root, platform, arch, 'out');
      await mkdir(join(bundle, folder), { recursive: true });
      await writeFile(join(bundle, folder, source), 'package');
      await release.stageDesktop(bundle, out, platform, arch, '0.9.2');
      assert.equal(await readFile(join(out, expected), 'utf8'), 'package');
    }
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

// Tauri's bundler captures the output of the platform packagers (bundle_dmg.sh,
// NSIS, dpkg) and prints it only in verbose mode; without it a failing
// installer step reports just "failed to run <script>" and no cause.
test('every desktop installer build logs the bundler subprocess output', async () => {
  const workflow = await readFile(new URL('../../.github/workflows/ui-desktop.yml', import.meta.url), 'utf8');
  const builds = workflow.split('\n').filter(line => /\bnpm run build\b/.test(line));
  assert.equal(builds.length, 1, 'expected exactly one installer build command');
  assert.match(builds[0], /\bnpm run build -- (?:\S+ )*--verbose\b/);
});

test('the macOS disk image is packed from a folder, never from a mounted writable image', async () => {
  assert.deepEqual(release.diskImageArguments('omo ui', '/src', '/out/omo ui.dmg'),
    ['create', '-volname', 'omo ui', '-srcfolder', '/src', '-fs', 'HFS+', '-format', 'UDZO', '-ov', '/out/omo ui.dmg']);
  const root = await mkdtemp(join(tmpdir(), 'omo-ui-release-dmg-'));
  try {
    await mkdir(join(root, 'macos'), { recursive: true });
    await assert.rejects(release.buildDiskImage(root), /exactly one \.app/i);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
  const workflow = await readFile(new URL('../../.github/workflows/ui-desktop.yml', import.meta.url), 'utf8');
  for (const line of workflow.split('\n').filter(line => /platform: darwin/.test(line))) {
    assert.match(line, /bundle: app\b/, 'Tauri must build only the app; the disk image is packed by release.mjs diskimage');
  }
  assert.match(workflow, /release\.mjs diskimage/);
});

// services.gradle.org timed out on a runner (the wrapper reads with a 10 s limit),
// so the wrapper takes the same zip from Gradle's GitHub release, which the
// runners reach as reliably as the rest of the job, and checks its digest.
test('the Gradle wrapper downloads the pinned distribution from GitHub and verifies it', async () => {
  const text = await readFile(new URL('../src-tauri/gen/android/gradle/wrapper/gradle-wrapper.properties', import.meta.url), 'utf8');
  const properties = Object.fromEntries(text.split('\n').filter(line => /^[A-Za-z0-9]+=/.test(line)).map(line => line.split(/=(.*)/s).slice(0, 2)));
  const version = /gradle-(\d+\.\d+(?:\.\d+)?)-bin\.zip$/.exec(properties.distributionUrl)?.[1];
  assert.ok(version, 'distributionUrl must name a gradle-<version>-bin.zip');
  assert.equal(properties.distributionUrl,
    `https\\://github.com/gradle/gradle-distributions/releases/download/v${version}/gradle-${version}-bin.zip`);
  assert.match(properties.distributionSha256Sum, /^[a-f0-9]{64}$/);
});
