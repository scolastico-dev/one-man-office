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
