use super::{
    crypto::{Identity, PairTranscript, commitment, fingerprint, random_bytes},
    wire::{self, Route},
};
use crate::Result;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::sync::{Arc, Mutex};
use tokio::sync::Mutex as AsyncMutex;

/// Platform boundary for *private* atomic durable storage. `save` must commit all
/// bytes before returning success; failures must preserve the previous value.
/// Missing and unavailable/corrupt storage are distinct. Implementations must not
/// expose values in errors. One open RelayClient owns a storage key at a time.
/// Desktop credentials and Android encrypted worker storage can implement this
/// without a WebView or a running Tauri application.
pub trait SecretStore: Send + Sync + 'static {
    fn load(&self, key: &str) -> Result<Option<Vec<u8>>>;
    fn save(&self, key: &str, value: &[u8]) -> Result<()>;
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum TrustState {
    Unpaired,
    Pairing,
    Candidate,
    Trusted,
}
/// Public metadata; admission credentials, seeds and roots are never serialized.
#[derive(Clone, Debug, Serialize)]
pub struct RelayStatus {
    pub route: Route,
    pub state: TrustState,
    pub host_fp: Option<String>,
}

#[derive(Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct HostCandidate {
    pub pk: String,
    pub epoch: String,
    #[serde(deserialize_with = "required_option")]
    pub root: Option<String>,
    pub trusted: bool,
}
#[derive(Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct Attempt {
    pub aid: String,
    #[serde(deserialize_with = "required_option")]
    pub ru: Option<String>,
    pub used: bool,
    #[serde(deserialize_with = "required_option")]
    pub host: Option<HostCandidate>,
}
#[derive(Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct PrivateState {
    version: u8,
    pub route: Route,
    seed: String,
    #[serde(deserialize_with = "required_option")]
    pub admission: Option<String>,
    #[serde(deserialize_with = "required_option")]
    pub attempt: Option<Attempt>,
}
pub(super) struct Inner {
    pub state: Mutex<PrivateState>,
    store: Arc<dyn SecretStore>,
    key: String,
    pub gate: Arc<AsyncMutex<()>>,
    failed: Mutex<bool>,
}
/// Clone this handle to share serialization, never open the same store key twice
/// concurrently. Reconstruct after closing the old client/session on restart.
#[derive(Clone)]
pub struct RelayClient {
    pub(super) inner: Arc<Inner>,
}

