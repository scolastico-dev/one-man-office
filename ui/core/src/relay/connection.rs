use super::{
    RelayClient,
    crypto::{self, AuthContext, Session, UIHandshake},
    state::{Inner, field},
    wire::{self, Route},
};
use crate::Result;
use base64::{Engine, engine::general_purpose::STANDARD};
use futures_util::{SinkExt, StreamExt};
use serde::Serialize;
use serde_json::{Value, json};
use std::{
    sync::{Arc, Mutex},
    time::Duration,
};
use tokio::sync::{Mutex as AsyncMutex, OwnedMutexGuard, mpsc, oneshot, watch};
use tokio_tungstenite::{
    connect_async_with_config,
    tungstenite::{Message, client::IntoClientRequest, protocol::WebSocketConfig},
};
use tokio_util::sync::CancellationToken;

const NETWORK_TIMEOUT: Duration = Duration::from_secs(30);
const QUEUE: usize = 32;

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum SessionStatus {
    Connecting,
    Pairing,
    Authenticating,
    Ready,
    Closed,
    Failed,
}
/// Cancellation and observable completion, usable while a receive is pending.
#[derive(Clone)]
pub struct SessionControl {
    cancel: CancellationToken,
    status: watch::Receiver<SessionStatus>,
}
impl SessionControl {
    pub fn status(&self) -> SessionStatus {
        *self.status.borrow()
    }
    pub fn subscribe(&self) -> watch::Receiver<SessionStatus> {
        self.status.clone()
    }
    pub fn cancel(&self) {
        self.cancel.cancel();
    }
    /// Waits for the socket driver to finish and release its resources.
    pub async fn close(&self) {
        self.cancel();
        let mut s = self.status.clone();
        while !matches!(
            *s.borrow_and_update(),
            SessionStatus::Closed | SessionStatus::Failed
        ) {
            if s.changed().await.is_err() {
                break;
            }
        }
    }
}
struct Write {
    connection: String,
    payload: Value,
    done: oneshot::Sender<Result<()>>,
}
struct Transport {
    tx: mpsc::Sender<Write>,
    rx: mpsc::Receiver<Value>,
    connection: String,
    control: SessionControl,
    status: watch::Sender<SessionStatus>,
    cipher: Arc<Mutex<Option<Session>>>,
}
impl Drop for Transport {
    fn drop(&mut self) {
        self.control.cancel();
    }
}

