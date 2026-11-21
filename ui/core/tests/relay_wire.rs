use omo_ui_core::relay::wire::*;
use serde_json::{Value, json};

fn decode(layer: &str, bytes: &[u8]) -> Result<Value, String> {
    match layer {
        "control" => decode_control(bytes),
        "inner" => decode_inner(bytes),
        "plain" => decode_plain(bytes),
        "page" => decode_notification_page(bytes),
        "request" => decode_notification_request(bytes),
        "envelope" => decode_notification_envelope(bytes),
        _ => panic!("unknown fixture layer"),
    }
}

#[test]
fn shared_negative_vectors_are_rejected() {
    let cases: Value = serde_json::from_str(include_str!(
        "../../../internal/relayproto/testdata/negative-v1.json"
    ))
    .unwrap();
    for case in cases.as_array().unwrap() {
        assert!(
            decode(
                case["layer"].as_str().unwrap(),
                case["json"].as_str().unwrap().as_bytes()
            )
            .is_err(),
            "{}",
            case["reason"]
        );
    }
}

#[test]
fn strict_json_and_counter_spellings() {
    for source in [
        r#"{"v":1e0,"type":"stream.open","id":"1"}"#,
        r#"{"v":1,"type":"stream.open","id":"\u0031"}"#,
        r#"{"v":1,"type":"stream.open","id":"18446744073709551616"}"#,
        r#"{"v":1,"type":"stream.open","id":"+1"}"#,
        r#"{"v":1,"type":"stream.open","id":"01"}"#,
        r#"{"v":1,"type":"stream.open","id":"1","\u0069d":"3"}"#,
        r#"{"v":1,"type":"stream.open","id":"1"} {}"#,
        r#"{"v":1,"type":"stream.open","id":null}"#,
        r#"{"v":1,"type":"stream.open","id":"1","unknown":{"a":1,"a":2}}"#,
    ] {
        assert!(
            decode_plain(source.as_bytes()).is_err(),
            "accepted {source}"
        );
    }
    let mut invalid = br#"{"v":1,"type":"error","code":"protocol_error"}"#.to_vec();
    invalid[10] = 0xff;
    assert!(decode_control(&invalid).is_err());
    for id in ["1", "18446744073709551615"] {
        let value = json!({"v":1,"type":"stream.open","id":id});
        assert_eq!(decode_plain(&encode_plain(&value).unwrap()).unwrap(), value);
    }
}

#[test]
fn base64_is_canonical() {
    for data in [vec![], vec![0], vec![255, 254], vec![42; 1184]] {
        assert_eq!(unb64(&b64(&data)).unwrap(), data);
    }
    for invalid in ["AA=", "AA==", "AB", "A", "//8", "+w", "AA\n", " AA"] {
        assert!(unb64(invalid).is_err(), "accepted {invalid:?}");
    }
}

#[test]
fn route_matches_go_canonicalization_without_browser_url_rewriting() {
    for (origin, expected, allow) in [
        ("https://Relay.Example:443", "https://relay.example", false),
        ("https://127.000.0.1", "https://127.000.0.1", false),
        (
            "https://[2001:0DB8:0:0::1]:9443",
            "https://[2001:0db8:0:0::1]:9443",
            false,
        ),
        ("http://127.0.0.2:80", "http://127.0.0.2", true),
        (
            "http://[::ffff:127.0.0.1]",
            "http://[::ffff:127.0.0.1]",
            true,
        ),
        ("http://LOCALHOST:8080", "http://localhost:8080", true),
        (
            "https://xn--bcher-kva.example",
            "https://xn--bcher-kva.example",
            false,
        ),
    ] {
        let route = Route::new(origin, "office_1-X", allow).unwrap();
        assert_eq!(route.origin, expected);
        route.validate().unwrap();
    }
    for origin in [
        "https://relay.example/",
        "https://relay.example?",
        "https://relay.example#",
        "https://u@relay.example",
        "https://relay.example:",
        "https://relay.example:0443",
        "https://relay.example:0",
        "https://relay.example:65536",
        "https://relay.example.",
        "https://bücher.example",
        "https://a_b.example",
        "https://-a.example",
        "https://a..example",
        "https://[fe80::1%25eth0]",
        "https://127.0.0.1\\evil",
        "http://relay.example",
        "http://localhost",
    ] {
        assert!(
            Route::new(origin, "office", false).is_err(),
            "accepted {origin}"
        );
    }
    assert!(Route::new("http://relay.example", "office", true).is_err());
    assert!(Route::new("https://relay.example", "bad host", false).is_err());
    assert!(
        Route {
            origin: "https://RELAY.example:443".into(),
            host_id: "office".into()
        }
        .validate()
        .is_err()
    );
}

