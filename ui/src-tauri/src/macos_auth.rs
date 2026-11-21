//! Add only the missing public WKNavigationDelegate authentication callback.
//! Wry's existing navigation, download and new-window delegate methods remain intact.
use block2::Block;
use objc2::{
    AnyThread, msg_send,
    runtime::{AnyObject, Imp, Sel},
    sel,
};
use objc2_foundation::{
    NSString, NSURLAuthenticationChallenge, NSURLCredential, NSURLCredentialPersistence,
    NSURLSessionAuthChallengeDisposition as Disposition,
};
use omo_ui_core::{Config, Result};
use std::{
    collections::HashMap,
    sync::{Mutex, OnceLock},
};
use tauri::WebviewWindow;
static AUTH: Mutex<Option<HashMap<usize, (Config, String)>>> = Mutex::new(None);
static INSTALLED: OnceLock<bool> = OnceLock::new();

pub fn configure(window: &WebviewWindow, config: &Config, secret: &str) -> Result<()> {
    let pointer = std::sync::Arc::new(std::sync::atomic::AtomicUsize::new(0));
    let cleanup = pointer.clone();
    window.on_window_event(move |event| {
        if matches!(event, tauri::WindowEvent::Destroyed) {
            if let Some(map) = AUTH.lock().unwrap_or_else(|e| e.into_inner()).as_mut() {
                map.remove(&cleanup.load(std::sync::atomic::Ordering::Relaxed));
            }
        }
    });
    let config = config.clone();
    let secret = secret.to_string();
    window
        .with_webview(move |view| unsafe {
            let webview = view.inner().cast::<AnyObject>();
            // Host HTML fullscreen must not hide the native trust frame.
            let configuration: *mut AnyObject = msg_send![webview, configuration];
            let preferences: *mut AnyObject = msg_send![configuration, preferences];
            let supported: bool =
                msg_send![preferences, respondsToSelector: sel!(setElementFullscreenEnabled:)];
            if supported {
                let _: () = msg_send![preferences, setElementFullscreenEnabled: false];
            } else {
                let value = objc2_foundation::NSNumber::new_bool(false);
                let key = NSString::from_str("fullScreenEnabled");
                let _: () = msg_send![preferences, setValue: &*value, forKey: &*key];
            }
            pointer.store(webview as usize, std::sync::atomic::Ordering::Relaxed);
            let delegate: *mut AnyObject = msg_send![webview, navigationDelegate];
            if delegate.is_null() {
                return;
            }
            let installed = *INSTALLED.get_or_init(|| {
                let class = (*delegate).class();
                let imp: Imp = std::mem::transmute(authentication as *const ());
                objc2::ffi::class_addMethod(
                    class as *const _ as *mut _,
                    sel!(webView:didReceiveAuthenticationChallenge:completionHandler:),
                    imp,
                    c"v@:@@@?".as_ptr(),
                )
                .as_bool()
            });
            if installed {
                AUTH.lock()
                    .unwrap_or_else(|e| e.into_inner())
                    .get_or_insert_with(HashMap::new)
                    .insert(webview as usize, (config, secret));
            }
        })
        .map_err(|_| "Cannot configure WKWebView authentication.".into())
}
// Called by WebKit on the main thread. The supplied block must be completed exactly once.
unsafe extern "C-unwind" fn authentication(
    _this: &AnyObject,
    _cmd: Sel,
    webview: &AnyObject,
    challenge: &NSURLAuthenticationChallenge,
    completion: &Block<dyn Fn(Disposition, *mut NSURLCredential)>,
) {
    let space = challenge.protectionSpace();
    if space.authenticationMethod().to_string() != "NSURLAuthenticationMethodHTTPBasic" {
        completion.call((Disposition::PerformDefaultHandling, std::ptr::null_mut()));
        return;
    }
    let values = AUTH
        .lock()
        .unwrap_or_else(|e| e.into_inner())
        .as_ref()
        .and_then(|m| m.get(&(webview as *const _ as usize)))
        .cloned();
    if let Some((config, secret)) = values {
        let protocol = space.protocol().map(|v| v.to_string()).unwrap_or_default();
        let host = space
            .host()
            .to_string()
            .trim_matches(['[', ']'])
            .to_string();
        let authority = if host.contains(':') {
            format!("[{host}]")
        } else {
            host.clone()
        };
        let url = format!("{protocol}://{authority}:{}/", space.port());
        if challenge.previousFailureCount() == 0
            && config.challenge_allowed(
                &url,
                &host,
                &space.realm().map(|v| v.to_string()).unwrap_or_default(),
            )
        {
            let credential = NSURLCredential::initWithUser_password_persistence(
                NSURLCredential::alloc(),
                &NSString::from_str(&config.username),
                &NSString::from_str(&secret),
                NSURLCredentialPersistence::ForSession,
            );
            completion.call((
                Disposition::UseCredential,
                (&*credential as *const NSURLCredential).cast_mut(),
            ));
            return;
        }
    }
    completion.call((
        Disposition::CancelAuthenticationChallenge,
        std::ptr::null_mut(),
    ));
}
