package relayproto

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const NotificationContentType = "application/vnd.omo.relay-notifications+json"

// NotificationContext is taken from an active persisted device, never a route
// name supplied by an unauthenticated poll. Root is its saved pairing root N.
type NotificationContext struct {
	Route        Route
	HostFP, UIID string
	PairEpoch    Bytes
}

func (c NotificationContext) Clone() NotificationContext {
	c.PairEpoch = slices.Clone(c.PairEpoch)
	return c
}
func (c NotificationContext) validate(root []byte) error {
	if c.Route.validate() != nil || !validFingerprint(c.HostFP) || !validFingerprint(c.UIID) || len(c.PairEpoch) != 16 || len(root) != 32 {
		return ErrProtocol
	}
	return nil
}

// Receipt is compared as a complete tuple; Through alone is never sufficient.
type Receipt struct {
	Batch   Bytes   `json:"batch"`
	Through Counter `json:"through"`
	Token   Bytes   `json:"token"`
}

func (r Receipt) validate() error {
	if len(r.Batch) != 16 || len(r.Token) != 32 || r.Through == 0 {
		return ErrProtocol
	}
	return nil
}
func (r Receipt) Equal(other Receipt) bool {
	return r.Through == other.Through && hmac.Equal(r.Batch, other.Batch) && hmac.Equal(r.Token, other.Token)
}

type NotificationRequest struct {
	V         uint16   `json:"v"`
	PairEpoch Bytes    `json:"pair_epoch"`
	Request   Bytes    `json:"request"`
	Ack       *Receipt `json:"ack"`
}

func requestKey(c NotificationContext, root []byte) []byte {
	salt := hashFields([]byte(label+"notify-context"), []byte(c.Route.Origin), []byte(c.Route.HostID), []byte(c.HostFP), []byte(c.UIID), c.PairEpoch)
	return kdf(root, salt, "notify-request")
}
func requestMAC(c NotificationContext, key []byte, r NotificationRequest) []byte {
	var batch, through, token []byte
	if r.Ack != nil {
		batch = r.Ack.Batch
		through = u64(uint64(r.Ack.Through))
		token = r.Ack.Token
	}
	return mac(key, EncodeFields([]byte(label+"notify-get"), u16(Version), []byte(c.Route.Origin), []byte(c.Route.HostID), []byte(c.HostFP), []byte(c.UIID), c.PairEpoch, r.Request, batch, through, token))
}

// NewPoll signs a fresh caller-generated 32-byte request id and optional durable
// receipt. Keep one logical outstanding poll and abandon superseded ids.
func NewPoll(c NotificationContext, root, request []byte, ack *Receipt) (Poll, error) {
	if c.validate(root) != nil || len(request) != 32 || ack != nil && ack.validate() != nil {
		return Poll{}, ErrProtocol
	}
	r := NotificationRequest{V: Version, PairEpoch: c.PairEpoch, Request: request, Ack: ack}
	b, e := json.Marshal(r)
	if e != nil {
		return Poll{}, e
	}
	key := requestKey(c, root)
	defer clear(key)
	return Poll{RequestHeader: base64.RawURLEncoding.EncodeToString(b), RequestMAC: base64.RawURLEncoding.EncodeToString(requestMAC(c, key, r))}, nil
}
func VerifyPoll(c NotificationContext, root []byte, p Poll) (NotificationRequest, error) {
	if c.validate(root) != nil || !validPollHeaders(p.RequestHeader, p.RequestMAC) {
		return NotificationRequest{}, ErrAuthentication
	}
	b, _ := base64.RawURLEncoding.DecodeString(p.RequestHeader)
	var r NotificationRequest
	if decodeExact(b, &r, "v pair_epoch request ack", "ack", 3072) != nil || r.V != Version || len(r.Request) != 32 || !bytes.Equal(r.PairEpoch, c.PairEpoch) {
		return NotificationRequest{}, ErrAuthentication
	}
	if r.Ack != nil {
		raw, _ := strictObject(b, 3072)
		var ack Receipt
		if decodeExact(raw["ack"], &ack, "batch through token", "", 1024) != nil || ack.validate() != nil {
			return NotificationRequest{}, ErrAuthentication
		}
	}
	key := requestKey(c, root)
	defer clear(key)
	sig, _ := base64.RawURLEncoding.DecodeString(p.RequestMAC)
	if !hmac.Equal(sig, requestMAC(c, key, r)) {
		return NotificationRequest{}, ErrAuthentication
	}
	return r, nil
}

// Notification is a bounded projection. Unavailable entries contain only ID and
// Unavailable=true, preserving an already issued batch after source deletion.
type Notification struct {
	ID             Counter `json:"id"`
	Ticket         Counter `json:"ticket"`
	AuthorRole     string  `json:"author_role"`
	CreatedAt      string  `json:"created_at"`
	Title          string  `json:"title"`
	Body           string  `json:"body"`
	BodyTruncated  bool    `json:"body_truncated"`
	TitleTruncated bool    `json:"title_truncated"`
	Unavailable    bool    `json:"unavailable"`
}

