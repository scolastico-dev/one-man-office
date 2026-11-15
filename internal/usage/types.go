// Package usage reads how much of its limits an agent account has used: it
// runs a Lua usage script per account and keeps the readings.
package usage

import (
	"context"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// Window is one usage window a script reported.
type Window struct {
	Name        string
	UsedPercent float64
	// ResetsAt is the zero time when the script did not say.
	ResetsAt time.Time
}

// Job is one run of a usage script for one account.
type Job struct {
	// Script is the script's name.
	Script string
	// Text is the script's source.
	Text string
	// Profile is the account: CLI, command, account folder, environment.
	Profile api.Profile
	// AgentEnv is the general agent environment (settings agent_env).
	AgentEnv map[string]string
}

// Result is what a script reported.
type Result struct {
	Windows []Window
	Log     []string
}

// RunFunc runs a job. Its error text is already redacted and safe to show.
type RunFunc func(ctx context.Context, j Job) (Result, error)
