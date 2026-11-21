use super::*;
use serde_json::json;

fn record(kind: &str, id: u64) -> Value {
    json!({"v":1,"type":kind,"id":id.to_string()})
}
fn opened(m: &mut Machine) -> u64 {
    let id = m.allocate().unwrap();
    assert_eq!(m.next().unwrap(), record("stream.open", id));
    m.dispatch(record("stream.opened", id)).unwrap();
    id
}

#[test]
fn opens_are_sequential_and_data_waits_for_authorization() {
    let mut m = Machine::default();
    assert_eq!(m.allocate().unwrap(), 1);
    assert_eq!(m.allocate().unwrap(), 3);
    assert_eq!(m.next().unwrap(), record("stream.open", 1));
    assert_eq!(m.next().unwrap(), record("stream.open", 3));
    assert!(
        m.dispatch(json!({"v":1,"type":"stream.data","id":"1","data":"YQ"}))
            .is_err()
    );
}

#[test]
fn credit_is_returned_only_on_consumption_and_a_stalled_stream_does_not_block() {
    let mut m = Machine::default();
    let a = opened(&mut m);
    let b = opened(&mut m);
    for _ in 0..4 {
        m.dispatch(
            json!({"v":1,"type":"stream.data","id":a.to_string(),"data":wire::b64(&vec![7;16384])}),
        )
        .unwrap();
    }
    assert!(m.next().is_none());
    m.queue(b, b"other").unwrap();
    let r = m.next().unwrap();
    assert_eq!(r["id"], "3");
    assert_eq!(r["type"], "stream.data");
    let mut bytes = [0; 100];
    assert_eq!(m.read(a, &mut bytes).unwrap(), Some(100));
    assert_eq!(
        m.next().unwrap(),
        json!({"v":1,"type":"stream.credit","id":"1","bytes":"100"})
    );
    assert_eq!(bytes, [7; 100]);
}

#[test]
fn send_credit_supports_partial_grants_and_fin_follows_all_data() {
    let mut m = Machine::default();
    let a = opened(&mut m);
    for _ in 0..4 {
        assert_eq!(m.queue(a, &vec![3; 16384]).unwrap(), 16384);
        assert_eq!(m.next().unwrap()["type"], "stream.data");
    }
    m.queue(a, b"remaining").unwrap();
    m.finish(a);
    assert!(m.next().is_none());
    m.dispatch(json!({"v":1,"type":"stream.credit","id":"1","bytes":"1"}))
        .unwrap();
    assert_eq!(m.next().unwrap()["data"], "cg");
    assert!(m.next().is_none());
    m.dispatch(json!({"v":1,"type":"stream.credit","id":"1","bytes":"8"}))
        .unwrap();
    assert_eq!(m.next().unwrap()["data"], "ZW1haW5pbmc");
    assert_eq!(m.next().unwrap(), record("stream.fin", a));
}

#[test]
fn reset_keeps_credit_snapshot_until_reciprocal_reset_and_discards_crossed_data() {
    let mut m = Machine::default();
    let a = opened(&mut m);
    m.dispatch(json!({"v":1,"type":"stream.data","id":"1","data":wire::b64(&vec![7;16384])}))
        .unwrap();
    m.queue(a, b"pending payload").unwrap();
    m.reset(a, "cancelled");
    // Cancelling retains credit/FIN state, never the payload allocations.
    assert_eq!(m.slots[&a].input.capacity(), 0);
    assert_eq!(m.slots[&a].pending.capacity(), 0);
    assert_eq!(
        m.next().unwrap(),
        json!({"v":1,"type":"stream.reset","id":"1","code":"cancelled"})
    );
    m.sent(a);
    m.dispatch(json!({"v":1,"type":"stream.data","id":"1","data":"YQ"}))
        .unwrap();
    assert_eq!(m.slots.len(), 1);
    assert!(m.next().is_none());
    m.dispatch(json!({"v":1,"type":"stream.reset","id":"1","code":"closed"}))
        .unwrap();
    assert!(m.slots.is_empty());
    assert!(m.dispatch(record("stream.fin", a)).is_err());
}