impl Transport {
    async fn open(
        route: &Route,
        admission: Option<&str>,
        gate: OwnedMutexGuard<()>,
        completed: Option<oneshot::Sender<()>>,
    ) -> Result<Self> {
        let address = format!(
            "{}/v1/ws",
            route
                .origin
                .replacen("https://", "wss://", 1)
                .replacen("http://", "ws://", 1)
        );
        let mut request = address
            .into_client_request()
            .map_err(|_| "Invalid relay address.")?;
        if let Some(password) = admission {
            let value = format!("Basic {}", STANDARD.encode(format!("relay:{password}")));
            request.headers_mut().insert(
                "Authorization",
                value
                    .parse()
                    .map_err(|_| "Invalid relay admission credentials.")?,
            );
        }
        let config = WebSocketConfig::default()
            .max_message_size(Some(wire::MAX_CONTROL_SIZE))
            .max_frame_size(Some(wire::MAX_CONTROL_SIZE));
        let (mut socket, _) = tokio::time::timeout(
            NETWORK_TIMEOUT,
            connect_async_with_config(request, Some(config), false),
        )
        .await
        .map_err(|_| "Relay connection timed out.")?
        .map_err(|_| "Relay connection or admission failed.")?;
        let raw = wire::encode_control(&json!({"v":1,"type":"ui.attach","host_id":route.host_id}))?;
        tokio::time::timeout(
            NETWORK_TIMEOUT,
            socket.send(Message::Text(String::from_utf8(raw).unwrap().into())),
        )
        .await
        .map_err(|_| "Relay attach timed out.")?
        .map_err(|_| "Relay attach failed.")?;
        let (mut output, mut input) = socket.split();
        let (tx, mut writes) = mpsc::channel::<Write>(QUEUE);
        let (ping_tx, mut pings) = mpsc::channel(8);
        let (received, rx) = mpsc::channel(QUEUE);
        let (attached, attach_result) = oneshot::channel::<Result<String>>();
        let cancel = CancellationToken::new();
        let (status, status_rx) = watch::channel(SessionStatus::Connecting);
        let control = SessionControl {
            cancel: cancel.clone(),
            status: status_rx,
        };
        let final_status = status.clone();
        let cipher = Arc::new(Mutex::new(None::<Session>));
        let closing_cipher = cipher.clone();
        tokio::spawn(async move {
            let reader = async {
                let mut attached = Some(attached);
                let mut connection = None;
                while let Some(message) = input.next().await {
                    match message.map_err(|_| "Relay read failed.")? {
                        Message::Ping(p) => {
                            ping_tx
                                .try_send(p)
                                .map_err(|_| "Relay control queue is full.")?;
                        }
                        Message::Pong(_) => {}
                        Message::Text(raw) => {
                            let c = wire::decode_control(raw.as_bytes())?;
                            if connection.is_none() {
                                if c["type"] != "ui.attached" {
                                    return Err("Relay attach refused.".into());
                                }
                                let id = c["connection"]
                                    .as_str()
                                    .ok_or("Invalid relay attachment.")?
                                    .to_string();
                                connection = Some(id.clone());
                                if attached.take().unwrap().send(Ok(id)).is_err() {
                                    return Err("Relay attach cancelled.".into());
                                }
                            } else {
                                if c["type"] != "forward"
                                    || c["connection"].as_str() != connection.as_deref()
                                {
                                    return Err("Invalid relay routing message.".into());
                                }
                                let payload = field(&c, "payload")?;
                                let message = wire::decode_inner(&payload)?;
                                received
                                    .try_send(message)
                                    .map_err(|_| "Relay receive queue is full.")?;
                            }
                        }
                        _ => return Err("Relay connection closed.".into()),
                    }
                }
                Err::<(), String>("Relay connection closed.".into())
            };
            let writer = async {
                loop {
                    tokio::select! {
                        biased;
                        ping=pings.recv()=>{
                            let p=ping.ok_or("Relay control loop ended.")?;
                            tokio::time::timeout(NETWORK_TIMEOUT,output.send(Message::Pong(p))).await.map_err(|_|"Relay write timed out.")?.map_err(|_|"Relay write failed.")?;
                        },
                        write=writes.recv()=>{
                            let w=write.ok_or("Relay writer closed.")?;
                            let sent=async {
                                let payload=wire::encode_inner(&w.payload)?;
                                let raw=wire::encode_control(&json!({"v":1,"type":"forward","connection":w.connection,"payload":wire::b64(&payload)}))?;
                                tokio::time::timeout(NETWORK_TIMEOUT,output.send(Message::Text(String::from_utf8(raw).unwrap().into()))).await.map_err(|_|"Relay write timed out.")?.map_err(|_|"Relay write failed.")?;
                                Ok::<(),String>(())
                            }.await;
                            let failed=sent.is_err();
                            let _=w.done.send(sent);
                            if failed {return Err::<(),String>("Relay writer failed.".into());}
                        }
                    }
                }
            };
            tokio::select! { _=cancel.cancelled()=>{}, _=reader=>{}, _=writer=>{} }
            // Completion is observable only after both socket halves are gone.
            drop(input);
            drop(output);
            let cancelled = cancel.is_cancelled();
            cancel.cancel();
            if let Ok(mut cipher) = closing_cipher.lock()
                && let Some(mut session) = cipher.take()
            {
                session.close();
            }
            drop(gate);
            if let Some(completed) = completed {
                let _ = completed.send(());
            }
            final_status.send_replace(if cancelled {
                SessionStatus::Closed
            } else {
                SessionStatus::Failed
            });
        });
        let guard = CancelOnDrop::new(&control);
        let connection = tokio::time::timeout(NETWORK_TIMEOUT, attach_result)
            .await
            .map_err(|_| "Relay attach timed out.")?
            .map_err(|_| "Relay attach failed.")??;
        guard.disarm();
        Ok(Self {
            tx,
            rx,
            connection,
            control,
            status,
            cipher,
        })
    }
    fn install_crypto(&self, mut session: Session) -> Result<()> {
        let mut cipher = self
            .cipher
            .lock()
            .map_err(|_| "Relay cipher lock failed.")?;
        if self.control.cancel.is_cancelled() || cipher.is_some() {
            session.close();
            return Err("Relay connection closed.".into());
        }
        *cipher = Some(session);
        Ok(())
    }
    fn seal(&self, plain: &Value) -> Result<Value> {
        self.cipher
            .lock()
            .map_err(|_| "Relay cipher lock failed.")?
            .as_mut()
            .ok_or("Relay session closed.")?
            .seal(plain)
    }
    fn decrypt(&self, inner: &Value) -> Result<Value> {
        self.cipher
            .lock()
            .map_err(|_| "Relay cipher lock failed.")?
            .as_mut()
            .ok_or("Relay session closed.")?
            .open(inner)
    }
    async fn send(&self, payload: Value) -> Result<()> {
        let guard = CancelOnDrop::new(&self.control);
        let (done, result) = oneshot::channel();
        tokio::select! { _=self.control.cancel.cancelled()=>return Err("Relay connection closed.".into()), r=self.tx.send(Write{connection:self.connection.clone(),payload,done})=>r.map_err(|_|"Relay connection closed.")? }
        let result = tokio::select! { _=self.control.cancel.cancelled()=>Err("Relay connection closed.".into()), r=result=>r.map_err(|_|"Relay write failed.")? };
        if result.is_ok() {
            guard.disarm();
        }
        result
    }
    async fn receive(&mut self) -> Result<Value> {
        tokio::select! {_=self.control.cancel.cancelled()=>Err("Relay connection closed.".into()), v=self.rx.recv()=>v.ok_or("Relay connection closed.".into())}
    }
    async fn handshake_receive(&mut self) -> Result<Value> {
        tokio::time::timeout(NETWORK_TIMEOUT, self.receive())
            .await
            .map_err(|_| "Relay authentication timed out.".into())
            .and_then(|v| v)
    }
}
struct CancelOnDrop {
    control: SessionControl,
    armed: bool,
}
impl CancelOnDrop {
    fn new(c: &SessionControl) -> Self {
        Self {
            control: c.clone(),
            armed: true,
        }
    }
    fn disarm(mut self) {
        self.armed = false;
    }
}
impl Drop for CancelOnDrop {
    fn drop(&mut self) {
        if self.armed {
            self.control.cancel();
        }
    }
}

