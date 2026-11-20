use omo_ui_core::{Auth, LocalBackend, LocalManager, LocalOptions, LocalRuntime, Result};
use std::{
    collections::HashMap,
    fs,
    path::Path,
    sync::{Arc, Mutex},
};
use tempfile::TempDir;

#[derive(Default)]
struct Fake {
    downloads: Mutex<HashMap<String, Vec<u8>>>,
    started: Mutex<Vec<std::path::PathBuf>>,
    output: Mutex<Vec<u8>>,
    live: Mutex<bool>,
    stopped: Mutex<Vec<String>>,
    on_stop: Mutex<Option<Box<dyn Fn() + Send>>>,
    on_download: Mutex<Option<Box<dyn Fn(&str) + Send>>>,
    stop_status: Mutex<Option<u16>>,
}
impl LocalBackend for Fake {
    fn download(&self, url: &str, _: usize) -> Result<Vec<u8>> {
        if let Some(action) = self.on_download.lock().unwrap().as_ref() {
            action(url);
        }
        self.downloads
            .lock()
            .unwrap()
            .get(url)
            .cloned()
            .ok_or("download failed".into())
    }
    fn start(&self, executable: &Path, _: &Path) -> Result<Vec<u8>> {
        self.started.lock().unwrap().push(executable.to_owned());
        *self.live.lock().unwrap() = true;
        Ok(self.output.lock().unwrap().clone())
    }
    fn control(&self, runtime: &LocalRuntime, stop: bool) -> Result<u16> {
        if stop {
            self.stopped.lock().unwrap().push(runtime.token.clone());
            *self.live.lock().unwrap() = false;
            if let Some(action) = self.on_stop.lock().unwrap().take() {
                action();
            }
            Ok(self.stop_status.lock().unwrap().unwrap_or(202))
        } else if *self.live.lock().unwrap() {
            Ok(204)
        } else {
            Err("not running".into())
        }
    }
}
fn fixture() -> (TempDir, LocalOptions, Arc<Fake>) {
    let dir = tempfile::tempdir().unwrap();
    let options = LocalOptions {
        home: dir.path().join("omo"),
        user_home: dir.path().join("user"),
        install_dir: dir.path().join("user/.local/bin"),
        normal_install_dir: dir.path().join("user/.local/bin"),
        search_path: vec![],
        os: "linux".into(),
        arch: "amd64".into(),
        shell: "bash".into(),
    };
    fs::create_dir_all(options.home.join("run")).unwrap();
    fs::create_dir_all(&options.user_home).unwrap();
    (dir, options, Arc::new(Fake::default()))
}
fn runtime(token: &str) -> LocalRuntime {
    LocalRuntime {
        endpoint: "127.0.0.1:43210".into(),
        token: token.into(),
        url: format!("http://127.0.0.1:8090/#{}", "a".repeat(64)),
        launch_id: "launch-one".into(),
    }
}
fn write_runtime(options: &LocalOptions, runtime: &LocalRuntime) {
    fs::write(
        options.home.join("run/runtime.json"),
        serde_json::to_vec(runtime).unwrap(),
    )
    .unwrap();
}
fn executable(path: &Path) {
    fs::create_dir_all(path.parent().unwrap()).unwrap();
    fs::write(path, b"existing binary").unwrap();
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(path, fs::Permissions::from_mode(0o755)).unwrap();
    }
}
#[test]
fn attaches_to_live_home_without_any_binary_or_path() {
    let (_dir, options, backend) = fixture();
    write_runtime(&options, &runtime(&"b".repeat(48)));
    *backend.live.lock().unwrap() = true;
    let connection = LocalManager::new(options, backend.clone())
        .unwrap()
        .connect()
        .unwrap();
    assert!(!connection.owned());
    assert_eq!(connection.config.auth, Auth::Token);
    assert_eq!(connection.secret, "a".repeat(64));
    assert_eq!(connection.config.origin, "http://127.0.0.1:8090/");
    connection.stop_owned().unwrap();
    assert!(backend.started.lock().unwrap().is_empty());
    assert!(backend.stopped.lock().unwrap().is_empty());
}
#[test]
fn reuses_normal_install_with_limited_path_and_owns_only_cli_winner() {
    let (_dir, options, backend) = fixture();
    let exe = options.install_dir.join("omo");
    executable(&exe);
    *backend.output.lock().unwrap() = serde_json::to_vec(&serde_json::json!({
        "endpoint":"127.0.0.1:43210", "token":"b".repeat(48), "url":format!("http://127.0.0.1:8090/#{}", "a".repeat(64)),
        "launch_id":"launch-one", "owned":true
    })).unwrap();
    let connection = LocalManager::new(options, backend.clone())
        .unwrap()
        .connect()
        .unwrap();
    assert!(connection.owned());
    assert!(connection.is_alive().unwrap());
    assert_eq!(*backend.started.lock().unwrap(), vec![exe]);
    connection.stop_owned().unwrap();
    assert_eq!(*backend.stopped.lock().unwrap(), vec!["b".repeat(48)]);
}
#[test]
fn stop_uses_captured_secret_even_when_runtime_was_replaced() {
    let (_dir, options, backend) = fixture();
    executable(&options.install_dir.join("omo"));
    let first = runtime(&"b".repeat(48));
    let mut output = serde_json::to_value(&first).unwrap();
    output["owned"] = true.into();
    *backend.output.lock().unwrap() = serde_json::to_vec(&output).unwrap();
    let connection = LocalManager::new(options.clone(), backend.clone())
        .unwrap()
        .connect()
        .unwrap();
    write_runtime(&options, &runtime(&"c".repeat(48)));
    connection.stop_owned().unwrap();
    assert_eq!(*backend.stopped.lock().unwrap(), vec!["b".repeat(48)]);
    assert!(options.home.join("run/runtime.json").exists());
}
#[test]
fn quoted_yaml_basic_credentials_are_used_for_access_without_fragment() {
    let (_dir, options, backend) = fixture();
    let mut state = runtime(&"b".repeat(48));
    state.url = "http://127.0.0.1:8090/".into();
    write_runtime(&options, &state);
    fs::write(
        options.home.join("server.yaml"),
        "auth: basic\nbasic_auth: 'user:pass:word # literal'\n",
    )
    .unwrap();
    *backend.live.lock().unwrap() = true;
    let connection = LocalManager::new(options, backend)
        .unwrap()
        .connect()
        .unwrap();
    assert_eq!(connection.config.auth, Auth::Basic);
    assert_eq!(connection.config.username, "user");
    assert_eq!(connection.secret, "pass:word # literal");
}
#[test]
fn failed_download_does_not_leave_an_executable() {
    let (_dir, options, backend) = fixture();
    assert!(
        LocalManager::new(options.clone(), backend)
            .unwrap()
            .connect()
            .is_err()
    );
    assert!(!options.install_dir.join("omo").exists());
}

