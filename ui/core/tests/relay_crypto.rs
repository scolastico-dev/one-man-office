use omo_ui_core::relay::{
    crypto::{Identity, PairTranscript, fingerprint},
    wire::Route,
};
use serde_json::Value;
fn fixture() -> Value {
    serde_json::from_str(include_str!(
        "../../../internal/relayproto/testdata/protocol-v1.json"
    ))
    .unwrap()
}
fn bytes(f: &Value, k: &str) -> Vec<u8> {
    hex(f[k].as_str().unwrap())
}
fn hex(s: &str) -> Vec<u8> {
    s.as_bytes()
        .as_chunks::<2>()
        .0
        .iter()
        .map(|x| u8::from_str_radix(std::str::from_utf8(x).unwrap(), 16).unwrap())
        .collect()
}
fn transcript(f: &Value) -> PairTranscript {
    PairTranscript {
        route: Route {
            origin: "https://relay.example".into(),
            host_id: "office".into(),
        },
        aid: bytes(f, "aid"),
        window: bytes(f, "window"),
        pair_epoch: bytes(f, "epoch"),
        c: bytes(f, "commitment"),
        pku: bytes(f, "ui_pk"),
        ru: bytes(f, "ru"),
        pkh: bytes(f, "host_pk"),
        rh: bytes(f, "rh"),
    }
}
#[test]
fn identities_and_transcript_match_go_fixture() {
    let f = fixture();
    for (role, seed, pk, fp) in [
        ("host", "host_seed", "host_pk", "host_fp"),
        ("ui", "ui_seed", "ui_pk", "ui_id"),
    ] {
        let i = Identity::import(&bytes(&f, seed)).unwrap();
        assert_eq!(i.seed(), bytes(&f, seed));
        assert_eq!(i.public_key(), bytes(&f, pk));
        assert_eq!(fingerprint(role, &i.public_key()).unwrap(), f[fp]);
    }
    let t = transcript(&f);
    assert_eq!(t.encode().unwrap(), bytes(&f, "pair_transcript"));
    assert_eq!(t.hash().unwrap(), bytes(&f, "pair_hash"));
    assert_eq!(t.pin().unwrap(), "007026");
    assert_eq!(
        t.auth_context().unwrap().encode().unwrap(),
        bytes(&f, "auth_context")
    );
}

#[test]
fn mlkem_matches_every_nist_acvp_fixture() {
    use ml_kem::{
        Decapsulate, KeyExport,
        ml_kem_768::{DecapsulationKey, EncapsulationKey},
    };
    use omo_ui_core::relay::crypto::validate_public_key;
    let f: Value = serde_json::from_str(include_str!(
        "../../../internal/relayproto/testdata/mlkem768-acvp.json"
    ))
    .unwrap();
    for v in f["keyGen"]["tests"].as_array().unwrap() {
        let mut seed = bytes(v, "d");
        seed.extend(bytes(v, "z"));
        assert_eq!(
            Identity::import(&seed).unwrap().public_key(),
            bytes(v, "ek")
        );
    }
    for v in f["encapsulation"]["tests"].as_array().unwrap() {
        let pk = EncapsulationKey::new(
            &ml_kem::Key::<EncapsulationKey>::try_from(bytes(v, "ek").as_slice()).unwrap(),
        )
        .unwrap();
        let (ct, ss) =
            pk.encapsulate_deterministic(&ml_kem::B32::try_from(bytes(v, "m").as_slice()).unwrap());
        assert_eq!(ct.as_slice(), bytes(v, "c"));
        assert_eq!(ss.as_slice(), bytes(v, "k"));
    }
    for v in f["decapsulation"]["tests"].as_array().unwrap() {
        #[allow(deprecated)] // NIST fixture supplies expanded keys, not identity seeds.
        let dk = DecapsulationKey::from_expanded(
            &ml_kem::ExpandedDecapsulationKey::<ml_kem::MlKem768>::try_from(
                bytes(v, "dk").as_slice(),
            )
            .unwrap(),
        )
        .unwrap();
        assert_eq!(dk.encapsulation_key().to_bytes().as_slice(), bytes(v, "ek"));
        assert_eq!(
            dk.decapsulate_slice(&bytes(v, "c")).unwrap().as_slice(),
            bytes(v, "k")
        );
    }
    for v in f["encapsulationKeyCheck"]["tests"].as_array().unwrap() {
        assert_eq!(
            validate_public_key(&bytes(v, "ek")).is_ok(),
            v["testPassed"].as_bool().unwrap()
        );
    }
}

