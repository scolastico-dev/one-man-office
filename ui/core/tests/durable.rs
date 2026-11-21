use omo_ui_core::*;
use std::{
    io::{Read, Write},
    net::TcpListener,
    thread,
};
fn config(origin: String) -> Config {
    serde_json::from_value(serde_json::json!({"id":"3381221a-1afb-43cb-9b9b-27d3e140296b","mode":"direct","origin":origin,"auth":"token","username":"","notifications":true,"remember":true})).unwrap()
}
#[test]
fn android_saved_feed_retains_delivery_across_restart_and_auth_failures() {
    let dir = tempfile::tempdir().unwrap();
    let socket = TcpListener::bind("127.0.0.1:0").unwrap();
    let c = config(format!("http://{}/", socket.local_addr().unwrap()));
    write_json(
        dir.path(),
        &format!("{}.json", c.id),
        &Delivery {
            cursor: Some("40".into()),
            accepted: vec![],
        },
    )
    .unwrap();
    let server = thread::spawn(move || {
        for (status, body) in [
            (
                200,
                r#"{"notifications":[{"id":"event:41","kind":"question","title":"Question","body":"Choose","destination":"/7?tab=comments&comment=18"},{"id":"event:42","kind":"review","title":"Review","body":"Ready","destination":"/8"}],"next_cursor":"42","has_more":false}"#,
            ),
            (401, "{}"),
            (
                200,
                r#"{"notifications":[{"id":"event:41","kind":"question","title":"Question","body":"Choose","destination":"/7?tab=comments&comment=18"},{"id":"event:42","kind":"review","title":"Review","body":"Ready","destination":"/8"}],"next_cursor":"42","has_more":false}"#,
            ),
        ] {
            let (mut stream, _) = socket.accept().unwrap();
            let mut bytes = vec![];
            while !bytes.ends_with(b"\r\n\r\n") {
                let mut b = [0];
                stream.read_exact(&mut b).unwrap();
                bytes.push(b[0]);
            }
            let req = String::from_utf8(bytes).unwrap().to_ascii_lowercase();
            assert!(req.starts_with("get /api/ui/notifications?cursor=40&limit=200 "));
            assert!(req.contains(&format!("authorization: bearer {}\r\n", "a".repeat(64))));
            assert!(!req.contains("\r\norigin:") && !req.contains("\r\nsec-fetch-site:"));
            write!(
                stream,
                "HTTP/1.1 {status} Test\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
                body.len()
            )
            .unwrap();
        }
    });
    let secret = "a".repeat(64);
    let mut shown = vec![];
    assert!(
        poll_saved(dir.path(), &c, &secret, |n| {
            if n.id == "event:42" {
                return Err("delivery refused".into());
            }
            shown.push(n.id.clone());
            Ok(())
        })
        .is_err()
    );
    let before: Delivery = read_json(dir.path(), &format!("{}.json", c.id))
        .unwrap()
        .unwrap();
    assert_eq!(before.cursor.as_deref(), Some("40"));
    assert_eq!(before.accepted, vec!["event:41"]);
    assert!(poll_saved(dir.path(), &c, &secret, |_| panic!("auth failed")).is_err());
    let after: Delivery = read_json(dir.path(), &format!("{}.json", c.id))
        .unwrap()
        .unwrap();
    assert_eq!(after, before);
    assert!(matches!(
        poll_saved(dir.path(), &c, &secret, |n| {
            shown.push(n.id.clone());
            Ok(())
        })
        .unwrap(),
        Poll::Idle
    ));
    assert_eq!(shown, vec!["event:41", "event:42"]);
    let final_state: Delivery = read_json(dir.path(), &format!("{}.json", c.id))
        .unwrap()
        .unwrap();
    assert_eq!(final_state.cursor.as_deref(), Some("42"));
    assert!(
        read_json::<Delivery>(dir.path(), "other.json")
            .unwrap()
            .is_none()
    );
    server.join().unwrap();
}
#[test]
fn android_notification_modes_default_and_require_durable_credentials() {
    let mut c = config("https://board.example/".into());
    assert_eq!(c.notification_mode, NotificationMode::Background);
    c.remember = false;
    assert!(c.validate(true).is_err());
    c.notifications = false;
    c.validate(true).unwrap();
    c.notifications = true;
    c.auth = Auth::None;
    c.notification_mode = NotificationMode::Foreground;
    c.validate(true).unwrap();
}
