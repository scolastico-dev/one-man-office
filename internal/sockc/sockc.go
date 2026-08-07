// Package sockc is the agent-side socket client used by omo's CLI verbs.
package sockc

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/scolastico-dev/one-man-office/internal/proto"
)

// Env reads the agent identity injected at spawn time.
func Env() (socket, agentID string, err error) {
	socket = os.Getenv("OMO_SOCKET")
	agentID = os.Getenv("OMO_AGENT_ID")
	if socket == "" || agentID == "" {
		return "", "", errors.New("OMO_SOCKET / OMO_AGENT_ID not set — this command only works inside an omo agent session")
	}
	return socket, agentID, nil
}

// Call performs one verb round-trip. No deadline: verbs like `wait` block
// until the supervisor wakes the agent.
func Call(socket, agentID, verb string, args any, out any) error {
	conn, err := dial(socket)
	if err != nil {
		return fmt.Errorf("dial %s: %w", socket, err)
	}
	defer conn.Close()
	var raw json.RawMessage
	if args != nil {
		raw, err = json.Marshal(args)
		if err != nil {
			return err
		}
	}
	if err := json.NewEncoder(conn).Encode(proto.Request{AgentID: agentID, Verb: verb, Args: raw}); err != nil {
		return err
	}
	var resp proto.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if !resp.OK {
		return errors.New(resp.Error)
	}
	if out != nil && resp.Data != nil {
		return json.Unmarshal(resp.Data, out)
	}
	return nil
}
