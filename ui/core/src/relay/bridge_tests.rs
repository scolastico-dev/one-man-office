use super::*;

fn endpoint() -> BoardEndpoint {
    BoardEndpoint {
        origin: "http://127.0.0.1:32123".into(),
        username: "omo-ui".into(),
        password: "bridge-only-secret".into(),
    }
}
fn request(target: &str, headers: &str) -> Vec<u8> {
    format!(
        "POST {target} HTTP/1.1\r\nHost: 127.0.0.1:32123\r\nAuthorization: {}\r\n{headers}\r\n",
        endpoint().authorization()
    )
    .into_bytes()
}
#[test]
fn native_headers_are_private_and_virtual_host_origin_are_canonical() {
    let raw = request(
        "/api/tickets?x=1",
        "Origin: http://127.0.0.1:32123\r\nContent-Length: 3\r\nContent-Type: application/json\r\nLast-Event-ID: 17\r\n",
    );
    let got = rewrite(&raw, &endpoint()).unwrap();
    let got = String::from_utf8(got).unwrap();
    assert!(got.starts_with("POST /api/tickets?x=1 HTTP/1.1\r\nHost: omo-relay.invalid\r\n"));
    assert!(got.contains("Origin: http://omo-relay.invalid\r\n"));
    assert!(got.contains("Content-Length: 3\r\n"));
    assert!(got.contains("Last-Event-ID: 17\r\n"));
    assert!(got.ends_with("Connection: close\r\n\r\n"));
    assert!(!got.contains("Authorization"));
    assert!(!got.contains("bridge-only-secret"));
    assert!(!got.contains("127.0.0.1"));
}
#[test]
fn bridge_rejects_missing_auth_foreign_origins_and_ambiguous_headers() {
    for raw in [
        b"GET / HTTP/1.1\r\nHost: 127.0.0.1:32123\r\n\r\n".to_vec(),
        request("/", "Origin: https://attacker.invalid\r\n"),
        request("/", "Origin: null\r\n"),
        request("/", "Sec-Fetch-Site: cross-site\r\n"),
        request("/", "Host: attacker.invalid\r\n"),
        request("/", "Authorization: Bearer other\r\n"),
        request("/", "Content-Length: 1\r\nTransfer-Encoding: chunked\r\n"),
        request("/", "Content-Length: 1\r\nContent-Length: 1\r\n"),
        request("/", "Omo-Relay-Password: secret\r\n"),
    ] {
        assert!(rewrite(&raw, &endpoint()).is_err());
    }
    for target in [
        "http://attacker.invalid/",
        "attacker.invalid:80",
        "//attacker.invalid/",
        "/a\\b",
    ] {
        assert!(
            rewrite(&request(target, ""), &endpoint()).is_err(),
            "{target}"
        );
    }
}
#[test]
fn websocket_handshake_and_application_headers_are_preserved() {
    let raw = request(
        "/api/terminals/t/ws",
        "Origin: http://127.0.0.1:32123\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Protocol: omo\r\nSec-WebSocket-Key: MTIzNA==\r\n",
    );
    let got = String::from_utf8(rewrite(&raw, &endpoint()).unwrap()).unwrap();
    assert!(got.contains("Connection: Upgrade\r\n"));
    assert!(got.contains("Sec-WebSocket-Protocol: omo\r\n"));
    assert!(got.contains("Sec-WebSocket-Key: MTIzNA==\r\n"));
    assert!(!got.contains("Connection: close"));
}

#[tokio::test]
async fn start_rejects_another_connections_ownership_without_waiting_on_it() {
    #[derive(Default)]
    struct Memory(std::sync::Mutex<Option<Vec<u8>>>);
    impl super::super::SecretStore for Memory {
        fn load(&self, _: &str) -> crate::Result<Option<Vec<u8>>> {
            Ok(self.0.lock().unwrap().clone())
        }
        fn save(&self, _: &str, value: &[u8]) -> crate::Result<()> {
            *self.0.lock().unwrap() = Some(value.to_vec());
            Ok(())
        }
    }
    let client = RelayClient::open(
        Arc::new(Memory::default()),
        "test",
        wire::Route::new("http://127.0.0.1:1", "host", true).unwrap(),
        None,
    )
    .unwrap();
    let _unrelated = client.inner.gate.clone().lock_owned().await;
    let result = BoardTransport::start(client, None).await;
    assert!(
        result.is_err(),
        "must reject ownership by another connection"
    );
}
