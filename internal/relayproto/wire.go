package relayproto

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	MaxControlSize           = 1 << 20
	MaxInnerSize             = 64 << 10
	MaxNotificationSize      = 512 << 10
	MaxNotificationPlaintext = 256 << 10
	MaxStreamData            = 16 << 10
	MaxStreams               = 64
	InitialCredit            = 65536
)

// Control is the relay-visible discriminated union. Codecs reject fields that
// belong to another type. Payload and Body are opaque to the relay.
type Control struct {
	V             uint16 `json:"v"`
	Type          string `json:"type"`
	HostID        string `json:"host_id"`
	Registration  Bytes  `json:"registration"`
	Connection    Bytes  `json:"connection"`
	Payload       Bytes  `json:"payload"`
	Code          string `json:"code"`
	QID           Bytes  `json:"qid"`
	UIID          string `json:"ui_id"`
	RequestHeader string `json:"request_header"`
	RequestMAC    string `json:"request_mac"`
	Body          Bytes  `json:"body"`
}

// Inner is the endpoint handshake/encrypted-record discriminated union.
type Inner struct {
	V          uint16  `json:"v"`
	Type       string  `json:"type"`
	AID        Bytes   `json:"aid"`
	C          Bytes   `json:"C"`
	Window     Bytes   `json:"window"`
	PairEpoch  Bytes   `json:"pair_epoch"`
	PKH        Bytes   `json:"pkH"`
	RH         Bytes   `json:"rh"`
	PKU        Bytes   `json:"pkU"`
	RU         Bytes   `json:"ru"`
	HostFP     string  `json:"host_fp"`
	UIID       string  `json:"ui_id"`
	NU         Bytes   `json:"nu"`
	CTH        Bytes   `json:"ctH"`
	NH         Bytes   `json:"nh"`
	CTU        Bytes   `json:"ctU"`
	ProofH     Bytes   `json:"proofH"`
	ProofU     Bytes   `json:"proofU"`
	Session    Bytes   `json:"session"`
	Seq        Counter `json:"seq"`
	Ciphertext Bytes   `json:"ciphertext"`
}

// PlainRecord is the strict encrypted control/stream union. The pair.confirm
// extension is syntax only: no human trust decision or authority is implied.
type PlainRecord struct {
	V         uint16  `json:"v"`
	Type      string  `json:"type"`
	PairEpoch Bytes   `json:"pair_epoch"`
	State     string  `json:"state"`
	AID       Bytes   `json:"aid"`
	HostFP    string  `json:"host_fp"`
	UIID      string  `json:"ui_id"`
	ID        Counter `json:"id"`
	Data      Bytes   `json:"data"`
	Bytes     Counter `json:"bytes"`
	Code      string  `json:"code"`
}

// RecordIO has one session-wide serialized Send counter. Exactly one consumer
// calls Receive and demultiplexes it. Host integration supplies this adapter.
type RecordIO interface {
	Send(context.Context, PlainRecord) error
	Receive(context.Context) (PlainRecord, error)
	Close() error
}

var controlFields = map[string]string{
	"host.register": "host_id", "host.registered": "host_id registration",
	"ui.attach": "host_id", "ui.attached": "connection", "ui.open": "connection",
	"ui.closed": "connection", "ui.close": "connection", "forward": "connection payload",
	"error": "code", "notification.get": "qid host_id ui_id request_header request_mac",
	"notification.reply": "qid body", "notification.empty": "qid", "notification.cancel": "qid",
}
var innerFields = map[string]string{
	"pair.commit": "aid C", "pair.host": "aid window pair_epoch pkH rh", "pair.reveal": "aid pkU ru",
	"auth.init": "host_fp ui_id pair_epoch nu ctH", "auth.challenge": "nh ctU proofH", "auth.finish": "proofU",
	"pair.auth.init": "host_fp ui_id pair_epoch nu ctH", "pair.auth.challenge": "nh ctU proofH", "pair.auth.finish": "proofU",
	"record": "session seq ciphertext",
}
var plainFields = map[string]string{
	"ready": "pair_epoch state aid", "ready.ack": "pair_epoch",
	"pair.accepted": "aid host_fp ui_id pair_epoch", "pair.confirm": "aid pair_epoch", "pair.confirmed": "aid pair_epoch",
	"stream.open": "id", "stream.opened": "id", "stream.data": "id data", "stream.credit": "id bytes",
	"stream.fin": "id", "stream.reset": "id code",
}

func EncodeControl(m Control) ([]byte, error) {
	return encodeUnion(m, m.Type, controlFields, MaxControlSize)
}
func DecodeControl(b []byte) (m Control, err error) {
	err = decodeUnion(b, &m, controlFields, MaxControlSize)
	return
}
func EncodeInner(m Inner) ([]byte, error) { return encodeUnion(m, m.Type, innerFields, MaxInnerSize) }
func DecodeInner(b []byte) (m Inner, err error) {
	err = decodeUnion(b, &m, innerFields, MaxInnerSize)
	return
}
func EncodePlain(m PlainRecord) ([]byte, error) {
	return encodeUnion(m, m.Type, plainFields, MaxInnerSize)
}
func DecodePlain(b []byte) (m PlainRecord, err error) {
	err = decodeUnion(b, &m, plainFields, MaxInnerSize)
	return
}

