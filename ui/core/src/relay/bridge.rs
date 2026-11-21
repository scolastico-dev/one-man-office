//! App-side HTTP/SSE/WebSocket adapter; never grants native commands to a page.
use super::{RelayClient, RelaySession, StreamMux, wire};
use crate::Result;
use base64::{Engine, engine::general_purpose::STANDARD};
use serde::Serialize;
use std::{
    sync::{Arc, RwLock},
    time::Duration,
};
use subtle::ConstantTimeEq;
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::{TcpListener, TcpStream},
    sync::{Semaphore, oneshot, watch},
    task::JoinSet,
};
use tokio_util::sync::CancellationToken;

const HEADER_LIMIT: usize = 64 << 10;
/// Ephemeral Basic credentials for this loopback listener only. Supply these to
/// the shell's existing native Basic-auth callbacks; never persist or inject
/// them into JavaScript. Deliberately neither Debug nor Serialize.
#[derive(Clone)]
pub struct BoardEndpoint {
    pub origin: String,
    pub username: String,
    pub password: String,
}
impl BoardEndpoint {
    pub fn authorization(&self) -> String {
        format!(
            "Basic {}",
            STANDARD.encode(format!("{}:{}", self.username, self.password))
        )
    }
}
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum TransportStatus {
    Connecting,
    Ready,
    Reconnecting,
    Unavailable,
    Stopped,
}
/// Own with the configured connection, outside page JavaScript. Watch Ready to
/// (re)load a board document after initial connect/loss. Browser subscriptions
/// use ordinary Last-Event-ID and terminal replay; this adapter replays no bytes.
pub struct BoardTransport {
    endpoint: BoardEndpoint,
    cancel: CancellationToken,
    status: watch::Receiver<TransportStatus>,
    done: watch::Receiver<bool>,
}
impl Drop for BoardTransport {
    fn drop(&mut self) {
        self.cancel.cancel();
    }
}
impl BoardTransport {
    /// `session` may be freshly accepted pairing. With None, authenticate only
    /// against the client's saved trust. No failure ever starts a new ceremony.
    pub async fn start(client: RelayClient, session: Option<RelaySession>) -> Result<Self> {
        if session.is_none() && client.inner.gate.try_lock().is_err() {
            return Err("Relay connection is busy.".into());
        }
        let listener = TcpListener::bind((std::net::Ipv4Addr::LOCALHOST, 0))
            .await
            .map_err(|_| "Cannot open private board bridge.")?;
        let address = listener
            .local_addr()
            .map_err(|_| "Cannot inspect private board bridge.")?;
        let mut secret = [0u8; 32];
        getrandom::fill(&mut secret).map_err(|_| "Cannot create private bridge credentials.")?;
        let endpoint = BoardEndpoint {
            origin: format!("http://{address}"),
            username: "omo-ui".into(),
            password: wire::b64(&secret),
        };
        let serving_endpoint = endpoint.clone();
        let cancel = CancellationToken::new();
        let stopping = cancel.clone();
        let (status, status_rx) = watch::channel(TransportStatus::Connecting);
        let (done, done_rx) = watch::channel(false);
        let active = Arc::new(RwLock::new(None::<StreamMux>));
        let connecting = active.clone();
        tokio::spawn(async move {
            let mut attempt_done = None;
            let reconnect = async {
                let mut initial = session;
                let mut backoff = 1;
                loop {
                    let connected = match initial.take() {
                        Some(s) => Ok(s),
                        None => {
                            let (completed, done) = oneshot::channel();
                            attempt_done = Some(done);
                            client.reconnect_observed(completed).await
                        }
                    };
                    match connected {
                        Ok(s) => {
                            let mux = StreamMux::new(s);
                            *connecting.write().unwrap() = Some(mux.clone());
                            backoff = 1;
                            status.send_replace(TransportStatus::Ready);
                            mux.closed().await;
                            connecting.write().unwrap().take();
                            status.send_replace(TransportStatus::Reconnecting);
                        }
                        Err(_) => {
                            status.send_replace(TransportStatus::Unavailable);
                        }
                    }
                    if let Some(done) = attempt_done.as_mut() {
                        let _ = done.await;
                    }
                    attempt_done = None;
                    tokio::time::sleep(Duration::from_secs(backoff)).await;
                    backoff = (backoff * 2).min(30);
                }
            };
            {
                let accept = async {
                    let capacity = Arc::new(Semaphore::new(64));
                    let mut requests = JoinSet::new();
                    loop {
                        tokio::select! {
                            _=stopping.cancelled()=>break,
                            Some(_)=requests.join_next()=>{},
                            accepted=listener.accept()=>{
                                let Ok((socket,peer))=accepted else {break;};
                                if !peer.ip().is_loopback() {continue;}
                                let Ok(permit)=capacity.clone().try_acquire_owned() else {continue;};
                                let endpoint=serving_endpoint.clone();
                                let active=active.clone();
                                let cancel=stopping.clone();
                                requests.spawn(async move {
                                    let _permit=permit;
                                    tokio::select! {_=cancel.cancelled()=>{}, _=serve(socket,&endpoint,active)=>{}}
                                });
                            }
                        }
                    }
                    requests.abort_all();
                    while requests.join_next().await.is_some() {}
                };
                // Keep accept alive to join every accepted socket on all exit paths.
                tokio::pin!(accept);
                let accepted_done = tokio::select! {_=stopping.cancelled()=>false, _=&mut accept=>true, _=reconnect=>false};
                stopping.cancel();
                if !accepted_done {
                    accept.await;
                }
            }
            drop(listener);
            let mux = connecting.write().unwrap().take();
            if let Some(mux) = mux {
                mux.close().await;
            }
            // A cancelled authentication can own a driver before returning its
            // control. Join that attempt only, never another client's gate.
            if let Some(done) = attempt_done {
                let _ = done.await;
            }
            status.send_replace(TransportStatus::Stopped);
            done.send_replace(true);
        });
        Ok(Self {
            endpoint,
            cancel,
            status: status_rx,
            done: done_rx,
        })
    }
    pub fn endpoint(&self) -> &BoardEndpoint {
        &self.endpoint
    }
    pub fn status(&self) -> TransportStatus {
        *self.status.borrow()
    }
    pub fn subscribe(&self) -> watch::Receiver<TransportStatus> {
        self.status.clone()
    }
    pub async fn close(&self) {
        self.cancel.cancel();
        let mut done = self.done.clone();
        while !*done.borrow_and_update() {
            if done.changed().await.is_err() {
                break;
            }
        }
    }
}