#[test]
fn union_schemas_require_exact_fields_and_validate_lengths() {
    let cases = [
        (
            "control",
            json!({"v":1,"type":"host.register","host_id":"office"}),
        ),
        (
            "control",
            json!({"v":1,"type":"host.registered","host_id":"office","registration":b64(&[0;16])}),
        ),
        (
            "control",
            json!({"v":1,"type":"forward","connection":b64(&[0;16]),"payload":b64(b"opaque")}),
        ),
        (
            "inner",
            json!({"v":1,"type":"pair.commit","aid":b64(&[0;16]),"C":b64(&[0;48])}),
        ),
        (
            "inner",
            json!({"v":1,"type":"record","session":b64(&[0;48]),"seq":"0","ciphertext":b64(&[0;16])}),
        ),
        (
            "plain",
            json!({"v":1,"type":"ready","pair_epoch":b64(&[0;16]),"state":"active","aid":b64(&[0;16])}),
        ),
        (
            "plain",
            json!({"v":1,"type":"stream.credit","id":"1","bytes":"65536"}),
        ),
        (
            "plain",
            json!({"v":1,"type":"stream.data","id":"1","data":b64(&[0;16384])}),
        ),
    ];
    for (layer, value) in cases {
        let source = serde_json::to_vec(&value).unwrap();
        assert_eq!(decode(layer, &source).unwrap(), value);
        for key in value.as_object().unwrap().keys() {
            let mut missing = value.clone();
            missing.as_object_mut().unwrap().remove(key);
            assert!(
                decode(layer, &serde_json::to_vec(&missing).unwrap()).is_err(),
                "{layer} missing {key}"
            );
            let mut null = value.clone();
            null[key] = Value::Null;
            assert!(
                decode(layer, &serde_json::to_vec(&null).unwrap()).is_err(),
                "{layer} null {key}"
            );
        }
        let mut extra = value.clone();
        extra["unexpected"] = json!(false);
        assert!(decode(layer, &serde_json::to_vec(&extra).unwrap()).is_err());
    }
    assert!(encode_plain(&json!({"v":1,"type":"stream.credit","id":"1","bytes":"65537"})).is_err());
    assert!(encode_control(&json!({"v":1,"type":"error","code":"free text"})).is_err());
    assert!(decode_control(&vec![b' '; MAX_CONTROL_SIZE + 1]).is_err());
    assert!(decode_inner(&vec![b' '; MAX_INNER_SIZE + 1]).is_err());
}

