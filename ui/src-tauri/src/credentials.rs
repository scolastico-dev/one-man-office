//! Secrets never enter config.json or a command response.
use omo_ui_core::Result;
#[cfg(not(target_os = "android"))]
fn entry(id: &str) -> Result<keyring::Entry> {
    keyring::Entry::new("dev.scolastico.omo.ui", id).map_err(|_| "The system credential store is unavailable. Turn off Remember credentials to connect for this session.".into())
}
#[cfg(target_os = "android")]
fn entry(id: &str) -> Result<keyring_core::Entry> {
    static INIT: std::sync::OnceLock<bool> = std::sync::OnceLock::new();
    if !INIT.get_or_init(|| match android_native_keyring_store::Store::new() {
        Ok(store) => {
            keyring_core::set_default_store(store);
            true
        }
        Err(_) => false,
    }) {
        return Err("Android Keystore is unavailable. Turn off Remember credentials to connect for this session.".into());
    }
    keyring_core::Entry::new("dev.scolastico.omo.ui", id)
        .map_err(|_| "Cannot open Android credential store.".into())
}
pub fn read(id: &str) -> Result<String> {
    entry(id)?
        .get_password()
        .map_err(|_| "Saved credentials are unavailable. Enter them again.".into())
}
pub fn save(id: &str, value: &str) -> Result<()> {
    entry(id)?.set_password(value).map_err(|_| "Cannot save credentials in the system store. Turn off Remember credentials to connect for this session.".into())
}
pub fn forget(id: &str) -> Result<()> {
    let entry = entry(id)?;
    #[cfg(not(target_os = "android"))]
    use keyring::Error;
    #[cfg(target_os = "android")]
    use keyring_core::Error;
    match entry.delete_credential() {
        Ok(()) | Err(Error::NoEntry) => Ok(()),
        Err(_) => Err("Cannot remove saved credentials from the system store.".into()),
    }
}

/// Native relay secrets use a separate credential entry from direct-host auth.
/// The complete private protocol snapshot is replaced before each dependent
/// network send. It is never copied into setup configuration or command results.
pub struct RelaySecrets;
impl omo_ui_core::relay::SecretStore for RelaySecrets {
    fn load(&self, key: &str) -> Result<Option<Vec<u8>>> {
        #[cfg(not(target_os = "android"))]
        use keyring::Error;
        #[cfg(target_os = "android")]
        use keyring_core::Error;
        match entry(&format!("relay:{key}"))?.get_password() {
            Ok(value) => Ok(Some(value.into_bytes())),
            Err(Error::NoEntry) => Ok(None),
            Err(_) => Err("Saved relay secrets are unavailable.".into()),
        }
    }
    fn save(&self, key: &str, value: &[u8]) -> Result<()> {
        let value = std::str::from_utf8(value).map_err(|_| "Invalid private relay snapshot.")?;
        entry(&format!("relay:{key}"))?
            .set_password(value)
            .map_err(|_| "Cannot durably save relay secrets in the system store.".into())
    }
}