#[test]
fn refusals_need_no_reset_and_all_cancelled_slots_stay_bounded() {
    let mut m = Machine::default();
    let a = m.allocate().unwrap();
    m.next().unwrap();
    m.dispatch(json!({"v":1,"type":"stream.reset","id":"1","code":"refused"}))
        .unwrap();
    assert!(m.next().is_none());
    assert!(m.slots.is_empty());
    assert_eq!(a, 1);
    for _ in 0..64 {
        let id = opened(&mut m);
        m.reset(id, "cancelled");
        m.next().unwrap();
    }
    assert!(m.allocate().is_err());
    assert_eq!(m.slots.len(), 64);
}

#[test]
fn rejects_credit_overflow_oversend_duplicate_fin_and_traffic_after_reset() {
    for invalid in [
        json!({"v":1,"type":"stream.credit","id":"1","bytes":"1"}),
        record("stream.opened", 1),
        record("stream.fin", 3),
        record("stream.open", 3),
    ] {
        let mut m = Machine::default();
        opened(&mut m);
        assert!(m.dispatch(invalid).is_err());
    }
    let mut m = Machine::default();
    let a = opened(&mut m);
    for _ in 0..4 {
        m.dispatch(json!({"v":1,"type":"stream.data","id":"1","data":wire::b64(&vec![0;16384])}))
            .unwrap();
    }
    assert!(
        m.dispatch(json!({"v":1,"type":"stream.data","id":"1","data":"YQ"}))
            .is_err()
    );
    let mut m = Machine::default();
    opened(&mut m);
    m.dispatch(record("stream.fin", a)).unwrap();
    assert!(m.dispatch(record("stream.fin", a)).is_err());
    assert!(
        m.dispatch(json!({"v":1,"type":"stream.data","id":"1","data":"YQ"}))
            .is_err()
    );
}

#[test]
fn half_close_keeps_opposite_direction_and_final_credit_valid() {
    let mut m = Machine::default();
    let a = opened(&mut m);
    m.queue(a, b"request").unwrap();
    m.next().unwrap();
    m.finish(a);
    assert_eq!(m.next().unwrap(), record("stream.fin", a));
    m.dispatch(json!({"v":1,"type":"stream.data","id":"1","data":"cmVzcG9uc2U"}))
        .unwrap();
    m.dispatch(record("stream.fin", a)).unwrap();
    let mut bytes = [0; 8];
    assert_eq!(m.read(a, &mut bytes).unwrap(), Some(8));
    assert_eq!(&bytes, b"response");
    assert_eq!(m.next().unwrap()["bytes"], "8");
    m.sent(a);
    assert_eq!(m.slots.len(), 1);
    m.dispatch(json!({"v":1,"type":"stream.credit","id":"1","bytes":"7"}))
        .unwrap();
    assert!(m.slots.is_empty());
}

#[test]
fn ready_streams_take_turns_and_pending_output_is_bounded() {
    let mut m = Machine::default();
    let a = opened(&mut m);
    let b = opened(&mut m);
    assert_eq!(m.queue(a, &vec![4; 100000]).unwrap(), 16384);
    assert_eq!(m.queue(a, b"full").unwrap(), 0);
    m.queue(b, b"b").unwrap();
    assert_eq!(m.next().unwrap()["id"], "1");
    m.queue(a, b"again").unwrap();
    assert_eq!(m.next().unwrap()["id"], "3");
    assert_eq!(m.next().unwrap()["id"], "1");
}

