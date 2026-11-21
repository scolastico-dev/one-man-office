//! Native presentation policy, shared by setup navigation and command authorization.
pub fn trusted_setup_document(url: &url::Url) -> bool {
    let local = match url.scheme() {
        "tauri" => url.host_str() == Some("localhost"),
        "http" | "https" => url.host_str() == Some("tauri.localhost") && url.port().is_none(),
        _ => false,
    };
    local && url.path() == "/" && url.query().is_none() && url.fragment().is_none()
}
pub fn host_window_title(title: &str) -> String {
    format!(
        "omo ui · Host content · {}",
        crate::notification_text(title, 160, false)
    )
}
