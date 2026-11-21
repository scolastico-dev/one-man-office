//! Endpoint cryptography for relay protocol v1. Trust decisions belong to the
//! consumer; a fingerprint or a verified transcript alone grants no authority.
use super::wire::{self, Route, b64, unb64};
use crate::Result;
use aes_gcm::{
    Aes256Gcm, Nonce,
    aead::{Aead, KeyInit, Payload},
};
use hkdf::Hkdf;
use hmac::{Hmac, Mac};
use ml_kem::{
    Decapsulate, Encapsulate, KeyExport,
    ml_kem_768::{DecapsulationKey, EncapsulationKey},
};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sha2::{Digest, Sha384};
use subtle::ConstantTimeEq;
use zeroize::{Zeroize, Zeroizing};

const LABEL: &str = "omo-relay-v1/";
const SUITE: &[u8] = b"MLKEM768-HKDF384-AES256GCM";
const VERSION: &[u8] = &[0, 1];
const MAX_RECORDS: u64 = 1 << 24;
const MAX_RECORD_BYTES: u64 = 1 << 36;
fn protocol() -> String {
    "relay protocol violation".into()
}
fn authentication() -> String {
    "relay authentication failed".into()
}
fn label(s: &str) -> Vec<u8> {
    format!("{LABEL}{s}").into_bytes()
}
fn fields(parts: &[&[u8]]) -> Vec<u8> {
    let mut out = Vec::new();
    for p in parts {
        out.extend_from_slice(
            &u32::try_from(p.len())
                .expect("locally bounded relay field")
                .to_be_bytes(),
        );
        out.extend_from_slice(p);
    }
    out
}
fn hash(b: &[u8]) -> Vec<u8> {
    Sha384::digest(b).to_vec()
}
fn hash_fields(parts: &[&[u8]]) -> Vec<u8> {
    hash(&fields(parts))
}
fn mac(key: &[u8], b: &[u8]) -> Vec<u8> {
    let mut m = <Hmac<Sha384> as Mac>::new_from_slice(key).expect("HMAC accepts any key length");
    m.update(b);
    m.finalize().into_bytes().to_vec()
}
fn expand(prk: &[u8], info: &[u8], len: usize) -> Zeroizing<Vec<u8>> {
    let mut out = Zeroizing::new(vec![0; len]);
    Hkdf::<Sha384>::from_prk(prk)
        .expect("SHA384 PRK")
        .expand(info, &mut out)
        .expect("bounded HKDF output");
    out
}
fn kdf(secret: &[u8], salt: &[u8], name: &str) -> Zeroizing<Vec<u8>> {
    let (mut extracted, _) = Hkdf::<Sha384>::extract(Some(salt), secret);
    let prk = Zeroizing::new(extracted.to_vec());
    extracted.zeroize();
    expand(&prk, &fields(&[&label(name)]), 32)
}
fn fixed(value: &Value, name: &str, size: usize) -> Result<Vec<u8>> {
    let b = unb64(value[name].as_str().ok_or_else(protocol)?)?;
    if b.len() != size {
        return Err(protocol());
    }
    Ok(b)
}
fn validate_inner(value: &Value) -> Result<()> {
    wire::encode_inner(value).map(|_| ())
}
fn valid_fp(s: &str) -> bool {
    s.len() == 96
        && s.bytes()
            .all(|x| x.is_ascii_digit() || (b'a'..=b'f').contains(&x))
}

/// Production entropy comes only from the operating system. No deterministic
/// random source is exposed to connection or wire callers.
pub fn random_bytes(n: usize) -> Result<Vec<u8>> {
    let mut out = vec![0; n];
    getrandom::fill(&mut out).map_err(|_| "operating system randomness unavailable".to_string())?;
    Ok(out)
}

