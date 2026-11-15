package server

import (
	"net/http"
	"strconv"
)

// eventIDHeader names the response header that carries the id of the newest
// streamed event once a state-changing request is done. A client that waits
// until the event stream has delivered this id has seen every event the
// request caused.
const eventIDHeader = "Omo-Event-Id"

// newestStreamedEventID returns the highest id among the events the stream
// delivers, or 0 when there is none. Events that are not streamed are left
// out: the stream never delivers their ids, so a client waiting for one would
// wait forever.
func (s *Server) newestStreamedEventID() (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM events WHERE instr(kind, '.') > 0`).Scan(&id)
	return id, err
}

// withEventID wraps w so that the response to a state-changing API request
// carries eventIDHeader. Other requests get w back unchanged.
func (s *Server) withEventID(w http.ResponseWriter, r *http.Request) http.ResponseWriter {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || !isAPI(r.URL.Path) {
		return w
	}
	return &eventIDWriter{ResponseWriter: w, s: s}
}

// eventIDWriter adds eventIDHeader when the status line is written. Handlers
// write it after their changes are committed and, for settings changes, after
// their events were recorded, so the id read then is at least the id of every
// event the request produced. Responses with an error status get no header;
// a request that changed nothing reports the newest id that already exists.
type eventIDWriter struct {
	http.ResponseWriter
	s    *Server
	sent bool
}

func (e *eventIDWriter) WriteHeader(status int) {
	// Informational responses are followed by the final one.
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		e.ResponseWriter.WriteHeader(status)
		return
	}
	if !e.sent {
		e.sent = true
		if status < 400 {
			if id, err := e.s.newestStreamedEventID(); err != nil {
				e.s.log.Printf("read the newest event id: %v", err)
			} else if id > 0 {
				e.Header().Set(eventIDHeader, strconv.FormatInt(id, 10))
			}
		}
	}
	e.ResponseWriter.WriteHeader(status)
}

func (e *eventIDWriter) Write(b []byte) (int, error) {
	if !e.sent {
		e.WriteHeader(http.StatusOK)
	}
	return e.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (e *eventIDWriter) Unwrap() http.ResponseWriter { return e.ResponseWriter }