#[test]
fn peer_reset_is_reciprocated_once_and_crossed_fin_still_forbids_data() {
    let mut m = Machine::default();
    let a = opened(&mut m);
    m.dispatch(json!({"v":1,"type":"stream.reset","id":"1","code":"cancelled"}))
        .unwrap();
    assert!(m.read(a, &mut [0]).is_err());
    assert_eq!(
        m.next().unwrap(),
        json!({"v":1,"type":"stream.reset","id":"1","code":"closed"})
    );
    m.sent(a);
    assert!(m.slots.is_empty());
    assert!(m.next().is_none());
    let mut m = Machine::default();
    opened(&mut m);
    m.reset(a, "cancelled");
    m.next().unwrap();
    m.dispatch(record("stream.fin", a)).unwrap();
    assert!(
        m.dispatch(json!({"v":1,"type":"stream.data","id":"1","data":"YQ"}))
            .is_err()
    );
}

// Exercise the public owner's Drop, not just Machine::reset.
fn owned(m: Machine, id: u64) -> RelayStream {
    let failed = m.slots[&id].failed.clone();
    RelayStream {
        shared: Arc::new(Shared {
            machine: Mutex::new(m),
            wake: Notify::new(),
        }),
        id,
        failed,
    }
}
fn transmit(m: &mut Machine) -> Value {
    let r = m.next().expect("ready record");
    m.sent(r["id"].as_str().unwrap().parse().unwrap());
    r
}
fn grant(id: u64, bytes: usize) -> Value {
    json!({"v":1,"type":"stream.credit","id":id.to_string(),"bytes":bytes.to_string()})
}

#[test]
fn dropped_stream_with_peer_fin_and_uncredited_data_uses_reset_completion() {
    let mut m = Machine::default();
    let id = opened(&mut m);
    m.sent(id);
    m.queue(id, b"crossed request").unwrap();
    assert_eq!(transmit(&mut m)["type"], "stream.data");
    m.dispatch(record("stream.fin", id)).unwrap();
    // A fully closed peer need not grant any more consumption credit.
    let stream = owned(m, id);
    let shared = stream.shared.clone();
    drop(stream);
    let mut m = shared.machine.lock().unwrap();
    assert_eq!(transmit(&mut m)["type"], "stream.reset");
    assert_eq!(m.slots.len(), 1, "retain until reciprocal reset");
    m.dispatch(json!({"v":1,"type":"stream.reset","id":id.to_string(),"code":"closed"}))
        .unwrap();
    assert!(m.slots.is_empty());
    assert!(m.order.is_empty());
}

#[test]
fn dropped_stream_cancels_pending_data_beyond_credit_and_reuses_capacity() {
    let mut m = Machine::default();
    let other = opened(&mut m);
    m.sent(other);
    for iteration in 0..100 {
        let id = opened(&mut m);
        m.sent(id);
        for _ in 0..4 {
            m.queue(id, &vec![1; wire::MAX_STREAM_DATA]).unwrap();
            transmit(&mut m);
        }
        assert_eq!(m.slots[&id].send_credit, 0);
        if iteration % 2 == 0 {
            m.dispatch(grant(id, 1)).unwrap();
        }
        m.queue(id, b"cannot flush this without more credit")
            .unwrap();
        assert!(m.slots[&id].pending.len() > m.slots[&id].send_credit);
        m.dispatch(record("stream.fin", id)).unwrap();
        let stream = owned(m, id);
        let shared = stream.shared.clone();
        drop(stream);
        m = std::mem::take(&mut *shared.machine.lock().unwrap());
        assert_eq!(m.slots[&id].pending.capacity(), 0);
        assert!(m.queue(id, b"closed").is_err());
        assert_eq!(transmit(&mut m)["type"], "stream.reset");
        // A valid grant can cross cancellation, but cannot revive its writer.
        m.dispatch(grant(id, 1)).unwrap();
        assert!(m.next().is_none());
        m.dispatch(json!({"v":1,"type":"stream.reset","id":id.to_string(),"code":"closed"}))
            .unwrap();
        assert_eq!(m.slots.len(), 1);
        assert!(!m.failed);
    }
    m.queue(other, b"alive").unwrap();
    assert_eq!(transmit(&mut m)["data"], wire::b64(b"alive"));
}

