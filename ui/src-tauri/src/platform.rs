//! Per-WebView settings: these apply to the board and every opened child.
use omo_ui_core::{Config, Result};
use tauri::WebviewWindow;

pub fn configure(window: &WebviewWindow, config: &Config, secret: &str) -> Result<()> {
    #[cfg(target_os = "linux")]
    {
        use webkit2gtk::{
            AuthenticationRequestExt, Credential, CredentialPersistence, SettingsExt, WebViewExt,
        };
        let config = config.clone();
        let secret = secret.to_string();
        window
            .with_webview(move |view| {
                let webview = view.inner();
                if let Some(settings) = webview.settings() {
                    settings.set_enable_developer_extras(false);
                    settings.set_enable_fullscreen(false);
                }
                if config.auth == omo_ui_core::Auth::Basic {
                    let origin: tauri::Url = config.origin.parse().expect("validated origin");
                    webview.connect_authenticate(move |webview, request| {
                        if config.challenge_allowed(
                            webview.uri().as_deref().unwrap_or(""),
                            request.host().as_deref().unwrap_or(""),
                            request.realm().as_deref().unwrap_or(""),
                        ) && request.port() == origin.port_or_known_default().unwrap_or(0) as u32
                            && !request.is_retry()
                        {
                            let credential = Credential::new(
                                &config.username,
                                &secret,
                                CredentialPersistence::ForSession,
                            );
                            // webkit2gtk 2.0 omits this method from its safe AuthenticationRequest wrapper.
                            use glib::translate::ToGlibPtr;
                            unsafe {
                                webkit2gtk::ffi::webkit_authentication_request_authenticate(
                                    request.to_glib_none().0,
                                    credential.to_glib_none().0,
                                );
                            }
                        } else {
                            request.cancel();
                        }
                        true
                    });
                }
            })
            .map_err(|_| "Cannot configure WebKit keyboard/authentication settings.")?;
    }
    #[cfg(windows)]
    {
        use webview2_com::{
            BasicAuthenticationRequestedEventHandler,
            Microsoft::Web::WebView2::Win32::{ICoreWebView2_10, ICoreWebView2Settings3},
        };
        use windows_core::{HSTRING, Interface};
        let config = config.clone();
        let secret = secret.to_string();
        let protected = window.clone();
        window
            .with_webview(move |view| unsafe {
                // Fail closed before any host document is navigated.
                if protected
                    .hwnd()
                    .ok()
                    .and_then(|hwnd| crate::windows_chrome::install(hwnd.0).ok())
                    .is_none()
                {
                    let _ = protected.destroy();
                    return;
                }
                let Ok(core) = view.controller().CoreWebView2() else {
                    return;
                };
                if let Ok(settings) = core
                    .Settings()
                    .and_then(|s| s.cast::<ICoreWebView2Settings3>())
                {
                    let _ = settings.SetAreBrowserAcceleratorKeysEnabled(false);
                }
                if config.auth == omo_ui_core::Auth::Basic {
                    if let Ok(core) = core.cast::<ICoreWebView2_10>() {
                        let mut token = 0;
                        let _ = core.add_BasicAuthenticationRequested(
                            &BasicAuthenticationRequestedEventHandler::create(Box::new(
                                move |_, args| {
                                    if let Some(args) = args {
                                        let mut uri = windows_core::PWSTR::null();
                                        args.Uri(&mut uri)?;
                                        let uri = webview2_com::take_pwstr(uri);
                                        let allowed = tauri::Url::parse(&uri).is_ok_and(|u| {
                                            u.origin().ascii_serialization()
                                                == config.origin.trim_end_matches('/')
                                        });
                                        if allowed {
                                            let response = args.Response()?;
                                            response
                                                .SetUserName(&HSTRING::from(&config.username))?;
                                            response.SetPassword(&HSTRING::from(&secret))?;
                                        } else {
                                            args.SetCancel(true)?;
                                        }
                                    }
                                    Ok(())
                                },
                            )),
                            &mut token,
                        );
                    }
                }
            })
            .map_err(|_| "Cannot configure WebView2 keyboard/authentication settings.")?;
    }
    #[cfg(target_os = "macos")]
    crate::macos_auth::configure(window, config, secret)?;
    #[cfg(target_os = "android")]
    {
        let _ = window;
        crate::android_auth::configure(config, secret);
    }
    Ok(())
}

/// Credentials are bootstrapped only after reaching the configured document.
pub fn board_url(config: &Config, _secret: &str) -> Result<tauri::Url> {
    config
        .origin
        .parse()
        .map_err(|_| "Invalid board origin.".into())
}
