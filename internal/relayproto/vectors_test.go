package relayproto

import (
	"bytes"
	"crypto/mlkem"
	"crypto/mlkem/mlkemtest"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func readFixture(t *testing.T, path string, v any) {
	t.Helper()
	b, e := os.ReadFile("testdata/" + path)
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, v); e != nil {
		t.Fatal(e)
	}
}
func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, e := hex.DecodeString(s)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestNISTMLKEMVectors(t *testing.T) {
	type vector struct {
		TCID                  int
		D, Z, EK, DK, C, K, M string
		TestPassed            bool
	}
	type group struct{ Tests []vector }
	var data struct {
		KeyGen        group `json:"keyGen"`
		Encapsulation group `json:"encapsulation"`
		KeyCheck      group `json:"encapsulationKeyCheck"`
	}
	readFixture(t, "mlkem768-acvp.json", &data)
	for _, v := range data.KeyGen.Tests {
		if _, e := ImportIdentity(unhex(t, v.DK)); e == nil {
			t.Fatal("expanded FIPS key accepted as local seed")
		}
		id, e := ImportIdentity(unhex(t, v.D+v.Z))
		if e != nil || !bytes.Equal(id.PublicKey(), unhex(t, v.EK)) {
			t.Fatalf("keygen tc %d: %v", v.TCID, e)
		}
	}
	for _, v := range data.Encapsulation.Tests {
		pk, e := mlkem.NewEncapsulationKey768(unhex(t, v.EK))
		if e != nil {
			t.Fatal(e)
		}
		ss, ct, e := mlkemtest.Encapsulate768(pk, unhex(t, v.M))
		if e != nil || !bytes.Equal(ss, unhex(t, v.K)) || !bytes.Equal(ct, unhex(t, v.C)) {
			t.Fatalf("encapsulation tc %d: %v", v.TCID, e)
		}
	}
	for _, v := range data.KeyCheck.Tests {
		if valid := ValidatePublicKey(unhex(t, v.EK)) == nil; valid != v.TestPassed {
			t.Fatalf("key validation tc %d", v.TCID)
		}
	}
}
func TestFixedProtocolVectors(t *testing.T) {
	var v map[string]string
	readFixture(t, "protocol-v1.json", &v)
	b := func(name string) []byte { return unhex(t, v[name]) }
	same := func(name string, got []byte) {
		t.Helper()
		if !bytes.Equal(got, b(name)) {
			t.Fatalf("%s mismatch\ngot %x\nwant %s", name, got, v[name])
		}
	}
	host, e := ImportIdentity(b("host_seed"))
	if e != nil {
		t.Fatal(e)
	}
	ui, e := ImportIdentity(b("ui_seed"))
	if e != nil {
		t.Fatal(e)
	}
	same("host_pk", host.PublicKey())
	for _, role := range []string{"host", "ui"} {
		pk, e := mlkem.NewEncapsulationKey768(b(role + "_pk"))
		if e != nil {
			t.Fatal(e)
		}
		ss, ct, e := mlkemtest.Encapsulate768(pk, b(role+"_kem_random"))
		if e != nil {
			t.Fatal(e)
		}
		same(role+"_ss", ss)
		same(role+"_ct", ct)
	}
	same("ui_pk", ui.PublicKey())
	p := PairTranscript{Route: testRoute(), AID: b("aid"), Window: b("window"), PairEpoch: b("epoch"), C: b("commitment"), PKU: ui.PublicKey(), RU: b("ru"), PKH: host.PublicKey(), RH: b("rh")}
	c, e := PairCommitment(p.Route, p.AID, p.PKU, p.RU)
	if e != nil {
		t.Fatal(e)
	}
	same("commitment", c)
	pt, e := p.Encode()
	if e != nil {
		t.Fatal(e)
	}
	same("pair_transcript", pt)
	ph, e := p.Hash()
	if e != nil {
		t.Fatal(e)
	}
	same("pair_hash", ph)
	pin, e := p.PIN()
	if e != nil || pin != v["pin"] || pin[0] != '0' {
		t.Fatal("leading-zero PIN", pin, e)
	}
	ac, e := p.AuthContext()
	if e != nil {
		t.Fatal(e)
	}
	bc, e := ac.Encode()
	if e != nil {
		t.Fatal(e)
	}
	same("auth_context", bc)
	ssH, e := host.key.Decapsulate(b("host_ct"))
	if e != nil {
		t.Fatal(e)
	}
	same("host_ss", ssH)
	ssU, e := ui.key.Decapsulate(b("ui_ct"))
	if e != nil {
		t.Fatal(e)
	}
	same("ui_ss", ssU)
	a := deriveAuth(ac, Inner{NU: b("nu"), CTH: b("host_ct")}, Inner{NH: b("nh"), CTU: b("ui_ct")}, ssH, ssU, "ui")
	same("auth_transcript", a.transcript)
	same("prk", a.prk)
	same("proofH", hostProof(a.prk, a.transcript))
	same("proofU", uiProof(a.prk, a.transcript, b("proofH")))
	root, e := a.NotificationRoot()
	if e != nil {
		t.Fatal(e)
	}
	same("notification_root", root)
	s, e := a.NewSession()
	if e != nil {
		t.Fatal(e)
	}
	same("record_aad", s.aad("ui-host", 0))
	same("record_key_ui_host", expand(a.prk, EncodeFields([]byte(label+"record-ui-host")), 32))
	same("record_key_host_ui", expand(a.prk, EncodeFields([]byte(label+"record-host-ui")), 32))
	plain, e := EncodePlain(PlainRecord{V: 1, Type: "stream.open", ID: 1})
	if e != nil {
		t.Fatal(e)
	}
	same("record_plaintext", plain)
	r, e := s.Seal(PlainRecord{V: 1, Type: "stream.open", ID: 1})
	if e != nil {
		t.Fatal(e)
	}
	same("record_ciphertext", r.Ciphertext)
	wire, e := EncodeInner(r)
	if e != nil || string(wire) != v["record_wire"] {
		t.Fatal("record wire mismatch", e)
	}
	nc := NotificationContext{Route: testRoute(), HostFP: v["host_fp"], UIID: v["ui_id"], PairEpoch: b("epoch")}
	same("notification_request_key", requestKey(nc, root))
	same("notification_aad", responseAAD(nc, b("request"), b("reply")))
	same("notification_response_key", kdf(root, hashBytes(b("notification_aad")), "notify-response"))
	pagePlain, e := EncodeNotificationPage(NotificationPage{Through: 99, Notifications: []Notification{}})
	if e != nil {
		t.Fatal(e)
	}
	same("notification_plaintext", pagePlain)
	poll, e := NewPoll(nc, root, b("request"), nil)
	if e != nil {
		t.Fatal(e)
	}
	if poll.RequestHeader != v["request_header"] || poll.RequestMAC != v["request_mac"] {
		t.Fatal("poll vector mismatch")
	}
	env, e := SealNotification(nc, root, b("request"), NotificationPage{Through: 99, Notifications: []Notification{}}, bytes.NewReader(b("reply")))
	if e != nil {
		t.Fatal(e)
	}
	same("notification_ciphertext", env.Ciphertext)
	if _, e = OpenNotification(nc, root, b("request"), env); e != nil {
		t.Fatal(e)
	}
}

func TestNegativeWireVectors(t *testing.T) {
	var cases []struct{ Layer, Reason, JSON string }
	readFixture(t, "negative-v1.json", &cases)
	for _, v := range cases {
		t.Run(v.Reason, func(t *testing.T) {
			var e error
			switch v.Layer {
			case "control":
				_, e = DecodeControl([]byte(v.JSON))
			case "inner":
				_, e = DecodeInner([]byte(v.JSON))
			case "plain":
				_, e = DecodePlain([]byte(v.JSON))
			case "page":
				_, e = DecodeNotificationPage([]byte(v.JSON))
			default:
				t.Fatal("unknown fixture layer")
			}
			if e == nil {
				t.Fatal("invalid wire fixture accepted")
			}
		})
	}
}
