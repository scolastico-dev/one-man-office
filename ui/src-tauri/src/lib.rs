#[cfg(target_os = "android")]
mod android_auth;
mod credentials;
#[cfg(target_os = "macos")]
mod macos_auth;
mod platform;
mod windows;

use omo_ui_core::{
    Auth, BoardClient, Config, Delivery, Mode, Poll, Recovery, Result, load_config, read_json,
    save_config, write_json,
};
use serde::{Deserialize, Serialize};
use std::{path::PathBuf, sync::Mutex, time::Duration};
use tauri::{AppHandle, Manager, WebviewWindow};
#[cfg(not(target_os = "android"))]
use tauri_plugin_notification::NotificationExt;

struct Connection {
    client: BoardClient,
    secret: String,
    delivery: Delivery,
    polling: bool,
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
    remember: bool,
    secret: String,
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
fn snapshot(state: &Shell) -> Result<Setup> {
    let s = state
        .session
        .lock()
        .map_err(|_| "Connection state is unavailable.")?;
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
    tauri::async_runtime::spawn_blocking(move || snapshot(&app.state::<Shell>()))
        .await
        .map_err(|_| "Cannot read connection state.")?
}
#[tauri::command]
async fn connect(window: WebviewWindow, app: AppHandle, settings: Settings) -> Result<Setup> {
    require_setup(&window)?;
    tauri::async_runtime::spawn_blocking(move || {
        let state = app.state::<Shell>();
        let mut s = state
            .session
            .lock()
            .map_err(|_| "Connection state is unavailable.")?;
        let mut config = Config {
            id: Config::new_id(),
            mode: settings.mode,
            origin: settings.origin,
            auth: settings.auth,
            username: settings.username,
            notifications: settings.notifications,
            remember: settings.remember,
        };
        config.validate(cfg!(target_os = "android"))?;
        if config.mode == Mode::Local {
            return local_mode_unavailable();
        }
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
        let client = BoardClient::new(config.clone(), secret.clone())?;
        let polling = client.connect(cfg!(target_os = "android"))?;
        let mut delivery: Delivery =
            read_json(&state.root, &format!("{}.json", config.id))?.unwrap_or_default();
        if polling && delivery.cursor.is_none() {
            client.poll(
                &mut delivery,
                |_| Err("Unexpected notification during baseline.".into()),
                |d| write_json(&state.root, &format!("{}.json", config.id), d),
            )?;
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
        save_config(&state.root, &config)?;
        windows::close_boards(&app);
        let board = windows::open_board(&app, &config, &secret, polling, None)?;
        let board_label = board.label().to_string();
        s.config = Some(config);
        s.active = Some(Connection {
            client,
            secret,
            delivery,
            polling,
            board_label,
        });
        s.error.clear();
        s.recovery = None;
        drop(s);
        #[cfg(not(target_os = "android"))]
        if let Some(setup) = app.get_webview_window("setup") {
            let _ = setup.hide();
        }
        snapshot(&state)
    })
    .await
    .map_err(|_| "The connection could not be opened.")?
}
#[tauri::command]
async fn disconnect(window: WebviewWindow, app: AppHandle) -> Result<Setup> {
    require_setup(&window)?;
    tauri::async_runtime::spawn_blocking(move || {
        let state = app.state::<Shell>();
        let mut s = state
            .session
            .lock()
            .map_err(|_| "Connection state is unavailable.")?;
        s.active = None;
        s.recovery = None;
        s.error.clear();
        windows::close_boards(&app);
        drop(s);
        snapshot(&state)
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
        let recovery = s.recovery.clone().ok_or("No cursor recovery is pending.")?;
        let c = s.active.as_mut().ok_or("Connect to a board first.")?;
        recovery.apply(&mut c.delivery, replay, |d| {
            write_json(&state.root, &format!("{}.json", c.client.config.id), d)
        })?;
        s.recovery = None;
        s.error.clear();
        drop(s);
        snapshot(&state)
    })
    .await
    .map_err(|_| "Cannot recover the notification cursor.")?
}
fn local_mode_unavailable<T>() -> Result<T> {
    if cfg!(target_os = "android") {
        Err("Android requires an external omo host.".into())
    } else {
        Err("Local installation and lifecycle are not available in this build. Connect to an existing omo host directly.".into())
    }
}
#[tauri::command]
async fn local_connect(window: WebviewWindow) -> Result<()> {
    require_setup(&window)?;
    local_mode_unavailable()
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
                session: Mutex::new(Session {
                    config,
                    error,
                    ..Default::default()
                }),
            });
            windows::create_setup(app.handle())?;
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
                        let more = tauri::async_runtime::spawn_blocking(move || poll(&h))
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
        .run(tauri::generate_context!())
        .expect("omo ui could not start");
}
