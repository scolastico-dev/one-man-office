use omo_ui_core::{Config, Result};
#[cfg(not(target_os = "android"))]
use std::sync::atomic::{AtomicU64, Ordering};
use tauri::{AppHandle, Manager, WebviewWindow, WebviewWindowBuilder, webview::NewWindowFeatures};
#[cfg(not(target_os = "android"))]
use tauri::{WebviewUrl, webview::NewWindowResponse};
#[cfg(not(target_os = "android"))]
static NEXT: AtomicU64 = AtomicU64::new(1);

pub fn show_setup(app: &AppHandle) {
    // Closing Connection destroys its WebView; every entry point must recreate it.
    if let Some(window) = app
        .get_webview_window("setup")
        .or_else(|| create_setup(app).ok())
    {
        #[cfg(target_os = "android")]
        {
            let _ = crate::android_worker(app).call::<serde_json::Value>("showConnection", ());
        }
        let _ = window.show();
        let _ = window.set_focus();
    }
}
pub fn close_boards(app: &AppHandle) {
    #[cfg(target_os = "android")]
    let _ = crate::android_worker(app).call::<serde_json::Value>("closeBoard", ());
    for (label, window) in app.webview_windows() {
        if label.starts_with("board-") {
            let _ = window.destroy();
        }
    }
}
pub fn bridge(config: &Config, secret: &str, native: bool) -> String {
    let token = if config.auth == omo_ui_core::Auth::Token {
        secret
    } else {
        ""
    };
    let data = serde_json::json!({
        "origin":config.origin.trim_end_matches('/'),
        "auth":config.auth,
        "secret":token,
        "native":native,
        "capture":cfg!(target_os="android"),
    });
    format!("({})({});", include_str!("bridge.js"), data)
}
#[cfg(not(target_os = "android"))]
pub fn open_board(
    app: &AppHandle,
    config: &Config,
    secret: &str,
    native: bool,
    features: Option<NewWindowFeatures>,
) -> Result<WebviewWindow> {
    let label = format!("board-{}", NEXT.fetch_add(1, Ordering::Relaxed));
    let target = crate::platform::board_url(config, secret)?;
    let app_nav = app.clone();
    let app_child = app.clone();
    let child_config = config.clone();
    let child_secret = secret.to_string();
    let child = features.is_some();
    let mut builder = WebviewWindowBuilder::new(
        app,
        label,
        WebviewUrl::External("about:blank".parse().expect("blank URL")),
    )
    .title("omo ui · Host content")
    .decorations(true)
    .inner_size(1280., 840.)
    .min_inner_size(360., 480.)
    .incognito(true)
    .devtools(false)
    .zoom_hotkeys_enabled(false)
    .disable_drag_drop_handler()
    .enable_clipboard_access()
    .initialization_script(bridge(config, secret, native))
    .on_navigation(move |url| {
        if url.as_str() == "omo-ui:configure" {
            show_setup(&app_nav);
            return false;
        }
        matches!(url.scheme(), "http" | "https") || url.as_str() == "about:blank"
    })
    .on_document_title_changed(|window, title| {
        let _ = window.set_title(&omo_ui_core::host_window_title(&title));
    })
    .on_new_window(move |url, features| {
        if !(matches!(url.scheme(), "http" | "https") || url.as_str() == "about:blank") {
            return NewWindowResponse::Deny;
        }
        match open_child(&app_child, &child_config, &child_secret, native, features) {
            Ok(window) => NewWindowResponse::Create { window },
            Err(_) => NewWindowResponse::Deny,
        }
    });
    if let Some(features) = features {
        builder = builder.window_features(features);
    }
    let window = builder
        .build()
        .map_err(|_| "Cannot create the board WebView.")?;
    if let Err(error) = crate::platform::configure(&window, config, secret) {
        let _ = window.destroy();
        return Err(error);
    }
    if !child {
        let handle = app.clone();
        let main_label = window.label().to_string();
        window.on_window_event(move |event| {
            if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                crate::desktop::request_close(
                    &handle,
                    Some(main_label.clone()),
                    omo_ui_core::lifecycle::CloseIntent::Main,
                );
            }
        });
        if window.navigate(target).is_err() {
            let _ = window.destroy();
            return Err("Cannot open the board.".into());
        }
    }
    Ok(window)
}
#[cfg(not(target_os = "android"))]
fn open_child(
    app: &AppHandle,
    config: &Config,
    secret: &str,
    native: bool,
    features: NewWindowFeatures,
) -> Result<WebviewWindow> {
    // Create a related blank view: window.open('') reservations retain a live WindowProxy.
    // Tauri then navigates it to the requested URL, including one-use proxy bootstrap grants.
    let window = open_board(app, config, secret, native, Some(features))?;
    Ok(window)
}
#[cfg(target_os = "android")]
pub fn open_board(
    app: &AppHandle,
    config: &Config,
    secret: &str,
    native: bool,
    _features: Option<NewWindowFeatures>,
) -> Result<WebviewWindow> {
    let window = app
        .get_webview_window("setup")
        .ok_or("Main WebView is unavailable.")?;
    crate::platform::configure(&window, config, secret)?;
    let target = crate::platform::board_url(config, secret)?;
    crate::android_worker(app).call::<serde_json::Value>(
        "openBoard",
        serde_json::json!({
            "url":target.as_str(),
            "origin":config.origin.trim_end_matches('/'),
            "script":format!("{};{}",bridge(config, secret, native),include_str!("mobile-navigation.js")),
            "auth":config.auth,"username":config.username,"secret":secret,
        }),
    )?;
    Ok(window)
}

