#![cfg(not(target_os = "android"))]
use omo_ui_core::*;

#[test]
fn virtual_hosts_match_the_server_contract() {
    let fixture: serde_json::Value = serde_json::from_str(include_str!(
        "../../../internal/api/testdata/forward_tunnel_v1.json"
    ))
    .unwrap();
    for case in fixture["virtualHosts"].as_array().unwrap() {
        let app = RegisteredForward {
            id: "0123456789abcdef0123456789abcdef".into(),
            name: case["name"].as_str().unwrap().into(),
            start_path: "/".into(),
            ticket: None,
        };
        assert_eq!(app.virtual_host().unwrap(), case["host"].as_str().unwrap());
    }
}

#[test]
fn virtual_origins_are_distinct_root_addresses_and_start_path_is_only_navigation() {
    let a: RegisteredForward = serde_json::from_value(serde_json::json!({
        "id":"0123456789abcdef0123456789abcdef", "name":"My Great App!",
        "start_path":"/start?q=a%2Fb", "ticket":7
    }))
    .unwrap();
    assert_eq!(
        a.virtual_host().unwrap(),
        "my-great-app-0123456789abcdef0123456789abcdef.omo"
    );
    assert_eq!(
        a.preview_url().unwrap().as_str(),
        "http://my-great-app-0123456789abcdef0123456789abcdef.omo/start?q=a%2Fb"
    );
    let mut b = a.clone();
    b.id = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa".into();
    assert_ne!(a.virtual_host().unwrap(), b.virtual_host().unwrap());
    b.name = "☃".into();
    assert_eq!(
        b.virtual_host().unwrap(),
        "app-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.omo"
    );
    b.name = "a".repeat(100);
    assert!(b.virtual_host().unwrap().split('.').next().unwrap().len() <= 63);
    for invalid in [
        "//evil.example/",
        "http://evil.example/",
        "/\\evil.example/",
        "/ok\r\n",
    ] {
        b.start_path = invalid.into();
        assert!(b.preview_url().is_err(), "{invalid:?}");
    }
}
#[test]
fn local_port_requires_a_decimal_nonzero_tcp_port() {
    assert_eq!(local_port("8099").unwrap(), 8099);
    assert_eq!(local_port("65535").unwrap(), 65535);
    for invalid in ["", "0", "65536", "-1", "+80", " 80", "80 ", "1.1", "abc"] {
        assert!(local_port(invalid).is_err(), "{invalid}");
    }
}

