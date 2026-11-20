package server

import (
	"encoding/base64"
	"errors"
	"net/http"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/relayhost"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
)

type relayHumanKey struct{}

func (s *Server) initRelay() error {
	store, e := relayhost.OpenStore(s.opts.Layout.Relay)
	if e != nil {
		return e
	}
	manager, e := relayhost.NewManager(store, relayhost.Options{Origin: s.opts.Config.RelayURL, Enabled: s.opts.Config.RelayEnabled, SaveConfig: func(origin string, enabled bool) error {
		config, e := serverconfig.Load(s.opts.Layout.ServerConfig)
		if e != nil {
			return e
		}
		config.RelayURL = origin
		config.RelayEnabled = enabled
		return serverconfig.Save(s.opts.Layout.ServerConfig, config)
	}})
	if e != nil {
		return e
	}
	s.relay = manager
	manager.SetCallbacks(relayhost.Callbacks{PollNotifications: s.pollRelayNotifications})
	s.background(manager.Run)
	return nil
}

// RelayManager is the integration point for the application/notification
// consumers. They install ServeSession and PollNotifications callbacks without
// gaining a separate relay control reader or file writer.
func (s *Server) RelayManager() *relayhost.Manager { return s.relay }

func (s *Server) registerRelay(mux *http.ServeMux) {
	rows := []apiRoute{
		{"GET /api/relay/status", s.relayStatus, localHumanOnly},
		{"POST /api/relay/connect", s.relayConnect, localHumanOnly},
		{"POST /api/relay/disconnect", s.relayDisconnect, localHumanOnly},
		{"POST /api/relay/pair/begin", s.relayPairBegin, localHumanOnly},
		{"GET /api/relay/pair/pending", s.relayPairPending, localHumanOnly},
		{"POST /api/relay/pair/accept", s.relayPairAccept, localHumanOnly},
		{"POST /api/relay/pair/cancel", s.relayPairCancel, localHumanOnly},
		{"GET /api/relay/devices", s.relayDevices, localHumanOnly},
		{"DELETE /api/relay/devices/{id}", s.relayUnregister, localHumanOnly},
	}
	for i := range rows {
		next := rows[i].handler
		rows[i].handler = func(w http.ResponseWriter, r *http.Request) {
			if s.relay == nil {
				writeError(w, r, api.Errorf(api.CodeUnavailable, "relay is unavailable"))
				return
			}
			next(w, r)
		}
	}
	s.registerRoutes(mux, rows)
}
func relayOperationError(err error) error {
	if errors.Is(err, relayhost.ErrState) {
		return api.Errorf(api.CodeUnavailable, "relay private state unavailable; repair it before restarting")
	}
	return api.Errorf(api.CodeBadRequest, "relay operation refused; check the local configuration or begin a new pairing ceremony")
}
func (s *Server) relayStatus(w http.ResponseWriter, r *http.Request) {
	status := s.relay.Status()
	writeJSON(w, http.StatusOK, api.RelayStatus{State: status.State, Enabled: status.Enabled, Origin: status.Origin, HostID: status.HostID, HostFP: status.HostFP, RetrySeconds: status.RetrySeconds})
}
func (s *Server) relayConnect(w http.ResponseWriter, r *http.Request) {
	var req api.RelayConnectRequest
	if e := decode(w, r, &req); e != nil {
		writeError(w, r, e)
		return
	}
	if e := s.relay.Connect(relayhost.ConnectOptions{Origin: req.Origin, ClientID: req.ClientID, NewID: req.NewID, Password: req.Password}); e != nil {
		writeError(w, r, relayOperationError(e))
		return
	}
	s.relayStatus(w, r)
}
func (s *Server) relayDisconnect(w http.ResponseWriter, r *http.Request) {
	if !relayEmpty(w, r) {
		return
	}
	if e := s.relay.Disconnect(); e != nil {
		writeError(w, r, relayOperationError(e))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) relayPairBegin(w http.ResponseWriter, r *http.Request) {
	if !relayEmpty(w, r) {
		return
	}
	if e := s.relay.PairBegin(); e != nil {
		writeError(w, r, relayOperationError(e))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) relayPairPending(w http.ResponseWriter, r *http.Request) {
	pending, e := s.relay.PairPending()
	if e != nil {
		writeError(w, r, relayOperationError(e))
		return
	}
	writeJSON(w, http.StatusOK, api.RelayPending{Phase: string(pending.Phase), Expires: pending.Expires, AID: pending.AID, UIID: pending.UIID})
}
func (s *Server) relayPairCancel(w http.ResponseWriter, r *http.Request) {
	if !relayEmpty(w, r) {
		return
	}
	if e := s.relay.PairCancel(); e != nil {
		writeError(w, r, relayOperationError(e))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func decodeRelayBytes(text string) []byte {
	raw, e := base64.RawURLEncoding.DecodeString(text)
	if e != nil || base64.RawURLEncoding.EncodeToString(raw) != text {
		// Preserve a supplied invalid option. nil would turn a malformed full-key
		// attempt plus PIN into a valid PIN-only attempt. This marker cannot match
		// either fixed-length AID or full public key and consumes the local slot.
		return []byte{0}
	}
	return raw
}
func (s *Server) relayPairAccept(w http.ResponseWriter, r *http.Request) {
	var req api.RelayAcceptRequest
	if e := decode(w, r, &req); e != nil {
		writeError(w, r, e)
		return
	}
	d, e := s.relay.PairAccept(decodeRelayBytes(req.AID), req.PIN, decodeRelayBytes(req.PublicKey), req.Name)
	if e != nil {
		writeError(w, r, relayOperationError(e))
		return
	}
	writeJSON(w, http.StatusOK, api.RelayAccepted{AID: base64.RawURLEncoding.EncodeToString(d.AID), HostFP: s.relay.Status().HostFP, Device: s.relayDevice(d)})
}
func (s *Server) relayDevice(d relayhost.Device) api.RelayDevice {
	return api.RelayDevice{UIID: d.UIID, Name: d.Name, State: d.State, AcceptedAt: d.AcceptedAt, LiveConnections: s.relay.LiveConnections(d.UIID)}
}
func (s *Server) relayDevices(w http.ResponseWriter, r *http.Request) {
	devices, e := s.relay.Devices()
	if e != nil {
		writeError(w, r, relayOperationError(e))
		return
	}
	out := api.RelayDeviceList{Devices: make([]api.RelayDevice, 0, len(devices))}
	for _, d := range devices {
		out.Devices = append(out.Devices, s.relayDevice(d))
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) relayUnregister(w http.ResponseWriter, r *http.Request) {
	if e := s.relay.Unregister(r.PathValue("id")); e != nil {
		writeError(w, r, relayOperationError(e))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func relayEmpty(w http.ResponseWriter, r *http.Request) bool {
	if e := decode(w, r, &struct{}{}); e != nil {
		writeError(w, r, e)
		return false
	}
	return true
}
