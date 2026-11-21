#[cfg(target_os = "android")]
mod android_auth;
#[cfg(target_os = "android")]
mod android_notifications;
mod credentials;
pub use credentials::RelaySecrets;
#[cfg(not(target_os = "android"))]
mod desktop;
#[cfg(target_os = "macos")]
mod macos_auth;
mod platform;
mod windows;

use omo_ui_core::{
    Auth, BoardClient, Config, Mode, NotificationMode, Recovery, Result, load_config, save_config,
};
#[cfg(not(target_os = "android"))]
use omo_ui_core::{Delivery, Poll, read_json, write_json};
use serde::{Deserialize, Serialize};
#[cfg(not(target_os = "android"))]
use std::time::Duration;
use std::{path::PathBuf, sync::Mutex};
use tauri::{AppHandle, Manager, WebviewWindow};
#[cfg(not(target_os = "android"))]
use tauri_plugin_notification::NotificationExt;

struct Connection {
    client: BoardClient,
    secret: String,
    #[cfg(not(target_os = "android"))]
    delivery: Delivery,
    polling: bool,
    #[cfg(not(target_os = "android"))]
    board_label: String,
}
#[derive(Default)]
struct Session {
    config: Option<Config>,
    active: Option<Connection>,
    error: String,
    recovery: Option<Recovery>,
}
struct Shell {
    root: PathBuf,
    session: Mutex<Session>,
    #[cfg(not(target_os = "android"))]
    desktop: desktop::Desktop,
}
#[derive(Serialize)]
struct Setup {
    config: Option<Config>,
    android: bool,
    connected: bool,
    polling: bool,
    error: String,
    recovery: Option<Recovery>,
}
#[derive(Deserialize)]
struct Settings {
    mode: Mode,
    origin: String,
    auth: Auth,
    username: String,
    notifications: bool,
    #[serde(default)]
    notification_mode: NotificationMode,
    remember: bool,
    secret: String,
    #[serde(default)]
    stop_with_window: bool,
    #[serde(default)]
    reset_identity: bool,
}

