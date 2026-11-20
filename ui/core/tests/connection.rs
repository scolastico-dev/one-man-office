use omo_ui_core::*;
fn config() -> Config {
    Config {
        id: "3381221a-1afb-43cb-9b9b-27d3e140296b".into(),
        mode: Mode::Direct,
        origin: "https://board.example".into(),
        auth: Auth::Token,
        username: String::new(),
        notifications: true,
        remember: false,
    }
}
fn notice(id: &str) -> Notice {
    Notice {
        id: id.into(),
        kind: "question".into(),
        title: "A question".into(),
        body: "Choose one".into(),
        destination: "/7?tab=comments&comment=18".into(),
    }
}
#[test]
fn validates_origins_and_excludes_android_local_mode() {
    let mut c = config();
    c.validate(false).unwrap();
    assert_eq!(c.origin, "https://board.example/");
    for address in [
        "file:///etc/passwd",
        "https://u:p@host",
        "https://host/path",
        "https://host/?token=secret",
        "https://host/#secret",
        "https://host\\@other",
    ] {
        let mut c = config();
        c.origin = address.into();
        assert!(c.validate(false).is_err(), "{address}");
    }
    let mut c = config();
    c.mode = Mode::Local;
    assert!(c.validate(true).is_err());
    c.validate(false).unwrap();
}
#[test]
fn authenticates_without_accepting_header_injection_or_ambiguous_basic_users() {
    let mut c = config();
    assert_eq!(
        c.authorization(&"a".repeat(64)).unwrap(),
        Some(format!("Bearer {}", "a".repeat(64)))
    );
    for secret in ["", "broken", "a\r\nInjected: true"] {
        assert!(c.authorization(secret).is_err());
    }
    c.auth = Auth::Basic;
    c.username = "user".into();
    assert_eq!(
        c.authorization("p:ass").unwrap(),
        Some("Basic dXNlcjpwOmFzcw==".into())
    );
    c.username = "bad:user".into();
    assert!(c.authorization("pass").is_err());
    c.auth = Auth::None;
    assert_eq!(c.authorization("").unwrap(), None);
}
#[test]
fn saves_public_configuration_atomically_without_credentials_and_validates_on_load() {
    let dir = tempfile::tempdir().unwrap();
    assert_eq!(load_config(dir.path(), false).unwrap(), None);
    let mut c = config();
    c.validate(false).unwrap();
    save_config(dir.path(), &c).unwrap();
    assert_eq!(load_config(dir.path(), false).unwrap(), Some(c.clone()));
    c.mode = Mode::Local;
    save_config(dir.path(), &c).unwrap();
    assert!(load_config(dir.path(), true).is_err());
    assert_eq!(std::fs::read_dir(dir.path()).unwrap().count(), 1);
}
#[test]
fn negotiation_is_additive_and_only_missing_endpoint_is_an_older_server() {
    assert!(features(false, true).contains(&"ui.notifications.system.v1".into()));
    assert!(!features(true, true).contains(&"ui.notifications.system.v1".into()));
    assert!(!features(false, false).contains(&"ui.notifications.system.v1".into()));
    assert!(
        polling_supported(
            200,
            br#"{"features":["future.flag","ui.notifications.poll.v1"],"future":1}"#
        )
        .unwrap()
    );
    for body in [
        br#"{}"#.as_slice(),
        br#"{"features":null}"#,
        br#"{"features":[]}"#,
    ] {
        assert!(!polling_supported(200, body).unwrap());
    }
    assert!(!polling_supported(404, b"").unwrap());
    assert!(polling_supported(401, b"").is_err());
    assert!(polling_supported(500, b"").is_err());
}
#[test]
fn confines_activation_to_board_paths_without_external_or_executable_destinations() {
    let c = config();
    assert_eq!(
        c.destination("/7?tab=comments&comment=18")
            .unwrap()
            .as_str(),
        "https://board.example/7?tab=comments&comment=18"
    );
    for path in [
        "//evil.test",
        "https://evil.test",
        "/\\evil.test",
        "/%2f%2fevil.test",
        "/api/stop",
        "/proxy/forward/",
        "javascript:alert(1)",
        "/7\n",
    ] {
        assert!(c.destination(path).is_err(), "{path}");
    }
}
#[test]
fn accepts_empty_scanned_pages_and_keeps_full_width_string_cursor() {
    let mut d = Delivery {
        cursor: Some("9007199254740993".into()),
        accepted: vec![],
    };
    assert!(
        d.accept(
            &config(),
            Page {
                notifications: vec![],
                next_cursor: "9007199254740994".into(),
                has_more: true
            },
            |_| panic!("empty page"),
            |_| Ok(())
        )
        .unwrap()
    );
    assert_eq!(d.cursor.as_deref(), Some("9007199254740994"));
}
#[test]
fn persists_each_accepted_id_before_advancing_page_and_deduplicates_after_failure() {
    let mut d = Delivery {
        cursor: Some("40".into()),
        accepted: vec![],
    };
    let mut saved = Vec::new();
    let mut delivered = Vec::new();
    let result = d.accept(
        &config(),
        Page {
            notifications: vec![notice("event:41"), notice("event:42")],
            next_cursor: "45".into(),
            has_more: false,
        },
        |n| {
            if n.id == "event:42" {
                return Err("OS unavailable".into());
            };
            delivered.push(n.id.clone());
            Ok(())
        },
        |s| {
            saved.push(s.clone());
            Ok(())
        },
    );
    assert!(result.is_err());
    assert_eq!(d.cursor.as_deref(), Some("40"));
    assert_eq!(saved.last().unwrap().accepted, vec!["event:41"]);
    let mut d = saved.pop().unwrap();
    d.accept(
        &config(),
        Page {
            notifications: vec![notice("event:41"), notice("event:42")],
            next_cursor: "45".into(),
            has_more: false,
        },
        |n| {
            delivered.push(n.id.clone());
            Ok(())
        },
        |_| Ok(()),
    )
    .unwrap();
    assert_eq!(delivered, vec!["event:41", "event:42"]);
    assert_eq!(d.cursor.as_deref(), Some("45"));
    assert!(d.accepted.is_empty());
}
#[test]
fn validates_whole_page_before_delivery_and_never_commits_malformed_or_backward_cursors() {
    for (next, items, more) in [
        ("39", vec![], false),
        ("040", vec![], false),
        ("9223372036854775808", vec![], false),
        ("40", vec![], true),
        ("41", vec![notice("event:42")], false),
        ("43", vec![notice("event:42"), notice("event:41")], false),
    ] {
        let mut d = Delivery {
            cursor: Some("40".into()),
            accepted: vec![],
        };
        assert!(
            d.accept(
                &config(),
                Page {
                    notifications: items,
                    next_cursor: next.into(),
                    has_more: more
                },
                |_| panic!("invalid page delivered"),
                |_| panic!("invalid page persisted")
            )
            .is_err()
        );
        assert_eq!(d.cursor.as_deref(), Some("40"));
    }
}
#[test]
fn failed_persistence_keeps_previous_state() {
    let mut d = Delivery {
        cursor: Some("40".into()),
        accepted: vec![],
    };
    let before = d.clone();
    assert!(
        d.accept(
            &config(),
            Page {
                notifications: vec![],
                next_cursor: "45".into(),
                has_more: false
            },
            |_| Ok(()),
            |_| Err("disk full".into())
        )
        .is_err()
    );
    assert_eq!(d, before);
}
#[test]
fn cursor_recovery_requires_explicit_choice_and_checks_both_boundaries() {
    let c = config();
    let raw =
        br#"{"error":{"code":"conflict","details":{"reset_cursor":"100","latest_cursor":"350"}}}"#;
    let recovery = Recovery::parse(raw).unwrap();
    let mut d = Delivery {
        cursor: Some("2".into()),
        accepted: vec!["event:3".into()],
    };
    recovery.apply(&mut d, false, |_| Ok(())).unwrap();
    assert_eq!(d.cursor.as_deref(), Some("350"));
    assert!(d.accepted.is_empty());
    recovery.apply(&mut d, true, |_| Ok(())).unwrap();
    assert_eq!(d.cursor.as_deref(), Some("100"));
    assert!(
        Recovery::parse(br#"{"error":{"details":{"reset_cursor":"400","latest_cursor":"350"}}}"#)
            .is_err()
    );
    assert!(c.destination("/7").is_ok());
}

// This test exercises actual native HTTP, not a browser's CORS or mocked headers.
#[test]
fn native_requests_authenticate_omit_origin_and_never_follow_redirects() {
    use std::{
        io::{Read, Write},
        net::TcpListener,
        thread,
    };
    for (auth, username, secret, expected) in [
        (
            Auth::Token,
            "",
            "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            "Bearer aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        ),
        (Auth::Basic, "user", "pass", "Basic dXNlcjpwYXNz"),
        (Auth::None, "", "", ""),
    ] {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let address = listener.local_addr().unwrap();
        let server = thread::spawn(move || {
            let (mut stream, _) = listener.accept().unwrap();
            let mut buffer = Vec::new();
            let mut byte = [0];
            while !buffer.ends_with(b"\r\n\r\n") {
                stream.read_exact(&mut byte).unwrap();
                buffer.push(byte[0]);
            }
            let request = String::from_utf8(buffer).unwrap().to_ascii_lowercase();
            let authorized = if expected.is_empty() {
                !request.contains("authorization:")
            } else {
                request.contains(&format!(
                    "authorization: {}\r\n",
                    expected.to_ascii_lowercase()
                ))
            };
            let valid = authorized
                && !request.contains("\r\norigin:")
                && !request.contains("\r\nsec-fetch-site:")
                && request.starts_with("get /api/server ");
            let response = if valid {
                "HTTP/1.1 302 Found\r\nLocation: http://127.0.0.1:1/leak\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
            } else {
                "HTTP/1.1 401 Unauthorized\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
            };
            stream.write_all(response.as_bytes()).unwrap();
            valid
        });
        let mut c = config();
        c.origin = format!("http://{address}/");
        c.auth = auth;
        c.username = username.into();
        let client = BoardClient::new(c, secret.into()).unwrap();
        assert_eq!(
            client.request("GET", "/api/server", None).unwrap().status,
            302
        );
        assert!(server.join().unwrap());
    }
}
#[test]
fn basic_challenges_are_confined_to_exact_configured_origin_and_realm() {
    let mut c = config();
    c.auth = Auth::Basic;
    c.username = "user".into();
    assert!(c.challenge_allowed("https://board.example/api/server", "board.example", "omo"));
    for (url, host, realm) in [
        ("http://board.example/", "board.example", "omo"),
        ("https://board.example:444/", "board.example", "omo"),
        ("https://other.example/", "other.example", "omo"),
        ("https://board.example/", "board.example", "other"),
    ] {
        assert!(!c.challenge_allowed(url, host, realm));
    }
}
#[test]
fn notification_text_is_bounded_plain_text_and_escapes_linux_body_markup() {
    assert_eq!(
        notification_text("<b>A & B</b>\n\u{202e}C", 80, false),
        "<b>A & B</b>  C"
    );
    assert_eq!(
        notification_text("<b>A & B</b>", 80, true),
        "&lt;b&gt;A &amp; B&lt;/b&gt;"
    );
    assert_eq!(notification_text("😀abc", 2, false), "😀a");
}
#[test]
fn replay_recovery_retains_ids_already_submitted_from_an_incomplete_page() {
    let recovery = Recovery {
        reset_cursor: "100".into(),
        latest_cursor: "110".into(),
    };
    let mut d = Delivery {
        cursor: Some("90".into()),
        accepted: vec!["event:101".into()],
    };
    recovery.apply(&mut d, true, |_| Ok(())).unwrap();
    let mut delivered = vec![];
    d.accept(
        &config(),
        Page {
            notifications: vec![notice("event:101"), notice("event:102")],
            next_cursor: "102".into(),
            has_more: false,
        },
        |n| {
            delivered.push(n.id.clone());
            Ok(())
        },
        |_| Ok(()),
    )
    .unwrap();
    assert_eq!(delivered, vec!["event:102"]);
}
#[test]
fn basic_challenges_match_ipv6_hosts_without_url_brackets() {
    let mut c = config();
    c.auth = Auth::Basic;
    c.username = "user".into();
    c.origin = "http://[::1]:8090/".into();
    assert!(c.challenge_allowed("http://[::1]:8090/", "::1", "omo"));
    assert!(c.challenge_allowed("http://[::1]:8090/", "0:0:0:0:0:0:0:1", "omo"));
}
