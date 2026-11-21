//! Desktop resources belong to one active connection; app documents receive no commands.
use omo_ui_core::{BoardClient, ForwardConnector, ForwardListener, RegisteredForward, Result};
use serde::Serialize;
use tauri::{AppHandle, Manager};

pub struct Resources {
    connector: ForwardConnector,
    pub generation: String,
    entries: Vec<Entry>,
}
struct Entry {
    info: Exposure,
    listener: ForwardListener,
    #[cfg(windows)]
    profile: Option<crate::preview_profile::Profile>,
}
#[derive(Clone, Serialize)]
pub struct Exposure {
    pub id: String,
    pub name: String,
    pub address: String,
    pub preview: bool,
    pub error: String,
}
#[derive(Serialize)]
pub struct View {
    pub forwards: Vec<RegisteredForward>,
    pub exposures: Vec<Exposure>,
    pub error: String,
}
impl Resources {
    pub fn new(client: &BoardClient, secret: &str) -> Result<Self> {
        Ok(Self {
            connector: ForwardConnector::new(&client.config, secret)?,
            generation: omo_ui_core::Config::new_id(),
            entries: Vec::new(),
        })
    }
    pub fn snapshot(&self) -> Vec<Exposure> {
        self.entries
            .iter()
            .map(|e| Exposure {
                error: e.listener.error(),
                ..e.info.clone()
            })
            .collect()
    }
    pub fn open(
        &mut self,
        app: &AppHandle,
        client: &BoardClient,
        id: &str,
        port: Option<&str>,
    ) -> Result<Vec<Exposure>> {
        // A fresh authenticated read supplies all routing choices. Pages cannot pass hosts.
        let forward = client
            .forwards()?
            .into_iter()
            .find(|f| f.id == id)
            .ok_or("That app is no longer registered. Refresh the app list.")?;
        let preview = port.is_none();
        #[cfg(windows)]
        let mut profile = if preview {
            Some(crate::preview_profile::Profile::new()?)
        } else {
            None
        };
        let listener = tauri::async_runtime::block_on(async {
            match port {
                Some(port) => {
                    ForwardListener::local(
                        self.connector.clone(),
                        forward.id.clone(),
                        omo_ui_core::local_port(port)?,
                    )
                    .await
                }
                None => {
                    ForwardListener::preview(
                        self.connector.clone(),
                        forward.id.clone(),
                        forward.virtual_host()?,
                    )
                    .await
                }
            }
        })?;
        let id = omo_ui_core::Config::new_id();
        let address = if preview {
            forward.preview_url()?.to_string()
        } else {
            format!("http://{}/", listener.address())
        };
        #[cfg(windows)]
        let profile_path = profile.as_ref().map(|p| p.path());
        #[cfg(not(windows))]
        let profile_path = None;
        if preview {
            let mut prepare_profile = |_window: &tauri::WebviewWindow| -> Result<()> {
                #[cfg(windows)]
                if let Some(profile) = profile.as_mut() {
                    profile.arm(_window)?;
                }
                Ok(())
            };
            if let Err(error) = crate::windows::open_preview(
                app,
                &id,
                &address,
                listener.address(),
                profile_path,
                Some(&mut prepare_profile),
                None,
            ) {
                crate::windows::close_preview(app, &id);
                tauri::async_runtime::block_on(listener.stop())?;
                #[cfg(windows)]
                if let Some(profile) = profile {
                    profile.close()?;
                }
                return Err(error);
            }
        }
        self.entries.push(Entry {
            info: Exposure {
                id,
                name: if forward.name.is_empty() {
                    "App".into()
                } else {
                    forward.name
                },
                address,
                preview,
                error: String::new(),
            },
            listener,
            #[cfg(windows)]
            profile,
        });
        Ok(self.snapshot())
    }
    pub fn stop(&mut self, app: &AppHandle, id: &str) -> Result<Vec<Exposure>> {
        let index = self
            .entries
            .iter()
            .position(|e| e.info.id == id)
            .ok_or("That exposure has already ended.")?;
        let entry = self.entries.remove(index);
        close_entry(app, entry)?;
        if let Some(setup) = app.get_webview_window("setup") {
            let _ = setup.eval("window.dispatchEvent(new Event('omo-forwards-changed'))");
        }
        Ok(self.snapshot())
    }
    pub fn close(self, app: &AppHandle) -> Result<()> {
        close_each(self.entries, |entry| close_entry(app, entry))
    }
}
// These functions run only on native worker threads under Desktop::operation.
pub fn view(app: &AppHandle) -> Result<View> {
    let state = app.state::<crate::Shell>();
    let s = state
        .session
        .lock()
        .map_err(|_| "Connection state is unavailable.")?;
    let c = s.active.as_ref().ok_or("Connect to omo first.")?;
    let resources = c
        .forwards
        .as_ref()
        .ok_or("This server does not support desktop app forwarding.")?;
    let (forwards, error) = match c.client.forwards() {
        Ok(forwards) => (forwards, String::new()),
        Err(error) => (Vec::new(), error),
    };
    Ok(View {
        forwards,
        error,
        exposures: resources.snapshot(),
    })
}
pub fn open(app: &AppHandle, id: &str, port: Option<&str>) -> Result<Vec<Exposure>> {
    let state = app.state::<crate::Shell>();
    let mut s = state
        .session
        .lock()
        .map_err(|_| "Connection state is unavailable.")?;
    let c = s.active.as_mut().ok_or("Connect to omo first.")?;
    c.forwards
        .as_mut()
        .ok_or("This server does not support desktop app forwarding.")?
        .open(app, &c.client, id, port)
}
pub fn stop(app: &AppHandle, id: &str) -> Result<Vec<Exposure>> {
    let state = app.state::<crate::Shell>();
    let mut s = state
        .session
        .lock()
        .map_err(|_| "Connection state is unavailable.")?;
    s.active
        .as_mut()
        .and_then(|c| c.forwards.as_mut())
        .ok_or("This server does not support desktop app forwarding.")?
        .stop(app, id)
}
pub fn close_connection(app: &AppHandle, connection: &mut crate::Connection) -> Result<()> {
    if let Some(resources) = connection.forwards.take() {
        resources.close(app)?;
    }
    Ok(())
}

