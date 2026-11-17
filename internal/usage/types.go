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
	Name string
	// Label is "" or the window's name for people, as cleanLabel leaves it.
	Label       string
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
	// AgentEnv is the general agent environment (settings agent_env); a
	// run uses only its literal entries, except for a Codex refresh (see
	// the environment rule in helpers.go).
	AgentEnv map[string]string
}

// Result is what a script reported.
type Result struct {
	Windows []Window
	Log     []string
}

// RunFunc runs a job. Its error text is already redacted and safe to show.
type RunFunc func(ctx context.Context, j Job) (Result, error)

// RateLimitText is the error of a run that the usage API refused because
// it is asked too often.
const RateLimitText = "the usage API is rate limited; trying again later"

// RateLimitError is the error of a run that ended in omo.rate_limited: the
// usage API refused the request as too frequent. RetryAfter is the wait the
// API asked for, 0 when it did not say.
type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string { return RateLimitText }
