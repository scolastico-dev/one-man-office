//! Real native bridge -> encrypted records -> production Go relay/server.
use futures_util::{SinkExt, StreamExt};
use omo_ui_core::{
    Result,
    relay::{
        BoardEndpoint, BoardTransport, RelayClient, SecretStore, TransportStatus, wire::Route,
    },
};
use serde_json::{Value, json};
use std::{
    io::{BufRead, BufReader, Write},
    process::{Child, ChildStdin, ChildStdout, Command, Stdio},
    sync::{Arc, Mutex},
};
use tokio::{
    io::{AsyncBufReadExt, AsyncReadExt, AsyncWriteExt},
    net::TcpStream,
};
use tokio_tungstenite::tungstenite::{Message, client::IntoClientRequest};

#[derive(Default)]
struct Secrets(Mutex<Option<Vec<u8>>>);
impl SecretStore for Secrets {
    fn load(&self, _: &str) -> Result<Option<Vec<u8>>> {
        Ok(self.0.lock().unwrap().clone())
    }
    fn save(&self, _: &str, v: &[u8]) -> Result<()> {
        *self.0.lock().unwrap() = Some(v.to_vec());
        Ok(())
    }
}
struct Fixture {
    child: Child,
    input: Option<ChildStdin>,
    output: BufReader<ChildStdout>,
    info: Value,
}
impl Fixture {
    fn start() -> Self {
        let binary = std::env::var_os("OMO_UI_RELAY_FIXTURE")
            .expect("run node ui/scripts/relay-probe.mjs --board");
        let mut child = Command::new(binary)
            .args([
                "-test.run=^TestNativeRelayBoardFixture$",
                "-native-relay-fixture",
            ])
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::inherit())
            .spawn()
            .unwrap();
        let input = child.stdin.take();
        let output = BufReader::new(child.stdout.take().unwrap());
        let mut f = Self {
            child,
            input,
            output,
            info: Value::Null,
        };
        f.info = f.response();
        f
    }
    fn response(&mut self) -> Value {
        let mut line = String::new();
        assert_ne!(
            self.output.read_line(&mut line).unwrap(),
            0,
            "fixture exited"
        );
        let r: Value = serde_json::from_str(&line).unwrap_or_else(|_| {
            drop(self.input.take());
            let mut details = String::new();
            std::io::Read::read_to_string(&mut self.output, &mut details).unwrap();
            panic!("fixture failed: {line}{details}")
        });
        assert_eq!(r["ok"], true, "fixture error: {}", r["error"]);
        r["data"].clone()
    }
    fn command(&mut self, c: Value) -> Value {
        let input = self.input.as_mut().unwrap();
        serde_json::to_writer(&mut *input, &c).unwrap();
        input.write_all(b"\n").unwrap();
        input.flush().unwrap();
        self.response()
    }
    fn route(&self) -> Route {
        Route::new(
            self.info["origin"].as_str().unwrap(),
            self.info["host_id"].as_str().unwrap(),
            true,
        )
        .unwrap()
    }
}
impl Drop for Fixture {
    fn drop(&mut self) {
        drop(self.input.take());
        let status = self.child.wait().unwrap();
        if !std::thread::panicking() {
            assert!(status.success());
        }
    }
}
async fn ready(board: &BoardTransport) {
    let mut status = board.subscribe();
    loop {
        if *status.borrow_and_update() == TransportStatus::Ready {
            return;
        }
        status.changed().await.unwrap();
    }
}
async fn unavailable(board: &BoardTransport) {
    let mut status = board.subscribe();
    loop {
        if matches!(
            *status.borrow_and_update(),
            TransportStatus::Reconnecting | TransportStatus::Unavailable
        ) {
            return;
        }
        status.changed().await.unwrap();
    }
}
async fn connect(endpoint: &BoardEndpoint, method: &str, path: &str, headers: &str) -> TcpStream {
    let mut socket = TcpStream::connect(endpoint.origin.trim_start_matches("http://"))
        .await
        .unwrap();
    socket
        .write_all(
            format!(
                "{method} {path} HTTP/1.1\r\nHost: {}\r\nAuthorization: {}\r\n{headers}\r\n",
                endpoint.origin.trim_start_matches("http://"),
                endpoint.authorization()
            )
            .as_bytes(),
        )
        .await
        .unwrap();
    socket
}
async fn response(
    endpoint: &BoardEndpoint,
    method: &str,
    path: &str,
    headers: &str,
    body: &[u8],
) -> (String, Vec<u8>) {
    let mut socket = connect(endpoint, method, path, headers).await;
    socket.write_all(body).await.unwrap();
    let mut bytes = Vec::new();
    socket.read_to_end(&mut bytes).await.unwrap();
    let n = bytes
        .windows(4)
        .position(|b| b == b"\r\n\r\n")
        .expect("HTTP response")
        + 4;
    (
        String::from_utf8(bytes[..n].to_vec()).unwrap(),
        bytes[n..].to_vec(),
    )
}
fn ok(header: &str) {
    assert!(header.starts_with("HTTP/1.1 200"), "{header}");
}
async fn event(reader: &mut tokio::io::BufReader<TcpStream>, needle: &str) -> String {
    let mut block = String::new();
    loop {
        let mut line = String::new();
        assert_ne!(reader.read_line(&mut line).await.unwrap(), 0, "SSE ended");
        block.push_str(&line);
        if line == "\n" || line == "\r\n" {
            if block.contains(needle) {
                return block;
            }
            block.clear();
        }
    }
}
type TerminalSocket =
    tokio_tungstenite::WebSocketStream<tokio_tungstenite::MaybeTlsStream<TcpStream>>;