func (n Notification) validate() error {
	if n.ID == 0 {
		return ErrProtocol
	}
	if n.Unavailable {
		v := n
		v.ID = 0
		v.Unavailable = false
		if !reflect.ValueOf(v).IsZero() {
			return ErrProtocol
		}
		return nil
	}
	if n.Ticket == 0 || len(n.Title) > 128 || len(n.Body) > 384 || !utf8.ValidString(n.Title) || !utf8.ValidString(n.Body) {
		return ErrProtocol
	}
	switch n.AuthorRole {
	case "user", "ceo", "pm", "developer", "reviewer", "system":
	default:
		return ErrProtocol
	}
	if _, e := time.Parse(time.RFC3339, n.CreatedAt); e != nil || !strings.HasSuffix(n.CreatedAt, "Z") {
		return ErrProtocol
	}
	return nil
}
func (n Notification) MarshalJSON() ([]byte, error) {
	if e := n.validate(); e != nil {
		return nil, e
	}
	if n.Unavailable {
		return json.Marshal(struct {
			ID          Counter `json:"id"`
			Unavailable bool    `json:"unavailable"`
		}{n.ID, true})
	}
	type plain Notification
	b, e := json.Marshal(plain(n))
	if e != nil || len(b) > 4096 {
		return nil, ErrProtocol
	}
	return b, nil
}
func (n *Notification) UnmarshalJSON(b []byte) error {
	raw, e := strictObject(b, 4096)
	if e != nil {
		return e
	}
	fields := "id ticket author_role created_at title body body_truncated title_truncated unavailable"
	if bytes.Equal(raw["unavailable"], []byte("true")) {
		fields = "id unavailable"
	}
	type plain Notification
	var p plain
	if e = decodeExact(b, &p, fields, "", 4096); e != nil {
		return e
	}
	*n = Notification(p)
	return n.validate()
}

// NotificationPage is either an invalid_ack error or one page. Empty pages have
// nil batch/token and a nonnil empty notification list on decode.
type NotificationPage struct {
	Batch         Bytes
	Through       Counter
	Token         Bytes
	Notifications []Notification
	More          bool
	Acknowledged  Bytes
	Error         string
}

func (p NotificationPage) validate() error {
	if p.Error != "" {
		q := p
		q.Error = ""
		if p.Error != "invalid_ack" || !reflect.ValueOf(q).IsZero() {
			return ErrProtocol
		}
		return nil
	}
	if len(p.Acknowledged) != 0 && len(p.Acknowledged) != 16 {
		return ErrProtocol
	}
	if len(p.Notifications) > 50 {
		return ErrProtocol
	}
	if len(p.Notifications) == 0 {
		if len(p.Batch) != 0 || len(p.Token) != 0 || p.More {
			return ErrProtocol
		}
		return nil
	}
	if len(p.Batch) != 16 || len(p.Token) != 32 {
		return ErrProtocol
	}
	var previous Counter
	for _, n := range p.Notifications {
		if n.ID <= previous || n.validate() != nil {
			return ErrProtocol
		}
		previous = n.ID
	}
	if p.Through != previous {
		return ErrProtocol
	}
	return nil
}

type pageWire struct {
	Batch         *Bytes         `json:"batch"`
	Through       Counter        `json:"through"`
	Token         *Bytes         `json:"token"`
	Notifications []Notification `json:"notifications"`
	More          bool           `json:"more"`
	Acknowledged  *Bytes         `json:"acknowledged"`
}

func optionalBytes(b Bytes) *Bytes {
	if len(b) == 0 {
		return nil
	}
	return &b
}
func EncodeNotificationPage(p NotificationPage) ([]byte, error) {
	if e := p.validate(); e != nil {
		return nil, e
	}
	if p.Error != "" {
		return json.Marshal(struct {
			Error string `json:"error"`
		}{p.Error})
	}
	notices := p.Notifications
	if notices == nil {
		notices = []Notification{}
	}
	b, e := json.Marshal(pageWire{Batch: optionalBytes(p.Batch), Through: p.Through, Token: optionalBytes(p.Token), Notifications: notices, More: p.More, Acknowledged: optionalBytes(p.Acknowledged)})
	if e != nil || len(b) > MaxNotificationPlaintext {
		return nil, ErrProtocol
	}
	return b, nil
}
func DecodeNotificationPage(b []byte) (NotificationPage, error) {
	fields, e := strictObject(b, MaxNotificationPlaintext)
	if e != nil {
		return NotificationPage{}, e
	}
	if _, ok := fields["error"]; ok {
		var wire struct {
			Error string `json:"error"`
		}
		if decodeExact(b, &wire, "error", "", MaxNotificationPlaintext) != nil || wire.Error != "invalid_ack" {
			return NotificationPage{}, ErrProtocol
		}
		return NotificationPage{Error: wire.Error}, nil
	}
	var w pageWire
	if decodeExact(b, &w, "batch through token notifications more acknowledged", "batch token acknowledged", MaxNotificationPlaintext) != nil || w.Notifications == nil {
		return NotificationPage{}, ErrProtocol
	}
	p := NotificationPage{Through: w.Through, Notifications: w.Notifications, More: w.More}
	if w.Batch != nil {
		if len(*w.Batch) != 16 {
			return NotificationPage{}, ErrProtocol
		}
		p.Batch = *w.Batch
	}
	if w.Token != nil {
		if len(*w.Token) != 32 {
			return NotificationPage{}, ErrProtocol
		}
		p.Token = *w.Token
	}
	if w.Acknowledged != nil {
		if len(*w.Acknowledged) != 16 {
			return NotificationPage{}, ErrProtocol
		}
		p.Acknowledged = *w.Acknowledged
	}
	if e := p.validate(); e != nil {
		return NotificationPage{}, e
	}
	return p, nil
}

