use omo_ui_core::*;
use std::{
    io::{Read, Write},
    net::TcpListener,
    thread,
};
// A finite, joined local server: no global home, remote service, clock or sleep.
fn fixture(
    responses: Vec<(&'static str, u16, &'static str)>,
) -> (BoardClient, thread::JoinHandle<()>) {
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let address = listener.local_addr().unwrap();
    let handle = thread::spawn(move || {
        for (path, status, body) in responses {
            let (mut stream, _) = listener.accept().unwrap();
            let mut data = vec![];
            let mut b = [0];
            while !data.ends_with(b"\r\n\r\n") {
                stream.read_exact(&mut b).unwrap();
                data.push(b[0]);
            }
            let headers = String::from_utf8(data).unwrap();
            assert!(headers.lines().next().unwrap().contains(path), "{headers}");
            let size = headers
                .lines()
                .find_map(|l| {
                    l.to_ascii_lowercase()
                        .strip_prefix("content-length: ")
                        .and_then(|s| s.parse::<usize>().ok())
                })
                .unwrap_or(0);
            let mut payload = vec![0; size];
            stream.read_exact(&mut payload).unwrap();
            if path == "/api/ui/capabilities" {
                let value: serde_json::Value = serde_json::from_slice(&payload).unwrap();
                assert!(
                    value["features"]
                        .as_array()
                        .unwrap()
                        .contains(&serde_json::json!("ui.notifications.system.v1"))
                );
            }
            write!(stream,"HTTP/1.1 {status} Result\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",body.len()).unwrap();
        }
    });
    let c = Config {
        id: Config::new_id(),
        mode: Mode::Direct,
        origin: format!("http://{address}/"),
        auth: Auth::None,
        username: String::new(),
        notifications: true,
        notification_mode: NotificationMode::Background,
        remember: false,
        stop_with_window: false,
    };
    (BoardClient::new(c, String::new()).unwrap(), handle)
}
#[test]
fn connects_to_older_server_but_disables_optional_feed() {
    let (client, server) = fixture(vec![
        ("/api/server", 200, "{}"),
        ("/api/ui/capabilities", 404, "{}"),
    ]);
    let result = client.connect(false);
    assert!(!result.unwrap());
    server.join().unwrap();
}
#[test]
fn baselines_future_events_then_scans_empty_pages_without_touching_seen_marker() {
    let (client, server) = fixture(vec![
        (
            "/api/ui/notifications?cursor=9223372036854775807&limit=200",
            409,
            r#"{"error":{"details":{"reset_cursor":"10","latest_cursor":"40"}}}"#,
        ),
        (
            "/api/ui/notifications?cursor=40&limit=200",
            200,
            r#"{"notifications":[],"next_cursor":"42","has_more":true}"#,
        ),
        (
            "/api/ui/notifications?cursor=42&limit=200",
            200,
            r#"{"notifications":[{"id":"event:43","kind":"review","title":"Review","body":"Ready","destination":"/7"}],"next_cursor":"43","has_more":false}"#,
        ),
    ]);
    let mut d = Delivery::default();
    let mut shown = vec![];
    let mut durable = Delivery::default();
    assert!(matches!(
        client
            .poll(
                &mut d,
                |_| panic!("baseline must not deliver history"),
                |s| {
                    durable = s.clone();
                    Ok(())
                }
            )
            .unwrap(),
        Poll::Idle
    ));
    assert_eq!(durable.cursor.as_deref(), Some("40"));
    assert!(matches!(
        client
            .poll(
                &mut d,
                |_| panic!("empty scanned page"),
                |s| {
                    durable = s.clone();
                    Ok(())
                }
            )
            .unwrap(),
        Poll::More
    ));
    assert!(matches!(
        client
            .poll(
                &mut d,
                |n| {
                    shown.push(n.id.clone());
                    Ok(())
                },
                |s| {
                    durable = s.clone();
                    Ok(())
                }
            )
            .unwrap(),
        Poll::Idle
    ));
    assert_eq!(shown, vec!["event:43"]);
    assert_eq!(durable.cursor.as_deref(), Some("43"));
    server.join().unwrap();
}
#[test]
fn missing_history_returns_recovery_without_silently_skipping_events() {
    let (client, server) = fixture(vec![(
        "cursor=2&limit=200",
        409,
        r#"{"error":{"details":{"reset_cursor":"10","latest_cursor":"40"}}}"#,
    )]);
    let mut d = Delivery {
        cursor: Some("2".into()),
        accepted: vec![],
    };
    let before = d.clone();
    let result = client.poll(
        &mut d,
        |_| panic!("recovery delivered"),
        |_| panic!("recovery persisted"),
    );
    assert!(matches!(result.unwrap(), Poll::Recovery(_)));
    assert_eq!(d, before);
    server.join().unwrap();
}

#[test]
fn discovers_forward_support_without_enabling_it_on_android() {
    for android in [false, true] {
        let (client, server) = fixture(vec![(
            "/api/ui/capabilities",
            200,
            r#"{"features":["ui.notifications.poll.v1","ui.forwards.tunnel.v1","future"]}"#,
        )]);
        let result = client.capabilities(android).unwrap();
        assert!(result.notification_polling);
        assert_eq!(
            result.forward_tunnel,
            !android && !cfg!(target_os = "android")
        );
        server.join().unwrap();
    }
}
