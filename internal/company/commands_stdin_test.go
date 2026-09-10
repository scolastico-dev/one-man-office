package company

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"strings"
	"testing"
	"time"
)

func TestExecuteStreamsStdinByteExactly(t *testing.T) {
	s, server := testServer(t)
	payload := bytes.Repeat([]byte("stdin-byte-exact-"), 4096)
	data := executeMultipart(t, s, server, executeRequest{
		Command: os.Args[0], Args: []string{"-test.run=^TestCommandStdinHashHelper$"},
	}, payload)
	events := commandEvents(t, data)
	if got, want := stdoutData(events), hex.EncodeToString(sumBytes(payload)); got != want {
		t.Fatalf("stdin hash %q, want %q", got, want)
	}
	assertSuccessfulCommand(t, events)
}

func TestExecuteStreamsMultiMiBStdinByteExactly(t *testing.T) {
	s, server := testServer(t)
	payload := bytes.Repeat([]byte("multi-mebibyte-stdin-"), 256*1024)
	data := executeMultipart(t, s, server, executeRequest{
		Command: os.Args[0], Args: []string{"-test.run=^TestCommandStdinHashHelper$"},
	}, payload)
	events := commandEvents(t, data)
	if got, want := stdoutData(events), hex.EncodeToString(sumBytes(payload)); got != want {
		t.Fatalf("stdin hash %q, want %q", got, want)
	}
	assertSuccessfulCommand(t, events)
}

func TestExecuteCompletesWhenChildExitsBeforeConsumingStdin(t *testing.T) {
	s, server := testServer(t)
	payload := bytes.Repeat([]byte("child-exits-early-"), 256*1024)
	done := make(chan []byte, 1)
	go func() {
		done <- executeMultipart(t, s, server, executeRequest{
			Command: os.Args[0], Args: []string{"-test.run=^TestCommandEarlyExitHelper$"},
		}, payload)
	}()
	select {
	case data := <-done:
		assertSuccessfulCommand(t, commandEvents(t, data))
	case <-time.After(3 * time.Second):
		t.Fatal("command did not complete after early child exit")
	}
}

func TestExecuteRejectsMalformedMultipartParts(t *testing.T) {
	tests := []struct {
		name  string
		parts []multipartTestPart
	}{
		{name: "missing request", parts: []multipartTestPart{{Name: "stdin", Body: "data"}}},
		{name: "reversed", parts: []multipartTestPart{{Name: "stdin", Body: "data"}, {Name: "request", Body: `{"command":"cat"}`}}},
		{name: "unknown", parts: []multipartTestPart{{Name: "request", Body: `{"command":"cat"}`}, {Name: "other", Body: "data"}}},
		{name: "duplicate request", parts: []multipartTestPart{{Name: "request", Body: `{"command":"cat"}`}, {Name: "request", Body: `{"command":"cat"}`}}},
		{name: "duplicate stdin", parts: []multipartTestPart{{Name: "request", Body: `{"command":"cat"}`}, {Name: "stdin", Body: "one"}, {Name: "stdin", Body: "two"}}},
		{name: "malformed request", parts: []multipartTestPart{{Name: "request", Body: `{"command":`}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, server := testServer(t)
			body, contentType := makeMultipart(t, tc.parts)
			req, err := http.NewRequest("POST", server.URL+"/api/commands", bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+s.token)
			req.Header.Set("Content-Type", contentType)
			resp, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			responseBody, _ := io.ReadAll(resp.Body)
			if tc.name == "duplicate stdin" {
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("HTTP %d, want 200: %s", resp.StatusCode, responseBody)
				}
				events := commandEvents(t, responseBody)
				if !strings.Contains(events[len(events)-1].Error, "unexpected multipart part") {
					t.Fatalf("missing duplicate stdin error: %+v", events[len(events)-1])
				}
				return
			}
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("HTTP %d, want 400: %s", resp.StatusCode, responseBody)
			}
			if len(strings.TrimSpace(string(responseBody))) == 0 {
				t.Fatal("missing malformed multipart error")
			}
		})
	}
}

func TestExecuteJSONOnlyRequestRemainsUnchanged(t *testing.T) {
	s, server := testServer(t)
	body, _ := json.Marshal(executeRequest{Command: os.Args[0], Args: []string{"-test.run=^TestCommandStdinHashHelper$"}})
	status, data := requestAPI(t, s, server, "POST", "/api/commands", string(body))
	if status != http.StatusOK {
		t.Fatalf("execute: HTTP %d %s", status, data)
	}
	events := commandEvents(t, data)
	if got := stdoutData(events); got != hex.EncodeToString(sumBytes(nil)) {
		t.Fatalf("JSON-only stdin hash %q", got)
	}
	assertSuccessfulCommand(t, events)
}

