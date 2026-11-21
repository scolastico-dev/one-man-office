//! Delete a private WebView2 profile only after its browser process releases it.
use omo_ui_core::Result;
use std::{cell::RefCell, rc::Rc, sync::mpsc};
use tauri::WebviewWindow;
use webview2_com::{
    BrowserProcessExitedEventHandler, Microsoft::Web::WebView2::Win32::ICoreWebView2Environment5,
};
use windows_core::Interface;

pub struct Profile {
    directory: tempfile::TempDir,
    exited: Option<tokio::sync::oneshot::Receiver<()>>,
}
impl Profile {
    pub fn new() -> Result<Self> {
        Ok(Self {
            directory: tempfile::Builder::new()
                .prefix("omo-preview-")
                .tempdir()
                .map_err(|_| "Cannot create a private preview profile.")?,
            exited: None,
        })
    }
    pub fn path(&self) -> std::path::PathBuf {
        self.directory.path().to_path_buf()
    }
    pub fn arm(&mut self, window: &WebviewWindow) -> Result<()> {
        let (ready, registered) = mpsc::sync_channel(1);
        let (ended, exited) = tokio::sync::oneshot::channel();
        window
            .with_webview(move |view| unsafe {
                let result = (|| -> windows_core::Result<()> {
                    let environment = view.environment().cast::<ICoreWebView2Environment5>()?;
                    // Keep the COM environment on its UI thread until BrowserProcessExited.
                    // The callback removes itself and breaks this ownership cycle on that event.
                    let owner = Rc::new(RefCell::new(Some((environment.clone(), 0i64))));
                    let callback_owner = owner.clone();
                    let mut ended = Some(ended);
                    let mut token = 0;
                    environment.add_BrowserProcessExited(
                        &BrowserProcessExitedEventHandler::create(Box::new(move |_, _| {
                            if let Some((environment, token)) = callback_owner.borrow_mut().take() {
                                environment.remove_BrowserProcessExited(token)?;
                            }
                            if let Some(ended) = ended.take() {
                                let _ = ended.send(());
                            }
                            Ok(())
                        })),
                        &mut token,
                    )?;
                    owner
                        .borrow_mut()
                        .as_mut()
                        .expect("registered environment")
                        .1 = token;
                    Ok(())
                })();
                let _ = ready.send(
                    result.map_err(|_| "Cannot observe preview profile shutdown.".to_string()),
                );
            })
            .map_err(|_| "Cannot configure the preview profile.")?;
        registered
            .recv()
            .map_err(|_| "Preview profile registration failed.")??;
        self.exited = Some(exited);
        Ok(())
    }
    pub fn close(self) -> Result<()> {
        if let Some(exited) = self.exited {
            tauri::async_runtime::block_on(exited)
                .map_err(|_| "Preview browser shutdown could not be observed.")?;
        }
        let path = self.directory.path().display().to_string();
        self.directory
            .close()
            .map_err(|_| format!("Cannot remove the preview profile at {path}."))
    }
}