// A missing checksum check would publish these deliberately corrupted bytes.
#[test]
fn checksum_failure_leaves_no_executable_or_staging_files() {
    let (_dir, options, backend) = fixture();
    let base = "https://github.com/scolastico-dev/one-man-office/releases/download/v1.2.3";
    let mut downloads = backend.downloads.lock().unwrap();
    downloads.insert(
        "https://api.github.com/repos/scolastico-dev/one-man-office/releases/latest".into(),
        br#"{"tag_name":"v1.2.3"}"#.to_vec(),
    );
    downloads.insert(
        format!("{base}/omo-linux-amd64.tar.gz"),
        b"corrupted archive".to_vec(),
    );
    downloads.insert(
        format!("{base}/SHA256SUMS"),
        format!("{}  ./omo-linux-amd64.tar.gz\n", "0".repeat(64)).into_bytes(),
    );
    drop(downloads);
    assert!(
        LocalManager::new(options.clone(), backend)
            .unwrap()
            .connect()
            .unwrap_err_string()
            .contains("checksum")
    );
    assert!(!options.install_dir.join("omo").exists());
    if options.install_dir.exists() {
        assert_eq!(fs::read_dir(options.install_dir).unwrap().count(), 0);
    }
}
trait ErrString {
    fn unwrap_err_string(self) -> String;
}
impl<T> ErrString for Result<T> {
    fn unwrap_err_string(self) -> String {
        match self {
            Err(e) => e,
            Ok(_) => panic!("expected error"),
        }
    }
}

