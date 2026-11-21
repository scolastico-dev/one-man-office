//! Native process ownership is independent of the currently displayed board.
use omo_ui_core::{
    Result,
    lifecycle::{CloseAction, CloseIntent, close_action},
    local::LocalControl,
};
use std::sync::{
    Arc, Mutex,
    atomic::{AtomicBool, Ordering},
};
use tauri::{AppHandle, Manager};

#[derive(Default)]
pub struct Desktop {
    owned: Mutex<Option<Arc<LocalControl>>>,
    pub operation: Mutex<()>,
    pub stop_with_window: AtomicBool,
    busy: AtomicBool,
    pub exiting: AtomicBool,
}
impl Desktop {
    pub fn capture(&self, app: &AppHandle, connection: Arc<LocalControl>) -> Result<()> {
        let mut owned = self
            .owned
            .lock()
            .map_err(|_| "Local ownership state is unavailable.")?;
        // Reconnecting to the same instance must retain the launcher's original proof.
        if owned
            .as_ref()
            .is_some_and(|old| old.same_instance(&connection))
        {
            return Ok(());
        }
        // A replacement attachment does not transfer ownership. Retire the old
        // captured process before forgetting it, without touching the replacement.
        if let Some(old) = owned.as_ref() {
            old.stop_owned()?;
        }
        *owned = connection.owned().then_some(connection);
        let attached = owned.is_none();
        drop(owned);
        if attached {
            remove_tray(app);
        }
        Ok(())
    }
    pub fn release(&self, app: &AppHandle, direct_origin: Option<&str>) -> Result<bool> {
        let owned = self.live()?;
        let stopped = owned.is_some();
        if let Some(connection) = owned {
            if direct_origin.is_some() && direct_origin == connection.board_origin().as_deref() {
                return Err("Disconnect first to stop the server this app started, then start it independently before connecting directly.".into());
            }
            // Keep proof when shutdown fails, so a later close or retry can clean up.
            connection.stop_owned()?;
            *self
                .owned
                .lock()
                .map_err(|_| "Local ownership state is unavailable.")? = None;
        }
        remove_tray(app);
        Ok(stopped)
    }
    fn live(&self) -> Result<Option<Arc<LocalControl>>> {
        let mut owned = self
            .owned
            .lock()
            .map_err(|_| "Local ownership state is unavailable.")?;
        if let Some(connection) = owned.as_ref() {
            if !connection.is_alive()? {
                *owned = None;
            }
        }
        Ok(owned.clone())
    }
}
fn problem(app: &AppHandle, error: String) {
    if let Ok(mut session) = app.state::<crate::Shell>().session.lock() {
        session.error = error;
    }
    crate::windows::show_setup(app);
}
fn remove_tray(app: &AppHandle) {
    app.remove_tray_by_id("omo-owned");
}
fn ensure_tray(app: &AppHandle) -> Result<()> {
    use tauri::{
        menu::{Menu, MenuItem},
        tray::TrayIconBuilder,
    };
    if app.tray_by_id("omo-owned").is_some() {
        return Ok(());
    }
    let open = MenuItem::with_id(app, "owned-open", "Open omo", true, None::<&str>)
        .map_err(|_| "Cannot create the omo tray menu.")?;
    let exit = MenuItem::with_id(
        app,
        "owned-exit",
        "Exit omo and stop local server",
        true,
        None::<&str>,
    )
    .map_err(|_| "Cannot create the omo tray menu.")?;
    let menu =
        Menu::with_items(app, &[&open, &exit]).map_err(|_| "Cannot create the omo tray menu.")?;
    let mut builder = TrayIconBuilder::with_id("omo-owned")
        .tooltip("omo")
        .menu(&menu)
        .on_menu_event(|app, event| match event.id().as_ref() {
            "owned-open" => reopen(app),
            "owned-exit" => request_close(app, None, CloseIntent::Quit),
            _ => {}
        });
    if let Some(icon) = app.default_window_icon() {
        builder = builder.icon(icon.clone());
    }
    builder
        .build(app)
        .map_err(|_| "Cannot create the omo tray icon.")?;
    Ok(())
}
pub fn reopen(app: &AppHandle) {
    let app = app.clone();
    tauri::async_runtime::spawn_blocking(move || reopen_on_worker(&app));
}
fn reopen_on_worker(app: &AppHandle) {
    let label = app
        .state::<crate::Shell>()
        .session
        .lock()
        .ok()
        .and_then(|s| s.active.as_ref().map(|c| c.board_label.clone()));
    if let Some(window) = label.and_then(|label| app.get_webview_window(&label)) {
        let _ = window.show();
        let _ = window.set_focus();
    } else {
        crate::windows::show_setup(app);
    }
}
/// Runs on the polling worker, never the UI event thread.
pub fn refresh(app: &AppHandle) -> Result<()> {
    if app.state::<crate::Shell>().desktop.live()?.is_some() {
        ensure_tray(app)?;
    } else if app.tray_by_id("omo-owned").is_some() {
        remove_tray(app);
        if !app
            .webview_windows()
            .values()
            .any(|w| w.is_visible().unwrap_or(false))
        {
            crate::windows::show_setup(app);
        }
    }
    Ok(())
}
pub fn request_close(app: &AppHandle, label: Option<String>, intent: CloseIntent) {
    let state = app.state::<crate::Shell>();
    if state.desktop.busy.swap(true, Ordering::SeqCst) {
        return;
    }
    let app = app.clone();
    tauri::async_runtime::spawn_blocking(move || {
        let result = close(&app, label, intent);
        app.state::<crate::Shell>()
            .desktop
            .busy
            .store(false, Ordering::SeqCst);
        if let Err(error) = result {
            problem(&app, error);
        }
    });
}
fn close(app: &AppHandle, label: Option<String>, mut intent: CloseIntent) -> Result<()> {
    let state = app.state::<crate::Shell>();
    let _operation = state
        .desktop
        .operation
        .lock()
        .map_err(|_| "Local operation state is unavailable.")?;
    if intent == CloseIntent::LastSetup {
        let active = state
            .session
            .lock()
            .map_err(|_| "Connection state is unavailable.")?
            .active
            .as_ref()
            .map(|c| c.board_label.clone());
        if active
            .and_then(|label| app.get_webview_window(&label))
            .is_some_and(|w| w.is_visible().unwrap_or(false))
        {
            intent = CloseIntent::ConnectedSetup;
        }
    }
    if intent == CloseIntent::ConnectedSetup {
        if let Some(w) = label.and_then(|label| app.get_webview_window(&label)) {
            let _ = w.destroy();
        }
        return Ok(());
    }
    let owned = state.desktop.live()?;
    let action = close_action(
        intent,
        owned.is_some(),
        state.desktop.stop_with_window.load(Ordering::SeqCst),
    );
    match action {
        CloseAction::CloseWindow => {
            if let Some(w) = label.and_then(|label| app.get_webview_window(&label)) {
                let _ = w.destroy();
            }
        }
        CloseAction::Hide => {
            ensure_tray(app)?;
            if let Some(w) = label.and_then(|label| app.get_webview_window(&label)) {
                w.hide().map_err(|_| "Cannot hide the omo window.")?;
            }
        }
        CloseAction::StopAndExit | CloseAction::Exit => {
            if action == CloseAction::StopAndExit {
                owned.expect("owned stop policy").stop_owned()?;
            }
            crate::relay::stop(app)?;
            if let Some(connection) = state
                .session
                .lock()
                .map_err(|_| "Connection state is unavailable.")?
                .active
                .as_mut()
            {
                crate::forwards::close_connection(app, connection)?;
            }
            remove_tray(app);
            state.desktop.exiting.store(true, Ordering::SeqCst);
            app.exit(0);
        }
    }
    Ok(())
}
