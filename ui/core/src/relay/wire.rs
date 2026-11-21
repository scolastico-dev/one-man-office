//! Strict, bounded version-one relay encodings shared by every transport.
use crate::Result;
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use serde::{Deserialize, Serialize};
use serde_json::{Map, Value};
use std::net::IpAddr;

pub const MAX_CONTROL_SIZE: usize = 1 << 20;
pub const MAX_INNER_SIZE: usize = 64 << 10;
pub const MAX_NOTIFICATION_SIZE: usize = 512 << 10;
pub const MAX_NOTIFICATION_PLAINTEXT: usize = 256 << 10;
pub const MAX_STREAM_DATA: usize = 16 << 10;
pub const INITIAL_CREDIT: u64 = 65536;

fn protocol() -> String {
    "relay protocol violation".into()
}
fn require(ok: bool) -> Result<()> {
    if ok { Ok(()) } else { Err(protocol()) }
}
pub fn b64(bytes: &[u8]) -> String {
    URL_SAFE_NO_PAD.encode(bytes)
}
pub fn unb64(value: &str) -> Result<Vec<u8>> {
    let bytes = URL_SAFE_NO_PAD.decode(value).map_err(|_| protocol())?;
    require(b64(&bytes) == value)?;
    Ok(bytes)
}
fn valid_host_id(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 64
        && value
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b == b'_' || b == b'-')
}
fn valid_fingerprint(value: &str) -> bool {
    value.len() == 96
        && value
            .bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}

/// Configured relay origin and routing name, bound into cryptographic transcripts.
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct Route {
    pub origin: String,
    pub host_id: String,
}
impl Route {
    pub fn new(origin: &str, host_id: &str, allow_loopback_http: bool) -> Result<Self> {
        let (scheme, authority) = origin.split_once("://").ok_or_else(protocol)?;
        let scheme = scheme.to_ascii_lowercase();
        require(
            valid_host_id(host_id)
                && !authority.is_empty()
                && !authority.contains(['/', '?', '#', '@', '%', '\\'])
                && !authority.chars().any(char::is_whitespace),
        )?;
        let (host, port) = if let Some(bracketed) = authority.strip_prefix('[') {
            let (host, rest) = bracketed.split_once(']').ok_or_else(protocol)?;
            require(host.parse::<std::net::Ipv6Addr>().is_ok())?;
            (
                host,
                if rest.is_empty() {
                    None
                } else {
                    Some(rest.strip_prefix(':').ok_or_else(protocol)?)
                },
            )
        } else {
            match authority.split_once(':') {
                Some((host, port)) => (host, Some(port)),
                None => (authority, None),
            }
        };
        let host = host.to_ascii_lowercase();
        let ip = host.parse::<IpAddr>().ok();
        let valid_dns = !host.is_empty()
            && host.len() <= 253
            && host.split('.').all(|label| {
                !label.is_empty()
                    && label.len() <= 63
                    && !label.starts_with('-')
                    && !label.ends_with('-')
                    && label
                        .bytes()
                        .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || b == b'-')
            });
        require(ip.is_some() || valid_dns)?;
        if let Some(port) = port {
            let number: u16 = port.parse().map_err(|_| protocol())?;
            require(number != 0 && number.to_string() == port)?;
        }
        let loopback = match ip {
            Some(IpAddr::V4(ip)) => ip.is_loopback(),
            Some(IpAddr::V6(ip)) => {
                ip.is_loopback() || ip.to_ipv4_mapped().is_some_and(|ip| ip.is_loopback())
            }
            None => host == "localhost",
        };
        require(scheme == "https" || scheme == "http" && allow_loopback_http && loopback)?;
        let port = match (scheme.as_str(), port) {
            ("https", Some("443")) | ("http", Some("80")) => None,
            (_, p) => p,
        };
        let host = if host.contains(':') {
            format!("[{host}]")
        } else {
            host
        };
        let authority = if let Some(port) = port {
            format!("{host}:{port}")
        } else {
            host
        };
        Ok(Self {
            origin: format!("{scheme}://{authority}"),
            host_id: host_id.into(),
        })
    }
    pub fn validate(&self) -> Result<()> {
        require(Self::new(&self.origin, &self.host_id, true)? == *self)
    }
}