func TestExecuteRejectsOversizeStdinWithoutLargeAllocation(t *testing.T) {
	s, _ := testServer(t)
	body, contentType := makeMultipart(t, []multipartTestPart{{Name: "request", Body: mustJSON(t, executeRequest{
		Command: os.Args[0], Args: []string{"-test.run=^TestCommandStdinHashHelper$"},
	})}, {Name: "stdin", Body: "abc"}})
	req := httptest.NewRequest("POST", "/api/commands", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	recorder := httptest.NewRecorder()
	s.executeWithStdinLimit(recorder, req, 2)
	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", recorder.Code, recorder.Body.String())
	}
	events := commandEvents(t, recorder.Body.Bytes())
	if events[len(events)-1].Error == "" || !strings.Contains(events[len(events)-1].Error, "stdin") {
		t.Fatalf("missing stdin size error: %+v", events[len(events)-1])
	}
}

func TestExecuteCancellationStopsStreamingStdin(t *testing.T) {
	s, _ := testServer(t)
	requestJSON := mustJSON(t, executeRequest{Command: os.Args[0], Args: []string{"-test.run=^TestCommandStdinWaitHelper$"}})
	requestReader, requestWriter := io.Pipe()
	multipartWriter := multipart.NewWriter(requestWriter)
	request := httptest.NewRequest("POST", "/api/commands", requestReader)
	request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	ctx, cancel := context.WithCancel(context.Background())
	request = request.WithContext(ctx)

	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		s.executeWithStdinLimit(recorder, request, maxCommandStdinBytes)
		result <- recorder
	}()
	go func() {
		field, err := multipartWriter.CreateFormField("request")
		if err != nil {
			return
		}
		_, _ = field.Write([]byte(requestJSON))
		if _, err := multipartWriter.CreateFormField("stdin"); err != nil {
			return
		}
		// Keep the stdin part open until cancellation proves it can interrupt the copy.
		<-ctx.Done()
		_ = multipartWriter.Close()
		_ = requestWriter.Close()
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case recorder := <-result:
		if recorder.Code != http.StatusOK {
			t.Fatalf("HTTP %d: %s", recorder.Code, recorder.Body.String())
		}
		verifyCanceledCommandProcess(t, commandEvents(t, recorder.Body.Bytes()))
	case <-time.After(3 * time.Second):
		t.Fatal("canceled stdin command did not complete")
	}
}

func TestCommandStdinHashHelper(t *testing.T) {
	if os.Getenv("OMO_COMPANY") != "1" {
		return
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, os.Stdin); err != nil {
		os.Exit(2)
	}
	fmt.Fprint(os.Stdout, hex.EncodeToString(hash.Sum(nil)))
	os.Exit(0)
}

func TestCommandEarlyExitHelper(t *testing.T) {
	if os.Getenv("OMO_COMPANY") == "1" {
		os.Exit(0)
	}
}

func TestCommandStdinWaitHelper(t *testing.T) {
	if os.Getenv("OMO_COMPANY") == "1" {
		fmt.Fprintln(os.Stdout, os.Getpid())
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
}

type multipartTestPart struct {
	Name string
	Body string
}

func executeMultipart(t *testing.T, s *Server, server *httptest.Server, request executeRequest, stdin []byte) []byte {
	t.Helper()
	body, contentType := makeMultipart(t, []multipartTestPart{{Name: "request", Body: mustJSON(t, request)}, {Name: "stdin", Body: string(stdin)}})
	req, err := http.NewRequest("POST", server.URL+"/api/commands", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", contentType)
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("execute: HTTP %d %s", resp.StatusCode, data)
	}
	return data
}

func makeMultipart(t *testing.T, parts []multipartTestPart) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, part := range parts {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"`, part.Name))
		field, err := writer.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := field.Write([]byte(part.Body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), writer.FormDataContentType()
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func commandEvents(t *testing.T, data []byte) []commandEvent {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	events := make([]commandEvent, 0, len(lines))
	for _, line := range lines {
		var event commandEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("invalid command event %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func stdoutData(events []commandEvent) string {
	var data strings.Builder
	for _, event := range events {
		if event.Stream == "stdout" {
			data.WriteString(event.Data)
		}
	}
	return data.String()
}

func assertSuccessfulCommand(t *testing.T, events []commandEvent) {
	t.Helper()
	if len(events) == 0 || events[len(events)-1].Type != "exit" || events[len(events)-1].Code != 0 {
		t.Fatalf("command did not exit successfully: %+v", events)
	}
}

func sumBytes(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}
