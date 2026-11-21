//! Opt-in interoperability tests against the production Go relay and host.
//! Run `node ui/scripts/relay-probe.mjs` from the repository root.
use omo_ui_core::{
    Result,
    relay::{RelayClient, RelaySession, SecretStore, TrustState, wire::Route},
};
use serde_json::{Value, json};
use std::{
    fs,
    io::{BufRead, BufReader, Write},
    path::PathBuf,
    process::{Child, ChildStdin, ChildStdout, Command, Stdio},
    sync::Arc,
};

// Atomic disk storage makes client reconstruction exercise durable bytes, rather
// than sharing a live in-memory state object with the original client.
struct FileSecrets(PathBuf);
impl SecretStore for FileSecrets {
    fn load(&self, key: &str) -> Result<Option<Vec<u8>>> {
        assert_eq!(key, "connection");
        match fs::read(&self.0) {
            Ok(bytes) => Ok(Some(bytes)),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(None),
            Err(error) => Err(error.to_string()),
        }
    }
    fn save(&self, key: &str, value: &[u8]) -> Result<()> {
        assert_eq!(key, "connection");
        let mut staged = tempfile::NamedTempFile::new_in(self.0.parent().unwrap())
            .map_err(|error| error.to_string())?;
        staged.write_all(value).map_err(|error| error.to_string())?;
        staged
            .as_file()
            .sync_all()
            .map_err(|error| error.to_string())?;
        staged.persist(&self.0).map_err(|error| error.to_string())?;
        #[cfg(unix)]
        fs::File::open(self.0.parent().unwrap())
            .and_then(|directory| directory.sync_all())
            .map_err(|error| error.to_string())?;
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
        let executable = std::env::var_os("OMO_UI_RELAY_FIXTURE")
            .expect("run node ui/scripts/relay-probe.mjs to build the Go fixture");
        let mut child = Command::new(executable)
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::inherit())
            .spawn()
            .expect("start production relay/host fixture");
        let input = child.stdin.take();
        let output = BufReader::new(child.stdout.take().unwrap());
        let mut fixture = Self {
            child,
            input,
            output,
            info: Value::Null,
        };
        fixture.info = fixture.response();
        fixture
    }
    fn response(&mut self) -> Value {
        let mut line = String::new();
        assert_ne!(
            self.output.read_line(&mut line).unwrap(),
            0,
            "fixture exited"
        );
        let response: Value = serde_json::from_str(&line).unwrap();
        assert_eq!(response["ok"], true, "fixture response: {response}");
        response["data"].clone()
    }
    fn command(&mut self, command: Value) -> Value {
        let input = self.input.as_mut().unwrap();
        serde_json::to_writer(&mut *input, &command).unwrap();
        input.write_all(b"\n").unwrap();
        input.flush().unwrap();
        self.response()
    }
    fn route(&self) -> Route {
        Route {
            origin: self.info["origin"].as_str().unwrap().into(),
            host_id: self.info["host_id"].as_str().unwrap().into(),
        }
    }
    fn client(&self, path: PathBuf, admission: bool) -> RelayClient {
        RelayClient::open(
            Arc::new(FileSecrets(path)),
            "connection",
            self.route(),
            admission.then(|| self.info["admission"].as_str().unwrap().into()),
        )
        .unwrap()
    }
    fn restart(&mut self) {
        let before = self.info.clone();
        self.info = self.command(json!({"op":"restart"}));
        assert_eq!(self.info["origin"], before["origin"]);
        assert_eq!(self.info["host_id"], before["host_id"]);
        assert_eq!(self.info["host_fp"], before["host_fp"]);
    }
}
impl Drop for Fixture {
    fn drop(&mut self) {
        // EOF asks the fixture to join manager callbacks, upgraded sockets and
        // HTTP listeners, then delete its private temporary host directory.
        drop(self.input.take());
        let status = self.child.wait().expect("join relay/host fixture");
        if !std::thread::panicking() {
            assert!(status.success(), "fixture failed: {status}");
        }
    }
}

