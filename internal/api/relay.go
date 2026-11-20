package api

import "time"

// Relay administration is available only to a human on the local socket.
// These projections intentionally contain no private identity or trust material.
type RelayStatus struct {
	State        string `json:"state"`
	Enabled      bool   `json:"enabled"`
	Origin       string `json:"origin"`
	HostID       string `json:"host_id"`
	HostFP       string `json:"host_fp"`
	RetrySeconds int    `json:"retry_seconds"`
}
type RelayConnectRequest struct {
	Origin   string  `json:"origin"`
	ClientID string  `json:"client_id"`
	NewID    bool    `json:"new_id"`
	Password *string `json:"password,omitempty"`
}
type RelayAcceptRequest struct {
	AID       string `json:"aid"`
	PIN       string `json:"pin"`
	PublicKey string `json:"public_key"`
	Name      string `json:"name"`
}
type RelayPending struct {
	Phase   string    `json:"phase"`
	Expires time.Time `json:"expires"`
	AID     string    `json:"aid"`
	UIID    string    `json:"ui_id"`
}
type RelayDevice struct {
	UIID            string    `json:"ui_id"`
	Name            string    `json:"name"`
	State           string    `json:"state"`
	AcceptedAt      time.Time `json:"accepted_at"`
	LiveConnections int       `json:"live_connections"`
}
type RelayDeviceList struct {
	Devices []RelayDevice `json:"devices"`
}
type RelayAccepted struct {
	AID    string      `json:"aid"`
	HostFP string      `json:"host_fp"`
	Device RelayDevice `json:"device"`
}