use futures_util::{SinkExt, StreamExt};
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::{TcpListener, TcpStream},
};
use tokio_tungstenite::{
    accept_hdr_async,
    tungstenite::{
        Message,
        handshake::server::{Request, Response},
    },
};
const ID: &str = "0123456789abcdef0123456789abcdef";
fn connector(address: std::net::SocketAddr) -> ForwardConnector {
    ForwardConnector::new(
        &Config {
            id: Config::new_id(),
            mode: Mode::Direct,
            origin: format!("http://{address}"),
            auth: Auth::Token,
            username: String::new(),
            notifications: false,
            notification_mode: NotificationMode::Background,
            remember: false,
            stop_with_window: false,
        },
        &"a".repeat(64),
    )
    .unwrap()
}
async fn echo_server(listener: TcpListener) {
    let (tcp, _) = listener.accept().await.unwrap();
    let mut ws = accept_hdr_async(tcp, |r: &Request, mut response: Response| {
        assert_eq!(r.uri().path(), format!("/api/ui/forwards/{ID}/tunnel"));
        assert_eq!(
            r.headers()["authorization"],
            format!("Bearer {}", "a".repeat(64))
        );
        response.headers_mut().insert(
            "sec-websocket-protocol",
            "ui.forwards.tunnel.v1".parse().unwrap(),
        );
        Ok(response)
    })
    .await
    .unwrap();
    while let Some(Ok(Message::Binary(data))) = ws.next().await {
        ws.send(Message::Binary(data)).await.unwrap();
    }
}
async fn socks(address: std::net::SocketAddr, host: &str, port: u16) -> (TcpStream, u8) {
    let mut tcp = TcpStream::connect(address).await.unwrap();
    tcp.write_all(&[5, 1, 0]).await.unwrap();
    let mut method = [0; 2];
    tcp.read_exact(&mut method).await.unwrap();
    assert_eq!(method, [5, 0]);
    let mut request = vec![5, 1, 0, 3, host.len() as u8];
    request.extend(host.as_bytes());
    request.extend(port.to_be_bytes());
    tcp.write_all(&request).await.unwrap();
    let mut reply = [0; 10];
    tcp.read_exact(&mut reply).await.unwrap();
    (tcp, reply[1])
}
// No HTTP parsing/re-serialization may buffer streaming or damage app WS bytes.
#[tokio::test]
async fn local_listener_streams_and_stop_joins_inflight_connections_and_releases_port() {
    let board = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let service = ForwardListener::local(connector(board.local_addr().unwrap()), ID.into(), 0)
        .await
        .unwrap();
    let address = service.address();
    assert_eq!(address.ip().to_string(), "127.0.0.1");
    let server = tokio::spawn(echo_server(board));
    assert!(
        ForwardListener::local(connector(address), ID.into(), address.port())
            .await
            .is_err()
    );
    let mut client = TcpStream::connect(address).await.unwrap();
    for bytes in [
        b"GET /events HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n".as_slice(),
        b"\x81\x03app",
    ] {
        client.write_all(bytes).await.unwrap();
        let mut out = vec![0; bytes.len()];
        client.read_exact(&mut out).await.unwrap();
        assert_eq!(out, bytes);
    }
    service.stop().await.unwrap();
    let mut byte = [0];
    assert_eq!(client.read(&mut byte).await.unwrap(), 0);
    let rebound = TcpListener::bind(address).await.unwrap();
    drop(rebound);
    server.await.unwrap();
}
#[tokio::test]
async fn preview_resolves_only_its_virtual_host_at_http_port_without_dns() {
    let board = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let host = format!("app-{ID}.omo");
    let service = ForwardListener::preview(
        connector(board.local_addr().unwrap()),
        ID.into(),
        host.clone(),
    )
    .await
    .unwrap();
    for (name, port) in [
        ("board.example", 80),
        (host.as_str(), 443),
        ("127.0.0.1", 80),
    ] {
        let (_, code) = socks(service.address(), name, port).await;
        assert_ne!(code, 0);
    }
    let server = tokio::spawn(echo_server(board));
    let (mut client, code) = socks(service.address(), &host, 80).await;
    assert_eq!(code, 0);
    let request = format!("GET /root.js HTTP/1.1\r\nHost: {host}\r\n\r\n");
    client.write_all(request.as_bytes()).await.unwrap();
    let mut response = vec![0; request.len()];
    client.read_exact(&mut response).await.unwrap();
    assert_eq!(response, request.as_bytes());
    service.stop().await.unwrap();
    server.await.unwrap();
}
#[tokio::test]
async fn failed_outer_handshake_is_a_clear_local_http_error_and_status() {
    let board = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let service = ForwardListener::local(connector(board.local_addr().unwrap()), ID.into(), 0)
        .await
        .unwrap();
    let server = tokio::spawn(async move {
        let (mut tcp, _) = board.accept().await.unwrap();
        let mut bytes = Vec::new();
        while !bytes.ends_with(b"\r\n\r\n") {
            bytes.push(tcp.read_u8().await.unwrap());
        }
        tcp.write_all(b"HTTP/1.1 401 Unauthorized\r\nContent-Length: 0\r\n\r\n")
            .await
            .unwrap();
    });
    let mut client = TcpStream::connect(service.address()).await.unwrap();
    let mut out = String::new();
    client.read_to_string(&mut out).await.unwrap();
    assert!(out.starts_with("HTTP/1.1 502"));
    assert!(out.contains("401"));
    assert!(!out.contains(&"a".repeat(64)));
    // EOF follows recording of the error, not an arbitrary event-loop delay.
    assert!(service.error().contains("401"));
    service.stop().await.unwrap();
    server.await.unwrap();
}

