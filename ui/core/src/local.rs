//! Desktop discovery uses the same home, installation and authenticated lifecycle as the CLI.
use crate::{Auth, Config, Mode, Result};
use serde::{Deserialize, Serialize};
use std::{
    fs,
    io::{Read, Write},
    net::SocketAddr,
    path::{Path, PathBuf},
    sync::Arc,
    time::{Duration, Instant},
};
use url::Url;

const RELEASE_API: &str =
    "https://api.github.com/repos/scolastico-dev/one-man-office/releases/latest";
const MAX_ARCHIVE: usize = 256 * 1024 * 1024;
const MAX_RUNTIME: usize = 1024 * 1024;

#[derive(Clone, Serialize, Deserialize, Debug, PartialEq)]
pub struct LocalRuntime {
    pub endpoint: String,
    pub token: String,
    pub url: String,
    #[serde(default)]
    pub launch_id: String,
}
impl LocalRuntime {
    fn validate(&self) -> Result<()> {
        let address: SocketAddr = self
            .endpoint
            .parse()
            .map_err(|_| "Invalid local omo control endpoint.")?;
        if !address.ip().is_loopback()
            || address.port() == 0
            || self.token.is_empty()
            || self.token.len() > 256
            || !self.token.bytes().all(|b| b.is_ascii_hexdigit())
        {
            return Err("Invalid local omo control state.".into());
        }
        Ok(())
    }
}
/// Explicit paths and platform allow callers to isolate discovery without changing process globals.
#[derive(Clone)]
pub struct LocalOptions {
    pub home: PathBuf,
    pub user_home: PathBuf,
    pub install_dir: PathBuf,
    pub normal_install_dir: PathBuf,
    pub search_path: Vec<PathBuf>,
    pub os: String,
    pub arch: String,
    pub shell: String,
}
impl LocalOptions {
    pub fn from_env() -> Result<Self> {
        Self::from_environment(std::env::consts::OS, std::env::consts::ARCH, |name| {
            std::env::var_os(name)
        })
    }
    /// Resolve a launch environment without mutating process-wide variables.
    pub fn from_environment(
        os: &str,
        arch: &str,
        env: impl Fn(&str) -> Option<std::ffi::OsString>,
    ) -> Result<Self> {
        if cfg!(target_os = "android") || os == "android" {
            return Err("Android requires an external omo host.".into());
        }
        let windows = os == "windows";
        let env_path = |name| env(name).filter(|s| !s.is_empty()).map(PathBuf::from);
        let user_home = env_path(if windows { "USERPROFILE" } else { "HOME" })
            .ok_or("Cannot locate the user home.")?;
        let default_home = if windows {
            env_path("APPDATA")
                .ok_or("Cannot locate APPDATA.")?
                .join("omo")
        } else {
            user_home.join(".local/omo")
        };
        let normal_install_dir = if windows {
            env_path("LOCALAPPDATA")
                .ok_or("Cannot locate LOCALAPPDATA.")?
                .join("Programs/omo")
        } else {
            user_home.join(".local/bin")
        };
        let search_path = env("PATH")
            .map(|path| {
                if cfg!(windows) == windows {
                    std::env::split_paths(&path).collect()
                } else {
                    // Explicit environments can describe another desktop platform.
                    path.to_string_lossy()
                        .split(if windows { ';' } else { ':' })
                        .filter(|p| !p.is_empty())
                        .map(PathBuf::from)
                        .collect()
                }
            })
            .unwrap_or_default();
        Ok(Self {
            home: env_path("OMO_HOME").unwrap_or(default_home),
            user_home,
            install_dir: env_path("OMO_INSTALL_DIR").unwrap_or_else(|| normal_install_dir.clone()),
            normal_install_dir,
            search_path,
            os: match os {
                "macos" => "darwin",
                other => other,
            }
            .into(),
            arch: match arch {
                "x86_64" => "amd64",
                "aarch64" => "arm64",
                other => other,
            }
            .into(),
            shell: env("SHELL")
                .map(|value| value.to_string_lossy().into_owned())
                .unwrap_or_default(),
        })
    }
    fn binary_name(&self) -> &str {
        if self.os == "windows" {
            "omo.exe"
        } else {
            "omo"
        }
    }
}
/// Only external operations are replaceable; discovery, guarded reads, validation and installation remain real.
pub trait LocalBackend: Send + Sync {
    fn download(&self, url: &str, limit: usize) -> Result<Vec<u8>>;
    fn start(&self, executable: &Path, home: &Path) -> Result<Vec<u8>>;
    fn control(&self, runtime: &LocalRuntime, stop: bool) -> Result<u16>;
}
pub struct SystemLocalBackend;
impl LocalBackend for SystemLocalBackend {
    fn download(&self, address: &str, limit: usize) -> Result<Vec<u8>> {
        let url = Url::parse(address).map_err(|_| "Invalid release URL.")?;
        if url.scheme() != "https" {
            return Err("Release downloads require HTTPS.".into());
        }
        let client = reqwest::blocking::Client::builder()
            .timeout(Duration::from_secs(120))
            .redirect(reqwest::redirect::Policy::custom(|attempt| {
                if attempt.previous().len() >= 8 || attempt.url().scheme() != "https" {
                    attempt.stop()
                } else {
                    attempt.follow()
                }
            }))
            .build()
            .map_err(|_| "Cannot initialize release download.")?;
        let reply = client
            .get(url)
            .header("User-Agent", "omo-ui-installer")
            .send()
            .map_err(|_| "Cannot download omo release.")?
            .error_for_status()
            .map_err(|_| "Release download failed.")?;
        bounded(reply, limit, "Release download is too large.")
    }
    fn start(&self, executable: &Path, home: &Path) -> Result<Vec<u8>> {
        #[cfg(target_os = "android")]
        {
            let _ = (executable, home);
            Err("Android requires an external omo host.".into())
        }
        #[cfg(not(target_os = "android"))]
        {
            use std::process::{Command, Stdio};
            use wait_timeout::ChildExt;
            // Do not populate OMO_HOME before globalhome.Open writes its marker.
            // These private captures clean themselves up after the child is reaped.
            let output =
                tempfile::NamedTempFile::new().map_err(|_| "Cannot capture omo startup.")?;
            let error =
                tempfile::NamedTempFile::new().map_err(|_| "Cannot capture omo startup errors.")?;
            let mut command = Command::new(executable);
            command
                .args(["ui-local", "start"])
                .env_clear()
                .envs(child_environment(std::env::vars_os()))
                .env("OMO_HOME", home)
                .stdin(Stdio::null())
                .stdout(output.reopen().map_err(|_| "Cannot capture omo startup.")?)
                .stderr(
                    error
                        .reopen()
                        .map_err(|_| "Cannot capture omo startup errors.")?,
                );
            #[cfg(windows)]
            {
                use std::os::windows::process::CommandExt;
                command.creation_flags(0x08000000);
            }
            let mut child = command
                .spawn()
                .map_err(|_| "Cannot start omo. Check the configured executable.")?;
            match child.wait_timeout(Duration::from_secs(45)) {
                Ok(Some(status)) if status.success() => bounded(output.reopen().map_err(|_| "Cannot read omo startup.")?, MAX_RUNTIME, "Omo startup response is too large."),
                Ok(Some(_)) => Err("Omo failed to start. Run `omo update` if the CLI lacks ui-local support; inspect OMO_HOME/run/omo.log.".into()),
                _ => { let _ = child.kill(); let _ = child.wait(); Err("Omo startup did not finish; inspect OMO_HOME/run/omo.log.".into()) }
            }
        }
    }
    fn control(&self, runtime: &LocalRuntime, stop: bool) -> Result<u16> {
        runtime.validate()?;
        let client = reqwest::blocking::Client::builder()
            .no_proxy()
            .redirect(reqwest::redirect::Policy::none())
            .timeout(Duration::from_secs(2))
            .build()
            .map_err(|_| "Cannot initialize local control transport.")?;
        let request = client
            .request(
                if stop {
                    reqwest::Method::POST
                } else {
                    reqwest::Method::GET
                },
                format!(
                    "http://{}{path}",
                    runtime.endpoint,
                    path = if stop { "/stop" } else { "/status" }
                ),
            )
            .bearer_auth(&runtime.token);
        Ok(request
            .send()
            .map_err(|_| "Cannot reach the local omo control endpoint.")?
            .status()
            .as_u16())
    }
}
pub struct LocalManager {
    options: LocalOptions,
    backend: Arc<dyn LocalBackend>,
}
impl LocalManager {
    pub fn from_env() -> Result<Self> {
        Self::new(LocalOptions::from_env()?, Arc::new(SystemLocalBackend))
    }
    pub fn new(options: LocalOptions, backend: Arc<dyn LocalBackend>) -> Result<Self> {
        if cfg!(target_os = "android")
            || !matches!(options.os.as_str(), "linux" | "darwin" | "windows")
            || !matches!(options.arch.as_str(), "amd64" | "arm64")
        {
            return Err("Local omo is supported on desktop amd64 and arm64 only.".into());
        }
        Ok(Self { options, backend })
    }
    /// Retain every owned control handle in `capture` before preparing board
    /// credentials. A preparation failure must not discard a newly started server.
    pub fn connect_with_capture(
        &self,
        mut capture: impl FnMut(Arc<LocalControl>) -> Result<()>,
    ) -> Result<LocalConnection> {
        if let Some(runtime) = read_runtime(&self.options.home)? {
            runtime.validate()?;
            if self.backend.control(&runtime, false) == Ok(204) {
                return self.connection(self.control(runtime, false));
            }
        }
        let executable = self
            .discover()?
            .unwrap_or(self.options.install_dir.join(self.options.binary_name()));
        if !is_executable(&executable) {
            self.install(&executable)?;
        }
        let raw = self.backend.start(&executable, &self.options.home)?;
        if raw.len() > MAX_RUNTIME {
            return Err("Omo startup response is too large.".into());
        }
        #[derive(Deserialize)]
        struct Started {
            #[serde(flatten)]
            runtime: LocalRuntime,
            owned: bool,
        }
        let started: Started = serde_json::from_slice(&raw)
            .map_err(|_| "Invalid omo startup response. Update the omo CLI.")?;
        let control = self.control(started.runtime, started.owned);
        if control.owned() {
            capture(control.clone())?;
        }
        control.runtime.validate()?;
        if self.backend.control(&control.runtime, false) != Ok(204) {
            return Err("Omo did not report a live authenticated runtime.".into());
        }
        self.connection(control)
    }
    fn control(&self, runtime: LocalRuntime, owned: bool) -> Arc<LocalControl> {
        Arc::new(LocalControl {
            runtime,
            owned,
            home: self.options.home.clone(),
            backend: self.backend.clone(),
        })
    }
    fn connection(&self, control: Arc<LocalControl>) -> Result<LocalConnection> {
        let mut url = Url::parse(&control.runtime.url).map_err(|_| "Invalid local board URL.")?;
        let token = url.fragment().unwrap_or("").to_owned();
        url.set_fragment(None);
        let mut config = Config {
            id: Config::new_id(),
            mode: Mode::Local,
            origin: url.to_string(),
            auth: Auth::None,
            username: String::new(),
            notifications: true,
            remember: false,
            stop_with_window: false,
        };
        let secret = if !token.is_empty() {
            config.auth = Auth::Token;
            token
        } else {
            #[derive(Deserialize, Default)]
            struct Server {
                auth: Option<String>,
                basic_auth: Option<String>,
            }
            let settings = match guarded_read(&self.options.home.join("server.yaml"), 64 * 1024)? {
                Some(raw) => serde_yaml::from_slice::<Server>(&raw)
                    .map_err(|_| "Cannot parse server.yaml authentication.")?,
                None => Server::default(),
            };
            match settings.auth.as_deref().unwrap_or("token") {
                "none" => String::new(),
                "basic" => {
                    let basic = settings
                        .basic_auth
                        .ok_or("Missing local Basic credentials.")?;
                    let (user, password) = basic
                        .split_once(':')
                        .ok_or("Invalid local Basic credentials.")?;
                    if password.is_empty() {
                        return Err("Invalid local Basic credentials.".into());
                    }
                    config.auth = Auth::Basic;
                    config.username = user.into();
                    password.into()
                }
                _ => {
                    return Err(
                        "Local runtime has no login token. Restart omo to refresh its access URL."
                            .into(),
                    );
                }
            }
        };
        config.validate(false)?;
        config.authorization(&secret)?;
        Ok(LocalConnection {
            config,
            secret,
            control,
        })
    }
    fn discover(&self) -> Result<Option<PathBuf>> {
        let mut paths = vec![self.options.install_dir.join(self.options.binary_name())];
        // OMO_INSTALL_DIR can differ from the normal installation; inspect both before PATH.
        paths.push(
            self.options
                .normal_install_dir
                .join(self.options.binary_name()),
        );
        paths.extend(
            self.options
                .search_path
                .iter()
                .filter(|p| p.is_absolute())
                .map(|p| p.join(self.options.binary_name())),
        );
        if let Some(raw) = guarded_read(&self.options.home.join("run/autostart.json"), MAX_RUNTIME)?
        {
            #[derive(Deserialize)]
            struct Autostart {
                omo_home: PathBuf,
                path: String,
            }
            if let Ok(metadata) = serde_json::from_slice::<Autostart>(&raw) {
                if metadata.omo_home == self.options.home {
                    // Registration stores the actual launch PATH, not an executable.
                    let separator = if self.options.os == "windows" {
                        ';'
                    } else {
                        ':'
                    };
                    paths.extend(
                        metadata
                            .path
                            .split(separator)
                            .map(PathBuf::from)
                            .filter(|p| p.is_absolute())
                            .map(|p| p.join(self.options.binary_name())),
                    );
                }
            }
        }
        Ok(paths.into_iter().find(|p| is_executable(p)))
    }
    fn install(&self, target: &Path) -> Result<()> {
        use sha2::{Digest, Sha256};
        #[derive(Deserialize)]
        struct Release {
            tag_name: String,
        }
        let release: Release =
            serde_json::from_slice(&self.backend.download(RELEASE_API, MAX_RUNTIME)?)
                .map_err(|_| "Cannot determine the latest omo release.")?;
        if release.tag_name.is_empty()
            || !release
                .tag_name
                .bytes()
                .all(|b| b.is_ascii_alphanumeric() || b".-_+".contains(&b))
        {
            return Err("Invalid omo release tag.".into());
        }
        let ext = if self.options.os == "windows" {
            "zip"
        } else {
            "tar.gz"
        };
        let asset = format!("omo-{}-{}.{ext}", self.options.os, self.options.arch);
        let base = format!(
            "https://github.com/scolastico-dev/one-man-office/releases/download/{}",
            release.tag_name
        );
        let archive = self
            .backend
            .download(&format!("{base}/{asset}"), MAX_ARCHIVE)?;
        if archive.len() > MAX_ARCHIVE {
            return Err("Release download is too large.".into());
        }
        let sums = self
            .backend
            .download(&format!("{base}/SHA256SUMS"), MAX_RUNTIME)?;
        let sums = std::str::from_utf8(&sums).map_err(|_| "Invalid release checksums.")?;
        let expected = sums
            .lines()
            .find_map(|line| {
                let mut fields = line.split_whitespace();
                let hash = fields.next()?;
                let name = fields.next()?;
                if name.trim_start_matches('*').trim_start_matches("./") == asset
                    && fields.next().is_none()
                {
                    Some(hash)
                } else {
                    None
                }
            })
            .ok_or("Release checksum is missing.")?;
        if !format!("{:x}", Sha256::digest(&archive)).eq_ignore_ascii_case(expected) {
            return Err("Omo release checksum verification failed.".into());
        }
        let binary = extract(
            &archive,
            self.options.binary_name(),
            self.options.os == "windows",
        )?;
        let parent = target.parent().ok_or("Invalid omo installation path.")?;
        fs::create_dir_all(parent).map_err(|_| "Cannot create omo installation directory.")?;
        let mut staged = tempfile::NamedTempFile::new_in(parent)
            .map_err(|_| "Cannot stage omo installation.")?;
        staged
            .write_all(&binary)
            .and_then(|_| staged.as_file().sync_all())
            .map_err(|_| "Cannot write omo executable.")?;
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            staged
                .as_file()
                .set_permissions(fs::Permissions::from_mode(0o755))
                .map_err(|_| "Cannot make omo executable.")?;
        }
        match staged.persist_noclobber(target) {
            Ok(_) => (),
            Err(e)
                if e.error.kind() == std::io::ErrorKind::AlreadyExists && is_executable(target) =>
            {
                ()
            }
            Err(_) => return Err("Cannot install omo without replacing an existing file.".into()),
        }
        self.integrate_path(parent)
    }
    fn integrate_path(&self, install: &Path) -> Result<()> {
        if self.options.search_path.iter().any(|p| p == install) {
            return Ok(());
        }
        if self.options.os == "windows" {
            #[cfg(windows)]
            {
                use winreg::{
                    RegKey,
                    enums::{HKEY_CURRENT_USER, KEY_READ, KEY_WRITE},
                };
                let environment = RegKey::predef(HKEY_CURRENT_USER)
                    .open_subkey_with_flags("Environment", KEY_READ | KEY_WRITE)
                    .map_err(|_| "Cannot open user PATH.")?;
                let previous = match environment.get_raw_value("Path") {
                    Ok(value) => Some((value.vtype as u32, value.bytes)),
                    Err(error) if error.kind() == std::io::ErrorKind::NotFound => None,
                    Err(_) => return Err("Cannot read the existing user PATH.".into()),
                };
                if let Some((kind, bytes)) = append_windows_path(previous, install)? {
                    let vtype = if kind == 2 {
                        winreg::enums::REG_EXPAND_SZ
                    } else {
                        winreg::enums::REG_SZ
                    };
                    environment
                        .set_raw_value("Path", &winreg::RegValue { bytes, vtype })
                        .map_err(|_| "Cannot update user PATH.")?;
                }
                return Ok(());
            }
            #[cfg(not(windows))]
            return Ok(());
        }
        let profile = if self.options.shell.ends_with("zsh") {
            ".zshrc"
        } else if self.options.shell.ends_with("bash") {
            ".bashrc"
        } else {
            ".profile"
        };
        let profile = self.options.user_home.join(profile);
        let path = install.to_str().ok_or("Installation path is not UTF-8.")?;
        let escaped = path
            .replace('\\', "\\\\")
            .replace('"', "\\\"")
            .replace('$', "\\$")
            .replace('`', "\\`");
        if path.chars().any(char::is_control) {
            return Err("Installation path contains control characters.".into());
        }
        let line = format!("export PATH=\"{escaped}:$PATH\"");
        let existing = guarded_read(&profile, MAX_RUNTIME)?.unwrap_or_default();
        if String::from_utf8_lossy(&existing)
            .lines()
            .any(|v| v == line)
        {
            return Ok(());
        }
        let mut options = fs::OpenOptions::new();
        options.append(true).create(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options
                .custom_flags(libc::O_NOFOLLOW | libc::O_NONBLOCK)
                .mode(0o600);
        }
        let mut file = options
            .open(&profile)
            .map_err(|_| "Cannot add omo to the shell PATH.")?;
        if !file
            .metadata()
            .map_err(|_| "Cannot inspect shell profile.")?
            .is_file()
        {
            return Err("Shell profile is not a regular file.".into());
        }
        writeln!(file, "\n# omo\n{line}").map_err(|_| "Cannot add omo to the shell PATH.".into())
    }
}
/// A prepared board connection and its independently captured control handle.
pub struct LocalConnection {
    pub config: Config,
    pub secret: String,
    pub control: Arc<LocalControl>,
}
impl LocalConnection {
    pub fn owned(&self) -> bool {
        self.control.owned()
    }
    pub fn same_instance(&self, other: &Self) -> bool {
        self.control.same_instance(&other.control)
    }
    pub fn is_alive(&self) -> Result<bool> {
        self.control.is_alive()
    }
    pub fn stop_owned(&self) -> Result<()> {
        self.control.stop_owned()
    }
}
/// Ownership is a captured authenticated launch, independent of board credential
/// validation, never a PID or credentials read from a replacement runtime file.
pub struct LocalControl {
    runtime: LocalRuntime,
    owned: bool,
    home: PathBuf,
    backend: Arc<dyn LocalBackend>,
}
impl LocalControl {
    /// Derive a board origin for release policy without gating ownership capture.
    pub fn board_origin(&self) -> Option<String> {
        let mut url = Url::parse(&self.runtime.url).ok()?;
        url.set_fragment(None);
        if !matches!(url.scheme(), "http" | "https")
            || url.host_str().is_none()
            || !url.username().is_empty()
            || url.password().is_some()
            || url.path() != "/"
            || url.query().is_some()
        {
            return None;
        }
        Some(url.to_string())
    }
    pub fn owned(&self) -> bool {
        self.owned
    }
    /// Attachments preserve existing ownership proof only for this authenticated launch.
    pub fn same_instance(&self, other: &Self) -> bool {
        self.runtime.endpoint == other.runtime.endpoint && self.runtime.token == other.runtime.token
    }
    pub fn is_alive(&self) -> Result<bool> {
        self.runtime.validate()?;
        match self.backend.control(&self.runtime, false) {
            Ok(204) => Ok(true),
            result => {
                if !self.still_current()? || !lifecycle_held(&self.home)? {
                    return Ok(false);
                }
                match result {
                    Err(error) => Err(error),
                    Ok(status) => Err(format!("Local omo status returned HTTP {status}.")),
                }
            }
        }
    }
    fn still_current(&self) -> Result<bool> {
        Ok(read_runtime(&self.home)?
            .is_some_and(|r| r.token == self.runtime.token && r.endpoint == self.runtime.endpoint))
    }
    pub fn stop_owned(&self) -> Result<()> {
        if !self.owned {
            return Ok(());
        }
        self.runtime.validate()?;
        if !self.is_alive()? {
            return Ok(());
        }
        match self.backend.control(&self.runtime, true) {
            Ok(202) => (),
            reply => {
                if !self.still_current()? || !lifecycle_held(&self.home)? {
                    return Ok(());
                }
                return Err(match reply {
                    Err(error) => error,
                    Ok(status) => format!("Local omo stop returned HTTP {status}."),
                });
            }
        }
        let deadline = Instant::now() + cleanup_grace(&self.home)?;
        loop {
            let state = read_runtime(&self.home)?;
            if state.as_ref().is_none_or(|r| {
                r.token != self.runtime.token || r.endpoint != self.runtime.endpoint
            }) {
                return Ok(());
            }
            if !lifecycle_held(&self.home)? {
                return Ok(());
            }
            if Instant::now() >= deadline {
                return Err("Stop requested, but local omo cleanup has not finished.".into());
            }
            std::thread::sleep(Duration::from_millis(25));
        }
    }
}
fn is_executable(path: &Path) -> bool {
    let Ok(metadata) = fs::metadata(path) else {
        return false;
    };
    if !metadata.is_file() {
        return false;
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        metadata.permissions().mode() & 0o111 != 0
    }
    #[cfg(not(unix))]
    {
        true
    }
}
fn read_runtime(home: &Path) -> Result<Option<LocalRuntime>> {
    guarded_read(&home.join("run/runtime.json"), MAX_RUNTIME)?
        .map(|raw| {
            serde_json::from_slice(&raw).map_err(|_| "Invalid local omo runtime metadata.".into())
        })
        .transpose()
}
// The normal service owns this lock for its entire lifecycle. A failed HTTP
// request alone cannot prove death; an unlocked lifecycle can, even after a
// crash left the same runtime.json on disk. Rust's exclusive file lock uses
// flock on Unix and LockFileEx on Windows, matching internal/filelock.
fn lifecycle_held(home: &Path) -> Result<bool> {
    let Some(file) = guarded_file(&home.join("run/run.lock"), true)? else {
        return Ok(false);
    };
    match file.try_lock() {
        Ok(()) => Ok(false), // Dropping the handle releases only our probe lock.
        Err(fs::TryLockError::WouldBlock) => Ok(true),
        Err(fs::TryLockError::Error(_)) => {
            Err("Cannot inspect the local omo lifecycle lock.".into())
        }
    }
}
fn guarded_read(path: &Path, limit: usize) -> Result<Option<Vec<u8>>> {
    let Some(file) = guarded_file(path, false)? else {
        return Ok(None);
    };
    if file
        .metadata()
        .map_err(|_| "Cannot inspect local omo metadata.")?
        .len()
        > limit as u64
    {
        return Err("Local metadata must be a bounded regular file.".into());
    }
    bounded(file, limit, "Local metadata is too large.").map(Some)
}
fn guarded_file(path: &Path, writable: bool) -> Result<Option<fs::File>> {
    let mut options = fs::OpenOptions::new();
    options.read(true).write(writable);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.custom_flags(libc::O_NOFOLLOW | libc::O_NONBLOCK);
    }
    #[cfg(windows)]
    {
        use std::os::windows::fs::OpenOptionsExt;
        options.custom_flags(0x00200000);
    }
    let file = match options.open(path) {
        Ok(file) => file,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        Err(_) => return Err("Cannot safely open local omo metadata.".into()),
    };
    let metadata = file
        .metadata()
        .map_err(|_| "Cannot inspect local omo metadata.")?;
    #[cfg(windows)]
    {
        use std::os::windows::fs::MetadataExt;
        if metadata.file_attributes() & 0x400 != 0 {
            return Err("Local metadata cannot be a reparse point.".into());
        }
    }
    if !metadata.is_file() {
        return Err("Local metadata must be a regular file.".into());
    }
    Ok(Some(file))
}
fn bounded(reader: impl Read, limit: usize, message: &str) -> Result<Vec<u8>> {
    let mut data = Vec::new();
    reader
        .take(limit as u64 + 1)
        .read_to_end(&mut data)
        .map_err(|_| "Cannot read local omo data.")?;
    if data.len() > limit {
        return Err(message.into());
    }
    Ok(data)
}
fn extract(raw: &[u8], name: &str, windows: bool) -> Result<Vec<u8>> {
    if windows {
        let mut zip = zip::ZipArchive::new(std::io::Cursor::new(raw))
            .map_err(|_| "Invalid omo release archive.")?;
        if zip.len() > 2000 {
            return Err("Too many release archive entries.".into());
        }
        for i in 0..zip.len() {
            let entry = zip
                .by_index(i)
                .map_err(|_| "Invalid omo release archive entry.")?;
            if entry.name() == name || entry.name() == format!("./{name}") {
                if entry.is_dir() || entry.unix_mode().is_some_and(|m| m & 0o170000 == 0o120000) {
                    return Err("Omo executable is not a regular file.".into());
                }
                return bounded(entry, MAX_ARCHIVE, "Omo executable is too large.");
            }
        }
    } else {
        let mut archive =
            tar::Archive::new(flate2::read::GzDecoder::new(raw).take(MAX_ARCHIVE as u64 + 1));
        for (count, entry) in archive
            .entries()
            .map_err(|_| "Invalid omo release archive.")?
            .enumerate()
        {
            if count >= 2000 {
                return Err("Too many release archive entries.".into());
            }
            let entry = entry.map_err(|_| "Invalid omo release archive entry.")?;
            let path = entry.path().map_err(|_| "Invalid release archive path.")?;
            if path == Path::new(name) || path == Path::new(&format!("./{name}")) {
                if !entry.header().entry_type().is_file() {
                    return Err("Omo executable is not a regular file.".into());
                }
                return bounded(entry, MAX_ARCHIVE, "Omo executable is too large.");
            }
        }
    }
    Err("Omo executable is missing from release archive.".into())
}

