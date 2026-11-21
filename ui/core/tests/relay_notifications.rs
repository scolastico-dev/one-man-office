use omo_ui_core::{
    Notice,
    relay::{RelayDelivery, wire},
};
use serde_json::{Value, json};
fn receipt(n: u8, through: &str) -> Value {
    json!({"batch":wire::b64(&[n;16]),"through":through,"token":wire::b64(&[n;32])})
}
fn page(n: u8, id: &str, ack: Value) -> Value {
    json!({"batch":wire::b64(&[n;16]),"through":id,"token":wire::b64(&[n;32]),"notifications":[{"id":id,"ticket":"7","author_role":"developer","created_at":"2026-01-01T00:00:00Z","title":"Question","body":"@user choose","body_truncated":false,"title_truncated":false,"unavailable":false}],"more":false,"acknowledged":ack})
}
#[test]
fn receipt_and_pending_delivery_commit_together_before_os_delivery() {
    let mut d = RelayDelivery::default();
    let mut disk = RelayDelivery::default();
    d.accept(page(1, "18", Value::Null), |v| {
        disk = v.clone();
        Ok(())
    })
    .unwrap();
    assert_eq!(disk.receipt, Some(receipt(1, "18")));
    assert_eq!(disk.pending.len(), 1);
    let mut sent = Vec::<Notice>::new();
    d.deliver(
        |n| {
            sent.push(n.clone());
            Ok(())
        },
        |v| {
            disk = v.clone();
            Ok(())
        },
    )
    .unwrap();
    assert_eq!(sent[0].id, "comment:18");
    assert_eq!(sent[0].destination, "/7?tab=comments&comment=18");
    assert!(disk.pending.is_empty());
    assert!(disk.received.contains("18"));
}
#[test]
fn missing_or_wrong_ack_preserves_the_entire_receipt_chain() {
    let mut d = RelayDelivery::default();
    d.accept(page(1, "18", Value::Null), |_| Ok(())).unwrap();
    let before = serde_json::to_value(&d).unwrap();
    for ack in [Value::Null, json!(wire::b64(&[9; 16]))] {
        assert!(
            d.accept(page(2, "19", ack), |_| panic!("must not save"))
                .is_err()
        );
        assert_eq!(serde_json::to_value(&d).unwrap(), before);
    }
    d.accept(page(2, "19", json!(wire::b64(&[1; 16]))), |_| Ok(()))
        .unwrap();
    assert_eq!(d.receipt, Some(receipt(2, "19")));
    assert_eq!(d.pending.len(), 2);
}
#[test]
fn invalid_ack_keeps_ids_and_pending_and_deduplicates_replayed_batch() {
    let mut d = RelayDelivery::default();
    d.accept(page(1, "18", Value::Null), |_| Ok(())).unwrap();
    let restored = serde_json::to_vec(&d).unwrap();
    let mut d: RelayDelivery = serde_json::from_slice(&restored).unwrap();
    d.accept(json!({"error":"invalid_ack"}), |_| Ok(()))
        .unwrap();
    assert!(d.receipt.is_none());
    assert!(d.received.contains("18"));
    d.accept(page(2, "18", Value::Null), |_| Ok(())).unwrap();
    assert_eq!(d.pending.len(), 1);
    assert_eq!(d.receipt, Some(receipt(2, "18")));
}
#[test]
fn failed_persistence_does_not_install_receipt_or_lose_pending_delivery() {
    let mut d = RelayDelivery::default();
    assert!(
        d.accept(page(1, "18", Value::Null), |_| Err("disk".into()))
            .is_err()
    );
    assert!(d.receipt.is_none());
    assert!(d.received.is_empty());
    d.accept(page(1, "18", Value::Null), |_| Ok(())).unwrap();
    assert!(
        d.deliver(|_| Err("OS blocked".into()), |_| panic!())
            .is_err()
    );
    assert_eq!(d.pending.len(), 1);
    assert!(d.deliver(|_| Ok(()), |_| Err("disk".into())).is_err());
    assert_eq!(d.pending.len(), 1);
}
#[test]
fn authenticated_empty_and_unavailable_items_are_durably_accepted() {
    let mut d = RelayDelivery::default();
    let mut p = page(1, "18", Value::Null);
    p["notifications"] = json!([{"id":"18","unavailable":true}]);
    d.accept(p, |_| Ok(())).unwrap();
    assert!(d.pending.is_empty());
    assert!(d.received.contains("18"));
    d.accept(json!({"batch":null,"through":"18","token":null,"notifications":[],"more":false,"acknowledged":wire::b64(&[1;16])}), |_| Ok(())).unwrap();
    assert!(d.receipt.is_none());
    assert!(d.received.contains("18"));
}

#[test]
fn journal_keeps_comment_ids_as_decimal_strings_across_native_json_stores() {
    // Android JSONObject has signed longs/doubles, so unsigned IDs must never
    // become JSON numbers while an encrypted journal is parsed and rewritten.
    let mut d = RelayDelivery::default();
    d.accept(page(1, "18446744073709551615", Value::Null), |_| Ok(()))
        .unwrap();
    assert_eq!(
        serde_json::to_value(d).unwrap()["received"],
        json!([["18446744073709551615", "18446744073709551615"]])
    );
}

#[test]
fn received_ids_compact_contiguous_history_without_filling_unreceived_gaps() {
    let mut d = RelayDelivery::default();
    for id in 1..=100_000 {
        d.received.insert(id.to_string());
    }
    d.received.insert("100002".into());
    let encoded = serde_json::to_vec(&d).unwrap();
    assert!(encoded.len() < 512, "contiguous IDs were not compacted");
    let restored: RelayDelivery = serde_json::from_slice(&encoded).unwrap();
    assert_eq!(restored.received.len(), 100_001);
    assert!(restored.received.contains("100000"));
    assert!(!restored.received.contains("100001"));
    assert!(restored.received.contains("100002"));
}

#[test]
fn received_ranges_merge_both_neighbors_and_keep_u64_endpoints_exact() {
    let mut d = RelayDelivery::default();
    for id in [20, 22, 21, u64::MAX, u64::MAX - 1] {
        assert!(d.received.insert(id.to_string()));
    }
    assert!(!d.received.insert("21".into()));
    assert_eq!(
        serde_json::to_value(&d).unwrap()["received"],
        json!([
            ["20", "22"],
            ["18446744073709551614", "18446744073709551615"]
        ])
    );
    let saved = serde_json::to_value(&d).unwrap();
    for invalid in [
        json!([["0", "1"]]),
        json!([["01", "2"]]),
        json!([["3", "2"]]),
        json!([["1", "2"], ["2", "3"]]),
        json!([["1", "2"], ["3", "4"]]),
    ] {
        let mut value = saved.clone();
        value["received"] = invalid;
        assert!(serde_json::from_value::<RelayDelivery>(value).is_err());
    }
}
