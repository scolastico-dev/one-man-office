fn main() {
    tauri_build::try_build(tauri_build::Attributes::new().app_manifest(
        tauri_build::AppManifest::new().commands(&[
            "get_setup",
            "connect",
            "disconnect",
            "recover",
            "local_connect",
        ]),
    ))
    .expect("Tauri build configuration failed");
}