/// Shown by native UI only after verifying the host possession proof. Keep the
/// full key behind advanced UI; the six-character PIN retains its leading zeros.
#[derive(Clone, Debug, Serialize)]
pub struct PairingInfo {
    pub aid: String,
    pub pin: String,
    pub public_key: String,
    pub host_fp: String,
    pub ui_id: String,
}
/// Holds the exact consumed ceremony. Dropping it cancels the socket; it never
/// restarts or reveals again. A saved candidate can recover through reconnect.
pub struct Pairing {
    info: PairingInfo,
    transport: Transport,
    client: Arc<Inner>,
}
impl Pairing {
    pub fn info(&self) -> &PairingInfo {
        &self.info
    }
    pub fn control(&self) -> SessionControl {
        self.transport.control.clone()
    }
    pub async fn accepted(mut self) -> Result<RelaySession> {
        let inner = self.transport.receive().await?;
        let accepted = self.transport.decrypt(&inner)?;
        let s = self.client.snapshot()?;
        let a = s.attempt.as_ref().ok_or("No saved relay attempt.")?;
        let h = a.host.as_ref().ok_or("No saved relay candidate.")?;
        if accepted["type"] != "pair.accepted"
            || accepted["aid"] != a.aid
            || accepted["pair_epoch"] != h.epoch
            || accepted["host_fp"] != self.info.host_fp
            || accepted["ui_id"] != self.info.ui_id
        {
            return Err("Relay acceptance authentication failed.".into());
        }
        self.client.trust()?;
        RelaySession::new(self.transport)
    }
}
impl RelayClient {
    /// Explicit human-authorized new ceremony. No automatic fallback from a
    /// failed reconnect. Saves commitment and consumed reveal before sending.
    pub async fn begin_pair(&self) -> Result<Pairing> {
        self.begin_pair_inner(None).await
    }
    pub(super) async fn begin_pair_observed(
        &self,
        completed: oneshot::Sender<()>,
    ) -> Result<Pairing> {
        self.begin_pair_inner(Some(completed)).await
    }
    async fn begin_pair_inner(&self, completed: Option<oneshot::Sender<()>>) -> Result<Pairing> {
        let gate = self
            .inner
            .gate
            .clone()
            .try_lock_owned()
            .map_err(|_| "Relay connection is busy.")?;
        let commit = self.inner.prepare_commit()?;
        let s = self.inner.snapshot()?;
        let mut t = Transport::open(&s.route, s.admission.as_deref(), gate, completed).await?;
        transition(&t.status, SessionStatus::Pairing)?;
        t.send(commit).await?;
        let host = t.handshake_receive().await?;
        let (reveal, transcript) = self.inner.reveal(&host)?;
        t.send(reveal).await?;
        let (handshake, init) =
            UIHandshake::new(transcript.auth_context()?, &self.inner.identity()?)?;
        t.send(init).await?;
        let (finish, proof) = handshake.finish(&t.handshake_receive().await?)?;
        self.inner.save_root(&proof.notification_root()?)?;
        // This save precedes the final proof: a host could accept immediately
        // after processing it, even if the reply or this UI is then lost.
        t.install_crypto(proof.into_session()?)?;
        t.send(finish).await?;
        let info = PairingInfo {
            aid: wire::b64(&transcript.aid),
            pin: transcript.pin()?,
            public_key: wire::b64(&transcript.pku),
            host_fp: crypto::fingerprint("host", &transcript.pkh)?,
            ui_id: self.identity_id()?,
        };
        Ok(Pairing {
            info,
            transport: t,
            client: self.inner.clone(),
        })
    }
    /// Fresh handshake against only the saved key/epoch. The host must prove an
    /// active registration with the original attempt id; candidates gain no
    /// authority from a successful socket attachment or from a saved root.
    pub async fn reconnect(&self) -> Result<RelaySession> {
        self.reconnect_inner(None).await
    }
    // Completion belongs to exactly this attempt, even if its future is
    // cancelled during attach/authentication before it returns a SessionControl.
    pub(super) async fn reconnect_observed(
        &self,
        completed: oneshot::Sender<()>,
    ) -> Result<RelaySession> {
        self.reconnect_inner(Some(completed)).await
    }
    async fn reconnect_inner(
        &self,
        completed: Option<oneshot::Sender<()>>,
    ) -> Result<RelaySession> {
        let gate = self
            .inner
            .gate
            .clone()
            .try_lock_owned()
            .map_err(|_| "Relay connection is busy.")?;
        let s = self.inner.snapshot()?;
        let a = s.attempt.as_ref().ok_or("Relay pairing is required.")?;
        let h = a
            .host
            .as_ref()
            .filter(|h| h.root.is_some())
            .ok_or("Relay pairing did not finish possession proof.")?;
        let id = self.inner.identity()?;
        let context = AuthContext {
            route: s.route.clone(),
            mode: "reconnect".into(),
            pkh: wire::unb64(&h.pk)?,
            pku: id.public_key(),
            pair_epoch: wire::unb64(&h.epoch)?,
            pair_hash: vec![],
        };
        let mut t = Transport::open(&s.route, s.admission.as_deref(), gate, completed).await?;
        transition(&t.status, SessionStatus::Authenticating)?;
        let (hs, init) = UIHandshake::new(context, &id)?;
        t.send(init).await?;
        let (finish, proof) = hs.finish(&t.handshake_receive().await?)?;
        t.install_crypto(proof.into_session()?)?;
        t.send(finish).await?;
        let ready_record = t.handshake_receive().await?;
        let ready = t.decrypt(&ready_record)?;
        if ready["type"] != "ready"
            || ready["state"] != "active"
            || ready["pair_epoch"] != h.epoch
            || ready["aid"] != a.aid
        {
            return Err("Relay ready authentication failed.".into());
        }
        self.inner.trust()?;
        t.send(t.seal(&json!({"v":1,"type":"ready.ack","pair_epoch":h.epoch}))?)
            .await?;
        RelaySession::new(t)
    }
}

