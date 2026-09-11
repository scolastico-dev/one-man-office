package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
)

const (
	defaultLuaHTTPTimeout = 10 * time.Second
	maxLuaHTTPResponse    = 1 << 20
)

var errLuaHTTPCrossHostRedirect = errors.New("redirect destination host is not allowed")

type luaHTTPRequest struct {
	method  string
	url     *url.URL
	headers http.Header
	body    []byte
	timeout time.Duration
}

type luaHTTPResponse struct {
	status  int
	body    string
	headers map[string][]string
}

func (m *Manager) luaHTTP(ctx context.Context) lua.LGFunction {
	return func(state *lua.LState) int {
		request, err := parseLuaHTTPRequest(state.Get(1))
		if err != nil {
			return pushLuaHTTPError(state, err)
		}
		response, err := executeLuaHTTPRequest(ctx, request, nil)
		if err != nil {
			return pushLuaHTTPError(state, err)
		}
		state.Push(goToLua(state, map[string]any{
			"status":  response.status,
			"body":    response.body,
			"headers": response.headers,
		}))
		state.Push(lua.LNil)
		return 2
	}
}

func pushLuaHTTPError(state *lua.LState, err error) int {
	state.Push(lua.LNil)
	state.Push(lua.LString(err.Error()))
	return 2
}

