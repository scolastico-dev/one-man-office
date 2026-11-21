//! Desktop registry selection and connection-owned loopback routing.
use crate::{BoardClient, Result};
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct RegisteredForward {
    pub id: String,
    #[serde(default)]
    pub name: String,
    pub start_path: String,
    pub ticket: Option<u64>,
}
impl RegisteredForward {
    pub fn virtual_host(&self) -> Result<String> {
        if self.id.len() != 32
            || !self
                .id
                .bytes()
                .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
        {
            return Err("Invalid registered forward ID.".into());
        }
        let mut label = String::new();
        for b in self.name.bytes() {
            if b.is_ascii_alphanumeric() {
                if label.len() == 29 {
                    break;
                }
                label.push((b as char).to_ascii_lowercase());
            } else if !label.is_empty() && !label.ends_with('-') && label.len() < 29 {
                label.push('-');
            }
        }
        let label = label.trim_end_matches('-');
        let label = if label.is_empty() { "app" } else { label };
        Ok(format!("{label}-{}.omo", self.id))
    }
    pub fn preview_url(&self) -> Result<url::Url> {
        if !self.start_path.starts_with('/')
            || self.start_path.starts_with("//")
            || self.start_path.contains(['\\', '#'])
            || self.start_path.chars().any(char::is_control)
        {
            return Err("Invalid forward start path.".into());
        }
        url::Url::parse(&format!(
            "http://{}{}",
            self.virtual_host()?,
            self.start_path
        ))
        .map_err(|_| "Invalid forward start path.".into())
    }
}
pub fn local_port(value: &str) -> Result<u16> {
    if value.is_empty() || !value.bytes().all(|b| b.is_ascii_digit()) {
        return Err("Choose a local port from 1 to 65535.".into());
    }
    value
        .parse::<u16>()
        .ok()
        .filter(|p| *p != 0)
        .ok_or_else(|| "Choose a local port from 1 to 65535.".into())
}
impl BoardClient {
    pub fn forwards(&self) -> Result<Vec<RegisteredForward>> {
        let reply = self.request("GET", "/api/proxy/forwards", None)?;
        if reply.status != 200 {
            return Err(format!(
                "Cannot read registered apps (HTTP {}).",
                reply.status
            ));
        }
        #[derive(Deserialize)]
        struct Registry {
            forwards: Vec<RegisteredForward>,
        }
        let registry: Registry =
            serde_json::from_slice(&reply.body).map_err(|_| "Invalid registered apps response.")?;
        for f in &registry.forwards {
            f.preview_url()?;
        }
        Ok(registry.forwards)
    }
}