fn require_setup(window: &WebviewWindow) -> Result<()> {
    // ACL also enforces the invoking document's local origin, including subframes.
    if window.label() != "setup" {
        return Err("Only the bundled connection screen can configure omo ui.".into());
    }
    Ok(())
}
fn snapshot(state: &Shell, _app: &AppHandle) -> Result<Setup> {
    let s = state
        .session
        .lock()
        .map_err(|_| "Connection state is unavailable.")?;
    #[cfg(target_os = "android")]
    let native: serde_json::Value = android_worker(_app).call("status", ())?;
    #[cfg(target_os = "android")]
    return Ok(Setup {
        config: s.config.clone(),
        android: true,
        connected: s.active.is_some() || native["connected"].as_bool().unwrap_or(false),
        polling: native["polling"].as_bool().unwrap_or(false),
        error: if s.error.is_empty() {
            native["error"].as_str().unwrap_or("").into()
        } else {
            s.error.clone()
        },
        recovery: serde_json::from_value(native["recovery"].clone()).ok(),
    });
    #[cfg(not(target_os = "android"))]
    Ok(Setup {
        config: s.config.clone(),
        android: cfg!(target_os = "android"),
        connected: s.active.is_some(),
        polling: s.active.as_ref().is_some_and(|c| c.polling),
        error: s.error.clone(),
        recovery: s.recovery.clone(),
    })
}
#[tauri::command]
async fn get_setup(window: WebviewWindow, app: AppHandle) -> Result<Setup> {
    require_setup(&window)?;
    tauri::async_runtime::spawn_blocking(move || snapshot(&app.state::<Shell>(), &app))
        .await
        .map_err(|_| "Cannot read connection state.")?
}
#[tauri::command]
async fn connect(window: WebviewWindow, app: AppHandle, settings: Settings) -> Result<Setup> {
    require_setup(&window)?;
    if settings.mode == Mode::Local {
        return Err(if cfg!(target_os = "android") {
            "Android requires an external omo host."
        } else {
            "Use the local connection action."
        }
        .into());
    }
    tauri::async_runtime::spawn_blocking(move || {
        #[cfg(not(target_os = "android"))]
        let state = app.state::<Shell>();
        #[cfg(not(target_os = "android"))]
        let _operation = state
            .desktop
            .operation
            .lock()
            .map_err(|_| "Local operation state is unavailable.")?;
        open_connection(&app, settings, None)
    })
    .await
    .map_err(|_| "The connection could not be opened.")?
}
fn open_connection(
    app: &AppHandle,
    settings: Settings,
    local_config: Option<Config>,
) -> Result<Setup> {
    let state = app.state::<Shell>();
    let mut s = state
        .session
        .lock()
        .map_err(|_| "Connection state is unavailable.")?;
    let mut config = local_config.unwrap_or_else(|| Config {
        id: Config::new_id(),
        mode: settings.mode,
        origin: settings.origin,
        auth: settings.auth,
        username: settings.username,
        notifications: settings.notifications,
        notification_mode: settings.notification_mode.clone(),
        remember: settings.remember,
        stop_with_window: settings.stop_with_window,
    });
    config.notifications = settings.notifications;
    config.notification_mode = settings.notification_mode;
    config.remember = settings.remember;
    config.stop_with_window = settings.stop_with_window;
    config.validate(cfg!(target_os = "android"))?;
    let prior = s.config.as_ref();
    let same = prior.is_some_and(|p| {
        p.origin == config.origin && p.auth == config.auth && p.username == config.username
    });
    if same && !settings.reset_identity {
        config.id = prior.expect("matching prior config").id.clone();
    }
    let secret = if settings.secret.is_empty() && config.auth != Auth::None && same {
        if let Some(active) = s.active.as_ref() {
            active.secret.clone()
        } else if prior.is_some_and(|p| p.remember) {
            credentials::read(&prior.expect("saved prior config").id)?
        } else {
            settings.secret
        }
    } else {
        settings.secret
    };
    #[cfg(target_os = "android")]
    if config.notifications {
        use tauri_plugin_notification::NotificationExt;
        if app
            .notification()
            .request_permission()
            .map_err(|_| "Cannot request Android notification permission.")?
            != tauri::plugin::PermissionState::Granted
        {
            return Err(
                "Enable notifications for omo ui in Android Settings to use native polling.".into(),
            );
        }
    }
    let client = BoardClient::new(config.clone(), secret.clone())?;
    let polling = client.connect(cfg!(target_os = "android"))?;
    #[cfg(not(target_os = "android"))]
    let mut delivery: Delivery =
        read_json(&state.root, &format!("{}.json", config.id))?.unwrap_or_default();
    #[cfg(not(target_os = "android"))]
    if polling && delivery.cursor.is_none() {
        client.poll(
            &mut delivery,
            |_| Err("Unexpected notification during baseline.".into()),
            |d| write_json(&state.root, &format!("{}.json", config.id), d),
        )?;
    }
    #[cfg(not(target_os = "android"))]
    if config.mode == Mode::Direct && state.desktop.release(app, Some(&config.origin))? {
        // An alternate loopback hostname may identify the server we just stopped.
        // Verify that the proposed direct board is still available before saving it.
        client.connect(false)?;
    }
    if config.remember && config.auth != Auth::None {
        credentials::save(&config.id, &secret)?;
    }
    // An explicit opt-out must remove an earlier saved secret, not just hide its checkbox.
    if let Some(old) = prior.filter(|p| p.remember && p.auth != Auth::None) {
        if !config.remember || old.id != config.id {
            credentials::forget(&old.id)?;
        }
    }
    #[cfg(target_os = "android")]
    android_worker(app).call::<serde_json::Value>(
        "configure",
        serde_json::json!({
            "config":config,"secret":secret,"root":state.root,"enabled":polling,
        }),
    )?;
    save_config(&state.root, &config)?;
    #[cfg(not(target_os = "android"))]
    state
        .desktop
        .stop_with_window
        .store(config.stop_with_window, std::sync::atomic::Ordering::SeqCst);
    windows::close_boards(&app);
    let board = windows::open_board(&app, &config, &secret, polling, None)?;
    #[cfg(not(target_os = "android"))]
    let board_label = board.label().to_string();
    #[cfg(target_os = "android")]
    let _ = board;
    s.config = Some(config);
    s.active = Some(Connection {
        client,
        secret,
        #[cfg(not(target_os = "android"))]
        delivery,
        polling,
        #[cfg(not(target_os = "android"))]
        board_label,
    });
    s.error.clear();
    s.recovery = None;
    drop(s);
    #[cfg(not(target_os = "android"))]
    if let Some(setup) = app.get_webview_window("setup") {
        let _ = setup.hide();
    }
    snapshot(&state, app)
}

