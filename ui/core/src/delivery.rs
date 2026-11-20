use crate::{Config, Result};
use serde::{Deserialize, Serialize};
#[derive(Clone, Serialize, Deserialize, Debug, PartialEq)]
pub struct Notice {
    pub id: String,
    pub kind: String,
    pub title: String,
    pub body: String,
    pub destination: String,
}
#[derive(Serialize, Deserialize)]
pub struct Page {
    pub notifications: Vec<Notice>,
    pub next_cursor: String,
    pub has_more: bool,
}
#[derive(Clone, Serialize, Deserialize, Default, Debug, PartialEq)]
pub struct Delivery {
    pub cursor: Option<String>,
    pub accepted: Vec<String>,
}
pub fn cursor(s: &str) -> Result<u64> {
    let value = s
        .parse::<u64>()
        .map_err(|_| "Invalid notification cursor.")?;
    if value > i64::MAX as u64 || value.to_string() != s {
        return Err("Invalid notification cursor.".into());
    }
    Ok(value)
}
impl Delivery {
    pub fn accept(
        &mut self,
        config: &Config,
        page: Page,
        mut deliver: impl FnMut(&Notice) -> Result<()>,
        mut persist: impl FnMut(&Delivery) -> Result<()>,
    ) -> Result<bool> {
        let previous = cursor(
            self.cursor
                .as_deref()
                .ok_or("Notification baseline is missing.")?,
        )?;
        let next = cursor(&page.next_cursor)?;
        if next < previous || (page.has_more && next == previous) || page.notifications.len() > 200
        {
            return Err("Invalid notification page progress.".into());
        }
        // Recovery can rewind past earlier pages. Retain submissions beyond this
        // page's cursor until their own part of the replay has been scanned.
        let mut pending = Vec::new();
        for id in &self.accepted {
            let value = cursor(
                id.strip_prefix("event:")
                    .ok_or("Invalid accepted notification ID.")?,
            )?;
            if value > next {
                pending.push(id.clone());
            }
        }
        let mut last = previous;
        for item in &page.notifications {
            let id = cursor(
                item.id
                    .strip_prefix("event:")
                    .ok_or("Invalid notification ID.")?,
            )?;
            if id <= last || id > next {
                return Err("Notification IDs are out of range or order.".into());
            }
            config.destination(&item.destination)?;
            last = id;
        }
        for item in &page.notifications {
            if self.accepted.contains(&item.id) {
                continue;
            }
            deliver(item)?;
            let mut update = self.clone();
            update.accepted.push(item.id.clone());
            persist(&update)?;
            *self = update;
        }
        let update = Delivery {
            cursor: Some(page.next_cursor),
            accepted: pending,
        };
        persist(&update)?;
        *self = update;
        Ok(page.has_more)
    }
}
/// Bound display text before platform escaping; notification bodies use markup on Linux.
pub fn notification_text(text: &str, max: usize, markup: bool) -> String {
    let plain:String=text.chars().take(max).map(|c| {
  if c.is_control() || matches!(c,'\u{00ad}'|'\u{061c}'|'\u{200b}'|'\u{200e}'..='\u{200f}'|'\u{2028}'..='\u{202e}'|'\u{2060}'..='\u{2064}'|'\u{2066}'..='\u{206f}'|'\u{feff}'|'\u{fff9}'..='\u{fffb}'|'\u{e0000}'..='\u{e007f}') {' '} else {c}
 }).collect();
    if markup {
        plain
            .replace('&', "&amp;")
            .replace('<', "&lt;")
            .replace('>', "&gt;")
    } else {
        plain
    }
}
