#![cfg(not(target_os = "android"))]

use futures_util::{SinkExt, StreamExt};
use omo_ui_core::*;
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::TcpListener,
};
use tokio_tungstenite::{
    accept_hdr_async,
    tungstenite::{
        Message,
        handshake::server::{Request, Response},
    },
};

const ID: &str = "0123456789abcdef0123456789abcdef";
const PROTOCOL: &str = "ui.forwards.tunnel.v1";
fn config(address: std::net::SocketAddr, auth: Auth) -> Config {
    Config {
        id: Config::new_id(),
        mode: Mode::Direct,
        origin: format!("http://{address}"),
        auth,
        username: "user".into(),
        notifications: false,
        notification_mode: NotificationMode::Background,
        remember: false,
        stop_with_window: false,
    }
}
fn fixture() -> serde_json::Value {
    serde_json::from_str(include_str!(
        "../../../internal/api/testdata/forward_tunnel_v1.json"
    ))
    .unwrap()
}

// A wrong route, misplaced secret, text framing, or buffered response breaks this exchange.
#[tokio::test]
async fn authenticates_and_streams_the_shared_go_contract() {
    for (auth, secret, expected) in [
        (
            Auth::Token,
            "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            "Bearer aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        ),
        (Auth::Basic, "p:ass", "Basic dXNlcjpwOmFzcw=="),
        (Auth::None, "", ""),
    ] {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let (observed, continue_response) = tokio::sync::oneshot::channel();
        let server = tokio::spawn(async move {
            let (tcp, _) = listener.accept().await.unwrap();
            let mut ws = accept_hdr_async(tcp, move |request: &Request, mut response: Response| {
                assert_eq!(
                    request.uri().to_string(),
                    format!("/api/ui/forwards/{ID}/tunnel")
                );
                assert_eq!(
                    request
                        .headers()
                        .get("authorization")
                        .map(|h| h.to_str().unwrap())
                        .unwrap_or(""),
                    expected
                );
                assert_eq!(request.headers()["sec-websocket-protocol"], PROTOCOL);
                assert!(!request.headers().contains_key("origin"));
                response
                    .headers_mut()
                    .insert("sec-websocket-protocol", PROTOCOL.parse().unwrap());
                Ok(response)
            })
            .await
            .unwrap();
            let f = fixture();
            let mut data = vec![];
            while data.len() < f["request"].as_str().unwrap().len() {
                match ws.next().await.unwrap().unwrap() {
                    Message::Binary(b) => {
                        assert!(b.len() <= 65536);
                        data.extend_from_slice(&b)
                    }
                    other => panic!("unexpected {other:?}"),
                }
            }
            assert_eq!(data, f["request"].as_str().unwrap().as_bytes());
            let response = f["response"].as_str().unwrap().as_bytes();
            ws.send(Message::Binary(response[..9].to_vec().into()))
                .await
                .unwrap();
            // Client must receive this prefix before the server produces the remainder.
            continue_response.await.unwrap();
            ws.send(Message::Ping(b"ping".to_vec().into()))
                .await
                .unwrap();
            ws.send(Message::Binary(response[9..].to_vec().into()))
                .await
                .unwrap();
            loop {
                match ws.next().await {
                    Some(Ok(Message::Pong(_))) => continue,
                    Some(Ok(Message::Close(_))) | None | Some(Err(_)) => break,
                    other => panic!("unexpected {other:?}"),
                }
            }
        });
        let connector = ForwardConnector::new(&config(address, auth), secret).unwrap();
        let mut tunnel = connector.connect(ID).await.unwrap();
        let f = fixture();
        let request = f["request"].as_str().unwrap();
        tunnel.write_all(&request.as_bytes()[..7]).await.unwrap();
        tunnel.flush().await.unwrap();
        tunnel.write_all(&request.as_bytes()[7..]).await.unwrap();
        tunnel.flush().await.unwrap();
        let mut prefix = [0; 9];
        tunnel.read_exact(&mut prefix).await.unwrap();
        assert_eq!(&prefix, b"HTTP/1.1 ");
        observed.send(()).unwrap();
        let rest = &f["response"].as_str().unwrap().as_bytes()[9..];
        let mut data = vec![0; rest.len()];
        tunnel.read_exact(&mut data).await.unwrap();
        assert_eq!(data, rest);
        drop(tunnel);
        server.await.unwrap();
    }
}

#[tokio::test]
async fn bounds_messages_and_preserves_large_bidirectional_streams() {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap();
    let server = tokio::spawn(async move {
        let (tcp, _) = listener.accept().await.unwrap();
        let mut ws = accept_hdr_async(tcp, |_: &Request, mut response: Response| {
            response
                .headers_mut()
                .insert("sec-websocket-protocol", PROTOCOL.parse().unwrap());
            Ok(response)
        })
        .await
        .unwrap();
        let mut received = 0;
        while received < 512 * 1024 {
            let Message::Binary(data) = ws.next().await.unwrap().unwrap() else {
                panic!("expected binary")
            };
            assert!(data.len() <= 65536);
            received += data.len();
            ws.send(Message::Binary(data)).await.unwrap();
        }
    });
    let connector = ForwardConnector::new(&config(address, Auth::None), "").unwrap();
    let tunnel = connector.connect(ID).await.unwrap();
    let (mut read, mut write) = tokio::io::split(tunnel);
    let data = vec![0xAB; 512 * 1024];
    let ((), got) = tokio::join!(
        async {
            write.write_all(&data).await.unwrap();
            write.flush().await.unwrap()
        },
        async {
            let mut out = vec![0; data.len()];
            read.read_exact(&mut out).await.unwrap();
            out
        }
    );
    assert_eq!(got, data);
    server.await.unwrap();
}