pub struct Identity {
    key: DecapsulationKey,
}
impl Identity {
    pub fn generate() -> Result<Self> {
        let seed = Zeroizing::new(random_bytes(64)?);
        Self::import(&seed)
    }
    pub fn import(seed: &[u8]) -> Result<Self> {
        let seed = ml_kem::Seed::try_from(seed).map_err(|_| protocol())?;
        Ok(Self {
            key: DecapsulationKey::from_seed(seed),
        })
    }
    pub fn seed(&self) -> Vec<u8> {
        let mut seed = self.key.to_seed().expect("seed-generated identity");
        let bytes = seed.to_vec();
        seed.zeroize();
        bytes
    }
    pub fn public_key(&self) -> Vec<u8> {
        self.key.encapsulation_key().to_bytes().to_vec()
    }
}
fn public_key(pk: &[u8]) -> Result<EncapsulationKey> {
    let bytes = ml_kem::Key::<EncapsulationKey>::try_from(pk).map_err(|_| protocol())?;
    EncapsulationKey::new(&bytes).map_err(|_| protocol())
}
pub fn validate_public_key(pk: &[u8]) -> Result<()> {
    public_key(pk).map(|_| ())
}
pub fn fingerprint(role: &str, pk: &[u8]) -> Result<String> {
    if role != "host" && role != "ui" {
        return Err(protocol());
    }
    validate_public_key(pk)?;
    Ok(hash_fields(&[&label(&format!("{role}-key")), pk])
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect())
}

#[derive(Clone, Serialize, Deserialize)]
pub struct PairTranscript {
    pub route: Route,
    pub aid: Vec<u8>,
    pub window: Vec<u8>,
    pub pair_epoch: Vec<u8>,
    pub c: Vec<u8>,
    pub pku: Vec<u8>,
    pub ru: Vec<u8>,
    pub pkh: Vec<u8>,
    pub rh: Vec<u8>,
}
pub fn commitment(route: &Route, aid: &[u8], pku: &[u8], ru: &[u8]) -> Result<Vec<u8>> {
    route.validate()?;
    if aid.len() != 16 || ru.len() != 32 {
        return Err(protocol());
    }
    validate_public_key(pku)?;
    Ok(hash_fields(&[
        &label("pair-commit"),
        VERSION,
        SUITE,
        route.origin.as_bytes(),
        route.host_id.as_bytes(),
        aid,
        pku,
        ru,
    ]))
}
impl PairTranscript {
    pub fn encode(&self) -> Result<Vec<u8>> {
        let c = commitment(&self.route, &self.aid, &self.pku, &self.ru)?;
        if self.window.len() != 16
            || self.pair_epoch.len() != 16
            || self.rh.len() != 32
            || !bool::from(c.ct_eq(&self.c))
        {
            return Err(protocol());
        }
        validate_public_key(&self.pkh)?;
        Ok(fields(&[
            &label("pair-transcript"),
            VERSION,
            SUITE,
            self.route.origin.as_bytes(),
            self.route.host_id.as_bytes(),
            &self.aid,
            &self.window,
            &self.pair_epoch,
            &self.c,
            &self.pku,
            &self.ru,
            &self.pkh,
            &self.rh,
        ]))
    }
    pub fn hash(&self) -> Result<Vec<u8>> {
        Ok(hash(&self.encode()?))
    }
    /// Display only after a successful mutual possession proof.
    pub fn pin(&self) -> Result<String> {
        let p = self.encode()?;
        for i in 0..=u32::MAX {
            let h = hash_fields(&[&label("pin"), &p, &i.to_be_bytes()]);
            let n = u32::from_be_bytes(h[..4].try_into().expect("SHA384 length"));
            if n < 4_294_000_000 {
                return Ok(format!("{:06}", n % 1_000_000));
            }
        }
        Err(protocol())
    }
    pub fn auth_context(&self) -> Result<AuthContext> {
        Ok(AuthContext {
            route: self.route.clone(),
            mode: "pair".into(),
            pkh: self.pkh.clone(),
            pku: self.pku.clone(),
            pair_epoch: self.pair_epoch.clone(),
            pair_hash: self.hash()?,
        })
    }
}

#[derive(Clone, Serialize, Deserialize)]
pub struct AuthContext {
    pub route: Route,
    pub mode: String,
    pub pkh: Vec<u8>,
    pub pku: Vec<u8>,
    pub pair_epoch: Vec<u8>,
    pub pair_hash: Vec<u8>,
}
impl AuthContext {
    pub fn encode(&self) -> Result<Vec<u8>> {
        self.route.validate()?;
        if self.pair_epoch.len() != 16
            || !(self.mode == "pair" && self.pair_hash.len() == 48
                || self.mode == "reconnect" && self.pair_hash.is_empty())
        {
            return Err(protocol());
        }
        let hf = fingerprint("host", &self.pkh)?;
        let ui = fingerprint("ui", &self.pku)?;
        Ok(fields(&[
            &label("auth-context"),
            VERSION,
            SUITE,
            self.mode.as_bytes(),
            self.route.origin.as_bytes(),
            self.route.host_id.as_bytes(),
            hf.as_bytes(),
            ui.as_bytes(),
            &self.pkh,
            &self.pku,
            &self.pair_epoch,
            &self.pair_hash,
        ]))
    }
    fn prefix(&self) -> &'static str {
        if self.mode == "pair" {
            "pair.auth"
        } else {
            "auth"
        }
    }
}

