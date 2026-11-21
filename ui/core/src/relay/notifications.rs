//! Headless relay GET polling and durable receipt/delivery chain. No UI socket.
use super::received::ReceivedIds;
use super::{
    RelayClient,
    crypto::{self, NotificationContext, fingerprint},
    wire,
};
use crate::{Config, Mode, Notice, Poll, Result};
use base64::{Engine, engine::general_purpose::STANDARD};
use reqwest::{blocking::Client, redirect::Policy};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use std::{io::Read, time::Duration};
use zeroize::{Zeroize, Zeroizing};

/// Private worker capability: no ML-KEM seed, ceremony or application keys.
/// Serialize only into protected native storage, never setup responses/config.
#[derive(Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct NotificationSecrets {
    context: NotificationContext,
    root: String,
    admission: Option<String>,
}
impl Drop for NotificationSecrets {
    fn drop(&mut self) {
        self.root.zeroize();
        if let Some(password) = &mut self.admission {
            password.zeroize();
        }
    }
}
impl RelayClient {
    pub fn notification_secrets(&self) -> Result<NotificationSecrets> {
        let s = self.inner.snapshot()?;
        let host = s
            .attempt
            .as_ref()
            .and_then(|a| a.host.as_ref())
            .filter(|h| h.trusted)
            .ok_or("Relay notifications require accepted host trust.")?;
        Ok(NotificationSecrets {
            context: NotificationContext {
                route: s.route.clone(),
                host_fp: fingerprint("host", &wire::unb64(&host.pk)?)?,
                ui_id: self.identity_id()?,
                pair_epoch: wire::unb64(&host.epoch)?,
            },
            root: host
                .root
                .clone()
                .ok_or("Saved relay notification root is missing.")?,
            admission: s.admission.clone(),
        })
    }
}

/// Connection-scoped private journal. Persist a whole replacement atomically.
/// Received IDs survive receipt recovery; pending OS delivery survives process death.
#[derive(Clone, Default, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RelayDelivery {
    binding: Option<String>,
    pub outstanding: Option<String>,
    pub receipt: Option<Value>,
    pub received: ReceivedIds,
    pub pending: Vec<Notice>,
}
impl RelayDelivery {
    fn commit(&mut self, next: Self, persist: &mut impl FnMut(&Self) -> Result<()>) -> Result<()> {
        persist(&next)?;
        *self = next;
        Ok(())
    }
    /// Accept only an already authenticated page for the current request.
    pub fn accept(
        &mut self,
        page: Value,
        mut persist: impl FnMut(&Self) -> Result<()>,
    ) -> Result<bool> {
        wire::encode_notification_page(&page)?;
        let mut next = self.clone();
        next.outstanding = None;
        if page["error"] == "invalid_ack" {
            next.receipt = None;
            self.commit(next, &mut persist)?;
            return Ok(true);
        }
        let expected = self
            .receipt
            .as_ref()
            .map(|r| &r["batch"])
            .unwrap_or(&Value::Null);
        if &page["acknowledged"] != expected {
            return Err("Relay reply did not acknowledge the saved receipt.".into());
        }
        for notice in page["notifications"]
            .as_array()
            .ok_or("Invalid relay notifications.")?
        {
            let id = notice["id"].as_str().ok_or("Invalid comment ID.")?;
            if !next.received.insert(id.into()) || notice["unavailable"] == true {
                continue;
            }
            let ticket = notice["ticket"]
                .as_str()
                .ok_or("Invalid notification ticket.")?;
            next.pending.push(Notice {
                id: format!("comment:{id}"),
                kind: "mention".into(),
                title: notice["title"]
                    .as_str()
                    .ok_or("Invalid notification title.")?
                    .into(),
                body: notice["body"]
                    .as_str()
                    .ok_or("Invalid notification body.")?
                    .into(),
                destination: format!("/{ticket}?tab=comments&comment={id}"),
            });
        }
        next.receipt = if page["batch"].is_null() {
            None
        } else {
            Some(json!({"batch":page["batch"],"through":page["through"],"token":page["token"]}))
        };
        // Even the last nonempty page needs another GET to durably acknowledge it.
        let more = next.receipt.is_some();
        self.commit(next, &mut persist)?;
        Ok(more)
    }
    pub fn deliver(
        &mut self,
        mut deliver: impl FnMut(&Notice) -> Result<()>,
        mut persist: impl FnMut(&Self) -> Result<()>,
    ) -> Result<()> {
        while let Some(notice) = self.pending.first() {
            deliver(notice)?;
            let mut next = self.clone();
            next.pending.remove(0);
            self.commit(next, &mut persist)?;
        }
        Ok(())
    }
}