// Only header framing changes: virtual authority/origin, per-request close,
// removal of local bridge authentication. Body bytes, chunking, WebSocket
// frames and all application JSON remain opaque.
fn rewrite(raw: &[u8], endpoint: &BoardEndpoint) -> Result<Vec<u8>> {
    let bad = || "Private board request rejected.".to_string();
    if raw.len() > HEADER_LIMIT {
        return Err(bad());
    }
    let mut headers = [httparse::EMPTY_HEADER; 128];
    let mut r = httparse::Request::new(&mut headers);
    if !matches!(r.parse(raw),Ok(httparse::Status::Complete(n)) if n==raw.len())
        || r.version != Some(1)
    {
        return Err(bad());
    }
    let method = r.method.ok_or_else(bad)?;
    let target = r.path.ok_or_else(bad)?;
    if !matches!(
        method,
        "GET" | "HEAD" | "POST" | "PUT" | "PATCH" | "DELETE" | "OPTIONS"
    ) || !target.starts_with('/')
        || target.starts_with("//")
        || target.contains(['\\', '#'])
    {
        return Err(bad());
    }
    let values = |name: &str| {
        r.headers
            .iter()
            .filter(|h| h.name.eq_ignore_ascii_case(name))
            .map(|h| h.value)
            .collect::<Vec<_>>()
    };
    let authority = endpoint.origin.strip_prefix("http://").ok_or_else(bad)?;
    if values("host") != [authority.as_bytes()] {
        return Err(bad());
    }
    let authorization = endpoint.authorization();
    let auth = values("authorization");
    if auth.len() != 1 || !bool::from(auth[0].ct_eq(authorization.as_bytes())) {
        return Err(bad());
    }
    let origin = values("origin");
    if origin.len() > 1
        || origin
            .first()
            .is_some_and(|o| *o != endpoint.origin.as_bytes())
    {
        return Err(bad());
    }
    let site = values("sec-fetch-site");
    if site.len() > 1
        || site
            .first()
            .is_some_and(|s| !matches!(*s, b"same-origin" | b"none"))
    {
        return Err(bad());
    }
    let length = values("content-length");
    let encoding = values("transfer-encoding");
    if length.len() > 1 || encoding.len() > 1 || (!length.is_empty() && !encoding.is_empty()) {
        return Err(bad());
    }
    for h in r.headers.iter() {
        let name = h.name.to_ascii_lowercase();
        if name.starts_with("omo-relay-")
            || name.starts_with("omo-notification-")
            || name.starts_with("x-omo-")
            || name == "proxy-authorization"
        {
            return Err(bad());
        }
    }
    let upgrade = values("upgrade");
    if upgrade.len() > 1
        || upgrade
            .first()
            .is_some_and(|u| !u.eq_ignore_ascii_case(b"websocket"))
    {
        return Err(bad());
    }
    let upgrading = !upgrade.is_empty();
    let mut out = format!("{method} {target} HTTP/1.1\r\nHost: omo-relay.invalid\r\n").into_bytes();
    for h in r.headers.iter() {
        if h.name.eq_ignore_ascii_case("host") || h.name.eq_ignore_ascii_case("authorization") {
            continue;
        }
        if h.name.eq_ignore_ascii_case("origin") {
            out.extend_from_slice(b"Origin: http://omo-relay.invalid\r\n");
            continue;
        }
        if h.name.eq_ignore_ascii_case("connection") && !upgrading {
            continue;
        }
        out.extend_from_slice(h.name.as_bytes());
        out.extend_from_slice(b": ");
        out.extend_from_slice(h.value);
        out.extend_from_slice(b"\r\n");
    }
    if !upgrading {
        out.extend_from_slice(b"Connection: close\r\n");
    }
    out.extend_from_slice(b"\r\n");
    Ok(out)
}
async fn response(socket: &mut TcpStream, status: &str, challenge: bool) {
    let challenge = if challenge {
        "WWW-Authenticate: Basic realm=\"omo\"\r\n"
    } else {
        ""
    };
    let _=socket.write_all(format!("HTTP/1.1 {status}\r\n{challenge}Content-Length: 0\r\nConnection: close\r\nCache-Control: no-store\r\n\r\n").as_bytes()).await;
}
async fn serve(
    mut socket: TcpStream,
    endpoint: &BoardEndpoint,
    active: Arc<RwLock<Option<StreamMux>>>,
) -> Result<()> {
    let mut input = Vec::new();
    let end = loop {
        if let Some(i) = input.windows(4).position(|b| b == b"\r\n\r\n") {
            break i + 4;
        }
        if input.len() >= HEADER_LIMIT {
            response(&mut socket, "431 Request Header Fields Too Large", false).await;
            return Ok(());
        }
        let mut buf = [0; 4096];
        let n = socket
            .read(&mut buf)
            .await
            .map_err(|_| "Private board read failed.")?;
        if n == 0 {
            return Ok(());
        }
        input.extend_from_slice(&buf[..n]);
    };
    let headers = match rewrite(&input[..end], endpoint) {
        Ok(h) => h,
        Err(_) => {
            response(&mut socket, "401 Unauthorized", true).await;
            return Ok(());
        }
    };
    let mux = active.read().unwrap().clone();
    let Some(mux) = mux else {
        response(&mut socket, "503 Service Unavailable", false).await;
        return Ok(());
    };
    let mut stream = match mux.open().await {
        Ok(s) => s,
        Err(_) => {
            response(&mut socket, "503 Service Unavailable", false).await;
            return Ok(());
        }
    };
    stream
        .write_all(&headers)
        .await
        .map_err(|_| "Relay board write failed.")?;
    stream
        .write_all(&input[end..])
        .await
        .map_err(|_| "Relay board write failed.")?;
    let (mut local_read, mut local_write) = socket.split();
    let (mut remote_read, mut remote_write) = tokio::io::split(stream);
    let upload = async {
        tokio::io::copy(&mut local_read, &mut remote_write).await?;
        remote_write.shutdown().await
    };
    let download = async { tokio::io::copy(&mut remote_read, &mut local_write).await };
    tokio::pin!(download);
    // HTTP clients keep their write half open while waiting for the response.
    // A completed response ends that request; never await another client write.
    tokio::select! {
        result=upload=>{result.map_err(|_|"Relay board upload interrupted.")?;download.await.map_err(|_|"Relay board response interrupted.")?;},
        result=&mut download=>{result.map_err(|_|"Relay board response interrupted.")?;},
    }
    Ok(())
}
#[cfg(test)]
#[path = "bridge_tests.rs"]
mod tests;
