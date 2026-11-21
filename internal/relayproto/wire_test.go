package relayproto

import (
	"bytes"
	"strings"
	"testing"
)

func TestControlCodec(t *testing.T) {
	b, e := EncodeControl(Control{V: 1, Type: "host.register", HostID: "office"})
	if e != nil || string(b) != `{"host_id":"office","type":"host.register","v":1}` {
		t.Fatal(string(b), e)
	}
	c, e := DecodeControl(b)
	if e != nil || c.HostID != "office" {
		t.Fatal(c, e)
	}
	for _, raw := range []string{
		`{"v":1,"type":"host.register","host_id":"office","host_id":"other"}`,
		`{"v":1,"V":1,"type":"host.register","host_id":"office"}`,
		`{"v":1,"type":"host.register","host_id":"office","connection":"AAAAAAAAAAAAAAAAAAAAAA"}`,
		`{"v":1,"type":"host.register","host_id":null}`,
		`{"v":1,"type":"host.register"}`, `{"v":2,"type":"host.register","host_id":"office"}`,
		`{"v":1.0,"type":"host.register","host_id":"office"}`,
		`{"v":1,"type":"HOST.REGISTER","host_id":"office"}`,
		`{"v":1,"type":"host.register","host_id":"\ud800"}`,
		`{"v":1,"type":"host.register","host_id":"office"} {}`,
	} {
		if _, e := DecodeControl([]byte(raw)); e == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	raw := append([]byte(`{"v":1,"type":"host.register","host_id":"`), 255)
	raw = append(raw, []byte(`"}`)...)
	if _, e := DecodeControl(raw); e == nil {
		t.Fatal("invalid UTF8")
	}
	if _, e := EncodeControl(Control{V: 1, Type: "host.register", HostID: "office", Connection: make(Bytes, 16)}); e == nil {
		t.Fatal("cross-type field")
	}
	if _, e := DecodeControl(bytes.Repeat([]byte{' '}, MaxControlSize+1)); e == nil {
		t.Fatal("size limit")
	}
}
func TestInnerAndPlainCodecs(t *testing.T) {
	b, e := EncodeInner(Inner{V: 1, Type: "pair.commit", AID: make(Bytes, 16), C: make(Bytes, 48)})
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), "pkU") || strings.Contains(string(b), "ru") {
		t.Fatal("commit leaks reveal")
	}
	if _, e := DecodeInner(b); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{
		`{"v":1,"type":"pair.commit","aid":"AA","C":"AA"}`,
		`{"v":1,"type":"pair.commit","aid":"AAAAAAAAAAAAAAAAAAAAAA","C":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","pkU":"AA"}`,
		`{"v":1,"type":"record","session":"AA","seq":"00","ciphertext":"AA"}`,
		`{"v":1,"type":"stream.open","id":"1"}`,
	} {
		if _, e := DecodeInner([]byte(raw)); e == nil {
			t.Fatal("invalid inner", raw)
		}
	}
	for _, p := range []PlainRecord{
		{V: 1, Type: "stream.open", ID: 1}, {V: 1, Type: "stream.data", ID: 3, Data: Bytes{1}},
		{V: 1, Type: "stream.credit", ID: 1, Bytes: 65536}, {V: 1, Type: "stream.reset", ID: 1, Code: "cancelled"},
		{V: 1, Type: "ready.ack", PairEpoch: make(Bytes, 16)},
	} {
		b, e := EncodePlain(p)
		if e != nil {
			t.Fatal(p, e)
		}
		if _, e := DecodePlain(b); e != nil {
			t.Fatal(e)
		}
	}
	for _, p := range []PlainRecord{
		{V: 1, Type: "stream.open", ID: 2}, {V: 1, Type: "stream.open", ID: 0},
		{V: 1, Type: "stream.data", ID: 1}, {V: 1, Type: "stream.data", ID: 1, Data: make(Bytes, 16385)},
		{V: 1, Type: "stream.credit", ID: 1, Bytes: 65537}, {V: 1, Type: "stream.credit", ID: 1},
		{V: 1, Type: "stream.reset", ID: 1, Code: "secret error"},
	} {
		if _, e := EncodePlain(p); e == nil {
			t.Fatal("invalid plain", p)
		}
	}
}