func parseLuaHTTPRequest(value lua.LValue) (luaHTTPRequest, error) {
	table, ok := value.(*lua.LTable)
	if !ok {
		return luaHTTPRequest{}, errors.New("http request must be a table")
	}
	allowed := map[string]bool{
		"method": true, "url": true, "headers": true, "form": true,
		"json": true, "body": true, "timeout": true,
	}
	var invalidKey bool
	table.ForEach(func(key, _ lua.LValue) {
		name, ok := key.(lua.LString)
		if !ok || !allowed[string(name)] {
			invalidKey = true
		}
	})
	if invalidKey {
		return luaHTTPRequest{}, errors.New("http request contains an unknown field")
	}

	method := http.MethodGet
	if value, present := luaTableField(table, "method"); present {
		methodValue, ok := value.(lua.LString)
		if !ok || strings.TrimSpace(string(methodValue)) == "" {
			return luaHTTPRequest{}, errors.New("http method must be a non-empty string")
		}
		method = string(methodValue)
	}

	urlValue, present := luaTableField(table, "url")
	if !present {
		return luaHTTPRequest{}, errors.New("http url is required")
	}
	urlString, ok := urlValue.(lua.LString)
	if !ok || strings.TrimSpace(string(urlString)) == "" {
		return luaHTTPRequest{}, errors.New("http url must be a string")
	}
	requestURL, err := url.Parse(string(urlString))
	if err != nil || requestURL.Host == "" || (!strings.EqualFold(requestURL.Scheme, "http") && !strings.EqualFold(requestURL.Scheme, "https")) {
		return luaHTTPRequest{}, errors.New("http url must use http or https")
	}

	headers := make(http.Header)
	if value, present := luaTableField(table, "headers"); present {
		if err := parseLuaHTTPHeaders(value, headers); err != nil {
			return luaHTTPRequest{}, err
		}
	}

	bodyFields := 0
	for _, name := range []string{"form", "json", "body"} {
		if _, present := luaTableField(table, name); present {
			bodyFields++
		}
	}
	if bodyFields > 1 {
		return luaHTTPRequest{}, errors.New("http form, json, and body are mutually exclusive")
	}

	var body []byte
	if value, present := luaTableField(table, "form"); present {
		form, err := parseLuaHTTPForm(value)
		if err != nil {
			return luaHTTPRequest{}, err
		}
		body = []byte(form.Encode())
		if headers.Get("Content-Type") == "" {
			headers.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	if value, present := luaTableField(table, "json"); present {
		encoded, err := json.Marshal(luaToGo(value))
		if err != nil {
			return luaHTTPRequest{}, errors.New("http json body could not be encoded")
		}
		body = encoded
		if headers.Get("Content-Type") == "" {
			headers.Set("Content-Type", "application/json")
		}
	}
	if value, present := luaTableField(table, "body"); present {
		bodyValue, ok := value.(lua.LString)
		if !ok {
			return luaHTTPRequest{}, errors.New("http body must be a string")
		}
		body = []byte(string(bodyValue))
	}

	timeout := defaultLuaHTTPTimeout
	if value, present := luaTableField(table, "timeout"); present {
		timeoutValue, ok := value.(lua.LString)
		if !ok {
			return luaHTTPRequest{}, errors.New("http timeout must be a duration string")
		}
		timeout, err = time.ParseDuration(string(timeoutValue))
		if err != nil || timeout <= 0 {
			return luaHTTPRequest{}, errors.New("http timeout must be a positive duration")
		}
	}

	return luaHTTPRequest{method: method, url: requestURL, headers: headers, body: body, timeout: timeout}, nil
}

func luaTableField(table *lua.LTable, name string) (lua.LValue, bool) {
	value := table.RawGetString(name)
	return value, value != lua.LNil
}

func parseLuaHTTPHeaders(value lua.LValue, headers http.Header) error {
	table, ok := value.(*lua.LTable)
	if !ok {
		return errors.New("http headers must be a string-to-string table")
	}
	var parseErr error
	table.ForEach(func(key, value lua.LValue) {
		if parseErr != nil {
			return
		}
		name, nameOK := key.(lua.LString)
		headerValue, valueOK := value.(lua.LString)
		canonical := textproto.CanonicalMIMEHeaderKey(string(name))
		if !nameOK || !valueOK || canonical == "" || strings.ContainsAny(string(headerValue), "\r\n") {
			parseErr = errors.New("http headers must be a string-to-string table")
			return
		}
		headers.Set(canonical, string(headerValue))
	})
	return parseErr
}

func parseLuaHTTPForm(value lua.LValue) (url.Values, error) {
	table, ok := value.(*lua.LTable)
	if !ok {
		return nil, errors.New("http form must be a string-to-string table")
	}
	form := make(url.Values)
	var parseErr error
	table.ForEach(func(key, value lua.LValue) {
		if parseErr != nil {
			return
		}
		name, nameOK := key.(lua.LString)
		formValue, valueOK := value.(lua.LString)
		if !nameOK || !valueOK {
			parseErr = errors.New("http form must be a string-to-string table")
			return
		}
		form.Add(string(name), string(formValue))
	})
	return form, parseErr
}

func executeLuaHTTPRequest(ctx context.Context, request luaHTTPRequest, client *http.Client) (*luaHTTPResponse, error) {
	requestContext, cancel := context.WithTimeout(ctx, request.timeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestContext, request.method, request.url.String(), bytes.NewReader(request.body))
	if err != nil {
		return nil, errors.New("http request could not be created")
	}
	httpRequest.Header = request.headers.Clone()
	if client == nil {
		client = &http.Client{}
	} else {
		clientCopy := *client
		client = &clientCopy
	}
	client.CheckRedirect = func(next *http.Request, _ []*http.Request) error {
		if effectiveHTTPHost(request.url) != effectiveHTTPHost(next.URL) {
			return errLuaHTTPCrossHostRedirect
		}
		return nil
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return nil, sanitizeLuaHTTPError(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxLuaHTTPResponse+1))
	if err != nil {
		return nil, errors.New("http response body could not be read")
	}
	if len(body) > maxLuaHTTPResponse {
		return nil, errors.New("http response body exceeds 1048576 byte limit")
	}
	responseHeaders := make(map[string][]string, len(response.Header))
	for name, values := range response.Header {
		responseHeaders[name] = append([]string(nil), values...)
	}
	return &luaHTTPResponse{status: response.StatusCode, body: string(body), headers: responseHeaders}, nil
}

func sanitizeLuaHTTPError(err error) error {
	if errors.Is(err, errLuaHTTPCrossHostRedirect) {
		return errLuaHTTPCrossHostRedirect
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("http request timed out")
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("http request canceled")
	}
	return errors.New("http request failed")
}

func effectiveHTTPHost(value *url.URL) string {
	port := value.Port()
	if port == "" {
		if strings.EqualFold(value.Scheme, "https") {
			port = "443"
		} else {
			port = "80"
		}
	}
	return net.JoinHostPort(strings.ToLower(value.Hostname()), port)
}
