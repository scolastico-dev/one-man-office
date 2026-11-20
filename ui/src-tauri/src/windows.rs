use omo_ui_core::{Config, Result};
use std::sync::atomic::{AtomicU64, Ordering};
use tauri::{
    AppHandle, Manager, WebviewUrl, WebviewWindow, WebviewWindowBuilder,
    webview::{NewWindowFeatures, NewWindowResponse},
};
static NEXT: AtomicU64 = AtomicU64::new(1);

pub fn show_setup(app: &AppHandle) {
    // Closing Connection destroys its WebView; every entry point must recreate it.
    if let Some(window) = app
        .get_webview_window("setup")
        .or_else(|| create_setup(app).ok())
    {
        #[cfg(target_os = "android")]
        {
            let _ = window.navigate(tauri::Url::parse("http://tauri.localhost/").expect("app URL"));
        }
        let _ = window.show();
        let _ = window.set_focus();
    }
}
pub fn close_boards(app: &AppHandle) {
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
    .title("omo ui")
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
        let clean: String = title
            .chars()
            .filter(|c| !c.is_control())
            .take(160)
            .collect();
        let _ = window.set_title(&format!("{clean} · omo ui"));
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
    crate::platform::configure(&window, config, secret)?;
    if !child {
        let handle = app.clone();
        window.on_window_event(move |event| {
            if matches!(event, tauri::WindowEvent::CloseRequested { .. }) {
                handle.exit(0);
            }
        });
        window
            .navigate(target)
            .map_err(|_| "Cannot open the board.")?;
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
    _native: bool,
    _features: Option<NewWindowFeatures>,
) -> Result<WebviewWindow> {
    let window = app
        .get_webview_window("setup")
        .ok_or("Main WebView is unavailable.")?;
    crate::platform::configure(&window, config, secret)?;
    window
        .navigate(crate::platform::board_url(config, secret)?)
        .map_err(|_| "Cannot open the board.")?;
    Ok(window)
}

#[cfg(not(target_os = "android"))]
pub fn menu(app: &AppHandle) -> tauri::Result<()> {
    use tauri::menu::{Menu, MenuItem, PredefinedMenuItem, Submenu};
    let connection = MenuItem::with_id(app, "connection", "Connection…", true, None::<&str>)?;
    let quit = PredefinedMenuItem::quit(app, None)?;
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
    app.on_menu_event(|app, event| {
        if event.id().as_ref() == "connection" {
            show_setup(app);
        }
    });
    Ok(())
}

pub fn create_setup(app: &AppHandle) -> tauri::Result<WebviewWindow> {
    let app_nav = app.clone();
    WebviewWindowBuilder::from_config(app, &app.config().app.windows[0])?
        .on_navigation(move |url| {
            if url.as_str() == "omo-ui:configure" {
                show_setup(&app_nav);
                return false;
            }
            #[cfg(target_os = "android")]
            {
                return matches!(url.scheme(), "http" | "https" | "tauri");
            }
            #[cfg(not(target_os = "android"))]
            {
                url.scheme() == "tauri" || url.host_str() == Some("tauri.localhost")
            }
        })
        .on_page_load(|window, payload| {
            #[cfg(target_os = "android")]
            if matches!(payload.event(), tauri::webview::PageLoadEvent::Finished)
                && matches!(payload.url().scheme(), "http" | "https")
                && payload.url().host_str() != Some("tauri.localhost")
            {
                let window = window.clone();
                tauri::async_runtime::spawn_blocking(move || {
                    let app = window.app_handle();
                    let state = app.state::<crate::Shell>();
                    if let Ok(s) = state.session.lock() {
                        if let Some(c) = s.active.as_ref() {
                            let _ = window.eval(bridge(&c.client.config, &c.secret, false));
                            let _ = window.eval(include_str!("mobile-navigation.js"));
                        }
                    }
                });
            }
            #[cfg(not(target_os = "android"))]
            let _ = (window, payload);
        })
        .build()
}