#[tauri::command]
async fn disconnect(window: WebviewWindow, app: AppHandle) -> Result<Setup> {
    require_setup(&window)?;
    tauri::async_runtime::spawn_blocking(move || {
        let state = app.state::<Shell>();
        #[cfg(not(target_os = "android"))]
        let _operation = state
            .desktop
            .operation
            .lock()
            .map_err(|_| "Local operation state is unavailable.")?;
        #[cfg(not(target_os = "android"))]
        state.desktop.release(&app, None)?;
        let mut s = state
            .session
            .lock()
            .map_err(|_| "Connection state is unavailable.")?;
        #[cfg(target_os = "android")]
        android_worker(&app).call::<serde_json::Value>("disconnect", ())?;
        s.active = None;
        s.recovery = None;
        s.error.clear();
        windows::close_boards(&app);
        drop(s);
        snapshot(&state, &app)
    })
    .await
    .map_err(|_| "Cannot disconnect.")?
}
#[tauri::command]
async fn recover(window: WebviewWindow, app: AppHandle, replay: bool) -> Result<Setup> {
    require_setup(&window)?;
    tauri::async_runtime::spawn_blocking(move || {
        let state = app.state::<Shell>();
        let mut s = state
            .session
            .lock()
            .map_err(|_| "Connection state is unavailable.")?;
        #[cfg(target_os = "android")]
        {
            android_worker(&app)
                .call::<serde_json::Value>("recover", serde_json::json!({"replay":replay}))?;
            s.recovery = None;
            s.error.clear();
            drop(s);
            return snapshot(&state, &app);
        }
        #[cfg(not(target_os = "android"))]
        let recovery = s.recovery.clone().ok_or("No cursor recovery is pending.")?;
        #[cfg(not(target_os = "android"))]
        {
            let c = s.active.as_mut().ok_or("Connect to a board first.")?;
            recovery.apply(&mut c.delivery, replay, |d| {
                write_json(&state.root, &format!("{}.json", c.client.config.id), d)
            })?;
            s.recovery = None;
            s.error.clear();
            drop(s);
            snapshot(&state, &app)
        }
    })
    .await
    .map_err(|_| "Cannot recover the notification cursor.")?
}
#[derive(Deserialize)]
struct LocalSettings {
    notifications: bool,
    remember: bool,
    #[serde(default)]
    stop_with_window: bool,
}
#[tauri::command]
async fn local_connect(
    window: WebviewWindow,
    app: AppHandle,
    settings: LocalSettings,
) -> Result<Setup> {
    require_setup(&window)?;
    #[cfg(target_os = "android")]
    {
        let _ = (app, settings);
        Err("Android requires an external omo host.".into())
    }
    #[cfg(not(target_os = "android"))]
    {
        tauri::async_runtime::spawn_blocking(move || {
            let state = app.state::<Shell>();
            let _operation = state
                .desktop
                .operation
                .lock()
                .map_err(|_| "Local operation state is unavailable.")?;
            if state
                .desktop
                .exiting
                .load(std::sync::atomic::Ordering::SeqCst)
            {
                return Err("omo is exiting.".into());
            }
            state.desktop.stop_with_window.store(
                settings.stop_with_window,
                std::sync::atomic::Ordering::SeqCst,
            );
            // Keep the launch proof before authentication/configuration can fail.
            // A failed connection still leaves native close/Exit able to clean up.
            let local = omo_ui_core::local::LocalManager::from_env()?
                .connect_with_capture(|control| state.desktop.capture(&app, control))?;
            // An attached replacement retires any earlier launch proof only
            // after its connection preparation succeeds.
            state.desktop.capture(&app, local.control.clone())?;
            desktop::refresh(&app)?;
            let config = local.config.clone();
            open_connection(
                &app,
                Settings {
                    mode: Mode::Local,
                    origin: config.origin.clone(),
                    auth: config.auth.clone(),
                    username: config.username.clone(),
                    secret: local.secret.clone(),
                    notifications: settings.notifications,
                    notification_mode: NotificationMode::Background,
                    remember: settings.remember,
                    stop_with_window: settings.stop_with_window,
                    reset_identity: false,
                },
                Some(config),
            )
        })
        .await
        .map_err(|_| "The local connection could not be opened.")?
    }
}