/// A one-shot UI handshake. Consuming `finish` prevents contribution reuse.
/// Dropping a cancelled handshake releases and zeroes its ephemeral secrets.
pub struct UIHandshake {
    context: AuthContext,
    id: Identity,
    init: Value,
    ss_h: Zeroizing<Vec<u8>>,
}
impl UIHandshake {
    pub fn new(context: AuthContext, id: &Identity) -> Result<(Self, Value)> {
        context.encode()?;
        if id.public_key() != context.pku {
            return Err(authentication());
        }
        let nu = random_bytes(32)?;
        let (ct, mut ss) = public_key(&context.pkh)?.encapsulate();
        let ss_h = Zeroizing::new(ss.to_vec());
        ss.zeroize();
        let init = json!({"v":1,"type":format!("{}.init",context.prefix()),"host_fp":fingerprint("host",&context.pkh)?,"ui_id":fingerprint("ui",&context.pku)?,"pair_epoch":b64(&context.pair_epoch),"nu":b64(&nu),"ctH":b64(ct.as_slice())});
        validate_inner(&init)?;
        let private_seed = Zeroizing::new(id.seed());
        let handshake = Self {
            context,
            id: Identity::import(&private_seed)?,
            init: init.clone(),
            ss_h,
        };
        Ok((handshake, init))
    }
    pub fn finish(self, challenge: &Value) -> Result<(Value, Authenticated)> {
        validate_inner(challenge)?;
        if challenge["type"] != format!("{}.challenge", self.context.prefix()) {
            return Err(authentication());
        }
        let ct = fixed(challenge, "ctU", 1088)?;
        let mut ss = self
            .id
            .key
            .decapsulate_slice(&ct)
            .map_err(|_| authentication())?;
        let ss_u = Zeroizing::new(ss.to_vec());
        ss.zeroize();
        let authenticated = derive_auth(
            self.context.clone(),
            &self.init,
            challenge,
            &self.ss_h,
            &ss_u,
        )?;
        let expected = host_proof(&authenticated.prk, &authenticated.transcript);
        if !bool::from(expected.ct_eq(&fixed(challenge, "proofH", 48)?)) {
            return Err(authentication());
        }
        let proof = ui_proof(&authenticated.prk, &authenticated.transcript, &expected);
        Ok((
            json!({"v":1,"type":format!("{}.finish",self.context.prefix()),"proofU":b64(&proof)}),
            authenticated,
        ))
    }
}

pub struct Authenticated {
    context: AuthContext,
    transcript: Vec<u8>,
    prk: Zeroizing<Vec<u8>>,
}
impl Authenticated {
    pub fn session_id(&self) -> Vec<u8> {
        self.transcript.clone()
    }
    pub fn notification_root(&self) -> Result<Vec<u8>> {
        if self.context.mode != "pair" {
            return Err(protocol());
        }
        Ok(expand(
            &self.prk,
            &fields(&[&label("notification-root"), &self.context.pair_hash]),
            32,
        )
        .to_vec())
    }
    /// Consumes the authenticated result so record counters cannot be reset.
    pub fn into_session(self) -> Result<Session> {
        Ok(Session {
            transcript: self.transcript.clone(),
            send: Direction::new(&self.prk, "ui-host"),
            receive: Direction::new(&self.prk, "host-ui"),
            closed: false,
        })
    }
}
fn derive_auth(
    context: AuthContext,
    init: &Value,
    challenge: &Value,
    ss_h: &[u8],
    ss_u: &[u8],
) -> Result<Authenticated> {
    let transcript = hash_fields(&[
        &label("auth-transcript"),
        &context.encode()?,
        &fixed(init, "nu", 32)?,
        &fixed(challenge, "nh", 32)?,
        &fixed(init, "ctH", 1088)?,
        &fixed(challenge, "ctU", 1088)?,
    ]);
    let mut secrets = Zeroizing::new(ss_h.to_vec());
    secrets.extend_from_slice(ss_u);
    let (mut prk, _) = Hkdf::<Sha384>::extract(Some(&transcript), &secrets);
    let secret = Zeroizing::new(prk.to_vec());
    prk.zeroize();
    Ok(Authenticated {
        context,
        transcript,
        prk: secret,
    })
}
fn host_proof(prk: &[u8], t: &[u8]) -> Vec<u8> {
    mac(
        &expand(prk, &fields(&[&label("finish-host")]), 32),
        &fields(&[&label("host-finished"), t]),
    )
}
fn ui_proof(prk: &[u8], t: &[u8], proof_h: &[u8]) -> Vec<u8> {
    mac(
        &expand(prk, &fields(&[&label("finish-ui")]), 32),
        &fields(&[&label("ui-finished"), t, proof_h]),
    )
}