// Match settings.LoadGeneral's default and maximum handoff duration. Invalid hand
// edits retain a generous default; a successful stop still waits on file identity.
fn cleanup_grace(home: &Path) -> Result<Duration> {
    #[derive(Deserialize)]
    struct Settings {
        stop: Option<Stop>,
    }
    #[derive(Deserialize)]
    struct Stop {
        handoff_timeout: Option<String>,
    }
    let raw = guarded_read(&home.join("settings.yaml"), MAX_RUNTIME)?;
    let duration = raw
        .and_then(|raw| serde_yaml::from_slice::<Settings>(&raw).ok())
        .and_then(|settings| settings.stop)
        .and_then(|stop| stop.handoff_timeout)
        .and_then(|value| humantime::parse_duration(&value).ok())
        .filter(|d| *d >= Duration::from_secs(5) && *d <= Duration::from_secs(3600))
        .unwrap_or(Duration::from_secs(120));
    Ok(duration + Duration::from_secs(30))
}

#[cfg(test)]
mod local_boundary_tests {
    use super::*;
    #[test]
    fn registry_path_append_keeps_expandable_type_and_existing_placeholders() {
        let bytes: Vec<_> = "%USERPROFILE%\\tools\0"
            .encode_utf16()
            .flat_map(u16::to_le_bytes)
            .collect();
        let (kind, updated) = append_windows_path(Some((2, bytes)), Path::new("C:\\omo"))
            .unwrap()
            .unwrap();
        assert_eq!(kind, 2);
        let words: Vec<_> = updated
            .chunks_exact(2)
            .map(|b| u16::from_le_bytes([b[0], b[1]]))
            .collect();
        assert_eq!(
            String::from_utf16(&words).unwrap(),
            "%USERPROFILE%\\tools;C:\\omo\0"
        );
        assert!(append_windows_path(Some((4, vec![0, 0, 0, 0])), Path::new("C:\\omo")).is_err());
    }
    #[test]
    fn cli_environment_drops_all_inherited_omo_session_variables() {
        use std::ffi::OsString;
        let env = [
            ("OMO_HOME", "old-home"),
            ("OMO_AGENT_ID", "session"),
            ("OMO_FAKE_PID_DIR", "old-fixture"),
            ("PATH", "user-tools"),
        ]
        .map(|(k, v)| (OsString::from(k), OsString::from(v)));
        let actual = child_environment(env);
        assert_eq!(
            actual,
            vec![(OsString::from("PATH"), OsString::from("user-tools"))]
        );
    }
}