pub struct RelayNotifier {
    config: Config,
    secrets: NotificationSecrets,
    client: Client,
    binding: String,
}
impl RelayNotifier {
    pub fn new(mut config: Config, secrets: NotificationSecrets) -> Result<Self> {
        config.validate(cfg!(target_os = "android"))?;
        if config.mode != Mode::Relay
            || config.origin != secrets.context.route.origin
            || config.host_id != secrets.context.route.host_id
        {
            return Err("Relay notification credentials belong to another connection.".into());
        }
        if secrets
            .admission
            .as_ref()
            .is_some_and(|p| p.len() > 4096 || p.contains(['\r', '\n']))
        {
            return Err("Invalid relay admission credentials.".into());
        }
        let root = Zeroizing::new(wire::unb64(&secrets.root)?);
        crypto::new_poll(&secrets.context, &root, &[0; 32], None)?;
        let context = serde_json::to_vec(&(&config.id, &secrets.context))
            .map_err(|_| "Invalid notification context.")?;
        let binding = wire::b64(&Sha256::digest(context));
        let client = Client::builder()
            .redirect(Policy::none())
            .no_proxy()
            .timeout(Duration::from_secs(35))
            .build()
            .map_err(|_| "Cannot initialize relay notification transport.")?;
        Ok(Self {
            config,
            secrets,
            client,
            binding,
        })
    }
    /// Caller serializes this entire operation per saved connection, including
    /// OS delivery and persistence. Each retry supersedes the previous request.
    pub fn poll(
        &self,
        delivery: &mut RelayDelivery,
        deliver: impl FnMut(&Notice) -> Result<()>,
        mut persist: impl FnMut(&RelayDelivery) -> Result<()>,
    ) -> Result<Poll> {
        if delivery
            .binding
            .as_ref()
            .is_some_and(|b| b != &self.binding)
        {
            return Err("Relay notification journal belongs to another identity.".into());
        }
        // Flush saved OS work before another network operation, even if offline.
        // Receipt data was already durably accepted and is safe to retry.
        for notice in &delivery.pending {
            self.config.destination(&notice.destination)?;
        }
        delivery.deliver(deliver, &mut persist)?;
        let root = Zeroizing::new(wire::unb64(&self.secrets.root)?);
        let request = crypto::random_bytes(32)?;
        let headers = crypto::new_poll(
            &self.secrets.context,
            &root,
            &request,
            delivery.receipt.as_ref(),
        )?;
        let mut next = delivery.clone();
        next.binding = Some(self.binding.clone());
        next.outstanding = Some(wire::b64(&request));
        delivery.commit(next, &mut persist)?;
        let url = format!(
            "{}/notifications/{}/{}",
            self.secrets.context.route.origin,
            self.secrets.context.route.host_id,
            self.secrets.context.ui_id
        );
        let mut req = self
            .client
            .get(url)
            .header("Omo-Notification-Request", headers.request_header)
            .header("Omo-Notification-MAC", headers.request_mac);
        if let Some(password) = &self.secrets.admission {
            req = req.header(
                "Authorization",
                format!("Basic {}", STANDARD.encode(format!("relay:{password}"))),
            );
        }
        let response = req
            .send()
            .map_err(|_| "Cannot reach relay notifications.")?;
        if response.status().as_u16() != 200 {
            return Err(format!(
                "Relay notification check unavailable (HTTP {}).",
                response.status().as_u16()
            ));
        }
        if response
            .headers()
            .get("Content-Type")
            .and_then(|v| v.to_str().ok())
            != Some("application/vnd.omo.relay-notifications+json")
        {
            return Err("Invalid relay notification response type.".into());
        }
        let mut bytes = Vec::new();
        response
            .take(wire::MAX_NOTIFICATION_SIZE as u64 + 1)
            .read_to_end(&mut bytes)
            .map_err(|_| "Cannot read relay notification response.")?;
        let envelope = wire::decode_notification_envelope(&bytes)?;
        // This synchronous operation still owns the sole durable request.
        let outstanding = wire::unb64(
            delivery
                .outstanding
                .as_deref()
                .ok_or("No outstanding relay poll.")?,
        )?;
        let page =
            crypto::open_notification(&self.secrets.context, &root, &outstanding, &envelope)?;
        let more = delivery.accept(page, &mut persist)?;
        // Pending items were persisted with the receipt. Deliver on the next
        // call as well when the process ends between this save and OS delivery.
        Ok(if more || !delivery.pending.is_empty() {
            Poll::More
        } else {
            Poll::Idle
        })
    }
}
