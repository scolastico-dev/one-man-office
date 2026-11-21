use omo_ui_core::{host_window_title, trusted_setup_document};
#[test]
fn host_titles_cannot_impersonate_native_connection_or_hide_the_host_prefix() {
    assert_eq!(
        host_window_title("Connection"),
        "omo ui · Host content · Connection"
    );
    let title = host_window_title("\u{202e}omo ui · Connection\n");
    assert!(title.starts_with("omo ui · Host content · "));
    assert!(!title.contains(['\u{202e}', '\n']));
}
#[test]
fn only_bundled_top_level_documents_get_setup_authority() {
    for url in [
        "tauri://localhost/",
        "http://tauri.localhost/",
        "https://tauri.localhost/",
    ] {
        assert!(trusted_setup_document(&url.parse().unwrap()), "{url}");
    }
    for url in [
        "tauri://evil/",
        "https://host.example/",
        "http://tauri.localhost:8090/",
        "http://tauri.localhost/remote",
        "http://tauri.localhost.evil/",
        "about:blank",
        "file:///setup/index.html",
    ] {
        assert!(!trusted_setup_document(&url.parse().unwrap()), "{url}");
    }
}