// Parse before constructing Value: Value's ordinary decoder silently overwrites
// duplicate object keys and unescapes decimal strings. Neither is valid here.
struct Parser<'a> {
    source: &'a [u8],
    position: usize,
}
impl Parser<'_> {
    fn whitespace(&mut self) {
        while self
            .source
            .get(self.position)
            .is_some_and(|b| matches!(b, b' ' | b'\r' | b'\n' | b'\t'))
        {
            self.position += 1;
        }
    }
    fn byte(&mut self, expected: u8) -> Result<()> {
        self.whitespace();
        require(self.source.get(self.position) == Some(&expected))?;
        self.position += 1;
        Ok(())
    }
    fn string(&mut self) -> Result<String> {
        self.whitespace();
        let start = self.position;
        self.byte(b'"')?;
        loop {
            match self.source.get(self.position) {
                Some(b'"') => {
                    self.position += 1;
                    break;
                }
                Some(b'\\') => {
                    self.position += 2;
                }
                Some(_) => {
                    self.position += 1;
                }
                None => return Err(protocol()),
            }
        }
        serde_json::from_slice(&self.source[start..self.position]).map_err(|_| protocol())
    }
    fn value(&mut self, depth: usize) -> Result<Value> {
        require(depth <= 32)?;
        self.whitespace();
        match self.source.get(self.position) {
            Some(b'{') => {
                let object_start = self.position;
                self.position += 1;
                self.whitespace();
                let mut object = Map::new();
                if self.source.get(self.position) != Some(&b'}') {
                    loop {
                        let key = self.string()?;
                        require(!object.contains_key(&key))?;
                        self.byte(b':')?;
                        self.whitespace();
                        let start = self.position;
                        let value = self.value(depth + 1)?;
                        let raw = &self.source[start..self.position];
                        // Version and counters have canonical wire spellings.
                        if key == "v" {
                            require(raw == b"1")?;
                        }
                        if matches!(key.as_str(), "id" | "seq" | "bytes" | "through" | "ticket") {
                            require(
                                raw.len() >= 3
                                    && raw.first() == Some(&b'"')
                                    && raw.last() == Some(&b'"')
                                    && raw[1..raw.len() - 1].iter().all(u8::is_ascii_digit),
                            )?;
                            counter_value(&value)?;
                        }
                        object.insert(key, value);
                        self.whitespace();
                        if self.source.get(self.position) != Some(&b',') {
                            break;
                        }
                        self.position += 1;
                    }
                }
                self.byte(b'}')?;
                // These nested schemas have tighter limits than their enclosing
                // page/request. Count their received bytes, including whitespace.
                if object.contains_key("unavailable") {
                    require(self.position - object_start <= 4096)?;
                }
                if object.len() == 3
                    && object.contains_key("batch")
                    && object.contains_key("through")
                    && object.contains_key("token")
                {
                    require(self.position - object_start <= 1024)?;
                }
                Ok(Value::Object(object))
            }
            Some(b'[') => {
                self.position += 1;
                self.whitespace();
                let mut values = Vec::new();
                if self.source.get(self.position) != Some(&b']') {
                    loop {
                        values.push(self.value(depth + 1)?);
                        self.whitespace();
                        if self.source.get(self.position) != Some(&b',') {
                            break;
                        }
                        self.position += 1;
                    }
                }
                self.byte(b']')?;
                Ok(Value::Array(values))
            }
            Some(b'"') => self.string().map(Value::String),
            Some(_) => {
                let start = self.position;
                while self
                    .source
                    .get(self.position)
                    .is_some_and(|b| !b.is_ascii_whitespace() && !matches!(b, b',' | b'}' | b']'))
                {
                    self.position += 1;
                }
                let value: Value = serde_json::from_slice(&self.source[start..self.position])
                    .map_err(|_| protocol())?;
                require(!value.is_array() && !value.is_object())?;
                Ok(value)
            }
            None => Err(protocol()),
        }
    }
}
fn strict_object(bytes: &[u8], limit: usize) -> Result<Value> {
    require(!bytes.is_empty() && bytes.len() <= limit && std::str::from_utf8(bytes).is_ok())?;
    let mut parser = Parser {
        source: bytes,
        position: 0,
    };
    let value = parser.value(0)?;
    parser.whitespace();
    require(parser.position == bytes.len() && value.is_object())?;
    Ok(value)
}
fn exact(value: &Value, fields: &str, nullable: &str) -> Result<()> {
    let object = value.as_object().ok_or_else(protocol)?;
    require(object.len() == fields.split_whitespace().count())?;
    for key in fields.split_whitespace() {
        let field = object.get(key).ok_or_else(protocol)?;
        require(!field.is_null() || nullable.split_whitespace().any(|name| name == key))?;
    }
    Ok(())
}
fn string<'a>(value: &'a Value, key: &str) -> Result<&'a str> {
    value.get(key).and_then(Value::as_str).ok_or_else(protocol)
}
fn bytes(value: &Value, key: &str) -> Result<Vec<u8>> {
    unb64(string(value, key)?)
}
fn length(value: &Value, key: &str, min: usize, max: usize) -> Result<()> {
    let n = bytes(value, key)?.len();
    require(n >= min && n <= max)
}
fn counter_value(value: &Value) -> Result<u64> {
    let s = value.as_str().ok_or_else(protocol)?;
    require(
        !s.is_empty()
            && !(s.len() > 1 && s.starts_with('0'))
            && s.bytes().all(|b| b.is_ascii_digit()),
    )?;
    s.parse().map_err(|_| protocol())
}
fn counter(value: &Value, key: &str) -> Result<u64> {
    counter_value(value.get(key).ok_or_else(protocol)?)
}
fn fingerprint(value: &Value, key: &str) -> Result<()> {
    require(valid_fingerprint(string(value, key)?))
}
fn version(value: &Value) -> Result<()> {
    require(value.get("v").and_then(Value::as_u64) == Some(1))
}
fn boolean(value: &Value, key: &str) -> Result<bool> {
    value.get(key).and_then(Value::as_bool).ok_or_else(protocol)
}