struct Direction {
    key: Zeroizing<Vec<u8>>,
    name: &'static str,
    seq: u64,
    bytes: u64,
}
impl Direction {
    fn new(prk: &[u8], name: &'static str) -> Self {
        Self {
            key: expand(prk, &fields(&[&label(&format!("record-{name}"))]), 32),
            name,
            seq: 0,
            bytes: 0,
        }
    }
}
/// `&mut self` serializes seal/open operations across the session. A caller must
/// also serialize seal-and-write; any uncertain write must discard the session.
pub struct Session {
    transcript: Vec<u8>,
    send: Direction,
    receive: Direction,
    closed: bool,
}
impl Session {
    fn aad(&self, name: &str, seq: u64) -> Vec<u8> {
        fields(&[
            &label("record"),
            VERSION,
            SUITE,
            &self.transcript,
            name.as_bytes(),
            &seq.to_be_bytes(),
        ])
    }
    pub fn close(&mut self) {
        self.closed = true;
        self.send.key.zeroize();
        self.receive.key.zeroize();
    }
    pub fn seal(&mut self, plain: &Value) -> Result<Value> {
        let result = self.seal_inner(plain);
        if result.is_err() {
            self.close();
        }
        result
    }
    fn seal_inner(&mut self, plain: &Value) -> Result<Value> {
        if self.closed {
            return Err(protocol());
        }
        let b = Zeroizing::new(wire::encode_plain(plain)?);
        if self.send.seq >= MAX_RECORDS || b.len() as u64 > MAX_RECORD_BYTES - self.send.bytes {
            return Err(protocol());
        }
        let ct = encrypt(
            &self.send.key,
            &nonce(self.send.seq),
            &b,
            &self.aad(self.send.name, self.send.seq),
        )?;
        let out = json!({"v":1,"type":"record","session":b64(&self.transcript),"seq":self.send.seq.to_string(),"ciphertext":b64(&ct)});
        validate_inner(&out)?;
        self.send.seq += 1;
        self.send.bytes += b.len() as u64;
        Ok(out)
    }
    pub fn open(&mut self, message: &Value) -> Result<Value> {
        let result = self.open_inner(message);
        if result.is_err() {
            self.close();
        }
        result
    }
    fn open_inner(&mut self, message: &Value) -> Result<Value> {
        if self.closed {
            return Err(protocol());
        }
        validate_inner(message)?;
        if message["type"] != "record"
            || fixed(message, "session", 48)? != self.transcript
            || message["seq"].as_str().and_then(|s| s.parse::<u64>().ok()) != Some(self.receive.seq)
            || self.receive.seq >= MAX_RECORDS
        {
            return Err(authentication());
        }
        let ct = unb64(message["ciphertext"].as_str().ok_or_else(protocol)?)?;
        if ct.len() < 16 || (ct.len() - 16) as u64 > MAX_RECORD_BYTES - self.receive.bytes {
            return Err(authentication());
        }
        let b = Zeroizing::new(decrypt(
            &self.receive.key,
            &nonce(self.receive.seq),
            &ct,
            &self.aad(self.receive.name, self.receive.seq),
        )?);
        let out = wire::decode_plain(&b)?;
        self.receive.seq += 1;
        self.receive.bytes += b.len() as u64;
        Ok(out)
    }
}
fn nonce(seq: u64) -> [u8; 12] {
    let mut n = [0; 12];
    n[4..].copy_from_slice(&seq.to_be_bytes());
    n
}
fn encrypt(key: &[u8], nonce: &[u8; 12], b: &[u8], aad: &[u8]) -> Result<Vec<u8>> {
    Aes256Gcm::new_from_slice(key)
        .map_err(|_| protocol())?
        .encrypt(Nonce::from_slice(nonce), Payload { msg: b, aad })
        .map_err(|_| authentication())
}
fn decrypt(key: &[u8], nonce: &[u8; 12], b: &[u8], aad: &[u8]) -> Result<Vec<u8>> {
    Aes256Gcm::new_from_slice(key)
        .map_err(|_| protocol())?
        .decrypt(Nonce::from_slice(nonce), Payload { msg: b, aad })
        .map_err(|_| authentication())
}

