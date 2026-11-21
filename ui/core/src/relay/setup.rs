//! Native connection owner. Public presentation never includes private keys or bridge credentials.
use super::{BoardEndpoint, BoardTransport, PairingInfo, RelayClient, TransportStatus, TrustState};
use crate::Result;
use serde::Serialize;
use std::sync::{Arc, Mutex};
use tokio::sync::watch;
use tokio_util::sync::CancellationToken;

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum RelayPhase {
    Pairing,
    AwaitingAccept,
    Connecting,
    Ready,
    Reconnecting,
    Unavailable,
    Failed,
    Cancelled,
}
#[derive(Clone, Debug, Serialize)]
pub struct RelayView {
    pub phase: RelayPhase,
    pub pairing: Option<PairingInfo>,
}
pub struct RelayConnection {
    cancel: CancellationToken,
    view: watch::Receiver<RelayView>,
    endpoint: Arc<Mutex<Option<BoardEndpoint>>>,
    done: watch::Receiver<bool>,
}
impl Drop for RelayConnection {
    fn drop(&mut self) {
        self.cancel.cancel();
    }
}
impl RelayConnection {
    /// `new_pair` comes only from an explicit local user action. Restarts pass false.
    pub fn start(client: RelayClient, new_pair: bool) -> Self {
        let cancel = CancellationToken::new();
        let stopping = cancel.clone();
        let (view, receiver) = watch::channel(RelayView {
            phase: if new_pair {
                RelayPhase::Pairing
            } else {
                RelayPhase::Connecting
            },
            pairing: None,
        });
        let (done, completed) = watch::channel(false);
        let endpoint = Arc::new(Mutex::new(None));
        let address = endpoint.clone();
        tokio::spawn(async move {
            let mut transport = None;
            let mut pairing_done = None;
            let work = async {
                let session = if new_pair {
                    let (completed, done) = tokio::sync::oneshot::channel();
                    pairing_done = Some(done);
                    let pairing = client.begin_pair_observed(completed).await?;
                    view.send_replace(RelayView {
                        phase: RelayPhase::AwaitingAccept,
                        pairing: Some(pairing.info().clone()),
                    });
                    Some(pairing.accepted().await?)
                } else {
                    if !matches!(
                        client.status()?.state,
                        TrustState::Trusted | TrustState::Candidate
                    ) {
                        return Err("A new pairing requires an explicit user action.".into());
                    }
                    None
                };
                transport = Some(BoardTransport::start(client.clone(), session).await?);
                let board = transport.as_ref().unwrap();
                *address.lock().unwrap() = Some(board.endpoint().clone());
                let mut status = board.subscribe();
                loop {
                    let phase = match *status.borrow_and_update() {
                        TransportStatus::Connecting => RelayPhase::Connecting,
                        TransportStatus::Ready => RelayPhase::Ready,
                        TransportStatus::Reconnecting => RelayPhase::Reconnecting,
                        TransportStatus::Unavailable => RelayPhase::Unavailable,
                        TransportStatus::Stopped => return Err("Relay transport stopped.".into()),
                    };
                    view.send_replace(RelayView {
                        phase,
                        pairing: None,
                    });
                    status
                        .changed()
                        .await
                        .map_err(|_| "Relay transport stopped.")?;
                }
            };
            let result: Result<()> = tokio::select! {_=stopping.cancelled()=>Ok(()),r=work=>r};
            if let Some(board) = transport {
                board.close().await;
            }
            // Dropping a pending begin/accept cancels its socket. Join its driver
            // before this owner's completion is visible, including early attach.
            if let Some(done) = pairing_done {
                let _ = done.await;
            }
            *address.lock().unwrap() = None;
            view.send_replace(RelayView {
                phase: if result.is_err() {
                    RelayPhase::Failed
                } else {
                    RelayPhase::Cancelled
                },
                pairing: None,
            });
            done.send_replace(true);
        });
        Self {
            cancel,
            view: receiver,
            endpoint,
            done: completed,
        }
    }
    pub fn view(&self) -> RelayView {
        self.view.borrow().clone()
    }
    pub fn subscribe(&self) -> watch::Receiver<RelayView> {
        self.view.clone()
    }
    /// Native-only endpoint; never serialize this or send it to setup JavaScript.
    pub fn endpoint(&self) -> Option<BoardEndpoint> {
        self.endpoint.lock().unwrap().clone()
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

#[cfg(test)]
mod tests {
    use super::*;
    use crate::relay::{SecretStore, wire::Route};
    #[derive(Default)]
    struct Secrets(Mutex<Option<Vec<u8>>>);
    impl SecretStore for Secrets {
        fn load(&self, _: &str) -> Result<Option<Vec<u8>>> {
            Ok(self.0.lock().unwrap().clone())
        }
        fn save(&self, _: &str, value: &[u8]) -> Result<()> {
            *self.0.lock().unwrap() = Some(value.to_vec());
            Ok(())
        }
    }
    #[tokio::test]
    async fn closing_a_busy_owner_does_not_join_an_unrelated_clients_gate() {
        let client = RelayClient::open(
            Arc::new(Secrets::default()),
            "profile",
            Route::new("https://relay.example", "office", false).unwrap(),
            None,
        )
        .unwrap();
        let unrelated = client.inner.gate.clone().lock_owned().await;
        let owner = RelayConnection::start(client, true);
        owner.close().await;
        drop(unrelated);
    }
}