fn union(value: &Value, layer: &str) -> Result<()> {
    version(value)?;
    let kind = string(value, "type")?;
    let fields = match (layer, kind) {
        ("control", "host.register" | "ui.attach") => "host_id",
        ("control", "host.registered") => "host_id registration",
        ("control", "ui.attached" | "ui.open" | "ui.closed" | "ui.close") => "connection",
        ("control", "forward") => "connection payload",
        ("control", "error") => "code",
        ("control", "notification.get") => "qid host_id ui_id request_header request_mac",
        ("control", "notification.reply") => "qid body",
        ("control", "notification.empty" | "notification.cancel") => "qid",
        ("inner", "pair.commit") => "aid C",
        ("inner", "pair.host") => "aid window pair_epoch pkH rh",
        ("inner", "pair.reveal") => "aid pkU ru",
        ("inner", "auth.init" | "pair.auth.init") => "host_fp ui_id pair_epoch nu ctH",
        ("inner", "auth.challenge" | "pair.auth.challenge") => "nh ctU proofH",
        ("inner", "auth.finish" | "pair.auth.finish") => "proofU",
        ("inner", "record") => "session seq ciphertext",
        ("plain", "ready") => "pair_epoch state aid",
        ("plain", "ready.ack") => "pair_epoch",
        ("plain", "pair.accepted") => "aid host_fp ui_id pair_epoch",
        ("plain", "pair.confirm" | "pair.confirmed") => "aid pair_epoch",
        ("plain", "stream.open" | "stream.opened" | "stream.fin") => "id",
        ("plain", "stream.data") => "id data",
        ("plain", "stream.credit") => "id bytes",
        ("plain", "stream.reset") => "id code",
        _ => return Err(protocol()),
    };
    exact(value, &format!("v type {fields}"), "")?;
    for key in fields.split_whitespace() {
        match key {
            "host_id" => require(valid_host_id(string(value, key)?))?,
            "host_fp" | "ui_id" => fingerprint(value, key)?,
            "registration" | "connection" | "qid" | "aid" | "window" | "pair_epoch" => {
                length(value, key, 16, 16)?
            }
            "C" | "session" | "proofH" | "proofU" => length(value, key, 48, 48)?,
            "rh" | "ru" | "nu" | "nh" => length(value, key, 32, 32)?,
            "ctH" | "ctU" => length(value, key, 1088, 1088)?,
            "pkH" | "pkU" => super::crypto::validate_public_key(&bytes(value, key)?)?,
            "payload" => length(value, key, 1, MAX_INNER_SIZE)?,
            "body" => length(value, key, 1, MAX_NOTIFICATION_SIZE)?,
            "ciphertext" => length(value, key, 16, MAX_INNER_SIZE)?,
            "data" => length(value, key, 1, MAX_STREAM_DATA)?,
            "seq" => {
                counter(value, key)?;
            }
            "id" => {
                require(counter(value, key)? % 2 == 1)?;
            }
            "bytes" => {
                let n = counter(value, key)?;
                require(n > 0 && n <= INITIAL_CREDIT)?;
            }
            "state" => require(matches!(
                string(value, key)?,
                "active" | "awaiting-confirmation"
            ))?,
            "code" => require(if layer == "control" {
                matches!(
                    string(value, key)?,
                    "id_in_use" | "host_offline" | "protocol_error" | "overloaded"
                )
            } else {
                matches!(
                    string(value, key)?,
                    "cancelled" | "refused" | "internal" | "closed"
                )
            })?,
            "request_header" => {
                require(string(value, key)?.len() <= 4096)?;
                length(value, key, 1, 3072)?;
            }
            "request_mac" => length(value, key, 48, 48)?,
            _ => return Err(protocol()),
        }
    }
    Ok(())
}
fn decode_union(bytes: &[u8], layer: &str, limit: usize) -> Result<Value> {
    let value = strict_object(bytes, limit)?;
    union(&value, layer)?;
    Ok(value)
}
pub fn decode_control(bytes: &[u8]) -> Result<Value> {
    decode_union(bytes, "control", MAX_CONTROL_SIZE)
}
pub fn decode_inner(bytes: &[u8]) -> Result<Value> {
    decode_union(bytes, "inner", MAX_INNER_SIZE)
}
pub fn decode_plain(bytes: &[u8]) -> Result<Value> {
    decode_union(bytes, "plain", MAX_INNER_SIZE)
}

