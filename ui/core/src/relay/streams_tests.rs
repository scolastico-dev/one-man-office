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
