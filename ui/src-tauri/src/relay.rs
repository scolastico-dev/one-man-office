//! Tauri owns the relay task and ephemeral board endpoint; pages get presentation only.
use crate::{Connection, Setup, Shell, credentials, snapshot, windows};
use omo_ui_core::{
    Auth, BoardClient, Config, Mode, NotificationMode, Result,
    relay::{RelayClient, RelayConnection, RelayPhase, wire::Route},
    save_config,
};
use serde::Deserialize;
use std::sync::Arc;
use tauri::{AppHandle, Emitter, Manager, WebviewWindow};

#[derive(Deserialize)]
pub(crate) struct RelaySettings {
    origin: String,
    host_id: String,
    admission: Option<String>,
    new_pair: bool,
    #[serde(default)]
    relay_allow_loopback_http: bool,
    notifications: bool,
    #[serde(default)]
    notification_mode: NotificationMode,
}
#[tauri::command]
pub(crate) async fn relay_connect(
    window: WebviewWindow,
    app: AppHandle,
    settings: RelaySettings,
) -> Result<Setup> {
    crate::require_setup(&window)?;
    tauri::async_runtime::spawn_blocking(move || {
        let state = app.state::<Shell>();
        #[cfg(target_os = "android")]
        let _operation = state.operation.lock().map_err(|_| "Connection operation state is unavailable.")?;
        #[cfg(not(target_os = "android"))]
        let _operation = state.desktop.operation.lock().map_err(|_| "Connection operation state is unavailable.")?;
        let prior = state.session.lock().map_err(|_| "Connection state is unavailable.")?.config.clone();
        let mut config = Config {
            id: Config::new_id(), mode: Mode::Relay, origin: settings.origin, host_id: settings.host_id,
            relay_allow_loopback_http: settings.relay_allow_loopback_http,
            auth: Auth::None, username: String::new(), remember: true, notifications: settings.notifications,
            notification_mode: settings.notification_mode, stop_with_window: false,
        };
        config.validate(cfg!(target_os = "android"))?;
        if !settings.new_pair {
            let old = prior.filter(|p| p.mode == Mode::Relay && p.origin == config.origin && p.host_id == config.host_id)
                .ok_or("No saved relay connection for this route. Run pair begin on the host and choose Start new pairing.")?;
            config.id = old.id;
        }
        start(&app, config, settings.new_pair, settings.admission, false)?;
        snapshot(&state, &app)
    }).await.map_err(|_| "Cannot prepare the native relay connection.")?
}

pub(crate) fn stop(app: &AppHandle) -> Result<()> {
    let state = app.state::<Shell>();
    state
        .session
        .lock()
        .map_err(|_| "Connection state is unavailable.")?
        .relay_notifications = None;
    let old = state
        .relay
        .lock()
        .map_err(|_| "Relay connection state is unavailable.")?
        .take();
    if let Some(old) = old {
        tauri::async_runtime::block_on(old.close());
        windows::close_boards(app);
        let mut session = state
            .session
            .lock()
            .map_err(|_| "Connection state is unavailable.")?;
        session.active = None;
        session.relay_view = Some(old.view());
    }
    Ok(())
}