fn receipt(value: &Value) -> Result<()> {
    exact(value, "batch through token", "")?;
    length(value, "batch", 16, 16)?;
    length(value, "token", 32, 32)?;
    require(counter(value, "through")? > 0)
}
pub fn decode_notification_request(bytes: &[u8]) -> Result<Value> {
    let value = strict_object(bytes, 3072)?;
    exact(&value, "v pair_epoch request ack", "ack")?;
    version(&value)?;
    length(&value, "pair_epoch", 16, 16)?;
    length(&value, "request", 32, 32)?;
    if !value["ack"].is_null() {
        receipt(&value["ack"])?;
    }
    Ok(value)
}
pub fn decode_notification_envelope(bytes: &[u8]) -> Result<Value> {
    let value = strict_object(bytes, MAX_NOTIFICATION_SIZE)?;
    exact(
        &value,
        "v host_fp ui_id pair_epoch request reply ciphertext",
        "",
    )?;
    version(&value)?;
    fingerprint(&value, "host_fp")?;
    fingerprint(&value, "ui_id")?;
    length(&value, "pair_epoch", 16, 16)?;
    length(&value, "request", 32, 32)?;
    length(&value, "reply", 32, 32)?;
    length(&value, "ciphertext", 16, MAX_NOTIFICATION_PLAINTEXT + 16)?;
    Ok(value)
}