#[derive(Clone, Serialize, Deserialize)]
pub struct NotificationContext {
    pub route: Route,
    pub host_fp: String,
    pub ui_id: String,
    pub pair_epoch: Vec<u8>,
}
impl NotificationContext {
    fn validate(&self, root: &[u8]) -> Result<()> {
        self.route.validate()?;
        if !valid_fp(&self.host_fp)
            || !valid_fp(&self.ui_id)
            || self.pair_epoch.len() != 16
            || root.len() != 32
        {
            return Err(protocol());
        }
        Ok(())
    }
}
#[derive(Clone, Serialize, Deserialize)]
pub struct Poll {
    pub request_header: String,
    pub request_mac: String,
}
fn request_key(c: &NotificationContext, root: &[u8]) -> Zeroizing<Vec<u8>> {
    let salt = hash_fields(&[
        &label("notify-context"),
        c.route.origin.as_bytes(),
        c.route.host_id.as_bytes(),
        c.host_fp.as_bytes(),
        c.ui_id.as_bytes(),
        &c.pair_epoch,
    ]);
    kdf(root, &salt, "notify-request")
}
/// `request` must be newly generated for each logical poll and persisted as the
/// sole outstanding request. `ack` is the complete last durable receipt.
pub fn new_poll(
    c: &NotificationContext,
    root: &[u8],
    request: &[u8],
    ack: Option<&Value>,
) -> Result<Poll> {
    c.validate(root)?;
    if request.len() != 32 {
        return Err(protocol());
    }
    let (batch, through, token) = if let Some(a) = ack {
        if a.as_object().map(|o| o.len()) != Some(3) {
            return Err(protocol());
        }
        let batch = fixed(a, "batch", 16)?;
        let token = fixed(a, "token", 32)?;
        let s = a["through"].as_str().ok_or_else(protocol)?;
        let n = s.parse::<u64>().map_err(|_| protocol())?;
        if n == 0 || n.to_string() != s {
            return Err(protocol());
        }
        (batch, n.to_be_bytes().to_vec(), token)
    } else {
        (vec![], vec![], vec![])
    };
    let message = fields(&[
        &label("notify-get"),
        VERSION,
        c.route.origin.as_bytes(),
        c.route.host_id.as_bytes(),
        c.host_fp.as_bytes(),
        c.ui_id.as_bytes(),
        &c.pair_epoch,
        request,
        &batch,
        &through,
        &token,
    ]);
    // Field order here is intentionally the Go v1 request-header serialization.
    // JSON ordering does not enter the MAC; retaining it also matches fixtures.
    #[derive(Serialize)]
    struct Request<'a> {
        v: u16,
        pair_epoch: String,
        request: String,
        ack: Option<&'a Value>,
    }
    let header = serde_json::to_vec(&Request {
        v: 1,
        pair_epoch: b64(&c.pair_epoch),
        request: b64(request),
        ack,
    })
    .map_err(|_| protocol())?;
    Ok(Poll {
        request_header: b64(&header),
        request_mac: b64(&mac(&request_key(c, root), &message)),
    })
}
/// The outstanding id comes from local durable poll state, never the envelope.
/// Consumers persist the validated page before sending its receipt on a poll.
pub fn open_notification(
    c: &NotificationContext,
    root: &[u8],
    outstanding: &[u8],
    envelope: &Value,
) -> Result<Value> {
    c.validate(root)?;
    let raw = serde_json::to_vec(envelope).map_err(|_| protocol())?;
    let envelope = wire::decode_notification_envelope(&raw)?;
    if outstanding.len() != 32
        || envelope["host_fp"] != c.host_fp
        || envelope["ui_id"] != c.ui_id
        || fixed(&envelope, "pair_epoch", 16)? != c.pair_epoch
        || fixed(&envelope, "request", 32)? != outstanding
    {
        return Err(authentication());
    }
    let reply = fixed(&envelope, "reply", 32)?;
    let aad = fields(&[
        &label("notify-response"),
        VERSION,
        c.route.origin.as_bytes(),
        c.route.host_id.as_bytes(),
        c.host_fp.as_bytes(),
        c.ui_id.as_bytes(),
        &c.pair_epoch,
        outstanding,
        &reply,
    ]);
    let key = kdf(root, &hash(&aad), "notify-response");
    let ct = unb64(envelope["ciphertext"].as_str().ok_or_else(protocol)?)?;
    let plain = Zeroizing::new(decrypt(&key, &[0; 12], &ct, &aad)?);
    wire::decode_notification_page(&plain)
}

