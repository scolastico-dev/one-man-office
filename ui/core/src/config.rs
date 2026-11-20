use crate::Result;
use base64::{Engine, engine::general_purpose::STANDARD};
use serde::{Deserialize, Serialize, de::DeserializeOwned};
use std::{
    fs,
    io::{Read, Write},
    path::Path,
};
use url::Url;
use uuid::Uuid;

#[derive(Clone, Serialize, Deserialize, Debug, PartialEq)]
#[serde(rename_all = "snake_case")]
pub enum Mode {
    Direct,
    Local,
}
#[derive(Clone, Serialize, Deserialize, Debug, PartialEq)]
#[serde(rename_all = "snake_case")]
pub enum Auth {
    None,
    Token,
    Basic,
}
/// Public configuration only. Secrets live in the native credential store or session memory.
#[derive(Clone, Serialize, Deserialize, Debug, PartialEq)]
#[serde(deny_unknown_fields)]
pub struct Config {
    pub id: String,
    pub mode: Mode,
    pub origin: String,
    pub auth: Auth,
    pub username: String,
    pub notifications: bool,
    pub remember: bool,
    #[serde(default)]
    pub stop_with_window: bool,
}
impl Config {
    pub fn validate(&mut self, android: bool) -> Result<()> {
        if android && self.mode == Mode::Local {
            return Err("Android requires an external omo host.".into());
        }
        Uuid::parse_str(&self.id).map_err(|_| "Invalid connection identity.")?;
        if self.origin.contains('\\') || self.origin.chars().any(char::is_control) {
            return Err("Enter an HTTP(S) board origin.".into());
        }
        let u = Url::parse(self.origin.trim()).map_err(|_| "Enter an HTTP(S) board origin.")?;
        if !matches!(u.scheme(), "http" | "https")
            || u.host_str().is_none()
            || !u.username().is_empty()
            || u.password().is_some()
            || u.path() != "/"
            || u.query().is_some()
            || u.fragment().is_some()
        {
            return Err(
                "Use only the board origin, without path, credentials, query or fragment.".into(),
            );
        }
        self.origin = u.to_string();
        if self.auth == Auth::Basic
            && (self.username.is_empty()
                || self.username.contains(':')
                || self.username.chars().any(char::is_control))
        {
            return Err("Enter a Basic username without colons or control characters.".into());
        }
        Ok(())
    }
    pub fn authorization(&self, secret: &str) -> Result<Option<String>> {
        match self.auth {
            Auth::None => Ok(None),
            Auth::Token if secret.len() == 64 && secret.bytes().all(|b| b.is_ascii_hexdigit()) => {
                Ok(Some(format!("Bearer {secret}")))
            }
            Auth::Token => Err("Enter the 64 hexadecimal character login token.".into()),
            Auth::Basic => {
                if self.username.is_empty()
                    || self.username.contains(':')
                    || self.username.chars().any(char::is_control)
                    || secret.chars().any(char::is_control)
                {
                    return Err("Invalid Basic credentials.".into());
                }
                Ok(Some(format!(
                    "Basic {}",
                    STANDARD.encode(format!("{}:{secret}", self.username))
                )))
            }
        }
    }
    /// Notifications can navigate only to ticket documents, never an API, proxy or another origin.
    pub fn destination(&self, path: &str) -> Result<Url> {
        let u = Url::parse(&self.origin).map_err(|_| "Invalid board origin.")?;
        let valid = path.starts_with('/')
            && !path.starts_with("//")
            && !path.contains(['\\', '%', '#'])
            && !path.chars().any(char::is_control);
        let ticket = path.split('?').next().unwrap_or("").trim_start_matches('/');
        if !valid
            || ticket.is_empty()
            || !ticket.bytes().all(|b| b.is_ascii_digit())
            || ticket.starts_with('0')
        {
            return Err("Invalid notification destination.".into());
        }
        u.join(path)
            .map_err(|_| "Invalid notification destination.".into())
    }
    pub fn new_id() -> String {
        Uuid::new_v4().to_string()
    }
}

pub fn save_config(root: &Path, config: &Config) -> Result<()> {
    write_json(root, "config.json", config)
}
pub fn load_config(root: &Path, android: bool) -> Result<Option<Config>> {
    let Some(mut c) = read_json::<Config>(root, "config.json")? else {
        return Ok(None);
    };
    c.validate(android)?;
    Ok(Some(c))
}
/// Atomic replacement in the same directory; failure leaves the last complete file intact.
pub fn write_json(root: &Path, name: &str, value: &impl Serialize) -> Result<()> {
    fs::create_dir_all(root).map_err(|_| "Cannot create app configuration directory.")?;
    let data = serde_json::to_vec_pretty(value).map_err(|_| "Cannot encode app configuration.")?;
    let temp = root.join(format!(".{}.tmp", Uuid::new_v4()));
    let result = (|| {
        let mut options = fs::OpenOptions::new();
        options.write(true).create_new(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600);
        }
        let mut file = options
            .open(&temp)
            .map_err(|_| "Cannot create configuration file.")?;
        file.write_all(&data)
            .and_then(|_| file.sync_all())
            .map_err(|_| "Cannot write configuration file.")?;
        drop(file);
        fs::rename(&temp, root.join(name)).map_err(|_| "Cannot replace configuration file.")?;
        Ok(())
    })();
    if result.is_err() {
        let _ = fs::remove_file(temp);
    }
    result
}
pub fn read_json<T: DeserializeOwned>(root: &Path, name: &str) -> Result<Option<T>> {
    let mut file = match fs::File::open(root.join(name)) {
        Ok(f) => f,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        Err(_) => return Err("Cannot read app configuration.".into()),
    };
    let mut data = Vec::new();
    Read::by_ref(&mut file)
        .take(1024 * 1024 + 1)
        .read_to_end(&mut data)
        .map_err(|_| "Cannot read app configuration.")?;
    if data.len() > 1024 * 1024 {
        return Err("App configuration is too large.".into());
    }
    serde_json::from_slice(&data)
        .map(Some)
        .map_err(|_| "App configuration is invalid.".into())
}
impl Config {
    pub fn challenge_allowed(&self, resource: &str, host: &str, realm: &str) -> bool {
        if self.auth != Auth::Basic || realm != "omo" {
            return false;
        }
        let (Ok(base), Ok(request)) = (Url::parse(&self.origin), Url::parse(resource)) else {
            return false;
        };
        base.origin() == request.origin()
            && base.host_str().is_some_and(|h| {
                let configured = h.trim_matches(['[', ']']);
                let challenged = host.trim_matches(['[', ']']);
                match (
                    configured.parse::<std::net::IpAddr>(),
                    challenged.parse::<std::net::IpAddr>(),
                ) {
                    (Ok(a), Ok(b)) => a == b,
                    _ => configured.eq_ignore_ascii_case(challenged),
                }
            })
    }
}