// RFC3339's UTC projection, with optional fractional seconds. Validate the
// calendar directly so no timezone or external clock participates in decoding.
fn utc_timestamp(value: &str) -> bool {
    let Some(text) = value.strip_suffix('Z') else {
        return false;
    };
    let (base, fraction) = text
        .split_once('.')
        .or_else(|| text.split_once(','))
        .map_or((text, None), |(a, b)| (a, Some(b)));
    if !matches!(base.len(), 18 | 19)
        || fraction.is_some_and(|f| f.is_empty() || !f.bytes().all(|b| b.is_ascii_digit()))
    {
        return false;
    }
    let b = base.as_bytes();
    // Match time.Parse(time.RFC3339): its fallback allows one hour digit.
    let hour_end = base.len() - 6;
    if b[4] != b'-'
        || b[7] != b'-'
        || b[10] != b'T'
        || b[hour_end] != b':'
        || b[hour_end + 3] != b':'
    {
        return false;
    }
    let number = |range: std::ops::Range<usize>| -> Option<u32> {
        let part = &base.as_bytes()[range];
        if !part.iter().all(u8::is_ascii_digit) {
            return None;
        }
        std::str::from_utf8(part).ok()?.parse().ok()
    };
    let (Some(year), Some(month), Some(day), Some(hour), Some(minute), Some(second)) = (
        number(0..4),
        number(5..7),
        number(8..10),
        number(11..hour_end),
        number(hour_end + 1..hour_end + 3),
        number(hour_end + 4..hour_end + 6),
    ) else {
        return false;
    };
    let days = match month {
        1 | 3 | 5 | 7 | 8 | 10 | 12 => 31,
        4 | 6 | 9 | 11 => 30,
        2 => {
            if year % 4 == 0 && (year % 100 != 0 || year % 400 == 0) {
                29
            } else {
                28
            }
        }
        _ => return false,
    };
    day >= 1 && day <= days && hour < 24 && minute < 60 && second < 60
}
fn notification(value: &Value) -> Result<u64> {
    // The serialized projection has its own tighter bound in Go.
    require(serde_json::to_vec(value).map_err(|_| protocol())?.len() <= 4096)?;
    let unavailable = boolean(value, "unavailable")?;
    let id = counter(value, "id")?;
    require(id > 0)?;
    if unavailable {
        exact(value, "id unavailable", "")?;
        return Ok(id);
    }
    exact(
        value,
        "id ticket author_role created_at title body body_truncated title_truncated unavailable",
        "",
    )?;
    require(counter(value, "ticket")? > 0)?;
    require(matches!(
        string(value, "author_role")?,
        "user" | "ceo" | "pm" | "developer" | "reviewer" | "system"
    ))?;
    require(utc_timestamp(string(value, "created_at")?))?;
    require(string(value, "title")?.len() <= 128 && string(value, "body")?.len() <= 384)?;
    boolean(value, "body_truncated")?;
    boolean(value, "title_truncated")?;
    Ok(id)
}
pub fn decode_notification_page(bytes: &[u8]) -> Result<Value> {
    let value = strict_object(bytes, MAX_NOTIFICATION_PLAINTEXT)?;
    if value.get("error").is_some() {
        exact(&value, "error", "")?;
        require(string(&value, "error")? == "invalid_ack")?;
        return Ok(value);
    }
    exact(
        &value,
        "batch through token notifications more acknowledged",
        "batch token acknowledged",
    )?;
    let through = counter(&value, "through")?;
    let more = boolean(&value, "more")?;
    let notices = value["notifications"].as_array().ok_or_else(protocol)?;
    require(notices.len() <= 50)?;
    for (key, size) in [("batch", 16), ("token", 32), ("acknowledged", 16)] {
        if !value[key].is_null() {
            length(&value, key, size, size)?;
        }
    }
    if notices.is_empty() {
        require(value["batch"].is_null() && value["token"].is_null() && !more)?;
    } else {
        require(!value["batch"].is_null() && !value["token"].is_null())?;
        let mut previous = 0;
        for notice in notices {
            let id = notification(notice)?;
            require(id > previous)?;
            previous = id;
        }
        require(through == previous)?;
    }
    Ok(value)
}
fn encode(value: &Value, decode: fn(&[u8]) -> Result<Value>) -> Result<Vec<u8>> {
    let bytes = serde_json::to_vec(value).map_err(|_| protocol())?;
    decode(&bytes)?;
    Ok(bytes)
}
pub fn encode_control(value: &Value) -> Result<Vec<u8>> {
    encode(value, decode_control)
}
pub fn encode_inner(value: &Value) -> Result<Vec<u8>> {
    encode(value, decode_inner)
}
pub fn encode_plain(value: &Value) -> Result<Vec<u8>> {
    encode(value, decode_plain)
}
pub fn encode_notification_request(value: &Value) -> Result<Vec<u8>> {
    encode(value, decode_notification_request)
}
pub fn encode_notification_envelope(value: &Value) -> Result<Vec<u8>> {
    encode(value, decode_notification_envelope)
}
pub fn encode_notification_page(value: &Value) -> Result<Vec<u8>> {
    encode(value, decode_notification_page)
}
