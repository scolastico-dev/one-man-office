//! The worker may load this library in a fresh process without starting Tauri.
//! No AppHandle, activity, keyring or WebView is needed by these feed entry points.
use jni::{
    JNIEnv,
    objects::{JObject, JString, JValue},
    sys::{jboolean, jstring},
};
use omo_ui_core::{Config, Poll, Recovery, Result, notification_text, poll_saved, recover_saved};
use std::path::Path;

fn response(env: &JNIEnv<'_>, value: Result<serde_json::Value>) -> jstring {
    let value = value.unwrap_or_else(|error| serde_json::json!({"error":error}));
    env.new_string(value.to_string())
        .map(|s| s.into_raw())
        .unwrap_or(std::ptr::null_mut())
}
#[unsafe(no_mangle)]
pub extern "system" fn Java_dev_scolastico_omo_notifications_NativeFeed_pollFeed(
    mut env: JNIEnv,
    _this: JObject,
    config: JString,
    root: JString,
    secret: JString,
    sink: JObject,
    baseline: jboolean,
) -> jstring {
    let result = (|| -> Result<serde_json::Value> {
        let json: String = env
            .get_string(&config)
            .map_err(|_| "Invalid worker configuration.")?
            .into();
        let root: String = env
            .get_string(&root)
            .map_err(|_| "Invalid worker directory.")?
            .into();
        let secret: String = env
            .get_string(&secret)
            .map_err(|_| "Credentials unavailable.")?
            .into();
        let mut config: Config =
            serde_json::from_str(&json).map_err(|_| "Invalid worker configuration.")?;
        config.validate(true)?;
        if baseline != 0 {
            let delivery: omo_ui_core::Delivery =
                omo_ui_core::read_json(Path::new(&root), &format!("{}.json", config.id))?
                    .unwrap_or_default();
            if delivery.cursor.is_some() {
                return Ok(serde_json::json!({"more":false}));
            }
        }
        let poll = poll_saved(Path::new(&root), &config, &secret, |notice| {
            let mut notice = notice.clone();
            notice.title = notification_text(&notice.title, 80, false);
            notice.body = notification_text(&notice.body, 160, false);
            let payload = env
                .new_string(serde_json::to_string(&notice).map_err(|_| "Invalid notification.")?)
                .map_err(|_| "Notification unavailable.")?;
            let delivered = env
                .call_method(
                    &sink,
                    "deliver",
                    "(Ljava/lang/String;)Z",
                    &[JValue::Object(payload.as_ref())],
                )
                .and_then(|v| v.z())
                .unwrap_or(false);
            if env.exception_check().unwrap_or(false) {
                let _ = env.exception_clear();
            }
            if delivered {
                Ok(())
            } else {
                Err("Android notification delivery is blocked. Enable notifications and the Board notifications channel in Android Settings.".into())
            }
        })?;
        Ok(match poll {
            Poll::More => serde_json::json!({"more":true}),
            Poll::Idle => serde_json::json!({"more":false}),
            Poll::Recovery(recovery) => {
                serde_json::json!({"recovery":recovery,"error":"Notification history changed. Open Connection to choose recovery."})
            }
        })
    })();
    response(&env, result)
}
#[unsafe(no_mangle)]
pub extern "system" fn Java_dev_scolastico_omo_notifications_NativeFeed_recoverFeed(
    mut env: JNIEnv,
    _this: JObject,
    config: JString,
    root: JString,
    recovery: JString,
    replay: jboolean,
) -> jstring {
    let result = (|| -> Result<serde_json::Value> {
        let json: String = env
            .get_string(&config)
            .map_err(|_| "Invalid worker configuration.")?
            .into();
        let root: String = env
            .get_string(&root)
            .map_err(|_| "Invalid worker directory.")?
            .into();
        let recovery: String = env
            .get_string(&recovery)
            .map_err(|_| "Invalid recovery.")?
            .into();
        let mut config: Config =
            serde_json::from_str(&json).map_err(|_| "Invalid worker configuration.")?;
        config.validate(true)?;
        let recovery: Recovery =
            serde_json::from_str(&recovery).map_err(|_| "Invalid recovery.")?;
        recover_saved(Path::new(&root), &config, &recovery, replay != 0)?;
        Ok(serde_json::json!({}))
    })();
    response(&env, result)
}
