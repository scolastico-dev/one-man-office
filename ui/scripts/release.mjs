import { mkdtemp, mkdir, readdir, readFile, writeFile, copyFile, rm, symlink } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';

const ui = resolve(dirname(fileURLToPath(import.meta.url)), '..');

export function releaseConfig(tag) {
  // Match the existing stable release tags, optionally prefixed with v. Bounds
  // prevent collisions in Tauri's major*1000000 + minor*1000 + patch versionCode.
  const match = /^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.exec(tag ?? '');
  if (!match) throw new Error('Release tag must be [v]MAJOR.MINOR.PATCH');
  const [major, minor, patch] = match.slice(1).map(Number);
  const versionCode = major * 1000000 + minor * 1000 + patch;
  if (minor > 999 || patch > 999 || versionCode < 1 || versionCode > 2100000000) {
    throw new Error('Release tag exceeds Android versionCode limits (minor/patch <= 999)');
  }
  return {
    version: `${major}.${minor}.${patch}`,
    bundle: {
      createUpdaterArtifacts: false,
      macOS: { signingIdentity: null },
      windows: { certificateThumbprint: null, signCommand: null },
      android: { versionCode, autoIncrementVersionCode: false },
    },
  };
}

export function signingInputs(env) {
  for (const name of ['OMO_UI_ANDROID_KEYSTORE_BASE64', 'OMO_UI_ANDROID_KEYSTORE_PASSWORD',
    'OMO_UI_ANDROID_KEY_ALIAS', 'OMO_UI_ANDROID_KEY_PASSWORD', 'OMO_UI_ANDROID_CERT_SHA256']) {
    if (!env[name]?.trim()) throw new Error(`Missing ${name}; configure the persistent installation key as documented in wiki/ui.md`);
  }
  const encoded = env.OMO_UI_ANDROID_KEYSTORE_BASE64.replace(/\s/g, '');
  const key = Buffer.from(encoded, 'base64');
  if (!key.length || key.toString('base64') !== encoded) throw new Error('OMO_UI_ANDROID_KEYSTORE_BASE64 must contain valid base64');
  const fingerprint = env.OMO_UI_ANDROID_CERT_SHA256.replace(/:/g, '').trim().toLowerCase();
  if (!/^[a-f0-9]{64}$/.test(fingerprint)) throw new Error('OMO_UI_ANDROID_CERT_SHA256 must be a SHA256 certificate fingerprint');
  return { key, fingerprint };
}

export function verifyCertificate(output, fingerprint) {
  const digests = [...output.matchAll(/^Signer #\d+ certificate SHA-256 digest: ([a-f0-9]+)\s*$/gim)];
  if (digests.length !== 1 || digests[0][1].toLowerCase() !== fingerprint) {
    throw new Error('APK certificate does not match OMO_UI_ANDROID_CERT_SHA256; refusing to stage APK');
  }
}

async function stage(source, out, name, checksumName) {
  await mkdir(out, { recursive: true });
  const digest = createHash('sha256').update(await readFile(source)).digest('hex');
  await copyFile(source, join(out, name));
  await writeFile(join(out, checksumName), `${digest}  ${name}\n`);
}

export async function stageDesktop(bundle, out, platform, arch, tag) {
  const types = { linux: ['deb', '.deb'], windows: ['nsis', '.exe'], darwin: ['dmg', '.dmg'] };
  if (!Object.hasOwn(types, platform)) throw new Error('Unsupported desktop platform');
  if (!['amd64', 'arm64'].includes(arch)) throw new Error('Unsupported desktop architecture');
  const { version } = releaseConfig(tag);
  const [folder, extension] = types[platform];
  const dir = join(bundle, folder);
  const files = (await readdir(dir, { withFileTypes: true })).filter(file => file.isFile() && file.name.endsWith(extension));
  if (files.length !== 1) throw new Error(`Expected exactly one ${extension} installer in ${dir}`);
  await stage(join(dir, files[0].name), out, `omo-ui-${version}-${platform}-${arch}${extension}`, `omo-ui-${platform}-${arch}-SHA256SUMS`);
}

// The disk image is packed from a folder in one hdiutil call. Tauri's own dmg
// bundler mounts a writable image and ejects it again, and that eject fails on
// the macOS runners ("Resource busy") whenever something still holds the volume.
export function diskImageArguments(volume, source, image) {
  return ['create', '-volname', volume, '-srcfolder', source, '-fs', 'HFS+', '-format', 'UDZO', '-ov', image];
}

export async function buildDiskImage(bundle) {
  const apps = (await readdir(join(bundle, 'macos'), { withFileTypes: true }))
    .filter(entry => entry.isDirectory() && entry.name.endsWith('.app'));
  if (apps.length !== 1) throw new Error(`Expected exactly one .app bundle in ${join(bundle, 'macos')}`);
  const name = apps[0].name.slice(0, -'.app'.length);
  const out = join(bundle, 'dmg');
  const work = await mkdtemp(join(tmpdir(), 'omo-ui-dmg-'));
  try {
    const source = join(work, 'image');
    await mkdir(source);
    // ditto keeps the bundle's symbolic links, modes and extended attributes.
    run('ditto', [join(bundle, 'macos', apps[0].name), join(source, apps[0].name)]);
    await symlink('/Applications', join(source, 'Applications'));
    await rm(out, { recursive: true, force: true });
    await mkdir(out, { recursive: true });
    run('hdiutil', diskImageArguments(name, source, join(out, `${name}.dmg`)));
  } finally {
    await rm(work, { recursive: true, force: true });
  }
}

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: ui, stdio: 'inherit', ...options });
  if (result.error || result.status !== 0) throw new Error(`${command} failed; no release artifact was staged`);
  return result.stdout ?? '';
}