#[cfg(any(not(target_os = "android"), test))]
fn child_environment(
    environment: impl IntoIterator<Item = (std::ffi::OsString, std::ffi::OsString)>,
) -> Vec<(std::ffi::OsString, std::ffi::OsString)> {
    environment
        .into_iter()
        .filter(|(name, _)| {
            !name
                .to_string_lossy()
                .to_ascii_uppercase()
                .starts_with("OMO_")
        })
        .collect()
}

// Keep serialization platform independent so tests never modify HKCU Environment.
#[cfg(any(windows, test))]
fn append_windows_path(
    existing: Option<(u32, Vec<u8>)>,
    install: &Path,
) -> Result<Option<(u32, Vec<u8>)>> {
    let (kind, bytes) = existing.unwrap_or((2, vec![0, 0]));
    if !matches!(kind, 1 | 2) || bytes.len() % 2 != 0 {
        return Err("User PATH is not a registry string.".into());
    }
    let mut words: Vec<_> = bytes
        .chunks_exact(2)
        .map(|b| u16::from_le_bytes([b[0], b[1]]))
        .collect();
    while words.last() == Some(&0) {
        words.pop();
    }
    if words.contains(&0) {
        return Err("User PATH contains an embedded null.".into());
    }
    let path = String::from_utf16(&words).map_err(|_| "User PATH is not valid UTF-16.")?;
    let install = install
        .to_str()
        .ok_or("Installation path is not Unicode.")?;
    if path.split(';').any(|p| {
        p.trim_end_matches('\\')
            .eq_ignore_ascii_case(install.trim_end_matches('\\'))
    }) {
        return Ok(None);
    }
    let updated = if path.is_empty() {
        install.to_owned()
    } else {
        format!("{path};{install}")
    };
    let bytes = updated
        .encode_utf16()
        .chain(std::iter::once(0))
        .flat_map(u16::to_le_bytes)
        .collect();
    Ok(Some((kind, bytes)))
}
