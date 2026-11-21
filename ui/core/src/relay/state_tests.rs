use super::*;

#[derive(Default)]
struct Vault {
    value: Mutex<Option<Vec<u8>>>,
    fail: Mutex<bool>,
}
impl SecretStore for Vault {
    fn load(&self, _: &str) -> Result<Option<Vec<u8>>> {
        Ok(self.value.lock().unwrap().clone())
    }
    fn save(&self, _: &str, v: &[u8]) -> Result<()> {
        if *self.fail.lock().unwrap() {
            return Err("injected store error".into());
        }
        *self.value.lock().unwrap() = Some(v.to_vec());
        Ok(())
    }
}
fn client(v: &Arc<Vault>) -> RelayClient {
    RelayClient::open(
        v.clone(),
        "device",
        Route::new("https://relay.example", "office", false).unwrap(),
        None,
    )
    .unwrap()
}
fn contribution(commit: &Value) -> Value {
    json!({"v":1,"type":"pair.host","aid":commit["aid"],"window":wire::b64(&[2;16]),"pair_epoch":wire::b64(&[3;16]),"pkH":wire::b64(&Identity::generate().unwrap().public_key()),"rh":wire::b64(&[4;32])})
}
#[test]
fn consumed_commit_and_candidate_survive_reconstruction_without_reveal_reuse() {
    let v = Arc::new(Vault::default());
    let c = client(&v);
    let commit = c.inner.prepare_commit().unwrap();
    let loaded = client(&v);
    assert!(loaded.inner.prepare_commit().is_err());
    let (reveal, t) = c.inner.reveal(&contribution(&commit)).unwrap();
    assert_eq!(reveal["aid"], commit["aid"]);
    assert_eq!(t.c, wire::unb64(commit["C"].as_str().unwrap()).unwrap());
    c.inner.save_root(&[9; 32]).unwrap();
    let restored = client(&v);
    assert_eq!(restored.status().unwrap().state, TrustState::Candidate);
    assert!(restored.inner.reveal(&contribution(&commit)).is_err());
    assert!(restored.discard_unproved_attempt().is_err());
    let before = restored.inner.snapshot().unwrap();
    restored.inner.trust().unwrap();
    let trusted = client(&v).inner.snapshot().unwrap();
    let a = before.attempt.unwrap();
    let b = trusted.attempt.unwrap();
    assert_eq!(a.aid, b.aid);
    assert_eq!(a.host.as_ref().unwrap().pk, b.host.as_ref().unwrap().pk);
    assert_eq!(
        a.host.as_ref().unwrap().epoch,
        b.host.as_ref().unwrap().epoch
    );
    assert_eq!(a.host.as_ref().unwrap().root, b.host.as_ref().unwrap().root);
    assert_eq!(client(&v).status().unwrap().state, TrustState::Trusted);
}
#[test]
fn changed_or_malformed_first_contribution_burns_attempt() {
    for invalid in [
        json!({}),
        json!({"v":1,"type":"pair.commit","aid":wire::b64(&[7;16]),"C":wire::b64(&[1;48])}),
    ] {
        let v = Arc::new(Vault::default());
        let c = client(&v);
        c.inner.prepare_commit().unwrap();
        assert!(c.inner.reveal(&invalid).is_err());
        let restored = client(&v);
        assert!(restored.inner.snapshot().unwrap().attempt.unwrap().used);
        assert!(restored.inner.prepare_commit().is_err());
        assert!(restored.inner.reveal(&invalid).is_err());
        assert_eq!(restored.status().unwrap().state, TrustState::Pairing);
    }
}
#[test]
fn any_save_failure_latches_connection_closed_and_preserves_durable_state() {
    for transition in ["commit", "reveal", "root", "trust"] {
        let v = Arc::new(Vault::default());
        let c = client(&v);
        let commit = if transition != "commit" {
            Some(c.inner.prepare_commit().unwrap())
        } else {
            None
        };
        if ["root", "trust"].contains(&transition) {
            c.inner
                .reveal(&contribution(commit.as_ref().unwrap()))
                .unwrap();
        }
        if transition == "trust" {
            c.inner.save_root(&[9; 32]).unwrap();
        }
        let before = v.value.lock().unwrap().clone();
        *v.fail.lock().unwrap() = true;
        let result = match transition {
            "commit" => c.inner.prepare_commit().map(|_| ()),
            "reveal" => c
                .inner
                .reveal(&contribution(commit.as_ref().unwrap()))
                .map(|_| ()),
            "root" => c.inner.save_root(&[9; 32]),
            _ => c.inner.trust(),
        };
        assert!(result.is_err(), "{transition}");
        assert!(c.inner.snapshot().is_err());
        assert_eq!(before, *v.value.lock().unwrap());
    }
}

#[test]
fn trusted_candidate_and_root_survive_maximum_escaped_admission_update() {
    let v = Arc::new(Vault::default());
    let c = client(&v);
    let commit = c.inner.prepare_commit().unwrap();
    c.inner.reveal(&contribution(&commit)).unwrap();
    c.inner.save_root(&[9; 32]).unwrap();
    c.inner.trust().unwrap();
    let before = c.inner.snapshot().unwrap();
    RelayClient::open(
        v.clone(),
        "device",
        before.route.clone(),
        Some("\u{1}".repeat(4096)),
    )
    .unwrap();
    let restored = RelayClient::restore(v.clone(), "device", before.route.clone()).unwrap();
    let after = restored.inner.snapshot().unwrap();
    assert_eq!(before.seed, after.seed);
    let a = before.attempt.as_ref().unwrap();
    let b = after.attempt.as_ref().unwrap();
    assert_eq!(a.aid, b.aid);
    let ah = a.host.as_ref().unwrap();
    let bh = b.host.as_ref().unwrap();
    assert_eq!(ah.pk, bh.pk);
    assert_eq!(ah.epoch, bh.epoch);
    assert_eq!(ah.root, bh.root);
    assert!(bh.trusted);
    let durable = v.value.lock().unwrap().clone();
    assert!(durable.as_ref().unwrap().len() <= MAX_PRIVATE_STATE_BYTES);
    assert!(
        RelayClient::open(
            v.clone(),
            "device",
            before.route,
            Some("\u{1}".repeat(4097))
        )
        .is_err()
    );
    assert_eq!(*v.value.lock().unwrap(), durable);
}

#[test]
fn notification_export_requires_accepted_trust_and_contains_no_pairing_key() {
    let v = Arc::new(Vault::default());
    let c = client(&v);
    assert!(c.notification_secrets().is_err());
    let commit = c.inner.prepare_commit().unwrap();
    c.inner.reveal(&contribution(&commit)).unwrap();
    c.inner.save_root(&[9; 32]).unwrap();
    assert!(c.notification_secrets().is_err());
    c.inner.trust().unwrap();
    let exported = serde_json::to_value(c.notification_secrets().unwrap()).unwrap();
    assert_eq!(exported["context"]["ui_id"], c.identity_id().unwrap());
    assert_eq!(exported["root"], wire::b64(&[9; 32]));
    assert!(exported.get("seed").is_none());
    assert!(exported.get("attempt").is_none());
}