pub(crate) fn start(
    app: &AppHandle,
    config: Config,
    new_pair: bool,
    admission: Option<String>,
    resume_worker: bool,
) -> Result<()> {
    stop(app)?;
    let state = app.state::<Shell>();
    let route = Route::new(
        &config.origin,
        &config.host_id,
        config.relay_allow_loopback_http,
    )?;
    let store = Arc::new(credentials::RelaySecrets);
    let client = if new_pair {
        RelayClient::open(store, &config.id, route, admission)?
    } else {
        // Missing saved secrets must never generate a replacement identity.
        RelayClient::restore_with_admission(store, &config.id, route, admission)?
    };
    #[cfg(not(target_os = "android"))]
    state.desktop.release(app, None)?;
    #[cfg(target_os = "android")]
    if new_pair {
        crate::android_worker(app).call::<serde_json::Value>("disconnect", ())?;
    }
    windows::close_boards(app);
    {
        let mut session = state
            .session
            .lock()
            .map_err(|_| "Connection state is unavailable.")?;
        session.active = None;
        session.relay_view = None;
        session.error.clear();
        session.recovery = None;
        save_config(&state.root, &config)?;
        session.config = Some(config.clone());
    }
    if client.notification_secrets().is_ok() {
        configure_notifications(app, &config, &client, resume_worker)?;
    }
    let notifier_client = client.clone();
    let owner = Arc::new(tauri::async_runtime::block_on(async {
        RelayConnection::start(client, new_pair)
    }));
    *state
        .relay
        .lock()
        .map_err(|_| "Relay connection state is unavailable.")? = Some(owner.clone());
    let handle = app.clone();
    tauri::async_runtime::spawn(async move {
        let mut changes = owner.subscribe();
        loop {
            let view = changes.borrow_and_update().clone();
            let (app, connection, config, client) = (
                handle.clone(),
                owner.clone(),
                config.clone(),
                notifier_client.clone(),
            );
            let phase = view.phase;
            let updated = tauri::async_runtime::spawn_blocking(move || {
                let state = app.state::<Shell>();
                #[cfg(target_os = "android")]
                let _operation = state.operation.lock().map_err(|_| "Connection operation state is unavailable.")?;
                #[cfg(not(target_os = "android"))]
                let _operation = state.desktop.operation.lock().map_err(|_| "Connection operation state is unavailable.")?;
                let current = state.relay.lock().map_err(|_| "Relay connection state is unavailable.")?
                    .as_ref().is_some_and(|c| Arc::ptr_eq(c, &connection));
                if !current { return Ok::<_, String>(false); }
                if phase == RelayPhase::Ready {
                    if let Err(error) = configure_notifications(&app,&config,&client,resume_worker).and_then(|_| open_board(&app, &config, &connection)) {
                        state.session.lock().map_err(|_| "Connection state is unavailable.")?.error = error;
                        windows::show_setup(&app);
                    }
                } else if matches!(phase, RelayPhase::Failed | RelayPhase::Cancelled) {
                    state.session.lock().map_err(|_| "Connection state is unavailable.")?.error =
                        "Relay pairing ended. Reconnect saved trust or explicitly start a new ceremony on the host and here.".into();
                    windows::show_setup(&app);
                }
                if let Ok(setup) = snapshot(&state, &app) {
                    let _ = app.emit_to("setup", "relay-state", setup);
                }
                Ok(true)
            }).await;
            if !matches!(updated, Ok(Ok(true)))
                || matches!(phase, RelayPhase::Failed | RelayPhase::Cancelled)
            {
                break;
            }
            if changes.changed().await.is_err() {
                break;
            }
        }
    });
    Ok(())
}
fn configure_notifications(
    app: &AppHandle,
    config: &Config,
    client: &RelayClient,
    resume_worker: bool,
) -> Result<()> {
    let state = app.state::<Shell>();
    if state
        .session
        .lock()
        .map_err(|_| "Connection state is unavailable.")?
        .relay_notifications
        .is_some()
    {
        return Ok(());
    }
    let secrets = client.notification_secrets()?;
    #[cfg(target_os = "android")]
    {
        if config.notifications {
            use tauri_plugin_notification::NotificationExt;
            if app
                .notification()
                .request_permission()
                .map_err(|_| "Cannot request Android notification permission.")?
                != tauri::plugin::PermissionState::Granted
            {
                return Err(
                    "Enable notifications for omo ui in Android Settings to use native polling."
                        .into(),
                );
            }
        }
        crate::android_worker(app).call::<serde_json::Value>("configure",serde_json::json!({
            "config":config,"secret":serde_json::to_string(&secrets).map_err(|_| "Cannot prepare relay worker credentials.")?,
            "root":state.root,"enabled":config.notifications,"resume":resume_worker,
        }))?;
    }
    #[cfg(not(target_os = "android"))]
    let _ = (config, resume_worker);
    state
        .session
        .lock()
        .map_err(|_| "Connection state is unavailable.")?
        .relay_notifications = Some(secrets);
    Ok(())
}
fn open_board(app: &AppHandle, config: &Config, owner: &RelayConnection) -> Result<()> {
    let state = app.state::<Shell>();
    let endpoint = owner
        .endpoint()
        .ok_or("Relay board endpoint is unavailable.")?;
    let mut effective = config.clone();
    effective.mode = Mode::Direct;
    effective.origin = endpoint.origin;
    effective.host_id.clear();
    effective.relay_allow_loopback_http = false;
    effective.auth = Auth::Basic;
    effective.username = endpoint.username;
    let polling = config.notifications
        && state
            .session
            .lock()
            .map_err(|_| "Connection state is unavailable.")?
            .relay_notifications
            .is_some();
    effective.notifications = polling;
    let client = BoardClient::new(effective.clone(), endpoint.password.clone())?;
    client.connect(cfg!(target_os = "android"))?;
    let mut session = state
        .session
        .lock()
        .map_err(|_| "Connection state is unavailable.")?;
    if let Some(active) = session.active.as_ref() {
        #[cfg(not(target_os = "android"))]
        if let Some(window) = app.get_webview_window(&active.board_label) {
            window
                .navigate(
                    effective
                        .origin
                        .parse()
                        .map_err(|_| "Invalid private board origin.")?,
                )
                .map_err(|_| "Cannot reload the relay board.")?;
            session.error.clear();
            return Ok(());
        }
        #[cfg(target_os = "android")]
        let _ = active;
    }
    let board = windows::open_board(app, &effective, &endpoint.password, polling, None)?;
    #[cfg(target_os = "android")]
    if let Some((id, path)) = session
        .pending_activation
        .take()
        .filter(|(id, _)| id == &config.id)
    {
        let _ = id;
        let url = effective.destination(&path)?;
        crate::android_worker(app)
            .call::<serde_json::Value>("navigateBoard", serde_json::json!({"url":url.as_str()}))?;
    }
    session.active = Some(Connection {
        client,
        secret: endpoint.password,
        polling,
        #[cfg(not(target_os = "android"))]
        delivery: Default::default(),
        #[cfg(not(target_os = "android"))]
        board_label: board.label().to_string(),
    });
    #[cfg(target_os = "android")]
    let _ = board;
    session.error.clear();
    drop(session);
    #[cfg(not(target_os = "android"))]
    if let Some(setup) = app.get_webview_window("setup") {
        let _ = setup.hide();
    }
    Ok(())
}

pub(crate) fn restore(app: &AppHandle) {
    let app = app.clone();
    tauri::async_runtime::spawn_blocking(move || {
        let state = app.state::<Shell>();
        #[cfg(target_os = "android")]
        let Ok(_operation) = state.operation.lock() else {
            return;
        };
        #[cfg(not(target_os = "android"))]
        let Ok(_operation) = state.desktop.operation.lock() else {
            return;
        };
        let config = state
            .session
            .lock()
            .ok()
            .and_then(|s| s.config.clone())
            .filter(|c| c.mode == Mode::Relay);
        if let Some(config) = config {
            if let Err(error) = start(&app, config, false, None, true) {
                if let Ok(mut s) = state.session.lock() {
                    s.error = error;
                }
            }
        }
    });
}