struct Shared {
    crypto: Arc<Mutex<Option<Session>>>,
    send: AsyncMutex<()>,
    control: SessionControl,
}
/// Application records share one ordered cipher sequence in each direction.
/// Multiplexing/flow-control policy belongs to the next layer.
pub struct RelaySession {
    sender: RecordSender,
    receiver: RecordReceiver,
}
#[derive(Clone)]
pub struct RecordSender {
    shared: Arc<Shared>,
    tx: mpsc::Sender<Write>,
    connection: String,
}
/// There is deliberately no Clone. Exactly one consumer demultiplexes records.
pub struct RecordReceiver {
    shared: Arc<Shared>,
    transport: Transport,
}
impl RelaySession {
    fn new(transport: Transport) -> Result<Self> {
        transition(&transport.status, SessionStatus::Ready)?;
        let shared = Arc::new(Shared {
            crypto: transport.cipher.clone(),
            send: AsyncMutex::new(()),
            control: transport.control.clone(),
        });
        Ok(Self {
            sender: RecordSender {
                shared: shared.clone(),
                tx: transport.tx.clone(),
                connection: transport.connection.clone(),
            },
            receiver: RecordReceiver { shared, transport },
        })
    }
    pub fn control(&self) -> SessionControl {
        self.sender.control()
    }
    pub async fn close(&self) {
        self.control().close().await;
    }
    pub fn split(self) -> (RecordSender, RecordReceiver) {
        (self.sender, self.receiver)
    }
}
impl RecordSender {
    pub fn control(&self) -> SessionControl {
        self.shared.control.clone()
    }
    pub async fn send(&self, plain: Value) -> Result<()> {
        let _send = self.shared.send.lock().await;
        let guard = CancelOnDrop::new(&self.shared.control);
        if !plain["type"]
            .as_str()
            .is_some_and(|s| s.starts_with("stream."))
        {
            return Err("Unexpected relay application record.".into());
        }
        let payload = self
            .shared
            .crypto
            .lock()
            .map_err(|_| "Relay cipher lock failed.")?
            .as_mut()
            .ok_or("Relay session closed.")?
            .seal(&plain)?;
        let (done, result) = oneshot::channel();
        tokio::select! {_=self.shared.control.cancel.cancelled()=>return Err("Relay session closed.".into()), r=self.tx.send(Write{connection:self.connection.clone(),payload,done})=>r.map_err(|_|"Relay session closed.")?}
        tokio::select! {_=self.shared.control.cancel.cancelled()=>return Err("Relay session closed.".into()), r=result=>r.map_err(|_|"Relay write failed.")??}
        guard.disarm();
        Ok(())
    }
}
impl RecordReceiver {
    pub fn control(&self) -> SessionControl {
        self.shared.control.clone()
    }
    pub async fn receive(&mut self) -> Result<Value> {
        let inner = self.transport.receive().await?;
        let guard = CancelOnDrop::new(&self.shared.control);
        let plain = self.transport.decrypt(&inner)?;
        if !plain["type"]
            .as_str()
            .is_some_and(|s| s.starts_with("stream."))
        {
            return Err("Unexpected relay application record.".into());
        }
        guard.disarm();
        Ok(plain)
    }
}