#[test]
fn identity_transcript_and_context_reject_invalid_contributions() {
    use omo_ui_core::relay::crypto::{commitment, validate_public_key};
    let f = fixture();
    assert!(Identity::import(&[]).is_err());
    assert!(Identity::import(&vec![0; 2400]).is_err());
    assert!(fingerprint("other", &bytes(&f, "ui_pk")).is_err());
    let mut invalid = bytes(&f, "ui_pk");
    invalid[0] = 0xff;
    invalid[1] |= 0x0f;
    assert!(validate_public_key(&invalid).is_err());
    let t = transcript(&f);
    assert!(commitment(&t.route, &[], &t.pku, &t.ru).is_err());
    assert!(commitment(&t.route, &t.aid, &invalid, &t.ru).is_err());
    for field in ["aid", "window", "pair_epoch", "c", "pku", "ru", "pkh", "rh"] {
        let mut v = serde_json::to_value(t.clone()).unwrap();
        v[field] = serde_json::json!([]);
        let broken: PairTranscript = serde_json::from_value(v).unwrap();
        assert!(broken.encode().is_err(), "{field}");
    }
    let mut changed = t.clone();
    changed.route.host_id = "other".into();
    assert!(changed.encode().is_err());
    let mut c = t.auth_context().unwrap();
    c.mode = "unknown".into();
    assert!(c.encode().is_err());
    c.mode = "reconnect".into();
    assert!(c.encode().is_err());
    c.pair_hash.clear();
    assert!(c.encode().is_ok());
    c.route.origin = "https://RELAY.example".into();
    assert!(c.encode().is_err());
}

fn notification_context(f: &Value) -> omo_ui_core::relay::crypto::NotificationContext {
    omo_ui_core::relay::crypto::NotificationContext {
        route: transcript(f).route,
        host_fp: f["host_fp"].as_str().unwrap().into(),
        ui_id: f["ui_id"].as_str().unwrap().into(),
        pair_epoch: bytes(f, "epoch"),
    }
}
#[test]
fn notification_request_and_response_match_go_fixture_and_bind_outstanding_context() {
    use omo_ui_core::relay::{
        crypto::{new_poll, open_notification},
        wire::b64,
    };
    let f = fixture();
    let c = notification_context(&f);
    let root = bytes(&f, "notification_root");
    let request = bytes(&f, "request");
    let poll = new_poll(&c, &root, &request, None).unwrap();
    assert_eq!(poll.request_header, f["request_header"]);
    assert_eq!(poll.request_mac, f["request_mac"]);
    let env = serde_json::json!({"v":1,"host_fp":c.host_fp,"ui_id":c.ui_id,"pair_epoch":b64(&c.pair_epoch),"request":b64(&request),"reply":b64(&bytes(&f,"reply")),"ciphertext":b64(&bytes(&f,"notification_ciphertext"))});
    let expected: Value = serde_json::from_slice(&bytes(&f, "notification_plaintext")).unwrap();
    assert_eq!(
        open_notification(&c, &root, &request, &env).unwrap(),
        expected
    );
    let mut other = c.clone();
    other.route.host_id = "other".into();
    assert!(open_notification(&other, &root, &request, &env).is_err());
    let mut wrong = root.clone();
    wrong[0] ^= 1;
    assert!(open_notification(&c, &wrong, &request, &env).is_err());
    assert!(open_notification(&c, &root, &[0; 32], &env).is_err());
    for field in [
        "host_fp",
        "ui_id",
        "pair_epoch",
        "request",
        "reply",
        "ciphertext",
    ] {
        let mut tampered = env.clone();
        if field == "host_fp" || field == "ui_id" {
            tampered[field] = serde_json::json!("0".repeat(96));
        } else {
            let mut v = omo_ui_core::relay::wire::unb64(env[field].as_str().unwrap()).unwrap();
            v[0] ^= 1;
            tampered[field] = serde_json::json!(b64(&v));
        }
        assert!(
            open_notification(&c, &root, &request, &tampered).is_err(),
            "{field}"
        );
    }
    let ack = serde_json::json!({"batch":b64(&[1;16]),"through":"99","token":b64(&[2;32])});
    let signed = new_poll(&c, &root, &request, Some(&ack)).unwrap();
    assert_ne!(signed.request_mac, poll.request_mac);
    for field in ["batch", "through", "token"] {
        let mut a = ack.clone();
        a[field] = Value::Null;
        assert!(new_poll(&c, &root, &request, Some(&a)).is_err());
    }
    let mut bad = ack;
    bad["through"] = serde_json::json!("099");
    assert!(new_poll(&c, &root, &request, Some(&bad)).is_err());
}