func responseAAD(c NotificationContext, request, reply []byte) []byte {
	return EncodeFields([]byte(label+"notify-response"), u16(Version), []byte(c.Route.Origin), []byte(c.Route.HostID), []byte(c.HostFP), []byte(c.UIID), c.PairEpoch, request, reply)
}
func SealNotification(c NotificationContext, root, request []byte, page NotificationPage, random io.Reader) (NotificationEnvelope, error) {
	if c.validate(root) != nil || len(request) != 32 {
		return NotificationEnvelope{}, ErrProtocol
	}
	if random == nil {
		random = rand.Reader
	}
	b, e := EncodeNotificationPage(page)
	if e != nil {
		return NotificationEnvelope{}, e
	}
	defer clear(b)
	reply := make(Bytes, 32)
	if _, e = io.ReadFull(random, reply); e != nil {
		return NotificationEnvelope{}, e
	}
	aad := responseAAD(c, request, reply)
	key := kdf(root, hashBytes(aad), "notify-response")
	defer clear(key)
	aead, e := newGCM(key)
	if e != nil {
		return NotificationEnvelope{}, e
	}
	return NotificationEnvelope{V: Version, HostFP: c.HostFP, UIID: c.UIID, PairEpoch: slices.Clone(c.PairEpoch), Request: slices.Clone(request), Reply: reply, Ciphertext: aead.Seal(nil, make([]byte, 12), b, aad)}, nil
}
func OpenNotification(c NotificationContext, root, request []byte, env NotificationEnvelope) (NotificationPage, error) {
	if c.validate(root) != nil || len(request) != 32 || env.validate() != nil || env.HostFP != c.HostFP || env.UIID != c.UIID || !bytes.Equal(env.PairEpoch, c.PairEpoch) || !bytes.Equal(env.Request, request) {
		return NotificationPage{}, ErrAuthentication
	}
	aad := responseAAD(c, env.Request, env.Reply)
	key := kdf(root, hashBytes(aad), "notify-response")
	defer clear(key)
	aead, e := newGCM(key)
	if e != nil {
		return NotificationPage{}, e
	}
	b, e := aead.Open(nil, make([]byte, 12), env.Ciphertext, aad)
	if e != nil {
		return NotificationPage{}, ErrAuthentication
	}
	defer clear(b)
	return DecodeNotificationPage(b)
}
func (env NotificationEnvelope) validate() error {
	if env.V != Version || !validFingerprint(env.HostFP) || !validFingerprint(env.UIID) || len(env.PairEpoch) != 16 || len(env.Request) != 32 || len(env.Reply) != 32 || len(env.Ciphertext) < 16 || len(env.Ciphertext) > MaxNotificationPlaintext+16 {
		return ErrProtocol
	}
	return nil
}
func EncodeNotificationEnvelope(env NotificationEnvelope) ([]byte, error) {
	if e := env.validate(); e != nil {
		return nil, e
	}
	b, e := json.Marshal(env)
	if e != nil || len(b) > MaxNotificationSize {
		return nil, ErrProtocol
	}
	return b, nil
}
func DecodeNotificationEnvelope(b []byte) (env NotificationEnvelope, err error) {
	err = decodeExact(b, &env, "v host_fp ui_id pair_epoch request reply ciphertext", "", MaxNotificationSize)
	if err == nil {
		err = env.validate()
	}
	return
}
func decodeExact(b []byte, dst any, keys, nullable string, limit int) error {
	fields, e := strictObject(b, limit)
	if e != nil {
		return e
	}
	required := strings.Fields(keys)
	if len(fields) != len(required) {
		return ErrProtocol
	}
	nulls := map[string]bool{}
	for _, k := range strings.Fields(nullable) {
		nulls[k] = true
	}
	for _, k := range required {
		raw, ok := fields[k]
		if !ok || bytes.Equal(raw, []byte("null")) && !nulls[k] {
			return ErrProtocol
		}
	}
	if json.Unmarshal(b, dst) != nil {
		return ErrProtocol
	}
	return nil
}
