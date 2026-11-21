//! Durable transitions must precede the protocol-dependent network action.
use omo_ui_core::Result;
use omo_ui_core::relay::{RelayClient, SecretStore, wire::Route};
use std::sync::{Arc, Mutex};

#[derive(Default)]
struct Vault {
    value: Mutex<Option<Vec<u8>>>,
    fail: Mutex<bool>,
}
impl SecretStore for Vault {
    fn load(&self, _: &str) -> Result<Option<Vec<u8>>> {
        Ok(self.value.lock().unwrap().clone())
    }
    fn save(&self, _: &str, value: &[u8]) -> Result<()> {
        if *self.fail.lock().unwrap() {
            return Err("test failure".into());
        }
        *self.value.lock().unwrap() = Some(value.to_vec());
        Ok(())
    }
}
fn route() -> Route {
    Route::new("https://relay.example", "office", false).unwrap()
}
#[test]
fn reconstruction_keeps_identity_and_rejects_corrupt_or_rebound_secrets() {
    let vault = Arc::new(Vault::default());
    let client = RelayClient::open(
        vault.clone(),
        "device",
        route(),
        Some("admission-secret".into()),
    )
    .unwrap();
    let id = client.identity_id().unwrap();
    assert_eq!(
        RelayClient::open(vault.clone(), "device", route(), None)
            .unwrap()
            .identity_id()
            .unwrap(),
        id
    );
    let other = Route::new("https://other.example", "office", false).unwrap();
    assert!(RelayClient::open(vault.clone(), "device", other, None).is_err());
    let metadata = serde_json::to_string(&client.status().unwrap()).unwrap();
    assert!(!metadata.contains("admission-secret"));
    assert!(!metadata.contains("seed"));
    *vault.value.lock().unwrap() = Some(b"{broken".to_vec());
    assert!(RelayClient::open(vault, "device", route(), None).is_err());
}
#[test]
fn first_identity_save_failure_returns_no_usable_client() {
    let vault = Arc::new(Vault::default());
    *vault.fail.lock().unwrap() = true;
    assert!(RelayClient::open(vault.clone(), "device", route(), None).is_err());
    assert!(vault.value.lock().unwrap().is_none());
}

#[test]
fn truncated_private_state_cannot_erase_saved_attempt_or_admission() {
    let vault = Arc::new(Vault::default());
    RelayClient::open(vault.clone(), "device", route(), None).unwrap();
    let original = vault.value.lock().unwrap().clone().unwrap();
    for field in ["attempt", "admission"] {
        let mut value: serde_json::Value = serde_json::from_slice(&original).unwrap();
        value.as_object_mut().unwrap().remove(field);
        *vault.value.lock().unwrap() = Some(serde_json::to_vec(&value).unwrap());
        assert!(
            RelayClient::open(vault.clone(), "device", route(), None).is_err(),
            "missing {field} accepted"
        );
    }
}

#[test]
fn restoring_a_saved_profile_with_missing_secrets_never_generates_an_identity() {
    let vault = Arc::new(Vault::default());
    assert!(RelayClient::restore(vault.clone(), "device", route()).is_err());
    assert!(vault.value.lock().unwrap().is_none());
    let first = RelayClient::open(vault.clone(), "device", route(), None).unwrap();
    assert_eq!(
        RelayClient::restore(vault, "device", route())
            .unwrap()
            .identity_id()
            .unwrap(),
        first.identity_id().unwrap()
    );
}
