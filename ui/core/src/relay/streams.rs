//! UI-side byte streams. No application read or write runs in the record loops.
use super::{RelaySession, SessionControl, wire};
use crate::Result;
use serde_json::{Value, json};
use std::{
    collections::{BTreeMap, VecDeque},
    io,
    pin::Pin,
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, Ordering},
    },
    task::{Context, Poll, Waker},
};
use tokio::{
    io::{AsyncRead, AsyncWrite, ReadBuf},
    sync::{Notify, watch},
};
use tokio_util::sync::CancellationToken;

const CREDIT: usize = wire::INITIAL_CREDIT as usize;
const MAX_STREAMS: usize = 64;
fn closed() -> io::Error {
    io::Error::new(io::ErrorKind::ConnectionAborted, "Relay stream closed.")
}
fn protocol() -> String {
    "Relay stream protocol violation.".into()
}

#[derive(Default)]
struct Slot {
    open_sent: bool,
    opened: bool,
    input: VecDeque<u8>,
    pending: VecDeque<u8>,
    send_credit: usize,
    recv_credit: usize,
    credit: usize,
    fin_queued: bool,
    local_fin: bool,
    remote_fin: bool,
    draining: bool,
    reset: Option<&'static str>,
    resetting: bool,
    reset_sent: bool,
    peer_reset: bool,
    sending: bool,
    failed: Arc<AtomicBool>,
    reader: Option<Waker>,
    writer: Option<Waker>,
}
impl Slot {
    fn wake(&mut self) {
        if let Some(w) = self.reader.take() {
            w.wake();
        }
        if let Some(w) = self.writer.take() {
            w.wake();
        }
    }
    fn reset(&mut self, code: &'static str) {
        if self.resetting {
            return;
        }
        self.resetting = true;
        self.reset = Some(code);
        self.failed.store(true, Ordering::Release);
        self.input = VecDeque::new();
        self.pending = VecDeque::new();
        self.credit = 0;
        self.wake();
    }
    fn retired(&self) -> bool {
        if self.sending {
            return false;
        }
        if self.resetting {
            return self.reset_sent && self.peer_reset;
        }
        self.local_fin
            && self.remote_fin
            && self.input.is_empty()
            && self.credit == 0
            && self.recv_credit == CREDIT
            && self.send_credit == CREDIT
    }
}
struct Machine {
    slots: BTreeMap<u64, Slot>,
    order: VecDeque<u64>,
    next_id: u64,
    failed: bool,
}
impl Default for Machine {
    fn default() -> Self {
        Self {
            slots: BTreeMap::new(),
            order: VecDeque::new(),
            next_id: 1,
            failed: false,
        }
    }
}
impl Machine {
    fn allocate(&mut self) -> Result<u64> {
        if self.failed || self.next_id == 0 || self.slots.len() >= MAX_STREAMS {
            return Err("Relay stream capacity unavailable.".into());
        }
        let id = self.next_id;
        self.next_id = id.checked_add(2).unwrap_or(0);
        self.slots.insert(
            id,
            Slot {
                send_credit: CREDIT,
                recv_credit: CREDIT,
                ..Slot::default()
            },
        );
        self.order.push_back(id);
        Ok(id)
    }
    fn retire(&mut self, id: u64) {
        if self.slots.get(&id).is_some_and(Slot::retired) {
            if let Some(mut s) = self.slots.remove(&id) {
                s.wake();
            }
            self.order.retain(|i| *i != id);
        }
    }
    fn fail(&mut self) {
        self.failed = true;
        for s in self.slots.values_mut() {
            s.reset("closed");
        }
        self.slots.clear();
        self.order.clear();
    }
    fn reset(&mut self, id: u64, code: &'static str) {
        if let Some(s) = self.slots.get_mut(&id) {
            s.reset(code);
        }
    }
    fn finish(&mut self, id: u64) {
        if let Some(s) = self.slots.get_mut(&id) {
            s.fin_queued = true;
        }
    }
    fn queue(&mut self, id: u64, bytes: &[u8]) -> io::Result<usize> {
        let s = self.slots.get_mut(&id).ok_or_else(closed)?;
        if s.resetting || s.fin_queued || !s.opened {
            return Err(closed());
        }
        let n = bytes
            .len()
            .min(wire::MAX_STREAM_DATA.saturating_sub(s.pending.len()));
        s.pending.extend(&bytes[..n]);
        Ok(n)
    }
    fn read(&mut self, id: u64, bytes: &mut [u8]) -> io::Result<Option<usize>> {
        if self.failed {
            return Err(closed());
        }
        let Some(s) = self.slots.get_mut(&id) else {
            return Ok(Some(0));
        };
        if s.resetting {
            return Err(closed());
        }
        let n = bytes.len().min(s.input.len());
        if n > 0 {
            for b in &mut bytes[..n] {
                *b = s.input.pop_front().unwrap();
            }
            s.credit += n;
            return Ok(Some(n));
        }
        Ok(if s.remote_fin || bytes.is_empty() {
            Some(0)
        } else {
            None
        })
    }
    fn dispatch(&mut self, r: Value) -> Result<()> {
        wire::encode_plain(&r)?;
        let id = r["id"]
            .as_str()
            .and_then(|s| s.parse::<u64>().ok())
            .ok_or_else(protocol)?;
        let s = self.slots.get_mut(&id).ok_or_else(protocol)?;
        if !s.open_sent || s.peer_reset {
            return Err(protocol());
        }
        let kind = r["type"].as_str().ok_or_else(protocol)?;
        if !s.opened {
            match kind {
                "stream.opened" => s.opened = true,
                "stream.reset" => {
                    s.failed.store(true, Ordering::Release);
                    s.wake();
                    self.slots.remove(&id);
                    self.order.retain(|i| *i != id);
                    return Ok(());
                }
                _ => return Err(protocol()),
            }
        } else {
            match kind {
                "stream.data" => {
                    let data = wire::unb64(r["data"].as_str().ok_or_else(protocol)?)?;
                    if s.remote_fin || data.len() > s.recv_credit {
                        return Err(protocol());
                    }
                    s.recv_credit -= data.len();
                    if !s.resetting {
                        if s.draining {
                            s.credit += data.len();
                        } else {
                            s.input.extend(data);
                        }
                    }
                }
                "stream.credit" => {
                    let n = r["bytes"]
                        .as_str()
                        .and_then(|s| s.parse::<u64>().ok())
                        .ok_or_else(protocol)?;
                    if n > (CREDIT - s.send_credit) as u64 {
                        return Err(protocol());
                    }
                    s.send_credit += n as usize;
                }
                "stream.fin" => {
                    if s.remote_fin {
                        return Err(protocol());
                    }
                    s.remote_fin = true;
                }
                "stream.reset" => {
                    s.peer_reset = true;
                    s.reset("closed");
                }
                _ => return Err(protocol()),
            }
        }
        s.wake();
        self.retire(id);
        Ok(())
    }
    fn next(&mut self) -> Option<Value> {
        for _ in 0..self.order.len() {
            let id = self.order.pop_front()?;
            self.order.push_back(id);
            let s = self.slots.get_mut(&id)?;
            let mut r = json!({"v":1,"id":id.to_string()});
            if !s.open_sent {
                s.open_sent = true;
                r["type"] = json!("stream.open");
            } else if !s.opened {
                continue;
            } else if let Some(code) = s.reset.take() {
                s.reset_sent = true;
                r["type"] = json!("stream.reset");
                r["code"] = json!(code);
            } else if s.resetting {
                continue;
            } else if s.credit > 0 {
                let n = std::mem::take(&mut s.credit);
                s.recv_credit += n;
                r["type"] = json!("stream.credit");
                r["bytes"] = json!(n.to_string());
            } else if !s.pending.is_empty() && s.send_credit > 0 {
                let n = s.pending.len().min(s.send_credit);
                let data: Vec<u8> = s.pending.drain(..n).collect();
                s.send_credit -= n;
                r["type"] = json!("stream.data");
                r["data"] = json!(wire::b64(&data));
            } else if s.fin_queued && !s.local_fin && s.pending.is_empty() {
                s.local_fin = true;
                r["type"] = json!("stream.fin");
            } else {
                continue;
            }
            s.sending = true;
            return Some(r);
        }
        None
    }
    fn sent(&mut self, id: u64) {
        if let Some(s) = self.slots.get_mut(&id) {
            s.sending = false;
            s.wake();
        }
        self.retire(id);
    }
}
struct Shared {
    machine: Mutex<Machine>,
    wake: Notify,
}
struct Owner {
    shared: Arc<Shared>,
    control: SessionControl,
    cancel: CancellationToken,
    done: watch::Receiver<bool>,
}
impl Drop for Owner {
    fn drop(&mut self) {
        self.cancel.cancel();
        self.control.cancel();
    }
}
/// Owns one authenticated session, one receiver and one fair encrypted writer.
#[derive(Clone)]
pub struct StreamMux {
    owner: Arc<Owner>,
}
impl StreamMux {
    pub fn new(session: RelaySession) -> Self {
        let control = session.control();
        let (sender, mut receiver) = session.split();
        let shared = Arc::new(Shared {
            machine: Mutex::new(Machine::default()),
            wake: Notify::new(),
        });
        let running = shared.clone();
        let cancel = CancellationToken::new();
        let stopping = cancel.clone();
        let (done, done_rx) = watch::channel(false);
        let closing = control.clone();
        tokio::spawn(async move {
            let read = async {
                loop {
                    let r = receiver.receive().await?;
                    running.machine.lock().unwrap().dispatch(r)?;
                    running.wake.notify_one();
                }
                #[allow(unreachable_code)]
                Ok::<(), String>(())
            };
            let write = async {
                loop {
                    // Register before inspecting queues, so no wake is lost.
                    let notified = running.wake.notified();
                    let r = running.machine.lock().unwrap().next();
                    if let Some(r) = r {
                        let id = r["id"].as_str().unwrap().parse().unwrap();
                        sender.send(r).await?;
                        running.machine.lock().unwrap().sent(id);
                    } else {
                        notified.await;
                    }
                }
                #[allow(unreachable_code)]
                Ok::<(), String>(())
            };
            tokio::select! {_=stopping.cancelled()=>{}, _=read=>{}, _=write=>{}}
            running.machine.lock().unwrap().fail();
            closing.close().await;
            done.send_replace(true);
        });
        Self {
            owner: Arc::new(Owner {
                shared,
                control,
                cancel,
                done: done_rx,
            }),
        }
    }
    /// Cancellation of this future resets its allocated stream, including an
    /// open whose acknowledgement is still in flight.
    pub async fn open(&self) -> Result<RelayStream> {
        let shared = self.owner.shared.clone();
        let (id, failed) = {
            let mut m = shared.machine.lock().unwrap();
            let id = m.allocate()?;
            (id, m.slots[&id].failed.clone())
        };
        let stream = RelayStream { shared, id, failed };
        stream.shared.wake.notify_one();
        futures_util::future::poll_fn(|cx| -> Poll<Result<()>> {
            let mut m = stream.shared.machine.lock().unwrap();
            if m.failed || stream.failed.load(Ordering::Acquire) {
                return Poll::Ready(Err("Relay stream refused.".into()));
            }
            let Some(s) = m.slots.get_mut(&id) else {
                return Poll::Ready(Err("Relay stream closed.".into()));
            };
            if s.opened {
                Poll::Ready(Ok(()))
            } else {
                s.writer = Some(cx.waker().clone());
                Poll::Pending
            }
        })
        .await?;
        Ok(stream)
    }
    /// Completes only after the record loops and underlying socket have ended.
    pub async fn closed(&self) {
        let mut done = self.owner.done.clone();
        while !*done.borrow_and_update() {
            if done.changed().await.is_err() {
                break;
            }
        }
    }
    pub async fn close(&self) {
        self.owner.cancel.cancel();
        self.closed().await;
    }
}
/// One bounded HTTP/1.1 byte connection. Shutdown half-closes; Drop cancels
/// unfinished work or drains the final credit exchange after a committed FIN.
pub struct RelayStream {
    shared: Arc<Shared>,
    id: u64,
    failed: Arc<AtomicBool>,
}
impl Drop for RelayStream {
    fn drop(&mut self) {
        let mut m = self.shared.machine.lock().unwrap();
        if let Some(s) = m.slots.get_mut(&self.id) {
            if s.input.is_empty()
                && (s.local_fin
                    || (s.remote_fin && s.pending.is_empty() && s.send_credit == CREDIT))
            {
                // FIN already committed a graceful close, or all outbound work
                // completed. Drain crossed input and its final credit exchange:
                // a late reset could otherwise target a retired peer slot.
                s.draining = true;
                s.fin_queued = true;
            } else {
                // Peer FIN alone does not promise consumption of our data.
                // Cancel abandoned work instead of waiting forever for credit.
                m.reset(self.id, "cancelled");
            }
        }
        self.shared.wake.notify_one();
    }
}
impl AsyncRead for RelayStream {
    fn poll_read(
        self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &mut ReadBuf<'_>,
    ) -> Poll<io::Result<()>> {
        if self.failed.load(Ordering::Acquire) {
            return Poll::Ready(Err(closed()));
        }
        let mut m = self.shared.machine.lock().unwrap();
        match m.read(self.id, buf.initialize_unfilled()) {
            Ok(Some(n)) => {
                buf.advance(n);
                self.shared.wake.notify_one();
                Poll::Ready(Ok(()))
            }
            Ok(None) => {
                m.slots.get_mut(&self.id).unwrap().reader = Some(cx.waker().clone());
                Poll::Pending
            }
            Err(e) => Poll::Ready(Err(e)),
        }
    }
}
impl AsyncWrite for RelayStream {
    fn poll_write(
        self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        bytes: &[u8],
    ) -> Poll<io::Result<usize>> {
        if self.failed.load(Ordering::Acquire) {
            return Poll::Ready(Err(closed()));
        }
        let mut m = self.shared.machine.lock().unwrap();
        match m.queue(self.id, bytes) {
            Ok(0) if !bytes.is_empty() => {
                m.slots.get_mut(&self.id).unwrap().writer = Some(cx.waker().clone());
                Poll::Pending
            }
            result => {
                self.shared.wake.notify_one();
                Poll::Ready(result)
            }
        }
    }
    fn poll_flush(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        if self.failed.load(Ordering::Acquire) {
            return Poll::Ready(Err(closed()));
        }
        let mut m = self.shared.machine.lock().unwrap();
        if m.failed {
            return Poll::Ready(Err(closed()));
        }
        let Some(s) = m.slots.get_mut(&self.id) else {
            return Poll::Ready(Ok(()));
        };
        if s.pending.is_empty() && !s.sending {
            Poll::Ready(Ok(()))
        } else {
            s.writer = Some(cx.waker().clone());
            Poll::Pending
        }
    }
    fn poll_shutdown(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        if self.failed.load(Ordering::Acquire) {
            return Poll::Ready(Err(closed()));
        }
        let mut m = self.shared.machine.lock().unwrap();
        if m.failed {
            return Poll::Ready(Err(closed()));
        }
        m.finish(self.id);
        self.shared.wake.notify_one();
        let Some(s) = m.slots.get_mut(&self.id) else {
            return Poll::Ready(Ok(()));
        };
        if s.local_fin && !s.sending {
            Poll::Ready(Ok(()))
        } else {
            s.writer = Some(cx.waker().clone());
            Poll::Pending
        }
    }
}
#[cfg(test)]
#[path = "streams_tests.rs"]
mod tests;