async fn terminal(endpoint: &BoardEndpoint, id: &str) -> TerminalSocket {
    let target = format!(
        "{}/api/agents/{}/terminal",
        endpoint.origin.replacen("http://", "ws://", 1),
        id
    );
    let mut request = target.into_client_request().unwrap();
    request
        .headers_mut()
        .insert("Authorization", endpoint.authorization().parse().unwrap());
    request
        .headers_mut()
        .insert("Origin", endpoint.origin.parse().unwrap());
    request
        .headers_mut()
        .insert("Sec-WebSocket-Protocol", "omo".parse().unwrap());
    let (mut ws, reply) = tokio_tungstenite::connect_async(request).await.unwrap();
    assert_eq!(reply.headers()["Sec-WebSocket-Protocol"], "omo");
    loop {
        let r = ws.next().await.unwrap().unwrap();
        if r.to_text().is_ok_and(|s| s.contains("replay-end")) {
            break;
        }
    }
    ws
}
async fn paired(f: &mut Fixture, store: Arc<Secrets>) -> (RelayClient, BoardTransport) {
    let client = RelayClient::open(
        store,
        "board",
        f.route(),
        Some(f.info["admission"].as_str().unwrap().into()),
    )
    .unwrap();
    f.command(json!({"op":"begin"}));
    let pairing = client.begin_pair().await.unwrap();
    f.command(json!({"op":"pending"}));
    f.command(json!({"op":"accept","aid":pairing.info().aid,"pin":pairing.info().pin}));
    let board = BoardTransport::start(client.clone(), Some(pairing.accepted().await.unwrap()))
        .await
        .unwrap();
    ready(&board).await;
    (client, board)
}