#[cfg(test)]
mod tests {
    use super::*;
    fn fixture() -> Value {
        serde_json::from_str(include_str!(
            "../../../../internal/relayproto/testdata/protocol-v1.json"
        ))
        .unwrap()
    }
    fn bytes(f: &Value, name: &str) -> Vec<u8> {
        f[name]
            .as_str()
            .unwrap()
            .as_bytes()
            .as_chunks::<2>()
            .0
            .iter()
            .map(|x| u8::from_str_radix(std::str::from_utf8(x).unwrap(), 16).unwrap())
            .collect()
    }
    fn context(f: &Value) -> AuthContext {
        AuthContext {
            route: Route::new("https://relay.example", "office", false).unwrap(),
            mode: "pair".into(),
            pkh: bytes(f, "host_pk"),
            pku: bytes(f, "ui_pk"),
            pair_epoch: bytes(f, "epoch"),
            pair_hash: bytes(f, "pair_hash"),
        }
    }
    fn init(f: &Value) -> Value {
        json!({"v":1,"type":"pair.auth.init","host_fp":f["host_fp"],"ui_id":f["ui_id"],"pair_epoch":b64(&bytes(f,"epoch")),"nu":b64(&bytes(f,"nu")),"ctH":b64(&bytes(f,"host_ct"))})
    }
    fn challenge(f: &Value) -> Value {
        json!({"v":1,"type":"pair.auth.challenge","nh":b64(&bytes(f,"nh")),"ctU":b64(&bytes(f,"ui_ct")),"proofH":b64(&bytes(f,"proofH"))})
    }
    fn handshake(f: &Value) -> UIHandshake {
        UIHandshake {
            context: context(f),
            id: Identity::import(&bytes(f, "ui_seed")).unwrap(),
            init: init(f),
            ss_h: Zeroizing::new(bytes(f, "host_ss")),
        }
    }
    fn authenticated(f: &Value) -> Authenticated {
        handshake(f).finish(&challenge(f)).unwrap().1
    }
    fn host_session(f: &Value) -> Session {
        let mut s = authenticated(f).into_session().unwrap();
        std::mem::swap(&mut s.send, &mut s.receive);
        s
    }
    #[test]
    fn exact_auth_proofs_record_keys_aad_ciphertext_and_pair_root() {
        let f = fixture();
        let a = derive_auth(
            context(&f),
            &init(&f),
            &challenge(&f),
            &bytes(&f, "host_ss"),
            &bytes(&f, "ui_ss"),
        )
        .unwrap();
        assert_eq!(a.transcript, bytes(&f, "auth_transcript"));
        assert_eq!(*a.prk, bytes(&f, "prk"));
        assert_eq!(host_proof(&a.prk, &a.transcript), bytes(&f, "proofH"));
        assert_eq!(
            ui_proof(&a.prk, &a.transcript, &bytes(&f, "proofH")),
            bytes(&f, "proofU")
        );
        let (finish, a) = handshake(&f).finish(&challenge(&f)).unwrap();
        assert_eq!(fixed(&finish, "proofU", 48).unwrap(), bytes(&f, "proofU"));
        assert_eq!(
            a.notification_root().unwrap(),
            bytes(&f, "notification_root")
        );
        let mut s = a.into_session().unwrap();
        assert_eq!(*s.send.key, bytes(&f, "record_key_ui_host"));
        assert_eq!(*s.receive.key, bytes(&f, "record_key_host_ui"));
        assert_eq!(s.aad("ui-host", 0), bytes(&f, "record_aad"));
        let plain = wire::decode_plain(&bytes(&f, "record_plaintext")).unwrap();
        let record = s.seal(&plain).unwrap();
        assert_eq!(
            unb64(record["ciphertext"].as_str().unwrap()).unwrap(),
            bytes(&f, "record_ciphertext")
        );
        let expected: Value = serde_json::from_str(f["record_wire"].as_str().unwrap()).unwrap();
        assert_eq!(record, expected);
        assert_eq!(host_session(&f).open(&record).unwrap(), plain);
    }
    #[test]
    fn exact_notification_context_request_key_and_response_key() {
        let f = fixture();
        let c = NotificationContext {
            route: context(&f).route,
            host_fp: f["host_fp"].as_str().unwrap().into(),
            ui_id: f["ui_id"].as_str().unwrap().into(),
            pair_epoch: bytes(&f, "epoch"),
        };
        let root = bytes(&f, "notification_root");
        assert_eq!(
            *request_key(&c, &root),
            bytes(&f, "notification_request_key")
        );
        let aad = fields(&[
            &label("notify-response"),
            VERSION,
            c.route.origin.as_bytes(),
            c.route.host_id.as_bytes(),
            c.host_fp.as_bytes(),
            c.ui_id.as_bytes(),
            &c.pair_epoch,
            &bytes(&f, "request"),
            &bytes(&f, "reply"),
        ]);
        assert_eq!(aad, bytes(&f, "notification_aad"));
        let key = kdf(&root, &hash(&aad), "notify-response");
        assert_eq!(*key, bytes(&f, "notification_response_key"));
        assert_eq!(
            encrypt(&key, &[0; 12], &bytes(&f, "notification_plaintext"), &aad).unwrap(),
            bytes(&f, "notification_ciphertext")
        );
    }
    #[test]
    fn failed_proof_and_changed_context_never_produce_a_session() {
        let f = fixture();
        for field in ["nh", "ctU", "proofH"] {
            let mut m = challenge(&f);
            let mut v = unb64(m[field].as_str().unwrap()).unwrap();
            v[0] ^= 1;
            m[field] = json!(b64(&v));
            assert!(handshake(&f).finish(&m).is_err(), "{field}");
        }
        let mut m = challenge(&f);
        m["type"] = json!("auth.challenge");
        assert!(handshake(&f).finish(&m).is_err());
        let mut h = handshake(&f);
        h.context.route.host_id = "other".into();
        assert!(h.finish(&challenge(&f)).is_err());
        let mut h = handshake(&f);
        h.context.pair_epoch[0] ^= 1;
        assert!(h.finish(&challenge(&f)).is_err());
        let mut h = handshake(&f);
        h.context.pair_hash[0] ^= 1;
        assert!(h.finish(&challenge(&f)).is_err());
        let mut c = context(&f);
        c.mode = "reconnect".into();
        c.pair_hash.clear();
        let a = derive_auth(
            c,
            &init(&f),
            &challenge(&f),
            &bytes(&f, "host_ss"),
            &bytes(&f, "ui_ss"),
        )
        .unwrap();
        assert!(a.notification_root().is_err());
        assert!(
            UIHandshake::new(
                context(&f),
                &Identity::import(&bytes(&f, "host_seed")).unwrap()
            )
            .is_err()
        );
    }
    #[test]
    fn replay_gaps_reflection_and_schema_failures_permanently_close_records() {
        let f = fixture();
        let plain = json!({"v":1,"type":"stream.open","id":"1"});
        let record = authenticated(&f)
            .into_session()
            .unwrap()
            .seal(&plain)
            .unwrap();
        let mut h = host_session(&f);
        assert_eq!(h.open(&record).unwrap(), plain);
        assert!(h.open(&record).is_err());
        assert!(h.seal(&plain).is_err());
        for field in ["seq", "session", "ciphertext"] {
            let mut m = record.clone();
            if field == "seq" {
                m[field] = json!("1");
            } else {
                let mut v = unb64(m[field].as_str().unwrap()).unwrap();
                v[0] ^= 1;
                m[field] = json!(b64(&v));
            }
            let mut h = host_session(&f);
            assert!(h.open(&m).is_err());
            assert!(h.open(&record).is_err());
        }
        let mut ui = authenticated(&f).into_session().unwrap();
        assert!(ui.open(&record).is_err());
        let mut s = authenticated(&f).into_session().unwrap();
        assert!(
            s.seal(&json!({"v":1,"type":"stream.open","id":"2"}))
                .is_err()
        );
        assert!(s.seal(&plain).is_err());
        // Even an authentic GCM tag cannot authorize a malformed plaintext.
        let mut source = authenticated(&f).into_session().unwrap();
        let invalid = br#"{"v":1,"type":"stream.open","id":"2"}"#;
        let ciphertext = encrypt(
            &source.send.key,
            &nonce(0),
            invalid,
            &source.aad("ui-host", 0),
        )
        .unwrap();
        let mut bad = record;
        bad["ciphertext"] = json!(b64(&ciphertext));
        let mut h = host_session(&f);
        assert!(h.open(&bad).is_err());
        assert_eq!(h.receive.seq, 0);
        assert!(h.seal(&plain).is_err());
        source.close();
        assert!(source.seal(&plain).is_err());
    }
    #[test]
    fn production_constructor_completes_mutual_auth_and_bidirectional_records() {
        let f = fixture();
        let identity = Identity::generate().unwrap();
        let host = Identity::import(&bytes(&f, "host_seed")).unwrap();
        let mut c = context(&f);
        c.pku = identity.public_key();
        let (handshake, init) = UIHandshake::new(c.clone(), &identity).unwrap();
        let ss_h = host
            .key
            .decapsulate_slice(&fixed(&init, "ctH", 1088).unwrap())
            .unwrap();
        let (ct_u, ss_u) = public_key(&c.pku).unwrap().encapsulate();
        let mut challenge = json!({"v":1,"type":"pair.auth.challenge","nh":b64(&[42;32]),"ctU":b64(ct_u.as_slice()),"proofH":b64(&[0;48])});
        let host_auth = derive_auth(c, &init, &challenge, &ss_h, &ss_u).unwrap();
        let proof_h = host_proof(&host_auth.prk, &host_auth.transcript);
        challenge["proofH"] = json!(b64(&proof_h));
        let (finish, ui_auth) = handshake.finish(&challenge).unwrap();
        assert_eq!(
            fixed(&finish, "proofU", 48).unwrap(),
            ui_proof(&host_auth.prk, &host_auth.transcript, &proof_h)
        );
        assert_eq!(
            ui_auth.notification_root().unwrap(),
            host_auth.notification_root().unwrap()
        );
        let mut ui = ui_auth.into_session().unwrap();
        let mut host = host_auth.into_session().unwrap();
        std::mem::swap(&mut host.send, &mut host.receive);
        let p = json!({"v":1,"type":"stream.open","id":"1"});
        assert_eq!(host.open(&ui.seal(&p).unwrap()).unwrap(), p);
        assert_eq!(ui.open(&host.seal(&p).unwrap()).unwrap(), p);
    }
    #[test]
    fn record_quota_boundaries_are_checked_before_advancing() {
        let f = fixture();
        let plain = json!({"v":1,"type":"stream.open","id":"1"});
        let n = wire::encode_plain(&plain).unwrap().len() as u64;
        let mut s = authenticated(&f).into_session().unwrap();
        s.send.seq = MAX_RECORDS - 1;
        s.send.bytes = MAX_RECORD_BYTES - n;
        assert!(s.seal(&plain).is_ok());
        assert_eq!(s.send.seq, MAX_RECORDS);
        assert_eq!(s.send.bytes, MAX_RECORD_BYTES);
        assert!(s.seal(&plain).is_err());
        let mut s = authenticated(&f).into_session().unwrap();
        s.send.bytes = MAX_RECORD_BYTES - n + 1;
        assert!(s.seal(&plain).is_err());
        assert_eq!(s.send.seq, 0);
        let record = authenticated(&f)
            .into_session()
            .unwrap()
            .seal(&plain)
            .unwrap();
        let mut h = host_session(&f);
        h.receive.bytes = MAX_RECORD_BYTES - n;
        assert!(h.open(&record).is_ok());
        assert_eq!(h.receive.bytes, MAX_RECORD_BYTES);
        let mut h = host_session(&f);
        h.receive.bytes = MAX_RECORD_BYTES - n + 1;
        assert!(h.open(&record).is_err());
        assert_eq!(h.receive.seq, 0);
    }
}

#[cfg(test)]
pub(super) mod test_support {
    use super::*;
    /// Lifecycle tests use the independently checked fixture keys rather than
    /// adding a production constructor that could reset a live session counter.
    pub fn session() -> Session {
        let f: Value = serde_json::from_str(include_str!(
            "../../../../internal/relayproto/testdata/protocol-v1.json"
        ))
        .unwrap();
        let bytes = |name: &str| {
            f[name]
                .as_str()
                .unwrap()
                .as_bytes()
                .as_chunks::<2>()
                .0
                .iter()
                .map(|c| u8::from_str_radix(std::str::from_utf8(c).unwrap(), 16).unwrap())
                .collect::<Vec<u8>>()
        };
        Session {
            transcript: bytes("auth_transcript"),
            send: Direction {
                key: Zeroizing::new(bytes("record_key_ui_host")),
                name: "ui-host",
                seq: 0,
                bytes: 0,
            },
            receive: Direction {
                key: Zeroizing::new(bytes("record_key_host_ui")),
                name: "host-ui",
                seq: 0,
                bytes: 0,
            },
            closed: false,
        }
    }
}