pub fn retired_window(app: &AppHandle, group: String, destroyed: String) {
    let app = app.clone();
    tauri::async_runtime::spawn_blocking(move || {
        let state = app.state::<crate::Shell>();
        let Ok(_operation) = state.desktop.operation.lock() else {
            return;
        };
        if app
            .webview_windows()
            .keys()
            .any(|label| label != &destroyed && label.starts_with(&format!("preview-{group}-")))
        {
            return;
        }
        let Ok(mut session) = state.session.lock() else {
            return;
        };
        let Some(resources) = session.active.as_mut().and_then(|c| c.forwards.as_mut()) else {
            return;
        };
        if resources.entries.iter().any(|e| e.info.id == group) {
            if let Err(error) = resources.stop(&app, &group) {
                session.error = error;
            }
        }
    });
}

fn close_entry(app: &AppHandle, entry: Entry) -> Result<()> {
    crate::windows::close_preview(app, &entry.info.id);
    let stopped = tauri::async_runtime::block_on(entry.listener.stop());
    #[cfg(windows)]
    let profile = entry.profile.map(|p| p.close()).unwrap_or(Ok(()));
    #[cfg(not(windows))]
    let profile = Ok(());
    close_each([stopped, profile], |result| result)
}
fn close_each<T>(
    entries: impl IntoIterator<Item = T>,
    mut close: impl FnMut(T) -> Result<()>,
) -> Result<()> {
    let mut errors = Vec::new();
    for entry in entries {
        if let Err(error) = close(entry) {
            errors.push(error);
        }
    }
    if errors.is_empty() {
        Ok(())
    } else {
        Err(errors.join("\n"))
    }
}

#[cfg(test)]
mod tests {
    #[test]
    fn failed_cleanup_still_retires_every_remaining_family_and_reports_all_errors() {
        let mut retired = Vec::new();
        let result = super::close_each(["first", "second", "third"], |family| {
            retired.push(family);
            match family {
                "first" => Err("profile first is locked".into()),
                "third" => Err("third failed".into()),
                _ => Ok(()),
            }
        });
        assert_eq!(retired, ["first", "second", "third"]);
        let error = result.unwrap_err();
        assert!(error.contains("profile first is locked"));
        assert!(error.contains("third failed"));
    }
}

#[cfg(test)]
mod permissions {
    #[test]
    fn compiled_capabilities_deny_every_command_to_remote_pages_and_preview_families() {
        use tauri::ipc::Origin;
        let mut context: tauri::Context<tauri::Wry> = tauri::generate_context!();
        let authority = context.runtime_authority_mut();
        for command in [
            "get_setup",
            "connect",
            "disconnect",
            "recover",
            "local_connect",
            "get_forwards",
            "preview_forward",
            "expose_forward",
            "stop_forward",
        ] {
            assert!(
                authority
                    .resolve_access(command, "setup", "setup", &Origin::Local)
                    .is_some(),
                "setup lost {command}"
            );
            for label in ["setup", "board-1", "preview-family-1", "preview-family-2"] {
                for url in [
                    "http://app-0123456789abcdef0123456789abcdef.omo/start",
                    "https://board.example/",
                ] {
                    assert!(
                        authority
                            .resolve_access(
                                command,
                                label,
                                label,
                                &Origin::Remote {
                                    url: url.parse().unwrap()
                                }
                            )
                            .is_none(),
                        "{command} granted to {label} at {url}"
                    );
                }
                if label != "setup" {
                    assert!(
                        authority
                            .resolve_access(command, label, label, &Origin::Local)
                            .is_none(),
                        "{command} granted to local {label}"
                    );
                }
            }
        }
    }
}