#[cfg(not(target_os = "android"))]
fn poll(app: &AppHandle) -> Result<bool> {
    let state = app.state::<Shell>();
    let mut s = state
        .session
        .lock()
        .map_err(|_| "Connection state is unavailable.")?;
    if s.recovery.is_some() {
        return Ok(false);
    }
    let Some(c) = s.active.as_mut().filter(|c| c.polling) else {
        return Ok(false);
    };
    let config = c.client.config.clone();
    let result = c.client.poll(
        &mut c.delivery,
        |n| {
            let title = omo_ui_core::notification_text(&n.title, 80, false);
            let body = omo_ui_core::notification_text(&n.body, 160, cfg!(target_os = "linux"));
            app.notification()
                .builder()
                .title(title)
                .body(body)
                .extra("connection", &config.id)
                .extra("destination", &n.destination)
                .show()
                .map_err(|_| "Cannot submit the system notification.".into())
        },
        |d| write_json(&state.root, &format!("{}.json", config.id), d),
    );
    match result {
        Ok(Poll::More) => {
            s.error.clear();
            Ok(true)
        }
        Ok(Poll::Idle) => {
            s.error.clear();
            Ok(false)
        }
        Ok(Poll::Recovery(recovery)) => {
            s.recovery = Some(recovery);
            s.error="Notification history changed. Open Connection to choose whether to replay retained events or start from now.".into();
            Ok(false)
        }
        Err(e) => {
            s.error = e;
            Ok(false)
        }
    }
}
#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_notification::init())
        .plugin(tauri_plugin_omo_notifications::init())
        .invoke_handler(tauri::generate_handler![
            get_setup,
            connect,
            disconnect,
            recover,
            local_connect
        ])
        .setup(|app| {
            let root = app.path().app_config_dir()?;
            let (config, error) = match load_config(&root, cfg!(target_os = "android")) {
                Ok(c) => (c, String::new()),
                Err(e) => (None, e),
            };
            app.manage(Shell {
                root,
                #[cfg(not(target_os = "android"))]
                desktop: desktop::Desktop::default(),
                session: Mutex::new(Session {
                    config,
                    error,
                    ..Default::default()
                }),
            });
            windows::create_setup(app.handle())?;
            #[cfg(target_os = "android")]
            {
                let handle = app.handle().clone();
                android_worker(app.handle()).on_activation(move |id, path| {
                    let handle = handle.clone();
                    tauri::async_runtime::spawn_blocking(move || {
                        activate_android(&handle, &id, &path)
                    });
                })?;
            }
            #[cfg(not(target_os = "android"))]
            {
                windows::menu(app.handle())?;
                let handle = app.handle().clone();
                app.notification().on_action(move |event| {
                    let Some(n) = event.notification() else {
                        return;
                    };
                    let (Some(id), Some(path)) = (
                        n.extra().get("connection").and_then(|v| v.as_str()),
                        n.extra().get("destination").and_then(|v| v.as_str()),
                    ) else {
                        return;
                    };
                    let (handle, id, path) = (handle.clone(), id.to_string(), path.to_string());
                    tauri::async_runtime::spawn_blocking(move || {
                        let state = handle.state::<Shell>();
                        let Ok(s) = state.session.lock() else { return };
                        let Some(c) = s.active.as_ref().filter(|c| c.client.config.id == id) else {
                            return;
                        };
                        let Ok(url) = c.client.config.destination(&path) else {
                            return;
                        };
                        if let Some(window) = handle.get_webview_window(&c.board_label) {
                            let _ = window.navigate(url);
                            let _ = window.show();
                            let _ = window.set_focus();
                        } else if let Ok(window) = windows::open_board(
                            &handle,
                            &c.client.config,
                            &c.secret,
                            c.polling,
                            None,
                        ) {
                            let _ = window.navigate(url);
                        }
                    });
                })?;
                let handle = app.handle().clone();
                tauri::async_runtime::spawn(async move {
                    loop {
                        let h = handle.clone();
                        let more = tauri::async_runtime::spawn_blocking(move || {
                            if let Err(error) = desktop::refresh(&h) {
                                if let Ok(mut s) = h.state::<Shell>().session.lock() {
                                    s.error = error;
                                }
                            }
                            poll(&h)
                        })
                        .await
                        .ok()
                        .and_then(std::result::Result::ok)
                        .unwrap_or(false);
                        if !more {
                            tokio::time::sleep(Duration::from_secs(5)).await;
                        }
                    }
                });
            }
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("omo ui could not start")
        .run(|app, event| {
            #[cfg(not(target_os = "android"))]
            if let tauri::RunEvent::ExitRequested { api, .. } = event {
                if !app
                    .state::<Shell>()
                    .desktop
                    .exiting
                    .load(std::sync::atomic::Ordering::SeqCst)
                {
                    api.prevent_exit();
                    desktop::request_close(app, None, omo_ui_core::lifecycle::CloseIntent::Quit);
                }
            }
            #[cfg(target_os = "android")]
            let _ = (app, event);
        });
}

#[cfg(target_os = "android")]
fn android_worker(
    app: &AppHandle,
) -> tauri::State<'_, tauri_plugin_omo_notifications::AndroidNotifications<tauri::Wry>> {
    app.state::<tauri_plugin_omo_notifications::AndroidNotifications<tauri::Wry>>()
}

#[cfg(target_os = "android")]
fn activate_android(app: &AppHandle, id: &str, path: &str) {
    let state = app.state::<Shell>();
    let Ok(mut s) = state.session.lock() else {
        return;
    };
    let Some(config) = s.config.clone().filter(|c| c.id == id) else {
        return;
    };
    let Ok(url) = config.destination(path) else {
        return;
    };
    let secret = if let Some(active) = s.active.as_ref().filter(|c| c.client.config.id == id) {
        active.secret.clone()
    } else if config.auth == Auth::None {
        String::new()
    } else {
        let Ok(secret) = credentials::read(id) else {
            return;
        };
        secret
    };
    let Ok(client) = BoardClient::new(config.clone(), secret.clone()) else {
        return;
    };
    if let Ok(window) = windows::open_board(app, &config, &secret, config.notifications, None) {
        let _ = window.navigate(url);
        s.active = Some(Connection {
            client,
            secret,
            polling: config.notifications,
        });
    }
}