fn release(backend: &Fake, os: &str, arch: &str, bytes: Vec<u8>, name: &str) {
    use sha2::{Digest, Sha256};
    let base = "https://github.com/scolastico-dev/one-man-office/releases/download/v1.2.3";
    let mut downloads = backend.downloads.lock().unwrap();
    downloads.insert(
        "https://api.github.com/repos/scolastico-dev/one-man-office/releases/latest".into(),
        br#"{"tag_name":"v1.2.3"}"#.to_vec(),
    );
    let ext = if os == "windows" { "zip" } else { "tar.gz" };
    let asset = format!("omo-{os}-{arch}.{ext}");
    downloads.insert(
        format!("{base}/SHA256SUMS"),
        format!("{:x}  *./{asset}\n", Sha256::digest(&bytes)).into_bytes(),
    );
    downloads.insert(format!("{base}/{asset}"), bytes);
    let mut output = serde_json::to_value(runtime(&"b".repeat(48))).unwrap();
    output["owned"] = true.into();
    *backend.output.lock().unwrap() = serde_json::to_vec(&output).unwrap();
    assert!(!name.is_empty());
}
fn tar_binary() -> Vec<u8> {
    use flate2::{Compression, write::GzEncoder};
    let mut tar = tar::Builder::new(GzEncoder::new(Vec::new(), Compression::default()));
    let mut header = tar::Header::new_gnu();
    header.set_size(14);
    header.set_mode(0o755);
    header.set_cksum();
    tar.append_data(&mut header, "omo", &b"release binary"[..])
        .unwrap();
    tar.into_inner().unwrap().finish().unwrap()
}
#[test]
fn missing_install_publishes_verified_release_and_adds_shell_path_once() {
    let (_dir, options, backend) = fixture();
    release(&backend, "linux", "amd64", tar_binary(), "omo");
    let manager = LocalManager::new(options.clone(), backend.clone()).unwrap();
    assert!(manager.connect().unwrap().owned());
    assert_eq!(
        fs::read(options.install_dir.join("omo")).unwrap(),
        b"release binary"
    );
    let profile = fs::read_to_string(options.user_home.join(".bashrc")).unwrap();
    assert!(profile.contains(options.install_dir.to_str().unwrap()));
    assert_eq!(profile.matches("export PATH=").count(), 1);
    manager.connect().unwrap();
    assert_eq!(
        fs::read_to_string(options.user_home.join(".bashrc")).unwrap(),
        profile
    );
}
#[test]
fn refuses_nonlocal_control_state_before_transport_or_launch() {
    let (_dir, options, backend) = fixture();
    let mut state = runtime(&"b".repeat(48));
    state.endpoint = "203.0.113.1:8080".into();
    write_runtime(&options, &state);
    *backend.live.lock().unwrap() = true;
    assert!(
        LocalManager::new(options, backend.clone())
            .unwrap()
            .connect()
            .is_err()
    );
    assert!(backend.started.lock().unwrap().is_empty());
}
#[cfg(unix)]
#[test]
fn refuses_runtime_links_and_named_pipes_without_opening_them() {
    use std::os::unix::fs::symlink;
    let (dir, options, backend) = fixture();
    let other = dir.path().join("other");
    fs::write(
        &other,
        serde_json::to_vec(&runtime(&"b".repeat(48))).unwrap(),
    )
    .unwrap();
    symlink(other, options.home.join("run/runtime.json")).unwrap();
    assert!(
        LocalManager::new(options.clone(), backend.clone())
            .unwrap()
            .connect()
            .is_err()
    );
    fs::remove_file(options.home.join("run/runtime.json")).unwrap();
    let path = std::ffi::CString::new(
        options
            .home
            .join("run/runtime.json")
            .as_os_str()
            .as_encoded_bytes(),
    )
    .unwrap();
    assert_eq!(unsafe { libc::mkfifo(path.as_ptr(), 0o600) }, 0);
    assert!(
        LocalManager::new(options, backend)
            .unwrap()
            .connect()
            .is_err()
    );
}