#[tokio::test]
async fn rejects_invalid_ids_and_handshake_failures_without_exposing_secrets() {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap();
    let secret = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
    let connector = ForwardConnector::new(&config(address, Auth::Token), secret).unwrap();
    for id in [
        "",
        "../target",
        "0123456789ABCDEF0123456789ABCDEF",
        "0123456789abcdef0123456789abcdef?token=secret",
    ] {
        assert!(connector.connect(id).await.is_err())
    }
    let server = tokio::spawn(async move {
        for status in [401, 403, 404, 502, 302] {
            let (mut tcp, _) = listener.accept().await.unwrap();
            let mut headers = vec![];
            while !headers.ends_with(b"\r\n\r\n") {
                headers.push(tcp.read_u8().await.unwrap())
            }
            tcp.write_all(format!("HTTP/1.1 {status} Refused\r\nLocation: http://user:password@127.0.0.1/\r\nContent-Length: 0\r\nConnection: close\r\n\r\n").as_bytes()).await.unwrap();
        }
    });
    for status in [401, 403, 404, 502, 302] {
        let error = match connector.connect(ID).await {
            Ok(_) => panic!("accepted {status}"),
            Err(e) => e,
        };
        assert!(error.contains(&status.to_string()), "{error}");
        assert!(!error.contains(secret));
        assert!(!error.contains("password"));
    }
    server.await.unwrap();
}

#[tokio::test]
async fn rejects_text_oversized_messages_and_missing_subprotocol() {
    for case in ["text", "oversize", "protocol"] {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let server = tokio::spawn(async move {
            let (tcp, _) = listener.accept().await.unwrap();
            let mut ws = accept_hdr_async(tcp, move |_: &Request, mut response: Response| {
                if case != "protocol" {
                    response
                        .headers_mut()
                        .insert("sec-websocket-protocol", PROTOCOL.parse().unwrap());
                }
                Ok(response)
            })
            .await
            .unwrap();
            let message = if case == "text" {
                Message::Text("bad".into())
            } else {
                Message::Binary(vec![0; 65537].into())
            };
            let _ = ws.send(message).await;
        });
        let connector = ForwardConnector::new(&config(address, Auth::None), "").unwrap();
        if case == "protocol" {
            assert!(connector.connect(ID).await.is_err())
        } else {
            let mut tunnel = connector.connect(ID).await.unwrap();
            let mut data = [0; 1];
            assert!(tunnel.read_exact(&mut data).await.is_err())
        }
        server.await.unwrap();
    }
}

#[tokio::test]
async fn normal_close_is_eof_and_native_shutdown_releases_the_peer() {
    for native_shutdown in [false, true] {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let server = tokio::spawn(async move {
            let (tcp, _) = listener.accept().await.unwrap();
            let mut ws = accept_hdr_async(tcp, |_: &Request, mut response: Response| {
                response
                    .headers_mut()
                    .insert("sec-websocket-protocol", PROTOCOL.parse().unwrap());
                Ok(response)
            })
            .await
            .unwrap();
            if native_shutdown {
                assert!(matches!(ws.next().await, Some(Ok(Message::Close(_)))));
            } else {
                ws.send(Message::Binary(b"final response".to_vec().into()))
                    .await
                    .unwrap();
                ws.close(None).await.unwrap();
            }
        });
        let connector = ForwardConnector::new(&config(address, Auth::None), "").unwrap();
        let mut tunnel = connector.connect(ID).await.unwrap();
        if native_shutdown {
            tunnel.shutdown().await.unwrap();
            assert!(tunnel.write_all(b"closed").await.is_err());
        } else {
            let mut data = vec![];
            tunnel.read_to_end(&mut data).await.unwrap();
            assert_eq!(data, b"final response");
        }
        server.await.unwrap();
    }
}

#[tokio::test]
async fn error_close_status_is_not_successful_stream_completion() {
    use tokio_tungstenite::tungstenite::protocol::{CloseFrame, frame::coding::CloseCode};
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap();
    let server = tokio::spawn(async move {
        let (tcp, _) = listener.accept().await.unwrap();
        let mut ws = accept_hdr_async(tcp, |_: &Request, mut response: Response| {
            response
                .headers_mut()
                .insert("sec-websocket-protocol", PROTOCOL.parse().unwrap());
            Ok(response)
        })
        .await
        .unwrap();
        ws.close(Some(CloseFrame {
            code: CloseCode::Size,
            reason: "private server detail".into(),
        }))
        .await
        .unwrap();
    });
    let connector = ForwardConnector::new(&config(address, Auth::None), "").unwrap();
    let mut tunnel = connector.connect(ID).await.unwrap();
    let mut data = vec![];
    let error = tunnel
        .read_to_end(&mut data)
        .await
        .expect_err("1009 cannot be clean EOF");
    assert!(!error.to_string().contains("private server detail"));
    server.await.unwrap();
}
