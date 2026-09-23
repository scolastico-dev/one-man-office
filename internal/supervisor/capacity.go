package supervisor

import (
	"errors"
	"fmt"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/company/controlplane"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/modelusage"
	"github.com/scolastico-dev/one-man-office/internal/queue"
)

type capacitySpawn struct {
	role, profile, dir, goal                  string
	attempt                                   int
	configured, forceUsage, managementRestart bool
}

type jobSpawnKey struct {
	role  string
	jobID int64
}

type capacityDeferral struct {
	reason    string
	nextRetry time.Time
	denials   int
}

// CapacityDeferralView is the transient wait shown for a queued job.
type CapacityDeferralView struct {
	Reason    string
	NextRetry time.Time
}

// CapacityDeferralSnapshot reads waits for already loaded queued jobs without
// querying the job store again. The result is detached from the live map.
func (s *Supervisor) CapacityDeferralSnapshot(jobs []*queue.Job) map[int64]CapacityDeferralView {
	s.mu.Lock()
	defer s.mu.Unlock()
	views := make(map[int64]CapacityDeferralView)
	for _, job := range jobs {
		if job == nil || job.State != queue.StateQueued {
			continue
		}
		if d, ok := s.capacityDeferrals[jobSpawnKey{job.Role, job.ID}]; ok {
			views[job.ID] = CapacityDeferralView{Reason: d.reason, NextRetry: d.nextRetry}
		}
	}
	return views
}