async fn ordered_echo(session: RelaySession) {
    let control = session.control();
    let (sender, mut receiver) = session.split();
    let mut expected = vec![json!({"v":1,"type":"stream.open","id":"1"})];
    for i in 0..64_u32 {
        use base64::Engine;
        expected.push(json!({
            "v":1, "type":"stream.data", "id":"1",
            "data":base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(i.to_be_bytes())
        }));
    }
    expected.push(json!({"v":1,"type":"stream.fin","id":"1"}));
    // Host and router endpoint queues are intentionally bounded. Eight credits
    // permit pipelining; matching host echoes, rather than scheduler timing,
    // return credits and prove when another send can be admitted safely.
    let credits = tokio::sync::Semaphore::new(8);
    let send = async {
        for (index, record) in expected.iter().enumerate() {
            credits.acquire().await.unwrap().forget();
            sender
                .send(record.clone())
                .await
                .unwrap_or_else(|error| panic!("send record {index}: {error}"));
        }
    };
    let receive = async {
        for (index, record) in expected.iter().enumerate() {
            assert_eq!(
                receiver
                    .receive()
                    .await
                    .unwrap_or_else(|error| panic!("receive record {index}: {error}")),
                *record
            );
            credits.add_permits(1);
        }
    };
    tokio::join!(send, receive);
    control.close().await;
}

#[tokio::test]
async fn pin_acceptance_and_ordered_records_survive_both_process_state_reconstructions() {
    let mut fixture = Fixture::start();
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("connection.secret");
    let client = fixture.client(path.clone(), true);
    let ui_id = client.identity_id().unwrap();
    fixture.command(json!({"op":"begin"}));
    let pairing = client.begin_pair().await.unwrap();
    let pending = fixture.command(json!({"op":"pending"}));
    assert_eq!(pending["aid"], pairing.info().aid);
    assert_eq!(pending["ui_id"], ui_id);
    fixture.command(json!({"op":"accept", "aid":pairing.info().aid, "pin":pairing.info().pin}));
    ordered_echo(pairing.accepted().await.unwrap()).await;
    assert_eq!(client.status().unwrap().state, TrustState::Trusted);
    drop(client);

    fixture.restart();
    assert_eq!(fixture.info["devices"], 1);
    // No admission argument and no new human ceremony: both credentials and
    // exact peer trust must come from their respective private stores.
    let client = fixture.client(path, false);
    assert_eq!(client.identity_id().unwrap(), ui_id);
    ordered_echo(client.reconnect().await.unwrap()).await;
    assert_eq!(client.status().unwrap().state, TrustState::Trusted);
    assert!(client.begin_pair().await.is_err());
}

#[tokio::test]
async fn full_key_acceptance_with_lost_reply_recovers_candidate_after_restart() {
    let mut fixture = Fixture::start();
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("connection.secret");
    let client = fixture.client(path.clone(), true);
    let ui_id = client.identity_id().unwrap();
    fixture.command(json!({"op":"begin"}));
    let pairing = client.begin_pair().await.unwrap();
    fixture.command(json!({"op":"pending"}));
    fixture.command(json!({"op":"accept-drop", "aid":pairing.info().aid, "public_key":pairing.info().public_key}));
    assert!(pairing.accepted().await.is_err());
    assert_eq!(client.status().unwrap().state, TrustState::Candidate);
    drop(client);
    fixture.restart();
    assert_eq!(fixture.info["devices"], 1);
    let client = fixture.client(path, false);
    assert_eq!(client.identity_id().unwrap(), ui_id);
    ordered_echo(client.reconnect().await.unwrap()).await;
    assert_eq!(client.status().unwrap().state, TrustState::Trusted);
}

#[tokio::test]
async fn proved_but_unaccepted_candidate_cannot_reconnect_or_authorize_a_new_ceremony() {
    let mut fixture = Fixture::start();
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("connection.secret");
    let client = fixture.client(path.clone(), true);
    fixture.command(json!({"op":"begin"}));
    let pairing = client.begin_pair().await.unwrap();
    fixture.command(json!({"op":"pending"}));
    fixture.command(json!({"op":"cancel"}));
    assert!(pairing.accepted().await.is_err());
    assert_eq!(client.status().unwrap().state, TrustState::Candidate);
    drop(client);
    fixture.restart();
    assert_eq!(fixture.info["devices"], 0);
    let client = fixture.client(path, false);
    assert!(client.reconnect().await.is_err());
    assert_eq!(client.status().unwrap().state, TrustState::Candidate);
    assert!(client.begin_pair().await.is_err());
    assert_eq!(fixture.command(json!({"op":"info"}))["devices"], 0);
}

