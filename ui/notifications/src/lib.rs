#[cfg(target_os = "android")]
use tauri::{Manager, plugin::PluginHandle};

pub fn init<R: tauri::Runtime>() -> tauri::plugin::TauriPlugin<R> {
    tauri::plugin::Builder::new("omo-notifications")
        .setup(|_app, _api| {
            #[cfg(target_os = "android")]
            _app.manage(AndroidNotifications(_api.register_android_plugin(
                "dev.scolastico.omo.notifications",
                "PollingPlugin",
            )?));
            Ok(())
        })
        .build()
}

#[cfg(target_os = "android")]
pub struct AndroidNotifications<R: tauri::Runtime>(PluginHandle<R>);
#[cfg(target_os = "android")]
impl<R: tauri::Runtime> AndroidNotifications<R> {
    pub fn call<T: serde::de::DeserializeOwned>(
        &self,
        command: &str,
        args: impl serde::Serialize,
    ) -> Result<T, String> {
        self.0.run_mobile_plugin(command, args).map_err(|_| "Android notification operation failed. Check notification permissions in Android Settings.".into())
    }
    pub fn on_activation(
        &self,
        handler: impl Fn(String, String) + Send + Sync + 'static,
    ) -> Result<(), String> {
        use tauri::ipc::{Channel, InvokeResponseBody};
        let channel: Channel = Channel::new(move |body| {
            if let InvokeResponseBody::Json(json) = body {
                if let Ok(v) = serde_json::from_str::<serde_json::Value>(&json) {
                    if let (Some(id), Some(path)) =
                        (v["connection"].as_str(), v["destination"].as_str())
                    {
                        handler(id.into(), path.into());
                    }
                }
            }
            Ok(())
        });
        self.call("registerActivation", serde_json::json!({"handler":channel}))
    }
}