// Removing identity inspection would silently discard ownership after a transient control failure.
#[test]
fn unreachable_captured_runtime_keeps_ownership_and_surfaces_error() {
    let (_dir, options, backend) = fixture();
    executable(&options.install_dir.join("omo"));
    let first = runtime(&"b".repeat(48));
    let mut output = serde_json::to_value(&first).unwrap();
    output["owned"] = true.into();
    *backend.output.lock().unwrap() = serde_json::to_vec(&output).unwrap();
    let connection = LocalManager::new(options.clone(), backend.clone())
        .unwrap()
        .connect()
        .unwrap();
    write_runtime(&options, &first);
    *backend.live.lock().unwrap() = false;
    assert!(connection.is_alive().is_err());
    assert!(connection.stop_owned().is_err());
    write_runtime(&options, &runtime(&"c".repeat(48)));
    assert!(!connection.is_alive().unwrap());
    connection.stop_owned().unwrap();
    assert!(backend.stopped.lock().unwrap().is_empty());
}
#[test]
fn discovers_binary_in_registered_autostart_path_without_shell_path() {
    let (dir, mut options, backend) = fixture();
    options.os = if cfg!(windows) { "windows" } else { "linux" }.into();
    let name = if cfg!(windows) { "omo.exe" } else { "omo" };
    let install = dir.path().join("registered/bin");
    executable(&install.join(name));
    fs::write(
        options.home.join("run/autostart.json"),
        serde_json::to_vec(&serde_json::json!({
            "directory": dir.path(), "omo_home": options.home, "path": install
        }))
        .unwrap(),
    )
    .unwrap();
    let mut output = serde_json::to_value(runtime(&"b".repeat(48))).unwrap();
    output["owned"] = false.into();
    *backend.output.lock().unwrap() = serde_json::to_vec(&output).unwrap();
    let connection = LocalManager::new(options, backend.clone())
        .unwrap()
        .connect()
        .unwrap();
    assert!(!connection.owned());
    assert_eq!(*backend.started.lock().unwrap(), vec![install.join(name)]);
}

#[test]
fn concurrent_install_is_preserved_without_replacing_the_winner() {
    let (_dir, options, backend) = fixture();
    release(&backend, "linux", "amd64", tar_binary(), "omo");
    let target = options.install_dir.join("omo");
    let raced = target.clone();
    *backend.on_download.lock().unwrap() = Some(Box::new(move |url| {
        if url.ends_with("SHA256SUMS") {
            executable(&raced);
        }
    }));
    LocalManager::new(options.clone(), backend)
        .unwrap()
        .connect()
        .unwrap();
    assert_eq!(fs::read(target).unwrap(), b"existing binary");
    assert_eq!(fs::read_dir(options.install_dir).unwrap().count(), 1);
}
#[test]
fn refused_captured_stop_after_replacement_does_not_target_new_server() {
    let (_dir, options, backend) = fixture();
    executable(&options.install_dir.join("omo"));
    let first = runtime(&"b".repeat(48));
    let mut output = serde_json::to_value(&first).unwrap();
    output["owned"] = true.into();
    *backend.output.lock().unwrap() = serde_json::to_vec(&output).unwrap();
    write_runtime(&options, &first);
    let connection = LocalManager::new(options.clone(), backend.clone())
        .unwrap()
        .connect()
        .unwrap();
    // Connect first saw an unavailable runtime, then the CLI started our exact launch.
    let changed = options.clone();
    *backend.on_stop.lock().unwrap() = Some(Box::new(move || {
        write_runtime(&changed, &runtime(&"c".repeat(48)))
    }));
    *backend.stop_status.lock().unwrap() = Some(403);
    connection.stop_owned().unwrap();
    assert_eq!(*backend.stopped.lock().unwrap(), vec!["b".repeat(48)]);
}
#[test]
fn successful_stop_waits_for_original_runtime_cleanup() {
    let (_dir, options, backend) = fixture();
    executable(&options.install_dir.join("omo"));
    let first = runtime(&"b".repeat(48));
    let mut output = serde_json::to_value(&first).unwrap();
    output["owned"] = true.into();
    *backend.output.lock().unwrap() = serde_json::to_vec(&output).unwrap();
    let connection = LocalManager::new(options.clone(), backend.clone())
        .unwrap()
        .connect()
        .unwrap();
    write_runtime(&options, &first);
    let state = options.home.join("run/runtime.json");
    *backend.on_stop.lock().unwrap() = Some(Box::new(move || fs::remove_file(&state).unwrap()));
    connection.stop_owned().unwrap();
    assert!(!options.home.join("run/runtime.json").exists());
    assert!(!connection.is_alive().unwrap());
}
#[test]
fn installs_matching_archive_for_each_supported_desktop_target() {
    use std::io::Write;
    for os in ["linux", "darwin", "windows"] {
        for arch in ["amd64", "arm64"] {
            let (_dir, mut options, backend) = fixture();
            options.os = os.into();
            options.arch = arch.into();
            let (name, archive) = if os == "windows" {
                let mut zip = zip::ZipWriter::new(std::io::Cursor::new(Vec::new()));
                zip.start_file("omo.exe", zip::write::SimpleFileOptions::default())
                    .unwrap();
                zip.write_all(b"release binary").unwrap();
                ("omo.exe", zip.finish().unwrap().into_inner())
            } else {
                ("omo", tar_binary())
            };
            release(&backend, os, arch, archive, name);
            LocalManager::new(options.clone(), backend)
                .unwrap()
                .connect()
                .unwrap();
            assert_eq!(
                fs::read(options.install_dir.join(name)).unwrap(),
                b"release binary",
                "{os}/{arch}"
            );
        }
    }
}
#[test]
fn control_http_authenticates_exact_endpoint_and_refuses_redirects() {
    use omo_ui_core::SystemLocalBackend;
    use std::{
        io::{Read, Write},
        net::TcpListener,
        thread,
    };
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let endpoint = listener.local_addr().unwrap().to_string();
    let expected_endpoint = endpoint.clone();
    let server = thread::spawn(move || {
        let (mut stream, _) = listener.accept().unwrap();
        let mut request = Vec::new();
        let mut byte = [0];
        while !request.ends_with(b"\r\n\r\n") {
            stream.read_exact(&mut byte).unwrap();
            request.push(byte[0]);
        }
        let request = String::from_utf8(request).unwrap().to_ascii_lowercase();
        let valid = request.starts_with("get /status ")
            && request.contains(&format!("\r\nhost: {expected_endpoint}\r\n"))
            && request.contains(&format!("\r\nauthorization: bearer {}\r\n", "b".repeat(48)))
            && !request.contains("\r\norigin:")
            && !request.contains("\r\nsec-fetch-site:");
        stream.write_all(b"HTTP/1.1 302 Found\r\nLocation: http://127.0.0.1:1/leak\r\nContent-Length: 0\r\nConnection: close\r\n\r\n").unwrap();
        valid
    });
    let mut state = runtime(&"b".repeat(48));
    state.endpoint = endpoint;
    assert_eq!(SystemLocalBackend.control(&state, false).unwrap(), 302);
    assert!(server.join().unwrap());
}

