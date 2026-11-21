//! Real production host/relay with no board bridge, UI WebSocket or WebView while polling.
use omo_ui_core::{
    relay::{RelayClient, SecretStore, wire::Route},
    *,
};
use serde_json::{Value, json};
use std::{
    io::{BufRead, BufReader, Write},
    path::PathBuf,
    process::{Child, ChildStdin, ChildStdout, Command, Stdio},
    sync::Arc,
};
struct FileSecrets(PathBuf);
impl SecretStore for FileSecrets {
    fn load(&self, _: &str) -> Result<Option<Vec<u8>>> {
        match std::fs::read(&self.0) {
            Ok(v) => Ok(Some(v)),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(None),
            Err(_) => Err("storage".into()),
        }
    }
    fn save(&self, _: &str, v: &[u8]) -> Result<()> {
        let mut staged =
            tempfile::NamedTempFile::new_in(self.0.parent().unwrap()).map_err(|_| "storage")?;
        staged
            .write_all(v)
            .and_then(|_| staged.as_file().sync_all())
            .map_err(|_| "storage")?;
        staged.persist(&self.0).map_err(|_| "storage")?;
        #[cfg(unix)]
        std::fs::File::open(self.0.parent().unwrap())
            .and_then(|f| f.sync_all())
            .map_err(|_| "storage")?;
        Ok(())
    }
}
struct Fixture {
    child: Child,
    input: Option<ChildStdin>,
    output: BufReader<ChildStdout>,
    info: Value,
}
impl Fixture {
    fn start() -> Self {
        let mut child = Command::new(
            std::env::var_os("OMO_UI_RELAY_FIXTURE")
                .expect("run node ui/scripts/relay-probe.mjs --notifications"),
        )
        .args([
            "-test.run=^TestNativeRelayBoardFixture$",
            "-native-relay-fixture",
            "-native-relay-notifications-fixture",
        ])
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::inherit())
        .spawn()
        .unwrap();
        let input = child.stdin.take();
        let output = BufReader::new(child.stdout.take().unwrap());
        let mut f = Self {
            child,
            input,
            output,
            info: Value::Null,
        };
        f.info = f.response();
        f
    }
    fn response(&mut self) -> Value {
        let mut line = String::new();
        assert_ne!(
            self.output.read_line(&mut line).unwrap(),
            0,
            "fixture exited"
        );
        let reply: Value =
            serde_json::from_str(&line).unwrap_or_else(|_| panic!("fixture: {line}"));
        assert_eq!(reply["ok"], true, "fixture failure: {reply}");
        reply["data"].clone()
    }
    fn command(&mut self, value: Value) -> Value {
        let input = self.input.as_mut().unwrap();
        serde_json::to_writer(&mut *input, &value).unwrap();
        input.write_all(b"\n").unwrap();
        input.flush().unwrap();
        self.response()
    }
}
impl Drop for Fixture {
    fn drop(&mut self) {
        drop(self.input.take());
        let status = self.child.wait().unwrap();
        if !std::thread::panicking() {
            assert!(status.success());
        }
    }
}
#[test]
fn relay_notifications_deliver_real_mentions_and_recover_durable_receipts_without_ui_socket() {
    let mut f = Fixture::start();
    let directory = tempfile::tempdir().unwrap();
    let route = Route::new(
        f.info["origin"].as_str().unwrap(),
        f.info["host_id"].as_str().unwrap(),
        true,
    )
    .unwrap();
    let private = Arc::new(FileSecrets(directory.path().join("identity")));
    let client = RelayClient::open(
        private.clone(),
        "connection",
        route.clone(),
        Some(f.info["admission"].as_str().unwrap().into()),
    )
    .unwrap();
    let id = client.identity_id().unwrap();
    let rt = tokio::runtime::Runtime::new().unwrap();
    f.command(json!({"op":"begin"}));
    rt.block_on(async {
        let pair = client.begin_pair().await.unwrap();
        f.command(json!({"op":"pending"}));
        f.command(json!({"op":"accept","aid":pair.info().aid,"pin":pair.info().pin}));
        let session = pair.accepted().await.unwrap();
        session.control().close().await;
    });
    let secrets = client.notification_secrets().unwrap();
    drop(client);
    let config = Config {
        id: Config::new_id(),
        mode: Mode::Relay,
        origin: route.origin.clone(),
        host_id: route.host_id.clone(),
        relay_allow_loopback_http: true,
        auth: Auth::None,
        username: String::new(),
        notifications: true,
        notification_mode: NotificationMode::Background,
        remember: true,
        stop_with_window: false,
    };
    assert_eq!(f.command(json!({"op":"connections","ui_id":id})), 0);
    let name = format!("{}-relay-notifications.json", config.id);
    let journal = || {
        read_json::<relay::RelayDelivery>(directory.path(), &name)
            .unwrap()
            .unwrap()
    };
    let mut delivered = Vec::new();
    let mut poll = || {
        poll_relay_saved(directory.path(), &config, secrets.clone(), |n| {
            delivered.push(n.clone());
            Ok(())
        })
    };
    assert!(matches!(poll().unwrap(), Poll::Idle)); // authenticated online empty
    let comments = f.command(json!({"op":"mention","count":55}));
    assert!(matches!(poll().unwrap(), Poll::More));
    let first = journal();
    assert_eq!(first.pending.len(), 50);
    let receipt = first.receipt.clone().unwrap();
    // Host applies the ack, but its reply is lost. No transport success can retire it.
    f.command(json!({"op":"notification-reply","mode":"drop"}));
    assert!(poll().is_err());
    assert_eq!(journal().receipt, Some(receipt.clone()));
    f.command(json!({"op":"stop-host"}));
    assert!(poll().is_err());
    assert_eq!(journal().receipt, Some(receipt.clone()));
    f.command(json!({"op":"start-host"}));
    f.command(json!({"op":"stop-relay"}));
    f.command(json!({"op":"start-relay"}));
    let restored = RelayClient::restore(private, "connection", route).unwrap();
    assert_eq!(restored.identity_id().unwrap(), id);
    assert!(
        serde_json::to_value(restored.notification_secrets().unwrap()).unwrap()
            == serde_json::to_value(&secrets).unwrap(),
        "restored notification context/root changed"
    );
    // Every injected reply is a real host envelope, modified only after host commit.
    for mode in [
        "tamper",
        "wrong-request",
        "wrong-host",
        "wrong-device",
        "wrong-epoch",
        "error",
    ] {
        f.command(json!({"op":"notification-reply","mode":mode}));
        assert!(poll().is_err(), "{mode}");
        assert_eq!(journal().receipt, Some(receipt.clone()));
    }
    assert!(matches!(poll().unwrap(), Poll::More));
    assert_eq!(journal().pending.len(), 5);
    assert!(matches!(poll().unwrap(), Poll::Idle));
    assert!(journal().receipt.is_none());
    // Restore an older receipt after last_ack has moved on: authenticated invalid_ack.
    let mut stale = journal();
    stale.receipt = Some(receipt);
    write_json(directory.path(), &name, &stale).unwrap();
    assert!(matches!(poll().unwrap(), Poll::More));
    assert!(journal().receipt.is_none());
    assert_eq!(journal().received.len(), 55);
    assert!(matches!(poll().unwrap(), Poll::Idle));
    drop(poll);
    assert_eq!(delivered.len(), 55);
    let unique: std::collections::BTreeSet<_> = delivered.iter().map(|n| &n.id).collect();
    assert_eq!(unique.len(), 55);
    assert_eq!(
        delivered[0].id,
        format!("comment:{}", comments[0].as_str().unwrap())
    );
    assert_eq!(
        delivered[0].destination,
        format!(
            "/{}?tab=comments&comment={}",
            f.info["ticket"],
            comments[0].as_str().unwrap()
        )
    );
    let jni_comment = f.command(json!({"op":"mention","count":1}));
    let mut java =
        Command::new(std::env::var_os("OMO_UI_JNI_JAVA").expect("JNI probe Java executable"))
            .args(["-cp"])
            .arg(std::env::var_os("OMO_UI_JNI_CLASSES").unwrap())
            .arg("dev.scolastico.omo.notifications.NativeFeed")
            .arg(std::env::var_os("OMO_UI_JNI_LIBRARY").unwrap())
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::inherit())
            .spawn()
            .unwrap();
    {
        let mut input = java.stdin.take().unwrap();
        writeln!(input, "{}", serde_json::to_string(&config).unwrap()).unwrap();
        writeln!(input, "{}", directory.path().display()).unwrap();
        writeln!(input, "{}", serde_json::to_string(&secrets).unwrap()).unwrap();
        writeln!(
            input,
            "/{}?tab=comments&comment={}",
            f.info["ticket"],
            jni_comment[0].as_str().unwrap()
        )
        .unwrap();
    }
    let output = java.wait_with_output().unwrap();
    assert!(output.status.success(), "headless JNI probe failed");
    assert!(
        String::from_utf8(output.stdout)
            .unwrap()
            .contains("checks passed")
    );
    // Two desktop processes must share one logical outstanding poll. Hold the
    // first real host response, observe its OS lock, then release both workers.
    f.command(json!({"op":"mention","count":55}));
    write_json(directory.path(), "worker-config.json", &config).unwrap();
    write_json(directory.path(), "worker-secrets.json", &secrets).unwrap();
    f.command(json!({"op":"notification-reply","mode":"hold"}));
    let launch = || {
        Command::new(std::env::current_exe().unwrap())
            .args(["--exact", "saved_notification_worker", "--nocapture"])
            .env("RELAY_NOTIFICATION_WORKER_ROOT", directory.path())
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::inherit())
            .spawn()
            .unwrap()
    };
    let mut first_worker = launch();
    f.command(json!({"op":"wait-notification-held"}));
    let os_lock_held = std::fs::OpenOptions::new()
        .read(true)
        .write(true)
        .open(
            directory
                .path()
                .join(format!("{}-relay-notifications.lock", config.id)),
        )
        .map(|file| file.try_lock().is_err())
        .unwrap_or(false);
    let mut second_worker = launch();
    f.command(json!({"op":"release-notification"}));
    let first_status = first_worker.wait().unwrap();
    let second_status = second_worker.wait().unwrap();
    assert!(first_status.success() && second_status.success());
    assert!(
        os_lock_held,
        "a separate desktop process could own the notification journal during a poll"
    );
    assert_eq!(
        journal().pending.len(),
        5,
        "second process did not load the first committed receipt"
    );
    assert_eq!(journal().received.len(), 110);
    assert!(matches!(
        poll_relay_saved(directory.path(), &config, secrets.clone(), |_| Ok(())).unwrap(),
        Poll::Idle
    ));
    // A new connection journal cannot consume or replay another connection's saved deliveries.
    let other = Config {
        id: Config::new_id(),
        ..config
    };
    assert!(matches!(
        poll_relay_saved(directory.path(), &other, secrets, |_| panic!(
            "already acknowledged"
        ))
        .unwrap(),
        Poll::Idle
    ));
}

#[test]
fn saved_notification_worker() {
    let Some(root) = std::env::var_os("RELAY_NOTIFICATION_WORKER_ROOT") else {
        return;
    };
    let root = PathBuf::from(root);
    let config = read_json(&root, "worker-config.json").unwrap().unwrap();
    let secrets = read_json(&root, "worker-secrets.json").unwrap().unwrap();
    assert!(matches!(
        poll_relay_saved(&root, &config, secrets, |_| Ok(())).unwrap(),
        Poll::More
    ));
}