impl RelayClient {
    /// Creates and saves a new identity only for an absent key. Existing state is
    /// validated and bound to exactly this configured canonical relay and host.
    /// `admission=None` reuses the saved Basic password; it is not device auth.
    pub fn open(
        store: Arc<dyn SecretStore>,
        key: &str,
        route: Route,
        admission: Option<String>,
    ) -> Result<Self> {
        Self::load(store, key, route, admission, false)
    }
    /// Saved profiles must restore: missing secrets are an error, never an
    /// instruction to silently generate a replacement device identity.
    pub fn restore(store: Arc<dyn SecretStore>, key: &str, route: Route) -> Result<Self> {
        Self::load(store, key, route, None, true)
    }
    fn load(
        store: Arc<dyn SecretStore>,
        key: &str,
        route: Route,
        admission: Option<String>,
        require_existing: bool,
    ) -> Result<Self> {
        route.validate()?;
        if key.is_empty() {
            return Err("Invalid relay storage key.".into());
        }
        let loaded = store
            .load(key)
            .map_err(|_| "Relay secret storage is unavailable.")?;
        if require_existing && loaded.is_none() {
            return Err("Saved relay secrets are missing; restore them or unregister the device before a new ceremony.".into());
        }
        let update = admission.is_some() || loaded.is_none();
        let mut state = if let Some(bytes) = loaded.as_ref() {
            if bytes.len() > 16384 {
                return Err("Invalid private relay state.".into());
            }
            let s: PrivateState =
                serde_json::from_slice(bytes).map_err(|_| "Invalid private relay state.")?;
            validate(&s)?;
            if s.route != route {
                return Err("Saved relay identity belongs to another route.".into());
            }
            s
        } else {
            PrivateState {
                version: 1,
                route,
                seed: wire::b64(&Identity::generate()?.seed()),
                admission: None,
                attempt: None,
            }
        };
        if let Some(password) = admission {
            state.admission = Some(password);
        }
        validate(&state)?;
        let client = Self {
            inner: Arc::new(Inner {
                state: Mutex::new(state.clone()),
                store,
                key: key.into(),
                gate: Arc::new(AsyncMutex::new(())),
                failed: Mutex::new(false),
            }),
        };
        if update {
            client.inner.save(state)?;
        }
        Ok(client)
    }
    pub fn identity_id(&self) -> Result<String> {
        fingerprint("ui", &self.inner.identity()?.public_key())
    }
    pub fn status(&self) -> Result<RelayStatus> {
        let s = self.inner.snapshot()?;
        let host = s.attempt.as_ref().and_then(|a| a.host.as_ref());
        Ok(RelayStatus {
            route: s.route,
            state: match host {
                Some(h) if h.trusted => TrustState::Trusted,
                Some(h) if h.root.is_some() => TrustState::Candidate,
                _ if s.attempt.is_some() => TrustState::Pairing,
                _ => TrustState::Unpaired,
            },
            host_fp: host
                .map(|h| fingerprint("host", &wire::unb64(&h.pk)?))
                .transpose()?,
        })
    }
    /// Explicit local action after an unsuccessful ceremony. A candidate with a
    /// saved root may already be accepted on the host and cannot be discarded.
    pub fn discard_unproved_attempt(&self) -> Result<()> {
        let _guard = self
            .inner
            .gate
            .try_lock()
            .map_err(|_| "Relay connection is busy.")?;
        let mut s = self.inner.snapshot()?;
        if s.attempt
            .as_ref()
            .and_then(|a| a.host.as_ref())
            .is_some_and(|h| h.root.is_some())
        {
            return Err(
                "Unregister the possible host registration before replacing its identity.".into(),
            );
        }
        s.attempt = None;
        self.inner.save(s)
    }
}
impl Inner {
    pub fn snapshot(&self) -> Result<PrivateState> {
        if *self.failed.lock().map_err(|_| "Relay state lock failed.")? {
            return Err("Relay secret storage failed; reopen after repair.".into());
        }
        self.state
            .lock()
            .map(|s| s.clone())
            .map_err(|_| "Relay state lock failed.".into())
    }
    pub fn save(&self, s: PrivateState) -> Result<()> {
        validate(&s)?;
        let bytes = serde_json::to_vec(&s).map_err(|_| "Cannot encode private relay state.")?;
        if self.store.save(&self.key, &bytes).is_err() {
            *self.failed.lock().map_err(|_| "Relay state lock failed.")? = true;
            return Err("Cannot durably save private relay state.".into());
        }
        *self.state.lock().map_err(|_| "Relay state lock failed.")? = s;
        Ok(())
    }
    pub fn identity(&self) -> Result<Identity> {
        Identity::import(&wire::unb64(&self.snapshot()?.seed)?)
    }
    pub fn prepare_commit(&self) -> Result<Value> {
        let mut s = self.snapshot()?;
        if s.attempt.is_some() {
            return Err("A saved relay attempt already exists; reconnect or explicitly discard an unproved attempt.".into());
        }
        let aid = random_bytes(16)?;
        let ru = random_bytes(32)?;
        let c = commitment(&s.route, &aid, &self.identity()?.public_key(), &ru)?;
        s.attempt = Some(Attempt {
            aid: wire::b64(&aid),
            ru: Some(wire::b64(&ru)),
            used: false,
            host: None,
        });
        self.save(s)?;
        Ok(json!({"v":1,"type":"pair.commit","aid":wire::b64(&aid),"C":wire::b64(&c)}))
    }
    pub fn reveal(&self, response: &Value) -> Result<(Value, PairTranscript)> {
        let mut s = self.snapshot()?;
        let a = s.attempt.as_mut().ok_or("No saved relay attempt.")?;
        if a.used {
            return Err("Relay attempt is already consumed.".into());
        }
        // Burn first, even when the received contribution is invalid. The frozen
        // host pin is saved by the second transition before sending the reveal.
        let ru = wire::unb64(a.ru.as_deref().ok_or("Invalid relay attempt.")?)?;
        let aid = wire::unb64(&a.aid)?;
        a.used = true;
        a.ru = None;
        self.save(s.clone())?;
        wire::encode_inner(response)?;
        if response["type"] != "pair.host" || response["aid"] != wire::b64(&aid) {
            return Err("Invalid relay pairing contribution.".into());
        }
        let pku = self.identity()?.public_key();
        let t = PairTranscript {
            route: s.route.clone(),
            aid: aid.clone(),
            window: field(response, "window")?,
            pair_epoch: field(response, "pair_epoch")?,
            c: commitment(&s.route, &aid, &pku, &ru)?,
            pku,
            ru: ru.clone(),
            pkh: field(response, "pkH")?,
            rh: field(response, "rh")?,
        };
        t.encode()?;
        s.attempt.as_mut().unwrap().host = Some(HostCandidate {
            pk: wire::b64(&t.pkh),
            epoch: wire::b64(&t.pair_epoch),
            root: None,
            trusted: false,
        });
        self.save(s)?;
        Ok((
            json!({"v":1,"type":"pair.reveal","aid":wire::b64(&aid),"pkU":wire::b64(&t.pku),"ru":wire::b64(&ru)}),
            t,
        ))
    }
    pub fn save_root(&self, root: &[u8]) -> Result<()> {
        if root.len() != 32 {
            return Err("Invalid relay notification root.".into());
        }
        let mut s = self.snapshot()?;
        let h = s
            .attempt
            .as_mut()
            .and_then(|a| a.host.as_mut())
            .ok_or("No relay candidate.")?;
        if h.root.is_some() {
            return Err("Relay candidate already has a root.".into());
        }
        h.root = Some(wire::b64(root));
        self.save(s)
    }
    pub fn trust(&self) -> Result<()> {
        let mut s = self.snapshot()?;
        let h = s
            .attempt
            .as_mut()
            .and_then(|a| a.host.as_mut())
            .ok_or("No relay candidate.")?;
        if h.root.is_none() {
            return Err("Relay candidate has no notification root.".into());
        }
        h.trusted = true;
        self.save(s)
    }
}
pub(super) fn field(v: &Value, name: &str) -> Result<Vec<u8>> {
    wire::unb64(v[name].as_str().ok_or("Invalid relay message.")?)
}
fn validate(s: &PrivateState) -> Result<()> {
    let bad = || "Invalid private relay state.".to_string();
    if s.version != 1 {
        return Err(bad());
    }
    s.route.validate()?;
    Identity::import(&wire::unb64(&s.seed)?)?;
    if s.admission
        .as_ref()
        .is_some_and(|p| p.len() > 4096 || p.contains(['\r', '\n']))
    {
        return Err(bad());
    }
    if let Some(a) = &s.attempt {
        if wire::unb64(&a.aid)?.len() != 16 || a.used == a.ru.is_some() {
            return Err(bad());
        }
        if let Some(ru) = &a.ru
            && wire::unb64(ru)?.len() != 32
        {
            return Err(bad());
        }
        if let Some(h) = &a.host {
            if !a.used || wire::unb64(&h.epoch)?.len() != 16 {
                return Err(bad());
            }
            fingerprint("host", &wire::unb64(&h.pk)?)?;
            if let Some(root) = &h.root
                && wire::unb64(root)?.len() != 32
            {
                return Err(bad());
            }
            if h.trusted && h.root.is_none() {
                return Err(bad());
            }
        }
    }
    Ok(())
}

fn required_option<'de, D: serde::Deserializer<'de>, T: Deserialize<'de>>(
    d: D,
) -> std::result::Result<Option<T>, D::Error> {
    Option::<T>::deserialize(d)
}

#[cfg(test)]
#[path = "state_tests.rs"]
mod tests;