#[test]
fn notification_shapes_and_receipts_are_strict() {
    let mut page = json!({"batch":null,"through":"0","token":null,"notifications":[],"more":false,"acknowledged":null});
    assert_eq!(
        decode_notification_page(&encode_notification_page(&page).unwrap()).unwrap(),
        page
    );
    page["batch"] = json!(b64(&[1; 16]));
    page["token"] = json!(b64(&[2; 32]));
    page["through"] = json!("5");
    page["notifications"] = json!([{"id":"5","unavailable":true}]);
    assert_eq!(
        decode_notification_page(&encode_notification_page(&page).unwrap()).unwrap(),
        page
    );
    let mut duplicate = page.clone();
    duplicate["notifications"]
        .as_array_mut()
        .unwrap()
        .push(json!({"id":"5","unavailable":true}));
    assert!(encode_notification_page(&duplicate).is_err());
    page["notifications"][0]["ticket"] = json!("1");
    assert!(encode_notification_page(&page).is_err());
    assert!(encode_notification_page(&json!({"error":"invalid_ack"})).is_ok());
    let mut request = json!({"v":1,"pair_epoch":b64(&[0;16]),"request":b64(&[0;32]),"ack":null});
    assert!(encode_notification_request(&request).is_ok());
    request["ack"] = json!({"batch":b64(&[1;16]),"through":"5","token":b64(&[2;32])});
    assert_eq!(
        decode_notification_request(&encode_notification_request(&request).unwrap()).unwrap(),
        request
    );
    request["ack"]["through"] = json!("0");
    assert!(encode_notification_request(&request).is_err());
    let envelope = json!({"v":1,"host_fp":"a".repeat(96),"ui_id":"b".repeat(96),"pair_epoch":b64(&[0;16]),"request":b64(&[0;32]),"reply":b64(&[0;32]),"ciphertext":b64(&[0;16])});
    assert_eq!(
        decode_notification_envelope(&encode_notification_envelope(&envelope).unwrap()).unwrap(),
        envelope
    );
}

fn fixture() -> Value {
    serde_json::from_str(include_str!(
        "../../../internal/relayproto/testdata/protocol-v1.json"
    ))
    .unwrap()
}
fn hex(value: &str) -> Vec<u8> {
    value
        .as_bytes()
        .as_chunks::<2>()
        .0
        .iter()
        .map(|p| u8::from_str_radix(std::str::from_utf8(p).unwrap(), 16).unwrap())
        .collect()
}

#[test]
fn shared_positive_wire_vectors_round_trip() {
    let v = fixture();
    let record = decode_inner(v["record_wire"].as_str().unwrap().as_bytes()).unwrap();
    assert_eq!(
        encode_inner(&record).unwrap(),
        v["record_wire"].as_str().unwrap().as_bytes()
    );
    assert!(decode_plain(&hex(v["record_plaintext"].as_str().unwrap())).is_ok());
    assert!(decode_notification_page(&hex(v["notification_plaintext"].as_str().unwrap())).is_ok());
    assert!(
        decode_notification_request(&unb64(v["request_header"].as_str().unwrap()).unwrap()).is_ok()
    );
}

#[test]
fn every_union_variant_has_exact_schema_and_canonical_keys() {
    let f = fixture();
    let pk = b64(&hex(f["host_pk"].as_str().unwrap()));
    let fp = f["host_fp"].clone();
    let id = b64(&[0; 16]);
    let nonce = b64(&[0; 32]);
    let proof = b64(&[0; 48]);
    let ct = b64(&[0; 1088]);
    let mut cases = vec![];
    for kind in ["ui.attached", "ui.open", "ui.close", "ui.closed"] {
        cases.push(("control", json!({"v":1,"type":kind,"connection":id})));
    }
    for kind in ["host.register", "ui.attach"] {
        cases.push(("control", json!({"v":1,"type":kind,"host_id":"office"})));
    }
    for kind in ["notification.empty", "notification.cancel"] {
        cases.push(("control", json!({"v":1,"type":kind,"qid":id})));
    }
    cases.push((
        "control",
        json!({"v":1,"type":"notification.reply","qid":id,"body":b64(b"opaque")}),
    ));
    cases.push(("control",json!({"v":1,"type":"notification.get","qid":id,"host_id":"office","ui_id":fp,"request_header":f["request_header"],"request_mac":f["request_mac"]})));
    cases.push((
        "inner",
        json!({"v":1,"type":"pair.host","aid":id,"window":id,"pair_epoch":id,"pkH":pk,"rh":nonce}),
    ));
    cases.push((
        "inner",
        json!({"v":1,"type":"pair.reveal","aid":id,"pkU":pk,"ru":nonce}),
    ));
    for prefix in ["auth", "pair.auth"] {
        cases.push(("inner",json!({"v":1,"type":format!("{prefix}.init"),"host_fp":fp,"ui_id":fp,"pair_epoch":id,"nu":nonce,"ctH":ct})));
        cases.push((
            "inner",
            json!({"v":1,"type":format!("{prefix}.challenge"),"nh":nonce,"ctU":ct,"proofH":proof}),
        ));
        cases.push((
            "inner",
            json!({"v":1,"type":format!("{prefix}.finish"),"proofU":proof}),
        ));
    }
    cases.push((
        "plain",
        json!({"v":1,"type":"pair.accepted","aid":id,"host_fp":fp,"ui_id":fp,"pair_epoch":id}),
    ));
    for kind in ["pair.confirm", "pair.confirmed"] {
        cases.push(("plain", json!({"v":1,"type":kind,"aid":id,"pair_epoch":id})));
    }
    cases.push(("plain", json!({"v":1,"type":"ready.ack","pair_epoch":id})));
    for kind in ["stream.open", "stream.opened", "stream.fin"] {
        cases.push(("plain", json!({"v":1,"type":kind,"id":"1"})));
    }
    cases.push((
        "plain",
        json!({"v":1,"type":"stream.reset","id":"1","code":"cancelled"}),
    ));
    for (layer, value) in cases {
        assert_eq!(
            decode(layer, &serde_json::to_vec(&value).unwrap()).unwrap(),
            value
        );
        for key in value.as_object().unwrap().keys() {
            let mut invalid = value.clone();
            invalid.as_object_mut().unwrap().remove(key);
            assert!(
                decode(layer, &serde_json::to_vec(&invalid).unwrap()).is_err(),
                "{} accepts missing {key}",
                value["type"]
            );
        }
    }
    let bad_pk = b64(&[255; 1184]);
    assert!(
        encode_inner(&json!({"v":1,"type":"pair.reveal","aid":id,"pkU":bad_pk,"ru":nonce}))
            .is_err()
    );
}