func fieldSet(kind string, schema map[string]string) (map[string]bool, error) {
	fields, ok := schema[kind]
	if !ok {
		return nil, ErrProtocol
	}
	set := map[string]bool{"v": true, "type": true}
	for _, k := range strings.Fields(fields) {
		set[k] = true
	}
	return set, nil
}
func encodeUnion(m any, kind string, schema map[string]string, limit int) ([]byte, error) {
	set, e := fieldSet(kind, schema)
	if e != nil {
		return nil, e
	}
	out := map[string]any{}
	v := reflect.ValueOf(m)
	typ := v.Type()
	for i := 0; i < v.NumField(); i++ {
		name := typ.Field(i).Tag.Get("json")
		if set[name] {
			out[name] = v.Field(i).Interface()
		} else if !v.Field(i).IsZero() {
			return nil, ErrProtocol
		}
	}
	b, e := json.Marshal(out)
	if e != nil || len(b) > limit {
		return nil, ErrProtocol
	}
	// Keep encoding and decoding validation identical, including required zeros.
	dst := reflect.New(typ).Interface()
	if e = decodeUnion(b, dst, schema, limit); e != nil {
		return nil, e
	}
	return b, nil
}
func decodeUnion(b []byte, dst any, schema map[string]string, limit int) error {
	fields, e := strictObject(b, limit)
	if e != nil {
		return e
	}
	var kind string
	if json.Unmarshal(fields["type"], &kind) != nil {
		return ErrProtocol
	}
	set, e := fieldSet(kind, schema)
	if e != nil {
		return e
	}
	if len(fields) != len(set) {
		return ErrProtocol
	}
	for k, raw := range fields {
		if !set[k] || bytes.Equal(raw, []byte("null")) {
			return ErrProtocol
		}
	}
	if json.Unmarshal(b, dst) != nil {
		return ErrProtocol
	}
	switch m := dst.(type) {
	case *Control:
		return m.validate()
	case *Inner:
		return m.validate()
	case *PlainRecord:
		return m.validate()
	}
	return ErrProtocol
}
func (m Control) validate() error {
	if m.V != Version {
		return ErrProtocol
	}
	switch m.Type {
	case "host.register", "host.registered", "ui.attach", "notification.get":
		if !validHostID(m.HostID) {
			return ErrProtocol
		}
	}
	if m.Type == "host.registered" && len(m.Registration) != 16 {
		return ErrProtocol
	}
	if strings.HasPrefix(m.Type, "ui.") && m.Type != "ui.attach" || m.Type == "forward" {
		if len(m.Connection) != 16 {
			return ErrProtocol
		}
	}
	if m.Type == "forward" && (len(m.Payload) == 0 || len(m.Payload) > MaxInnerSize) {
		return ErrProtocol
	}
	if m.Type == "error" {
		switch m.Code {
		case "id_in_use", "host_offline", "protocol_error", "overloaded":
		default:
			return ErrProtocol
		}
	}
	if strings.HasPrefix(m.Type, "notification.") && len(m.QID) != 16 {
		return ErrProtocol
	}
	if m.Type == "notification.get" && (!validFingerprint(m.UIID) || !validPollHeaders(m.RequestHeader, m.RequestMAC)) {
		return ErrProtocol
	}
	if m.Type == "notification.reply" && (len(m.Body) == 0 || len(m.Body) > MaxNotificationSize) {
		return ErrProtocol
	}
	return nil
}
func (m Inner) validate() error {
	if m.V != Version {
		return ErrProtocol
	}
	switch m.Type {
	case "pair.commit":
		if len(m.AID) != 16 || len(m.C) != 48 {
			return ErrProtocol
		}
	case "pair.host":
		if len(m.AID) != 16 || len(m.Window) != 16 || len(m.PairEpoch) != 16 || len(m.RH) != 32 || ValidatePublicKey(m.PKH) != nil {
			return ErrProtocol
		}
	case "pair.reveal":
		if len(m.AID) != 16 || len(m.RU) != 32 || ValidatePublicKey(m.PKU) != nil {
			return ErrProtocol
		}
	case "auth.init", "pair.auth.init":
		if !validFingerprint(m.HostFP) || !validFingerprint(m.UIID) || len(m.PairEpoch) != 16 || len(m.NU) != 32 || len(m.CTH) != 1088 {
			return ErrProtocol
		}
	case "auth.challenge", "pair.auth.challenge":
		if len(m.NH) != 32 || len(m.CTU) != 1088 || len(m.ProofH) != 48 {
			return ErrProtocol
		}
	case "auth.finish", "pair.auth.finish":
		if len(m.ProofU) != 48 {
			return ErrProtocol
		}
	case "record":
		if len(m.Session) != 48 || len(m.Ciphertext) < 16 || len(m.Ciphertext) > MaxInnerSize {
			return ErrProtocol
		}
	default:
		return ErrProtocol
	}
	return nil
}
func (m PlainRecord) validate() error {
	if m.V != Version {
		return ErrProtocol
	}
	switch m.Type {
	case "ready":
		if len(m.PairEpoch) != 16 || len(m.AID) != 16 || (m.State != "active" && m.State != "awaiting-confirmation") {
			return ErrProtocol
		}
	case "ready.ack":
		if len(m.PairEpoch) != 16 {
			return ErrProtocol
		}
	case "pair.accepted":
		if len(m.AID) != 16 || len(m.PairEpoch) != 16 || !validFingerprint(m.HostFP) || !validFingerprint(m.UIID) {
			return ErrProtocol
		}
	case "pair.confirm", "pair.confirmed":
		if len(m.AID) != 16 || len(m.PairEpoch) != 16 {
			return ErrProtocol
		}
	case "stream.open", "stream.opened", "stream.data", "stream.credit", "stream.fin", "stream.reset":
		if m.ID == 0 || m.ID%2 != 1 {
			return ErrProtocol
		}
		if m.Type == "stream.data" && (len(m.Data) == 0 || len(m.Data) > MaxStreamData) {
			return ErrProtocol
		}
		if m.Type == "stream.credit" && (m.Bytes == 0 || m.Bytes > InitialCredit) {
			return ErrProtocol
		}
		if m.Type == "stream.reset" {
			switch m.Code {
			case "cancelled", "refused", "internal", "closed":
			default:
				return ErrProtocol
			}
		}
	default:
		return ErrProtocol
	}
	return nil
}