#[tokio::test]
async fn real_board_assets_api_streamed_upload_sse_and_terminal_share_encryption() {
    let mut f = Fixture::start();
    let (_client, board) = paired(&mut f, Arc::new(Secrets::default())).await;
    let endpoint = board.endpoint();
    let (head, body) = response(endpoint, "GET", "/", "", b"").await;
    ok(&head);
    let html = String::from_utf8(body).unwrap();
    assert!(html.contains("<script type=\"module\""));
    let asset = html
        .split("src=\"")
        .find_map(|part| {
            part.starts_with("/assets/")
                .then(|| part.split('"').next().unwrap())
        })
        .unwrap();
    let (head, body) = response(endpoint, "GET", asset, "", b"").await;
    ok(&head);
    assert!(body.len() > 1000);
    let (head, body) = response(endpoint, "GET", "/api/me", "", b"").await;
    ok(&head);
    assert_eq!(
        serde_json::from_slice::<Value>(&body).unwrap()["actor"]["role"],
        "user"
    );

    // A valid raw PNG, sent using real chunked framing, crosses many stream
    // credit windows. No buffered HTTP adapter can substitute for this path.
    let png = include_bytes!("../../src-tauri/icons/icon.png");
    let mut upload=connect(endpoint,"POST",&format!("/api/tickets/{}/images",f.info["ticket"]),"Transfer-Encoding: chunked\r\nContent-Type: application/octet-stream\r\nOmo-Image-Name: native.png\r\n").await;
    let mut upload_body = Vec::new();
    // PNG accepts trailing bytes; preserve those too, beyond 64 KiB.
    upload_body.extend_from_slice(png);
    upload_body.extend(vec![0x53; 150_000]);
    for chunk in upload_body.chunks(4096) {
        upload
            .write_all(format!("{:x}\r\n", chunk.len()).as_bytes())
            .await
            .unwrap();
        upload.write_all(chunk).await.unwrap();
        upload.write_all(b"\r\n").await.unwrap();
    }
    upload.write_all(b"0\r\n\r\n").await.unwrap();
    let mut uploaded = Vec::new();
    upload.read_to_end(&mut uploaded).await.unwrap();
    assert!(
        uploaded.starts_with(b"HTTP/1.1 201"),
        "{}",
        String::from_utf8_lossy(&uploaded)
    );
    let n = uploaded.windows(4).position(|b| b == b"\r\n\r\n").unwrap() + 4;
    let image: Value = serde_json::from_slice(&uploaded[n..]).unwrap();
    let (head, body) = response(
        endpoint,
        "GET",
        &format!("/api/images/{}/content", image["id"].as_str().unwrap()),
        "",
        b"",
    )
    .await;
    ok(&head);
    assert_eq!(body, upload_body);

    let mut sse = tokio::io::BufReader::new(connect(endpoint, "GET", "/api/events", "").await);
    event(&mut sse, "retry: 3000").await;
    let (head, _) = response(
        endpoint,
        "PUT",
        "/api/server/paused",
        "Content-Type: application/json\r\nContent-Length: 15\r\n",
        br#"{"paused":true}"#,
    )
    .await;
    ok(&head);
    let live = event(&mut sse, "event: server.paused").await;
    let id = live
        .lines()
        .find_map(|line| line.strip_prefix("id: "))
        .unwrap();
    drop(sse);
    let (head, _) = response(
        endpoint,
        "PUT",
        "/api/server/paused",
        "Content-Type: application/json\r\nContent-Length: 16\r\n",
        br#"{"paused":false}"#,
    )
    .await;
    ok(&head);
    let mut sse = tokio::io::BufReader::new(
        connect(
            endpoint,
            "GET",
            "/api/events",
            &format!("Last-Event-ID: {id}\r\n"),
        )
        .await,
    );
    event(&mut sse, "event: server.paused").await;
    let mut resync = tokio::io::BufReader::new(
        connect(endpoint, "GET", "/api/events?since=999999999999", "").await,
    );
    event(&mut resync, "event: resync").await;

    let mut ws = terminal(endpoint, f.info["terminal"].as_str().unwrap()).await;
    ws.send(Message::Binary(b"typed".to_vec().into()))
        .await
        .unwrap();
    loop {
        let r = ws.next().await.unwrap().unwrap();
        if r.to_text().is_ok_and(|s| s.contains("input-ack")) {
            break;
        }
    }
    assert_eq!(f.command(json!({"op":"typed"})), "typed");
    ws.send(Message::Text(
        r#"{"type":"message","id":7,"text":"hello","keys":"\r"}"#.into(),
    ))
    .await
    .unwrap();
    loop {
        let r = ws.next().await.unwrap().unwrap();
        if r.to_text().is_ok_and(|s| s.contains("message-ack")) {
            assert!(safely_text(&r).contains("\"id\":7"));
            break;
        }
    }
    assert_eq!(f.command(json!({"op":"typed"})), "typedhello\r");
    f.command(json!({"op":"output"}));
    loop {
        let r = ws.next().await.unwrap().unwrap();
        if let Message::Binary(bytes) = r {
            if bytes.windows(19).any(|b| b == b"native relay output") {
                break;
            }
        }
    }
    ws.close(None).await.unwrap();
    drop(ws);
    drop(sse);
    drop(resync);
    board.close().await;
    assert_eq!(board.status(), TransportStatus::Stopped);
    assert!(
        TcpStream::connect(endpoint.origin.trim_start_matches("http://"))
            .await
            .is_err()
    );
}
fn safely_text(r: &Message) -> &str {
    r.to_text().unwrap()
}

