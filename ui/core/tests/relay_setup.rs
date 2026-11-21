use futures_util::{SinkExt, StreamExt};
use omo_ui_core::{
    Result,
    relay::{
        RelayClient, RelayConnection, RelayPhase, SecretStore,
        wire::{self, Route},
    },
};
use serde_json::json;
use std::sync::{Arc, Mutex};
use tokio_tungstenite::tungstenite::Message;
#[derive(Default)]
struct Secrets(Mutex<Option<Vec<u8>>>);
impl SecretStore for Secrets {
    fn load(&self, _: &str) -> Result<Option<Vec<u8>>> {
        Ok(self.0.lock().unwrap().clone())
    }
    fn save(&self, _: &str, data: &[u8]) -> Result<()> {
        *self.0.lock().unwrap() = Some(data.to_vec());
        Ok(())
    }
}
#[tokio::test]
async fn cancelling_pairing_joins_socket_and_reconnect_never_sends_another_commit() {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let route = Route::new(
        &format!("http://{}", listener.local_addr().unwrap()),
        "office",
        true,
    )
    .unwrap();
    let secrets = Arc::new(Secrets::default());
    let client = RelayClient::open(secrets.clone(), "device", route.clone(), None).unwrap();
    let (committed, commit) = tokio::sync::oneshot::channel();
    let server = tokio::spawn(async move {
        let (socket, _) = listener.accept().await.unwrap();
        let mut ws = tokio_tungstenite::accept_async(socket).await.unwrap();
        ws.next().await.unwrap().unwrap();
        ws.send(Message::Text(
            serde_json::to_string(
                &json!({"v":1,"type":"ui.attached","connection":wire::b64(&[1;16])}),
            )
            .unwrap()
            .into(),
        ))
        .await
        .unwrap();
        let message = ws.next().await.unwrap().unwrap();
        let outer = wire::decode_control(&message.into_data()).unwrap();
        let inner =
            wire::decode_inner(&wire::unb64(outer["payload"].as_str().unwrap()).unwrap()).unwrap();
        assert_eq!(inner["type"], "pair.commit");
        committed.send(()).unwrap();
        assert!(matches!(
            ws.next().await,
            None | Some(Err(_)) | Some(Ok(Message::Close(_)))
        ));
    });
    let connection = RelayConnection::start(client.clone(), true);
    commit.await.unwrap();
    connection.close().await;
    assert_eq!(connection.view().phase, RelayPhase::Cancelled);
    server.await.unwrap();
    drop(connection);
    drop(client);
    let restored = RelayClient::restore(secrets, "device", route).unwrap();
    let reconnect = RelayConnection::start(restored, false);
    let mut state = reconnect.subscribe();
    while state.borrow_and_update().phase != RelayPhase::Failed {
        state.changed().await.unwrap();
    }
    assert!(state.borrow().pairing.is_none());
    reconnect.close().await;
}
#[tokio::test]
async fn restore_missing_secrets_does_not_create_a_replacement_identity() {
    let secrets = Arc::new(Secrets::default());
    let route = Route::new("https://relay.example", "office", false).unwrap();
    assert!(RelayClient::restore(secrets.clone(), "saved", route.clone()).is_err());
    assert!(
        RelayClient::restore_with_admission(
            secrets.clone(),
            "saved",
            route,
            Some("new-password".into())
        )
        .is_err()
    );
    assert!(secrets.0.lock().unwrap().is_none());
}