// Treating discovery-only directories as PATH entries would leave a fresh CLI unavailable in shells.
#[test]
fn environment_keeps_normal_install_separate_from_actual_path() {
    use std::ffi::OsString;
    let env = HashMap::from([
        ("HOME", "/isolated/user"),
        ("PATH", "/usr/bin:/bin"),
        ("OMO_INSTALL_DIR", "/isolated/custom"),
    ]);
    let options =
        LocalOptions::from_environment("linux", "x86_64", |key| env.get(key).map(OsString::from))
            .unwrap();
    assert_eq!(options.install_dir, Path::new("/isolated/custom"));
    assert_eq!(
        options.normal_install_dir,
        Path::new("/isolated/user/.local/bin")
    );
    assert_eq!(
        options.search_path,
        vec![
            std::path::PathBuf::from("/usr/bin"),
            std::path::PathBuf::from("/bin")
        ]
    );
}
#[test]
fn windows_environment_uses_appdata_locations_even_with_install_override() {
    use std::ffi::OsString;
    let env = HashMap::from([
        ("USERPROFILE", "/isolated/user"),
        ("APPDATA", "/relocated/roaming"),
        ("LOCALAPPDATA", "/relocated/local"),
        ("PATH", "/windows/system;/tools"),
        ("OMO_INSTALL_DIR", "/isolated/custom"),
    ]);
    let options = LocalOptions::from_environment("windows", "aarch64", |key| {
        env.get(key).map(OsString::from)
    })
    .unwrap();
    assert_eq!(options.home, Path::new("/relocated/roaming/omo"));
    assert_eq!(
        options.normal_install_dir,
        Path::new("/relocated/local/Programs/omo")
    );
    assert_eq!(options.install_dir, Path::new("/isolated/custom"));
    assert_eq!(options.arch, "arm64");
    assert_eq!(options.search_path.len(), 2);
}

