//! Mobile jobs use the same transport and delivery journal without a running UI.
use crate::*;
use std::path::Path;

pub fn poll_saved(
    root: &Path,
    config: &Config,
    secret: &str,
    deliver: impl FnMut(&Notice) -> Result<()>,
) -> Result<Poll> {
    if config.mode == Mode::Relay {
        let secrets: relay::NotificationSecrets = serde_json::from_str(secret)
            .map_err(|_| "Saved relay notification secrets are invalid.")?;
        return poll_relay_saved(root, config, secrets, deliver);
    }
    let name = format!("{}.json", config.id);
    let mut delivery: Delivery = read_json(root, &name)?.unwrap_or_default();
    BoardClient::new(config.clone(), secret.into())?
        .poll(&mut delivery, deliver, |d| write_json(root, &name, d))
}

pub fn recover_saved(
    root: &Path,
    config: &Config,
    recovery: &Recovery,
    replay: bool,
) -> Result<()> {
    let name = format!("{}.json", config.id);
    let mut delivery = read_json(root, &name)?.unwrap_or_default();
    recovery.apply(&mut delivery, replay, |d| write_json(root, &name, d))
}

/// Native app adapters serialize polls for the current connection through their
/// operation/worker lock. The journal is private, separate from the direct cursor.
pub fn poll_relay_saved(
    root: &Path,
    config: &Config,
    secrets: relay::NotificationSecrets,
    deliver: impl FnMut(&Notice) -> Result<()>,
) -> Result<Poll> {
    let notifier = relay::RelayNotifier::new(config.clone(), secrets)?;
    std::fs::create_dir_all(root).map_err(|_| "Cannot create notification directory.")?;
    let mut options = std::fs::OpenOptions::new();
    options.read(true).write(true).create(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    // The journal's inode changes on atomic save, so lock a separate stable file.
    // Hold it across load, OS delivery, GET and every durable replacement. The OS
    // releases it on process death; never remove this file while a peer may wait.
    let lock = options
        .open(root.join(format!("{}-relay-notifications.lock", config.id)))
        .map_err(|_| "Cannot open relay notification lock.")?;
    lock.lock()
        .map_err(|_| "Cannot lock relay notification journal.")?;
    let name = format!("{}-relay-notifications.json", config.id);
    let mut delivery = load_relay_delivery(root, &name)?;
    notifier.poll(&mut delivery, deliver, |d| {
        save_relay_delivery(root, &name, d)
    })
}

fn save_relay_delivery(root: &Path, name: &str, value: &relay::RelayDelivery) -> Result<()> {
    // History is app-owned, cumulative data, not an untrusted config document.
    // Stream it through the same private atomic/fsynced writer without imposing
    // the public configuration limit on the lifetime of a notification chain.
    crate::config::atomic_write(root, name, |file| {
        use std::io::Write;
        let mut writer = std::io::BufWriter::new(file);
        serde_json::to_writer(&mut writer, value)
            .map_err(|_| "Cannot encode relay notification journal.")?;
        writer
            .flush()
            .map_err(|_| "Cannot write relay notification journal.".into())
    })
}
fn load_relay_delivery(root: &Path, name: &str) -> Result<relay::RelayDelivery> {
    match std::fs::File::open(root.join(name)) {
        Ok(file) => serde_json::from_reader(std::io::BufReader::new(file))
            .map_err(|_| "Cannot read relay notification journal.".into()),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(Default::default()),
        Err(_) => Err("Cannot read relay notification journal.".into()),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn sparse_received_history_can_grow_past_the_public_config_size_limit() {
        let dir = tempfile::tempdir().unwrap();
        let mut delivery = relay::RelayDelivery::default();
        // Preserve gaps: an ID not received must remain eligible after recovery.
        for id in (1..200_000).step_by(2) {
            delivery.received.insert(id.to_string());
        }
        save_relay_delivery(dir.path(), "history.json", &delivery).unwrap();
        assert!(
            std::fs::metadata(dir.path().join("history.json"))
                .unwrap()
                .len()
                > 1024 * 1024
        );
        let restored = load_relay_delivery(dir.path(), "history.json").unwrap();
        assert_eq!(restored.received.len(), 100_000);
        assert!(restored.received.contains("199999"));
        assert!(!restored.received.contains("199998"));
    }
}