#[tokio::test]
async fn bridge_recovers_after_host_and_relay_loss_without_pairing_and_revocation_stays_visible() {
    let mut f = Fixture::start();
    let store = Arc::new(Secrets::default());
    let (client, board) = paired(&mut f, store.clone()).await;
    let id = client.identity_id().unwrap();
    let origin = board.endpoint().origin.clone();
    let mut ws = terminal(board.endpoint(), f.info["terminal"].as_str().unwrap()).await;
    ws.send(Message::Binary(b"before-loss".to_vec().into()))
        .await
        .unwrap();
    loop {
        let r = ws.next().await.unwrap().unwrap();
        if r.to_text().is_ok_and(|s| s.contains("input-ack")) {
            break;
        }
    }
    assert_eq!(f.command(json!({"op":"typed"})), "before-loss");
    let body = br#"{"body":"uncertain-once"}"#;
    let mut uncertain = connect(
        board.endpoint(),
        "POST",
        &format!("/api/tickets/{}/comments", f.info["ticket"]),
        &format!(
            "Content-Type: application/json\r\nContent-Length: {}\r\n",
            body.len()
        ),
    )
    .await;
    uncertain.write_all(body).await.unwrap();
    // Observe committed storage through the control pipe, leaving the caller's
    // HTTP response unread. Reconnect must not resend an uncertain mutation.
    let (returned, count) = tokio::task::spawn_blocking(move || {
        let count = f.command(json!({"op":"wait-write"}));
        (f, count)
    })
    .await
    .unwrap();
    f = returned;
    assert_eq!(count, 1);
    f.command(json!({"op":"stop-host"}));
    unavailable(&board).await;
    // A failed reconnect before host re-registration must keep retrying.
    let mut status = board.subscribe();
    loop {
        if *status.borrow_and_update() == TransportStatus::Unavailable {
            break;
        }
        status.changed().await.unwrap();
    }
    let (head, _) = response(
        board.endpoint(),
        "POST",
        "/api/tickets",
        "Content-Type: application/json\r\nContent-Length: 2\r\n",
        b"{}",
    )
    .await;
    assert!(head.starts_with("HTTP/1.1 503"));
    f.info = f.command(json!({"op":"start-host"}));
    ready(&board).await;
    assert_eq!(board.endpoint().origin, origin);
    assert_eq!(f.command(json!({"op":"write-count"})), 1);
    assert_eq!(f.command(json!({"op":"typed"})), "");
    drop(ws);
    drop(uncertain);

    let (head, _) = response(board.endpoint(), "GET", "/api/me", "", b"").await;
    ok(&head);
    assert_eq!(f.command(json!({"op":"write-count"})), 1);
    f.command(json!({"op":"stop-relay"}));
    unavailable(&board).await;
    f.command(json!({"op":"start-relay"}));
    ready(&board).await;
    let (head, _) = response(board.endpoint(), "GET", "/api/me", "", b"").await;
    ok(&head);
    board.close().await;
    let client = RelayClient::restore(store, "board", f.route()).unwrap();
    assert_eq!(client.identity_id().unwrap(), id);
    let board = BoardTransport::start(client, None).await.unwrap();
    ready(&board).await;
    f.command(json!({"op":"revoke","ui_id":id}));
    unavailable(&board).await;
    let (head, _) = response(board.endpoint(), "GET", "/api/me", "", b"").await;
    assert!(head.starts_with("HTTP/1.1 503"));
    board.close().await;
}