#[test]
fn attachment_matches_ownership_only_for_same_authenticated_instance() {
    let (_dir, options, backend) = fixture();
    executable(&options.install_dir.join("omo"));
    let first = runtime(&"b".repeat(48));
    let mut output = serde_json::to_value(&first).unwrap();
    output["owned"] = true.into();
    *backend.output.lock().unwrap() = serde_json::to_vec(&output).unwrap();
    let manager = LocalManager::new(options.clone(), backend).unwrap();
    let owned = manager.connect().unwrap();
    write_runtime(&options, &first);
    let attached = manager.connect().unwrap();
    assert!(!attached.owned());
    assert!(owned.same_instance(&attached));
    // The launch id and endpoint remain the same; the per-run token must decide identity.
    write_runtime(&options, &runtime(&"c".repeat(48)));
    let replacement = manager.connect().unwrap();
    assert!(!owned.same_instance(&replacement));
}

// This executable fixture runs through the real process backend. The CLI must
// see an absent or empty home so globalhome.Open can initialize its layout.
#[test]
fn cli_capture_files_do_not_initialize_global_home() {
    use omo_ui_core::SystemLocalBackend;
    let dir = tempfile::tempdir().unwrap();
    let home = dir.path().join("fresh-home");
    SystemLocalBackend
        .start(&std::env::current_exe().unwrap(), &home)
        .unwrap();
    assert!(home.with_extension("observed").exists());
    assert!(!home.exists());
}

// `omo ui-local start` arguments also select this libtest helper when the test
// executable is used as the fixture. No shell, installed omo or ambient env is
// needed; SystemLocalBackend supplies OMO_HOME only to this child process.
#[test]
fn ui_local_start_fixture() {
    let args: Vec<_> = std::env::args().skip(1).collect();
    if args != ["ui-local", "start"] {
        return;
    }
    let home = std::path::PathBuf::from(std::env::var_os("OMO_HOME").unwrap());
    assert!(
        !home.exists() || fs::read_dir(&home).unwrap().next().is_none(),
        "CLI found an unmarked nonempty global home before initialization"
    );
    fs::write(home.with_extension("observed"), b"CLI saw untouched home").unwrap();
}

#[test]
fn owned_control_is_captured_before_basic_credentials_can_fail_validation() {
    let (_dir, options, backend) = fixture();
    executable(&options.install_dir.join("omo"));
    fs::write(
        options.home.join("server.yaml"),
        "auth: basic\nbasic_auth: \"user:pass\\tword\"\n",
    )
    .unwrap();
    let mut state = runtime(&"b".repeat(48));
    state.url = "http://127.0.0.1:8090/".into();
    let mut output = serde_json::to_value(&state).unwrap();
    output["owned"] = true.into();
    *backend.output.lock().unwrap() = serde_json::to_vec(&output).unwrap();
    let mut captured = None;
    let result = LocalManager::new(options, backend.clone())
        .unwrap()
        .connect_with_capture(|control| {
            captured = Some(control);
            Ok(())
        });
    assert!(result.is_err());
    let control = captured.expect("owned runtime control must survive invalid board credentials");
    assert!(control.owned());
    assert!(control.is_alive().unwrap());
    assert_eq!(
        control.board_origin().as_deref(),
        Some("http://127.0.0.1:8090/")
    );
    control.stop_owned().unwrap();
    assert_eq!(*backend.stopped.lock().unwrap(), vec!["b".repeat(48)]);
}
#[test]
fn invalid_attached_credentials_never_capture_or_stop_runtime() {
    let (_dir, options, backend) = fixture();
    fs::write(
        options.home.join("server.yaml"),
        "auth: basic\nbasic_auth: \"user:pass\\tword\"\n",
    )
    .unwrap();
    let mut state = runtime(&"b".repeat(48));
    state.url = "http://127.0.0.1:8090/".into();
    write_runtime(&options, &state);
    *backend.live.lock().unwrap() = true;
    let mut captured = false;
    let result = LocalManager::new(options, backend.clone())
        .unwrap()
        .connect_with_capture(|_| {
            captured = true;
            Ok(())
        });
    assert!(result.is_err());
    assert!(!captured);
    assert!(backend.stopped.lock().unwrap().is_empty());
}

// Success-path fixtures do not exercise native ownership storage. Production
// callers must use connect_with_capture and retain the supplied control handle.
trait ConnectFixture {
    fn connect(&self) -> Result<omo_ui_core::LocalConnection>;
}
impl ConnectFixture for LocalManager {
    fn connect(&self) -> Result<omo_ui_core::LocalConnection> {
        self.connect_with_capture(|_| Ok(()))
    }
}