async function android(tag, env) {
  const config = releaseConfig(tag);
  const { key, fingerprint } = signingInputs(env); // Fail before a costly build.
  const tools = join(env.ANDROID_HOME ?? '', 'build-tools', '36.0.0');
  const apksigner = join(tools, 'apksigner');
  const work = await mkdtemp(join(tmpdir(), 'omo-ui-sign-'));
  try {
    const keystore = join(work, 'installation.jks');
    await writeFile(keystore, key, { mode: 0o600 });
    // The compiler/Gradle never needs to receive the installation identity.
    const buildEnv = Object.fromEntries(Object.entries(env).filter(([name]) => !name.startsWith('OMO_UI_ANDROID_')));
    const apkDir = join(ui, 'src-tauri/gen/android/app/build/outputs/apk/universal/release');
    // Only this build's APK is eligible for publication; reject stale output.
    await rm(apkDir, { recursive: true, force: true });
    run(process.execPath, [join(ui, 'node_modules/@tauri-apps/cli/tauri.js'), 'android', 'build',
      '--ci', '--target', 'aarch64', '--apk', '--config', JSON.stringify(config)], { env: buildEnv });
    const files = (await readdir(apkDir)).filter(file => file.endsWith('.apk'));
    if (files.length !== 1) throw new Error('Expected exactly one release APK');
    const aligned = join(work, 'aligned.apk');
    const signed = join(work, 'signed.apk');
    run(join(tools, 'zipalign'), ['-P', '16', '-f', '4', join(apkDir, files[0]), aligned]);
    // Capture signing-tool output so a keystore failure cannot echo private input.
    run(apksigner, ['sign', '--ks', keystore, '--ks-key-alias', env.OMO_UI_ANDROID_KEY_ALIAS,
      '--ks-pass', 'env:OMO_UI_ANDROID_KEYSTORE_PASSWORD', '--key-pass', 'env:OMO_UI_ANDROID_KEY_PASSWORD',
      '--out', signed, aligned], { env, stdio: 'pipe', encoding: 'utf8' });
    const verified = run(apksigner, ['verify', '--verbose', '--print-certs', signed], { stdio: 'pipe', encoding: 'utf8' });
    verifyCertificate(verified, fingerprint);
    await stage(signed, join(ui, 'dist'), `omo-ui-${config.version}-android-arm64.apk`, 'omo-ui-android-arm64-SHA256SUMS');
    console.log(`Verified installation certificate SHA256: ${fingerprint}`);
  } finally {
    await rm(work, { recursive: true, force: true });
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const [command, ...args] = process.argv.slice(2);
  try {
    if (command === 'config') console.log(JSON.stringify(releaseConfig(args[0])));
    else if (command === 'desktop') await stageDesktop(...args);
    else if (command === 'diskimage') await buildDiskImage(args[0]);
    else if (command === 'android') await android(args[0], process.env);
    else throw new Error('Usage: release.mjs config TAG | diskimage BUNDLE | desktop BUNDLE OUT PLATFORM ARCH TAG | android TAG');
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
