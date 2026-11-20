//! Mobile jobs use the same transport and delivery journal without a running UI.
use crate::*;
use std::path::Path;

pub fn poll_saved(
    root: &Path,
    config: &Config,
    secret: &str,
    deliver: impl FnMut(&Notice) -> Result<()>,
) -> Result<Poll> {
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