#[test]
fn peer_fin_keeps_live_outbound_data_and_half_close_waiting_for_credit() {
    let mut m = Machine::default();
    let id = opened(&mut m);
    m.sent(id);
    for _ in 0..4 {
        m.queue(id, &vec![1; wire::MAX_STREAM_DATA]).unwrap();
        transmit(&mut m);
    }
    m.dispatch(record("stream.fin", id)).unwrap();
    m.queue(id, b"still valid").unwrap();
    let mut stream = owned(m, id);
    let shared = stream.shared.clone();
    let mut cx = Context::from_waker(Waker::noop());
    assert!(Pin::new(&mut stream).poll_shutdown(&mut cx).is_pending());
    {
        let mut m = shared.machine.lock().unwrap();
        assert!(m.next().is_none());
        m.dispatch(grant(id, 11)).unwrap();
        assert_eq!(transmit(&mut m)["data"], wire::b64(b"still valid"));
        assert_eq!(transmit(&mut m), record("stream.fin", id));
    }
    assert!(Pin::new(&mut stream).poll_shutdown(&mut cx).is_ready());
    // After FIN and all receive grants, cancellation could cross the peer's
    // graceful retirement. Leave the final valid credit exchange intact.
    drop(stream);
    let mut m = shared.machine.lock().unwrap();
    assert!(m.next().is_none());
    m.dispatch(grant(id, CREDIT)).unwrap();
    assert!(m.slots.is_empty());
}

#[test]
fn clean_response_drop_finishes_and_preserves_final_receive_credit() {
    let mut m = Machine::default();
    let id = opened(&mut m);
    m.sent(id);
    m.dispatch(
        json!({"v":1,"type":"stream.data","id":id.to_string(),"data":wire::b64(b"response")}),
    )
    .unwrap();
    m.dispatch(record("stream.fin", id)).unwrap();
    assert_eq!(m.read(id, &mut [0; 8]).unwrap(), Some(8));
    let stream = owned(m, id);
    let shared = stream.shared.clone();
    drop(stream);
    let mut m = shared.machine.lock().unwrap();
    assert_eq!(transmit(&mut m), grant(id, 8));
    assert_eq!(transmit(&mut m), record("stream.fin", id));
    assert!(m.slots.is_empty());
}

#[test]
fn drop_after_shutdown_drains_peer_data_without_a_late_reset() {
    let mut m = Machine::default();
    let id = opened(&mut m);
    m.sent(id);
    m.finish(id);
    assert_eq!(transmit(&mut m), record("stream.fin", id));
    let stream = owned(m, id);
    let shared = stream.shared.clone();
    drop(stream);
    let mut m = shared.machine.lock().unwrap();
    assert!(
        m.next().is_none(),
        "FIN already committed the graceful close"
    );
    m.dispatch(json!({"v":1,"type":"stream.data","id":id.to_string(),"data":"YQ"}))
        .unwrap();
    assert!(m.slots[&id].input.is_empty());
    assert_eq!(transmit(&mut m), grant(id, 1));
    m.dispatch(record("stream.fin", id)).unwrap();
    assert!(m.slots.is_empty());
}

#[test]
fn final_fin_and_reset_retain_slots_until_the_writer_completes() {
    for reset in [false, true] {
        let mut m = Machine::default();
        let id = opened(&mut m);
        m.sent(id);
        if reset {
            m.reset(id, "cancelled");
        } else {
            m.finish(id);
        }
        let r = m.next().unwrap();
        assert_eq!(r["type"], if reset { "stream.reset" } else { "stream.fin" });
        m.dispatch(if reset {
            json!({"v":1,"type":"stream.reset","id":id.to_string(),"code":"closed"})
        } else {
            record("stream.fin", id)
        })
        .unwrap();
        assert_eq!(m.slots.len(), 1, "selected record is still being sent");
        m.sent(id);
        assert!(m.slots.is_empty());
    }
}