fn transition(status: &watch::Sender<SessionStatus>, next: SessionStatus) -> Result<()> {
    let changed = status.send_if_modified(|current| {
        if matches!(*current, SessionStatus::Closed | SessionStatus::Failed) {
            return false;
        }
        *current = next;
        true
    });
    if changed {
        Ok(())
    } else {
        Err("Relay connection closed.".into())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[tokio::test]
    async fn close_finishes_socket_and_erases_crypto_before_releasing_gate() {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let route = Route::new(
            &format!("http://{}", listener.local_addr().unwrap()),
            "office",
            true,
        )
        .unwrap();
        let server = tokio::spawn(async move {
            let (stream, _) = listener.accept().await.unwrap();
            let mut ws = tokio_tungstenite::accept_async(stream).await.unwrap();
            let attach = ws.next().await.unwrap().unwrap();
            assert_eq!(
                wire::decode_control(attach.into_data().as_ref()).unwrap()["type"],
                "ui.attach"
            );
            let raw = wire::encode_control(
                &json!({"v":1,"type":"ui.attached","connection":wire::b64(&[1;16])}),
            )
            .unwrap();
            ws.send(Message::Text(String::from_utf8(raw).unwrap().into()))
                .await
                .unwrap();
            // Exact socket EOF/error from the client's close, no elapsed-time condition.
            assert!(matches!(
                ws.next().await,
                None | Some(Err(_)) | Some(Ok(Message::Close(_)))
            ));
        });
        let gate = Arc::new(AsyncMutex::new(()));
        let t = Transport::open(&route, None, gate.clone().lock_owned().await, None)
            .await
            .unwrap();
        t.install_crypto(crypto::test_support::session()).unwrap();
        t.control.close().await;
        assert!(gate.try_lock().is_ok());
        assert!(t.cipher.lock().unwrap().is_none());
        assert!(
            t.seal(&json!({"v":1,"type":"stream.open","id":"1"}))
                .is_err()
        );
        server.await.unwrap();
    }
    #[test]
    fn terminal_status_cannot_be_overwritten_by_a_late_handshake_transition() {
        for terminal in [SessionStatus::Closed, SessionStatus::Failed] {
            for phase in [
                SessionStatus::Pairing,
                SessionStatus::Authenticating,
                SessionStatus::Ready,
            ] {
                let (tx, rx) = watch::channel(terminal);
                assert!(transition(&tx, phase).is_err());
                assert_eq!(*rx.borrow(), terminal);
            }
        }
    }
}