#[tokio::test]
async fn relay_admission_requires_saved_password_without_granting_device_authority() {
    let mut fixture = Fixture::start();
    let dir = tempfile::tempdir().unwrap();
    let client = fixture.client(dir.path().join("connection.secret"), false);
    fixture.command(json!({"op":"begin"}));
    assert!(client.begin_pair().await.is_err());
    assert_eq!(fixture.command(json!({"op":"info"}))["devices"], 0);
}

#[tokio::test]
async fn cloned_senders_share_session_counter_and_preserve_each_producers_order() {
    let mut fixture = Fixture::start();
    let dir = tempfile::tempdir().unwrap();
    let client = fixture.client(dir.path().join("connection.secret"), true);
    fixture.command(json!({"op":"begin"}));
    let pairing = client.begin_pair().await.unwrap();
    fixture.command(json!({"op":"pending"}));
    fixture.command(json!({"op":"accept", "aid":pairing.info().aid, "pin":pairing.info().pin}));
    let session = pairing.accepted().await.unwrap();
    let control = session.control();
    let (sender, mut receiver) = session.split();
    let other = sender.clone();
    let credits = tokio::sync::Semaphore::new(8);
    // The streams have independent producer order, while all cloned senders
    // must serialize through one authenticated record counter.
    let produce = |sender: omo_ui_core::relay::RecordSender, producer: u64| {
        let credits = &credits;
        async move {
            for index in 0..32_u64 {
                credits.acquire().await.unwrap().forget();
                let id = (index * 4 + producer * 2 + 1).to_string();
                sender
                    .send(json!({"v":1,"type":"stream.open","id":id}))
                    .await
                    .unwrap();
            }
        }
    };
    let receive = async {
        let mut next = [0_u64; 2];
        for _ in 0..64 {
            let record = receiver.receive().await.unwrap();
            let id: u64 = record["id"].as_str().unwrap().parse().unwrap();
            let producer = ((id - 1) % 4 / 2) as usize;
            assert_eq!(
                record,
                json!({"v":1,"type":"stream.open","id":(next[producer]*4+producer as u64*2+1).to_string()})
            );
            next[producer] += 1;
            credits.add_permits(1);
        }
        assert_eq!(next, [32, 32]);
    };
    tokio::join!(produce(sender, 0), produce(other, 1), receive);
    control.close().await;
}

#[tokio::test]
async fn closed_session_handles_cannot_block_or_cancel_a_fresh_reconnect() {
    let mut fixture = Fixture::start();
    let dir = tempfile::tempdir().unwrap();
    let client = fixture.client(dir.path().join("connection.secret"), true);
    fixture.command(json!({"op":"begin"}));
    let pairing = client.begin_pair().await.unwrap();
    fixture.command(json!({"op":"pending"}));
    fixture.command(json!({"op":"accept", "aid":pairing.info().aid, "pin":pairing.info().pin}));
    let old_session = pairing.accepted().await.unwrap();
    let old_control = old_session.control();
    let (old_sender, mut old_receiver) = old_session.split();
    old_control.close().await;
    let new_session = client.reconnect().await.unwrap();
    assert!(
        old_sender
            .send(json!({"v":1,"type":"stream.open","id":"1"}))
            .await
            .is_err()
    );
    assert!(old_receiver.receive().await.is_err());
    drop(old_sender);
    drop(old_receiver);
    let (sender, mut receiver) = new_session.split();
    let control = sender.control();
    let record = json!({"v":1,"type":"stream.open","id":"1"});
    sender.send(record.clone()).await.unwrap();
    assert_eq!(receiver.receive().await.unwrap(), record);
    control.close().await;
}
