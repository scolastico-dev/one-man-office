//! Native callback for Wry's HTTP auth challenge, injected via ui/.cargo/config.toml.
use jni::{
    JNIEnv,
    objects::{JObject, JString},
    sys::jobjectArray,
};
use omo_ui_core::Config;
use std::sync::Mutex;
static AUTH: Mutex<Option<(Config, String, String)>> = Mutex::new(None);
pub fn configure(config: &Config, secret: &str) {
    *AUTH.lock().unwrap_or_else(|e| e.into_inner()) =
        Some((config.clone(), secret.to_string(), Config::new_id()));
}
#[unsafe(no_mangle)]
pub extern "system" fn Java_dev_scolastico_omo_ui_RustWebViewClient_omoUiCredentials(
    mut env: JNIEnv,
    _this: JObject,
    url: JString,
    host: JString,
    realm: JString,
) -> jobjectArray {
    let result = (|| -> jni::errors::Result<jobjectArray> {
        let url: String = env.get_string(&url)?.into();
        let host: String = env.get_string(&host)?.into();
        let realm: String = env.get_string(&realm)?.into();
        let auth = AUTH.lock().unwrap_or_else(|e| e.into_inner());
        let Some((config, secret, generation)) = auth
            .as_ref()
            .filter(|(c, _, _)| c.challenge_allowed(&url, &host, &realm))
        else {
            return Ok(std::ptr::null_mut());
        };
        let array = env.new_object_array(3, "java/lang/String", JObject::null())?;
        for (i, value) in [generation, &config.username, secret].iter().enumerate() {
            let s = env.new_string(value)?;
            env.set_object_array_element(&array, i as i32, s)?;
        }
        Ok(array.into_raw())
    })();
    result.unwrap_or(std::ptr::null_mut())
}
