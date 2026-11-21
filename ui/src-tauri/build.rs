fn main() {
    tauri_build::try_build(tauri_build::Attributes::new().app_manifest(
        tauri_build::AppManifest::new().commands(&[
            "get_setup",
            "connect",
            "disconnect",
            "recover",
            "local_connect",
            "relay_connect",
            "get_forwards",
            "preview_forward",
            "expose_forward",
            "stop_forward",
        ]),
    ))
    .expect("Tauri build configuration failed");
}