#[cfg(not(target_os = "android"))]
pub fn menu(app: &AppHandle) -> tauri::Result<()> {
    #[cfg(target_os = "macos")]
    use tauri::menu::PredefinedMenuItem;
    use tauri::menu::{Menu, MenuItem, Submenu};
    let connection = MenuItem::with_id(app, "connection", "Connection…", true, None::<&str>)?;
    let quit = MenuItem::with_id(app, "exit", "Exit omo", true, None::<&str>)?;
    let app_menu = Submenu::with_items(app, "omo ui", true, &[&connection, &quit])?;
    // macOS needs the standard edit menu for clipboard/undo. No browser/developer accelerators.
    #[cfg(target_os = "macos")]
    let items = vec![
        PredefinedMenuItem::undo(app, None)?,
        PredefinedMenuItem::redo(app, None)?,
        PredefinedMenuItem::cut(app, None)?,
        PredefinedMenuItem::copy(app, None)?,
        PredefinedMenuItem::paste(app, None)?,
        PredefinedMenuItem::select_all(app, None)?,
    ];
    #[cfg(target_os = "macos")]
    let edit = Submenu::with_items(
        app,
        "Edit",
        true,
        &items
            .iter()
            .map(|i| i as &dyn tauri::menu::IsMenuItem<_>)
            .collect::<Vec<_>>(),
    )?;
    #[cfg(target_os = "macos")]
    app.set_menu(Menu::with_items(app, &[&app_menu, &edit])?)?;
    #[cfg(not(target_os = "macos"))]
    app.set_menu(Menu::with_items(app, &[&app_menu])?)?;
    app.on_menu_event(|app, event| match event.id().as_ref() {
        "connection" => show_setup(app),
        "exit" => {
            crate::desktop::request_close(app, None, omo_ui_core::lifecycle::CloseIntent::Quit)
        }
        _ => {}
    });
    Ok(())
}

pub fn create_setup(app: &AppHandle) -> tauri::Result<WebviewWindow> {
    let app_nav = app.clone();
    let window = WebviewWindowBuilder::from_config(app, &app.config().app.windows[0])?
        .on_navigation(move |url| {
            if url.as_str() == "omo-ui:configure" {
                show_setup(&app_nav);
                return false;
            }
            omo_ui_core::trusted_setup_document(url)
        })
        .build()?;
    #[cfg(not(target_os = "android"))]
    {
        let handle = app.clone();
        window.on_window_event(move |event| {
            if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                crate::desktop::request_close(
                    &handle,
                    Some("setup".into()),
                    omo_ui_core::lifecycle::CloseIntent::LastSetup,
                );
            }
        });
    }
    Ok(window)
}