#[test]
fn notification_projection_validates_all_fields_and_nested_raw_limits() {
    let notice = json!({"id":"7","ticket":"3","author_role":"developer","created_at":"2026-11-21T12:30:00.125Z","title":"hi","body":"body","body_truncated":false,"title_truncated":false,"unavailable":false});
    let page = json!({"batch":b64(&[0;16]),"through":"7","token":b64(&[0;32]),"notifications":[notice],"more":false,"acknowledged":null});
    assert!(encode_notification_page(&page).is_ok());
    // Go's RFC3339 parser accepts a single hour digit and decimal comma.
    for timestamp in ["2026-11-21T1:30:00Z", "2026-11-21T12:30:00,125Z"] {
        let mut compatible = page.clone();
        compatible["notifications"][0]["created_at"] = json!(timestamp);
        assert!(encode_notification_page(&compatible).is_ok(), "{timestamp}");
    }
    for (key, bad) in [
        ("created_at", json!("2026-02-29T12:30:00Z")),
        ("created_at", json!("2026-11-21T12:30:00+00:00")),
        ("body", json!("ä".repeat(193))),
        ("title", json!("a".repeat(129))),
        ("author_role", json!("admin")),
        ("ticket", json!("0")),
        ("body_truncated", json!(0)),
    ] {
        let mut invalid = page.clone();
        invalid["notifications"][0][key] = bad;
        assert!(
            encode_notification_page(&invalid).is_err(),
            "accepted invalid {key}"
        );
    }
    let normal = serde_json::to_string(&page).unwrap();
    let oversized = normal.replacen(
        "\"id\":\"7\"",
        &format!("\"id\":{}\"7\"", " ".repeat(4096)),
        1,
    );
    assert!(decode_notification_page(oversized.as_bytes()).is_err());
    let duplicate = normal.replacen("\"id\":\"7\"", "\"id\":\"7\",\"\\u0069d\":\"7\"", 1);
    assert!(decode_notification_page(duplicate.as_bytes()).is_err());
    let request = format!(
        r#"{{"v":1,"pair_epoch":"{}","request":"{}","ack":{{"batch":"{}","through":{}"7","token":"{}"}}}}"#,
        b64(&[0; 16]),
        b64(&[0; 32]),
        b64(&[0; 16]),
        " ".repeat(1024),
        b64(&[0; 32])
    );
    assert!(decode_notification_request(request.as_bytes()).is_err());
}
