package apiclient

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
)

// NewWithTransport returns a client that sends its requests through rt
// instead of the socket. A non-empty session is sent as X-Omo-Session;
// without it the caller is the user.
func NewWithTransport(rt http.RoundTripper, session string) *Client {
	return &Client{http: &http.Client{Transport: rt}, session: session}
}

// HandlerTransport serves every request in process through h (the server's
// socket handler), so a client built with NewWithTransport goes through
// exactly the routes, checks and refusals a socket client meets. The
// request's context reaches the handler: cancelling it ends the request.
func HandlerTransport(h http.Handler) http.RoundTripper {
	return handlerTransport{h}
}

type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	in := req.Clone(req.Context())
	if in.Body == nil {
		in.Body = http.NoBody
	}
	in.RequestURI = req.URL.RequestURI()
	in.RemoteAddr = "in-process"
	if in.Host == "" {
		in.Host = req.URL.Host
	}
	rec := &recorder{header: http.Header{}}
	t.h.ServeHTTP(rec, in)
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	if req.Body != nil {
		req.Body.Close()
	}
	if err := req.Context().Err(); err != nil {
		// As on the socket: a request whose caller gave up has no answer.
		return nil, err
	}
	return &http.Response{
		Status:        strconv.Itoa(rec.status) + " " + http.StatusText(rec.status),
		StatusCode:    rec.status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        rec.header,
		Body:          io.NopCloser(bytes.NewReader(rec.body.Bytes())),
		ContentLength: int64(rec.body.Len()),
		Request:       req,
	}, nil
}

// recorder keeps a handler's answer in memory.
type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

func (r *recorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(p)
}

// Flush does nothing: the answer is handed over whole.
func (r *recorder) Flush() {}
