use crate::Result;
use serde::Deserialize;
pub fn features(android: bool, notifications: bool) -> Vec<String> {
    let mut out = vec!["ui.capabilities.v1".into()];
    if !android && !cfg!(target_os = "android") {
        out.push("ui.forwards.tunnel.v1".into());
    }
    if notifications {
        out.extend([
            "ui.notifications.poll.v1".into(),
            "ui.notifications.system.v1".into(),
        ]);
    }
    out
}
/// Optional server behavior; missing discovery and unknown flags disable features.
#[derive(Debug, Default, Clone, Copy, PartialEq, Eq)]
pub struct ServerCapabilities {
    pub notification_polling: bool,
    pub forward_tunnel: bool,
}
fn server_capabilities(status: u16, body: &[u8]) -> Result<ServerCapabilities> {
    if status == 404 {
        return Ok(ServerCapabilities::default());
    }
    if status != 200 {
        return Err(format!("Capability exchange failed (HTTP {status})."));
    }
    #[derive(Deserialize)]
    struct Capabilities {
        features: Option<Vec<String>>,
    }
    let c: Capabilities =
        serde_json::from_slice(body).map_err(|_| "Invalid capability response.")?;
    let flags = c.features.unwrap_or_default();
    Ok(ServerCapabilities {
        notification_polling: flags.iter().any(|f| f == "ui.notifications.poll.v1"),
        forward_tunnel: flags.iter().any(|f| f == "ui.forwards.tunnel.v1"),
    })
}
pub fn polling_supported(status: u16, body: &[u8]) -> Result<bool> {
    Ok(server_capabilities(status, body)?.notification_polling)
}
pub fn tunnel_supported(status: u16, body: &[u8]) -> Result<bool> {
    Ok(server_capabilities(status, body)?.forward_tunnel)
}
use crate::{Config, Delivery, cursor};
use reqwest::{blocking::Client, redirect::Policy};
use std::{io::Read, time::Duration};
pub struct Reply {
    pub status: u16,
    pub body: Vec<u8>,
}
/// Native-only authenticated transport. Redirects never forward a connection's secrets.
pub struct BoardClient {
    pub config: Config,
    client: Client,
    authorization: Option<String>,
}
impl BoardClient {
    pub fn new(mut config: Config, secret: String) -> Result<Self> {
        config.validate(cfg!(target_os = "android"))?;
        let authorization = config.authorization(&secret)?;
        let client = Client::builder()
            .redirect(Policy::none())
            .no_proxy()
            .timeout(Duration::from_secs(20))
            .build()
            .map_err(|_| "Cannot initialize HTTP transport.")?;
        Ok(Self {
            config,
            client,
            authorization,
        })
    }
    pub fn request(
        &self,
        method: &str,
        path: &str,
        body: Option<serde_json::Value>,
    ) -> Result<Reply> {
        if !path.starts_with("/api/") || path.contains(['\\', '\r', '\n']) {
            return Err("Invalid API path.".into());
        }
        let url = url::Url::parse(&self.config.origin)
            .map_err(|_| "Invalid board origin.")?
            .join(path)
            .map_err(|_| "Invalid API path.")?;
        let method =
            reqwest::Method::from_bytes(method.as_bytes()).map_err(|_| "Invalid HTTP method.")?;
        let mut req = self.client.request(method, url);
        if let Some(auth) = &self.authorization {
            req = req.header("Authorization", auth);
        }
        if let Some(body) = body {
            req = req.json(&body);
        }
        let response = req.send().map_err(
            |_| "Cannot reach the board. Check its address, TLS certificate and network.",
        )?;
        let status = response.status().as_u16();
        let mut data = Vec::new();
        response
            .take(16 * 1024 * 1024 + 1)
            .read_to_end(&mut data)
            .map_err(|_| "Cannot read the server response.")?;
        if data.len() > 16 * 1024 * 1024 {
            return Err("Server response is too large.".into());
        }
        Ok(Reply { status, body: data })
    }
}
#[derive(Clone, serde::Serialize, serde::Deserialize)]
pub struct Recovery {
    pub reset_cursor: String,
    pub latest_cursor: String,
}
impl Recovery {
    pub fn parse(data: &[u8]) -> Result<Self> {
        #[derive(serde::Deserialize)]
        struct Envelope {
            error: Error,
        }
        #[derive(serde::Deserialize)]
        struct Error {
            details: Recovery,
        }
        let r = serde_json::from_slice::<Envelope>(data)
            .map_err(|_| "Invalid cursor recovery response.")?
            .error
            .details;
        if cursor(&r.reset_cursor)? > cursor(&r.latest_cursor)? {
            return Err("Invalid recovery boundaries.".into());
        }
        Ok(r)
    }
    pub fn apply(
        &self,
        delivery: &mut Delivery,
        replay: bool,
        mut persist: impl FnMut(&Delivery) -> Result<()>,
    ) -> Result<()> {
        let reset = cursor(&self.reset_cursor)?;
        let latest = cursor(&self.latest_cursor)?;
        if reset > latest {
            return Err("Invalid recovery boundaries.".into());
        }
        let mut accepted = Vec::new();
        if replay {
            for id in &delivery.accepted {
                let value = cursor(
                    id.strip_prefix("event:")
                        .ok_or("Invalid accepted notification ID.")?,
                )?;
                if value > reset && value <= latest {
                    accepted.push(id.clone());
                }
            }
        }
        let update = Delivery {
            cursor: Some(if replay {
                self.reset_cursor.clone()
            } else {
                self.latest_cursor.clone()
            }),
            accepted,
        };
        persist(&update)?;
        *delivery = update;
        Ok(())
    }
}
#[derive(serde::Serialize)]
pub enum Poll {
    Idle,
    More,
    Recovery(Recovery),
}
impl BoardClient {
    pub fn connect(&self, android: bool) -> Result<bool> {
        let check = self.request("GET", "/api/server", None)?;
        if check.status != 200 {
            return Err(format!(
                "Connection refused (HTTP {}). Check the authentication mode and credentials.",
                check.status
            ));
        }
        Ok(self.capabilities(android)?.notification_polling && self.config.notifications)
    }
    /// Exchange additive flags independently of the notification setting.
    pub fn capabilities(&self, android: bool) -> Result<ServerCapabilities> {
        let exchange = self.request(
            "POST",
            "/api/ui/capabilities",
            Some(serde_json::json!({"features":features(android,self.config.notifications)})),
        )?;
        let mut supported = server_capabilities(exchange.status, &exchange.body)?;
        supported.forward_tunnel &= !android && !cfg!(target_os = "android");
        Ok(supported)
    }
    pub fn poll(
        &self,
        delivery: &mut Delivery,
        deliver: impl FnMut(&crate::Notice) -> Result<()>,
        mut persist: impl FnMut(&Delivery) -> Result<()>,
    ) -> Result<Poll> {
        let baseline = delivery.cursor.is_none();
        let value = delivery.cursor.as_deref().unwrap_or("9223372036854775807");
        cursor(value)?;
        let reply = self.request(
            "GET",
            &format!("/api/ui/notifications?cursor={value}&limit=200"),
            None,
        )?;
        if reply.status == 409 {
            let recovery = Recovery::parse(&reply.body)?;
            if baseline {
                recovery.apply(delivery, false, persist)?;
                return Ok(Poll::Idle);
            }
            return Ok(Poll::Recovery(recovery));
        }
        if reply.status != 200 {
            return Err(format!(
                "Notification polling failed (HTTP {}).",
                reply.status
            ));
        }
        let page: crate::Page = serde_json::from_slice(&reply.body)
            .map_err(|_| "Invalid notification feed response.")?;
        if baseline {
            cursor(&page.next_cursor)?;
            if !page.notifications.is_empty() || page.has_more {
                return Err("Unexpected notification baseline response.".into());
            }
            let update = Delivery {
                cursor: Some(page.next_cursor),
                accepted: vec![],
            };
            persist(&update)?;
            *delivery = update;
            return Ok(Poll::Idle);
        }
        Ok(if delivery.accept(&self.config, page, deliver, persist)? {
            Poll::More
        } else {
            Poll::Idle
        })
    }
}