// strictObject rejects ambiguous JSON at every nesting level, including duplicate
// decoded key names and lone UTF-16 surrogates (encoding/json replaces those).
func strictObject(b []byte, limit int) (map[string]json.RawMessage, error) {
	if len(b) == 0 || len(b) > limit || !utf8.Valid(b) || !validJSONStrings(b) {
		return nil, ErrProtocol
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if e := walkJSON(d, 0); e != nil {
		return nil, ErrProtocol
	}
	if _, e := d.Token(); e != io.EOF {
		return nil, ErrProtocol
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(b, &obj) != nil || obj == nil {
		return nil, ErrProtocol
	}
	return obj, nil
}
func walkJSON(d *json.Decoder, depth int) error {
	if depth > 32 {
		return ErrProtocol
	}
	token, e := d.Token()
	if e != nil {
		return e
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return e
			}
			s, ok := k.(string)
			if !ok || seen[s] {
				return ErrProtocol
			}
			seen[s] = true
			if e = walkJSON(d, depth+1); e != nil {
				return e
			}
		}
	case '[':
		for d.More() {
			if e = walkJSON(d, depth+1); e != nil {
				return e
			}
		}
	default:
		return ErrProtocol
	}
	_, e = d.Token()
	return e
}
func validJSONStrings(b []byte) bool {
	for i := 0; i < len(b); i++ {
		if b[i] != '"' {
			continue
		}
		i++
		for ; i < len(b) && b[i] != '"'; i++ {
			if b[i] != '\\' {
				continue
			}
			i++
			if i >= len(b) {
				return false
			}
			if b[i] != 'u' {
				continue
			}
			if i+4 >= len(b) {
				return false
			}
			v, e := strconv.ParseUint(string(b[i+1:i+5]), 16, 16)
			if e != nil {
				return false
			}
			i += 4
			if v >= 0xdc00 && v <= 0xdfff {
				return false
			}
			if v >= 0xd800 && v <= 0xdbff {
				if i+6 >= len(b) || b[i+1] != '\\' || b[i+2] != 'u' {
					return false
				}
				w, e := strconv.ParseUint(string(b[i+3:i+7]), 16, 16)
				if e != nil || w < 0xdc00 || w > 0xdfff {
					return false
				}
				i += 6
			}
		}
		if i >= len(b) {
			return false
		}
	}
	return true
}

// Poll carries the two HTTP header values unchanged through the relay.
type Poll struct {
	RequestHeader string
	RequestMAC    string
}

func validPollHeaders(header, signature string) bool {
	if len(header) == 0 || len(header) > 4096 {
		return false
	}
	var b Bytes
	if b.UnmarshalJSON([]byte(strconv.Quote(header))) != nil || len(b) == 0 || len(b) > 3072 {
		return false
	}
	if b.UnmarshalJSON([]byte(strconv.Quote(signature))) != nil || len(b) != 48 {
		return false
	}
	return true
}

// NotificationEnvelope is encrypted JSON carried opaquely in notification.reply.
type NotificationEnvelope struct {
	V          uint16 `json:"v"`
	HostFP     string `json:"host_fp"`
	UIID       string `json:"ui_id"`
	PairEpoch  Bytes  `json:"pair_epoch"`
	Request    Bytes  `json:"request"`
	Reply      Bytes  `json:"reply"`
	Ciphertext Bytes  `json:"ciphertext"`
}