fn registry(body: &str) -> Vec<RegisteredForward> {
    use std::io::{Read, Write};
    let body = body.to_owned();
    let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let address = listener.local_addr().unwrap();
    let worker = std::thread::spawn(move || {
        let (mut stream, _) = listener.accept().unwrap();
        let mut request = Vec::new();
        while !request.ends_with(b"\r\n\r\n") {
            let mut byte = [0];
            stream.read_exact(&mut byte).unwrap();
            request.push(byte[0]);
        }
        let request = String::from_utf8(request).unwrap().to_ascii_lowercase();
        assert!(request.starts_with("get /api/proxy/forwards http/1.1\r\n"));
        assert!(request.contains(&format!("authorization: bearer {}", "a".repeat(64))));
        assert!(!request.contains("origin:"));
        write!(
            stream,
            "HTTP/1.1 200 OK\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
            body.len()
        )
        .unwrap();
    });
    let client = BoardClient::new(
        Config {
            id: Config::new_id(),
            mode: Mode::Direct,
            origin: format!("http://{address}"),
            auth: Auth::Token,
            username: String::new(),
            notifications: false,
            notification_mode: NotificationMode::Background,
            remember: false,
            stop_with_window: false,
        },
        "a".repeat(64),
    )
    .unwrap();
    let apps = client.forwards();
    worker.join().unwrap();
    apps.unwrap()
}

#[test]
fn registry_uses_native_auth_and_ignores_upstream_addresses() {
    let apps = registry(
        r#"{"forwards":[{"id":"0123456789abcdef0123456789abcdef","name":"","start_path":"/start","ticket":null,"host":"private-upstream","port":1234,"address":"https://do-not-open.example/proxy/secret"}]}"#,
    );
    assert_eq!(apps.len(), 1);
    assert_eq!(
        apps[0].preview_url().unwrap().as_str(),
        "http://app-0123456789abcdef0123456789abcdef.omo/start"
    );
}

// A preview-only path error must not discard other apps or prevent local use.
#[test]
fn registry_preserves_query_backslashes_and_defers_preview_validation() {
    let apps = registry(
        r#"{"forwards":[
        {"id":"0123456789abcdef0123456789abcdef","name":"query","start_path":"/start?a=\\b&escaped=%2F%5C&next=?yes","ticket":null},
        {"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","name":"ordinary","start_path":"/","ticket":null},
        {"id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","name":"bad preview","start_path":"/\\bad-path","ticket":null}
    ]}"#,
    );
    assert_eq!(apps.len(), 3);
    assert_eq!(
        apps[0].preview_url().unwrap().as_str(),
        r"http://query-0123456789abcdef0123456789abcdef.omo/start?a=\b&escaped=%2F%5C&next=?yes"
    );
    assert_eq!(
        apps[1].preview_url().unwrap().as_str(),
        "http://ordinary-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.omo/"
    );
    assert!(apps[2].preview_url().is_err());

    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .unwrap();
    runtime.block_on(async {
        // Local selection uses only the ID, including when preview is unusable.
        for id in [
            ID,
            "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
        ] {
            let selected = apps.iter().find(|app| app.id == id).unwrap();
            let board = TcpListener::bind("127.0.0.1:0").await.unwrap();
            let service = ForwardListener::local(
                connector(board.local_addr().unwrap()),
                selected.id.clone(),
                0,
            )
            .await
            .unwrap();
            let selected_id = selected.id.clone();
            let server = tokio::spawn(async move {
                let (tcp, _) = board.accept().await.unwrap();
                let mut ws = accept_hdr_async(tcp, |r: &Request, mut response: Response| {
                    assert_eq!(
                        r.uri().path(),
                        format!("/api/ui/forwards/{selected_id}/tunnel")
                    );
                    response.headers_mut().insert(
                        "sec-websocket-protocol",
                        "ui.forwards.tunnel.v1".parse().unwrap(),
                    );
                    Ok(response)
                })
                .await
                .unwrap();
                let Some(Ok(Message::Binary(data))) = ws.next().await else {
                    panic!("missing request")
                };
                ws.send(Message::Binary(data)).await.unwrap();
                while let Some(Ok(_)) = ws.next().await {}
            });
            let mut client = TcpStream::connect(service.address()).await.unwrap();
            let request = b"GET / HTTP/1.1\r\nHost: localhost:8099\r\n\r\n";
            client.write_all(request).await.unwrap();
            let mut response = vec![0; request.len()];
            client.read_exact(&mut response).await.unwrap();
            assert_eq!(response, request);
            service.stop().await.unwrap();
            server.await.unwrap();
        }
    });
}