// CapacityDeferral reports an active aggregate lease wait for a queued job.
func (s *Supervisor) CapacityDeferral(jobID int64) (string, time.Time, bool) {
	j, err := s.Jobs.Get(jobID)
	if err != nil || j.State != queue.StateQueued {
		return "", time.Time{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.capacityDeferrals[jobSpawnKey{j.Role, jobID}]
	return d.reason, d.nextRetry, ok
}

func (s *Supervisor) capacityRetryDue(role string, jobID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.capacityDeferrals[jobSpawnKey{role, jobID}]
	return !ok || !time.Now().Before(d.nextRetry)
}

func (s *Supervisor) recordCapacityDenial(role string, jobID int64, denial error) {
	if jobID == 0 {
		return
	}
	j, err := s.Jobs.Get(jobID)
	if err != nil || j.State != queue.StateQueued {
		return
	}
	cfg := s.Config().Agents.CapacityRetry
	delay := time.Duration(cfg.Initial)
	if delay <= 0 {
		delay = 5 * time.Second
	}
	maxDelay := time.Duration(cfg.Max)
	if maxDelay < delay {
		maxDelay = delay
	}
	key := jobSpawnKey{role, jobID}
	s.mu.Lock()
	d := s.capacityDeferrals[key]
	d.denials++
	for i := 1; i < d.denials && delay < maxDelay; i++ {
		if delay > maxDelay/2 {
			delay = maxDelay
		} else {
			delay *= 2
		}
	}
	d.reason = "capacity"
	d.nextRetry = time.Now().Add(delay)
	if s.capacityDeferrals == nil {
		s.capacityDeferrals = map[jobSpawnKey]capacityDeferral{}
	}
	s.capacityDeferrals[key] = d
	s.mu.Unlock()
	_ = db.AppendEvent(s.DB, "dispatch_deferred", "", jobID, fmt.Sprintf("aggregate agent capacity reached; retry in %s", delay))
	if generation, ok := controlplane.DeniedGeneration(denial); ok && s.Control != nil && s.Control.CapacityChangedSince(generation) {
		s.capacityAvailable()
	}
}

func (s *Supervisor) clearCapacityDeferral(role string, jobID int64) {
	s.mu.Lock()
	delete(s.capacityDeferrals, jobSpawnKey{role, jobID})
	s.mu.Unlock()
}

func (s *Supervisor) capacityAvailable() {
	s.mu.Lock()
	clear(s.capacityDeferrals)
	s.mu.Unlock()
	s.kickDispatch()
}

var errDeferredProfile = errors.New("deferred spawn is waiting for an eligible profile")

// A capacity wait must preserve real handshake progress, but it must not
// preserve permission to launch credentials that have since become ineligible.
func (s *Supervisor) revalidateDeferredSpawn(request capacitySpawn, jobID int64) (capacitySpawn, error) {
	if !request.configured || request.managementRestart {
		return request, s.checkExplicitProfile(request.profile, request.forceUsage)
	}
	role := request.role
	if role == "branch_namer" {
		role = "smokealarm"
	}
	eligible, _, err := s.usageEligible(role, s.Config().Roles[role].Models)
	if err != nil {
		return request, err
	}
	for _, key := range eligible {
		if key == request.profile {
			return request, nil
		}
	}
	profile, err := s.roleProfile(role, request.attempt)
	if err != nil {
		return request, err
	}
	request.profile = profile
	return request, nil
}

// Selection before spawnAttempt must not discard a deferred request's retry
// state or turn its temporary quota wait into a terminal selection failure.
func (s *Supervisor) deferredJobSpawn(role string, jobID int64) (capacitySpawn, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	request, ok := s.pendingJobSpawns[jobSpawnKey{role, jobID}]
	return request, ok
}

func (s *Supervisor) takeDeferredJobSpawn(role string, jobID int64) (capacitySpawn, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := jobSpawnKey{role, jobID}
	request, ok := s.pendingJobSpawns[key]
	delete(s.pendingJobSpawns, key)
	return request, ok
}

func (s *Supervisor) rememberDeferredJobSpawn(role string, jobID int64, request capacitySpawn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendingJobSpawns == nil {
		s.pendingJobSpawns = map[jobSpawnKey]capacitySpawn{}
	}
	s.pendingJobSpawns[jobSpawnKey{role, jobID}] = request
}

func (s *Supervisor) clearDeferredJobSpawns(jobID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.pendingJobSpawns {
		if key.jobID == jobID {
			delete(s.pendingJobSpawns, key)
		}
	}
	for key := range s.capacityDeferrals {
		if key.jobID == jobID {
			delete(s.capacityDeferrals, key)
		}
	}
}

func (s *Supervisor) deferExplicitRestart(a *db.Agent) {
	if a.JobID != 0 || a.Role == "ceo" || a.Role == "firefighter" || a.Role == "smokealarm" {
		return
	}
	s.queueExplicitRestart(a.Name, capacitySpawn{role: a.Role, profile: a.Profile, dir: a.WorkDir, goal: a.Goal, configured: true, managementRestart: true})
}

func (s *Supervisor) queueExplicitRestart(name string, request capacitySpawn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendingRestarts == nil {
		s.pendingRestarts = map[string]capacitySpawn{}
	}
	s.pendingRestarts[name] = request
}

func (s *Supervisor) resumeExplicitRestarts() {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return
	}
	pending := s.pendingRestarts
	s.pendingRestarts = nil
	s.mu.Unlock()
	for name, request := range pending {
		validated, err := s.revalidateDeferredSpawn(request, 0)
		if err != nil {
			s.queueExplicitRestart(name, request)
			continue
		}
		request = validated
		if _, err := s.spawnAttempt(request.role, request.profile, 0, request.dir, request.goal, request.attempt, request.configured, request.forceUsage, request.managementRestart); errors.Is(err, controlplane.ErrLimit) {
			s.queueExplicitRestart(name, request)
		}
	}
}

func spawnBackpressure(err error) bool {
	return errors.Is(err, controlplane.ErrLimit) || errors.Is(err, ErrSpawningHalted) || errors.Is(err, errDeferredProfile)
}

func (s *Supervisor) deferManagementSpawn(request capacitySpawn) {
	if request.role != "ceo" && request.role != "firefighter" && request.role != "smokealarm" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if request.role == "smokealarm" {
		for _, pending := range s.pendingSmoke {
			if pending == request {
				return
			}
		}
		s.pendingSmoke = append(s.pendingSmoke, request)
		return
	}
	if s.pendingCapacity == nil {
		s.pendingCapacity = map[string]capacitySpawn{}
	}
	s.pendingCapacity[request.role] = request
}

func (s *Supervisor) resumeCapacitySpawns() {
	for _, role := range []string{"ceo", "firefighter"} {
		if !s.spawnAllowed(role) {
			continue
		}
		s.mu.Lock()
		request, ok := s.pendingCapacity[role]
		delete(s.pendingCapacity, role)
		s.mu.Unlock()
		if !ok {
			continue
		}
		if n, err := db.CountLivingByRole(s.DB, role); err != nil || n > 0 {
			continue
		}
		validated, err := s.revalidateDeferredSpawn(request, 0)
		if err != nil {
			s.deferManagementSpawn(request)
			continue
		}
		request = validated
		_, _ = s.spawnAttempt(request.role, request.profile, 0, request.dir, request.goal, request.attempt, request.configured, request.forceUsage, request.managementRestart)
	}
	s.resumeExplicitRestarts()
	s.mu.Lock()
	pendingSmoke := len(s.pendingSmoke) > 0
	s.mu.Unlock()
	if pendingSmoke {
		select {
		case s.smokeCapacityWake <- struct{}{}:
		default:
		}
	}
}

func (s *Supervisor) takePendingSmoke() []capacitySpawn {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := s.pendingSmoke
	s.pendingSmoke = nil
	return pending
}

// Preserve durable work when a replacement cannot obtain a process lease.
// Review jobs remain in review and are picked up by resumePendingReviews.
func (s *Supervisor) deferJobSpawn(role string, jobID int64, reason error) {
	if jobID == 0 {
		return
	}
	if role == "branch_namer" {
		s.failBranchNaming(jobID, reason)
		return
	}
	j, err := s.Jobs.Get(jobID)
	if err != nil {
		return
	}
	if role != "reviewer" && (j.State == queue.StateAssigned || j.State == queue.StateWorking || j.State == queue.StateRework) {
		_ = s.Jobs.SetAssignee(jobID, "")
		_ = s.Jobs.Transition(jobID, queue.StateQueued)
	}
	if errors.Is(reason, controlplane.ErrLimit) {
		// A replacement can be denied while its job is still assigned.
		// Requeue first, then make the wait visible to dispatch.
		if _, _, ok := s.CapacityDeferral(jobID); !ok {
			s.recordCapacityDenial(role, jobID, reason)
		}
	} else {
		s.kickDispatch()
	}
}

// The parent froze usage profiles at registration. Reject incompatible
// reloads before preflight so neither stale accounting nor a poisoned client
// can follow a local model edit. Ordinary arguments and limits may change.
func (s *Supervisor) validateSupervisedReload(next *config.Config) error {
	if s.Control == nil {
		return nil
	}
	current := s.Config()
	for key := range current.Models {
		if _, ok := next.Models[key]; !ok {
			return fmt.Errorf("profile %q was registered with the parent and cannot be removed during reload; restart the supervised office to apply this configuration", key)
		}
	}
	for key, profile := range next.Models {
		old, ok := current.Models[key]
		if !ok || agentcli.Resolve(old.Provider, old.Cmd) != agentcli.Resolve(profile.Provider, profile.Cmd) || modelusage.Scope(old) != modelusage.Scope(profile) {
			return fmt.Errorf("profile %q changes the parent's registered provider or credential scope; restart the supervised office to apply this configuration", key)
		}
	}
	return nil
}