/// A separate ephemeral WebView uses a scoped native SOCKS proxy: its URL is
/// genuinely HTTP at the virtual host. No board script or credentials are installed.
#[cfg(not(target_os = "android"))]
pub fn open_preview(
    app: &AppHandle,
    group: &str,
    address: &str,
    proxy: std::net::SocketAddr,
    profile: Option<std::path::PathBuf>,
    mut prepare_profile: Option<&mut dyn FnMut(&WebviewWindow) -> Result<()>>,
    features: Option<NewWindowFeatures>,
) -> Result<WebviewWindow> {
    let target: tauri::Url = address.parse().map_err(|_| "Invalid preview address.")?;
    let origin = target.origin().ascii_serialization();
    let label = format!("preview-{group}-{}", NEXT.fetch_add(1, Ordering::Relaxed));
    let (child_app, child_group, child_address) =
        (app.clone(), group.to_string(), address.to_string());
    let navigation_origin = origin.clone();
    let child_origin = origin.clone();
    let child_profile = profile.clone();
    let child = features.is_some();
    let mut builder = WebviewWindowBuilder::new(
        app,
        label,
        WebviewUrl::External("about:blank".parse().expect("blank")),
    )
    .title(omo_ui_core::host_window_title(&format!(
        "{origin} · omo preview"
    )))
    .inner_size(1280., 840.)
    .min_inner_size(360., 480.)
    .incognito(true)
    .devtools(false)
    .zoom_hotkeys_enabled(false)
    .disable_drag_drop_handler()
    .enable_clipboard_access()
    .proxy_url(format!("socks5://{proxy}").parse().expect("loopback proxy"))
    .initialization_script(include_str!("preview.js"))
    .on_navigation(move |url| {
        url.as_str() == "about:blank" || url.origin().ascii_serialization() == navigation_origin
    })
    .on_new_window(move |url, features| {
        if url.as_str() != "about:blank" && url.origin().ascii_serialization() != child_origin {
            return NewWindowResponse::Deny;
        }
        match open_preview(
            &child_app,
            &child_group,
            &child_address,
            proxy,
            child_profile.clone(),
            None,
            Some(features),
        ) {
            Ok(window) => NewWindowResponse::Create { window },
            Err(_) => NewWindowResponse::Deny,
        }
    })
    .on_document_title_changed(move |window, title| {
        let _ = window.set_title(&omo_ui_core::host_window_title(&format!(
            "{origin} · {title}"
        )));
    });
    // WebView2 environments sharing a data folder must share proxy arguments.
    // Keep each preview family separate from the board and other previews.
    #[cfg(windows)]
    {
        if let Some(profile) = profile {
            builder = builder.data_directory(profile);
        }
    }
    if let Some(features) = features {
        builder = builder.window_features(features);
    }
    let window = builder
        .build()
        .map_err(|_| "Cannot create the app preview WebView.")?;
    let config = omo_ui_core::Config {
        id: group.into(),
        mode: omo_ui_core::Mode::Direct,
        origin: target.origin().ascii_serialization(),
        host_id: String::new(),
        relay_allow_loopback_http: false,
        auth: omo_ui_core::Auth::None,
        username: String::new(),
        notifications: false,
        notification_mode: omo_ui_core::NotificationMode::Background,
        remember: false,
        stop_with_window: false,
    };
    let app_closed = app.clone();
    let closed_group = group.to_string();
    let closed_label = window.label().to_string();
    window.on_window_event(move |event| {
        if matches!(event, tauri::WindowEvent::Destroyed) {
            crate::forwards::retired_window(
                &app_closed,
                closed_group.clone(),
                closed_label.clone(),
            );
        }
    });
    let ready = (|| -> Result<()> {
        if let Some(prepare) = prepare_profile.as_mut() {
            prepare(&window)?;
        }
        crate::platform::configure(&window, &config, "")?;
        if !child {
            window
                .navigate(target)
                .map_err(|_| "Cannot open the app preview.")?;
        }
        Ok(())
    })();
    if let Err(error) = ready {
        let _ = window.destroy();
        return Err(error);
    }
    Ok(window)
}
#[cfg(not(target_os = "android"))]
pub fn close_preview(app: &AppHandle, group: &str) {
    for (label, window) in app.webview_windows() {
        if label.starts_with(&format!("preview-{group}-")) {
            let _ = window.destroy();
        }
    }
}
