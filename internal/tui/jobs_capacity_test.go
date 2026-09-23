package tui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/company/controlplane"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"gopkg.in/yaml.v3"
)

func TestLiveJobsViewShowsDeniedLeaseAndClearsCancelledJob(t *testing.T) {
	m := testModel(t)
	m.o.Dir = m.o.Sup.OfficeDir
	m.o.Sup.Cfg.Models = make(map[string]config.Profile)
	m.o.Sup.Cfg.Roles = make(map[string]config.RoleModels)
	for _, role := range config.AllRoles {
		m.o.Sup.Cfg.Models[role] = config.Profile{Cmd: "true"}
		m.o.Sup.Cfg.Roles[role] = config.RoleModels{Models: []string{role}, Assignment: config.AssignmentRoundRobin}
	}
	m.o.Sup.Cfg.Usage.Enabled = false
	if err := os.MkdirAll(filepath.Join(m.o.Dir, ".omo"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := yaml.Marshal(m.o.Sup.Cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.o.Dir, ".omo", "omo.yaml"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	server := controlplane.New(1, nil, time.Minute)
	h := httptest.NewServer(server.Handler())
	defer h.Close()
	parentToken, err := server.Register("parent", m.o.Dir)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := controlplane.NewClient(h.URL, parentToken)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := parent.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Release(context.Background(), lease)
	waitingToken, err := server.Register("waiting", m.o.Dir)
	if err != nil {
		t.Fatal(err)
	}
	m.o.Sup.Control, err = controlplane.NewClient(h.URL, waitingToken)
	if err != nil {
		t.Fatal(err)
	}
	j := &queue.Job{Title: "waiting job", Goal: "work", Role: "freelancer"}
	if err := m.o.Sup.Jobs.Create(j); err != nil {
		t.Fatal(err)
	}
	if _, err := m.o.Sup.Spawn("freelancer", "freelancer", j.ID, m.o.Dir, j.Goal); !errors.Is(err, controlplane.ErrLimit) {
		t.Fatalf("spawn error = %v, want aggregate capacity denial", err)
	}
	reason, retry, ok := m.o.Sup.CapacityDeferral(j.ID)
	if !ok || reason != "capacity" || !retry.After(time.Now()) {
		t.Fatalf("spawn did not create a future capacity deferral: reason=%q retry=%s ok=%t", reason, retry, ok)
	}
	retryLine := "Next retry: " + retry.UTC().Format(time.RFC3339)
	m.tab = tabJobs
	m.cache = &viewCache{}
	var list strings.Builder
	m.renderJobs(&list)
	if !strings.Contains(list.String(), "[deferred] waiting job") || !strings.Contains(list.String(), "Capacity: capacity") || !strings.Contains(list.String(), retryLine) {
		t.Fatalf("live Jobs list omitted deferral:\n%s", list.String())
	}
	detail, ok := m.selectedDetail()
	if !ok || !strings.Contains(detail.body, "Capacity: capacity") || !strings.Contains(detail.body, retryLine) {
		t.Fatalf("live Jobs detail omitted deferral: %+v", detail)
	}
	if err := m.o.Sup.Jobs.Transition(j.ID, queue.StateCancelled); err != nil {
		t.Fatal(err)
	}
	m.cache.beginView()
	list.Reset()
	m.renderJobs(&list)
	if strings.Contains(list.String(), "[deferred]") || strings.Contains(list.String(), "Capacity: capacity") {
		t.Fatalf("cancelled job retained deferral:\n%s", list.String())
	}
}

func TestJobsCapacityDeferralOnlyForQueuedJob(t *testing.T) {
	for _, tc := range []struct {
		name string
		job  string
		want bool
	}{
		{"queued", `{"ID":7,"Title":"capacity test","State":"queued","capacity_deferral_reason":"capacity","capacity_retry_at":"2026-09-23T12:34:56Z"}`, true},
		{"assigned stale fields", `{"ID":7,"Title":"capacity test","State":"assigned","capacity_deferral_reason":"capacity","capacity_retry_at":"2026-09-23T12:34:56Z"}`, false},
		{"queued without deferral", `{"ID":7,"Title":"capacity test","State":"queued"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var job queue.Job
			if err := json.Unmarshal([]byte(tc.job), &job); err != nil {
				t.Fatal(err)
			}
			m := testModel(t)
			m.tab = tabJobs
			m.cache = &viewCache{jobsLoaded: true, jobs: []*queue.Job{&job}}
			var overview strings.Builder
			m.renderJobs(&overview)
			if got := strings.Contains(overview.String(), "deferred"); got != tc.want {
				t.Errorf("overview deferred = %t, want %t:\n%s", got, tc.want, overview.String())
			}
			if tc.want && strings.Index(overview.String(), "[deferred]") > strings.Index(overview.String(), "capacity test") {
				t.Errorf("deferred indicator follows the title and may be clipped:\n%s", overview.String())
			}
			for _, fragment := range []string{"Capacity: capacity", "Next retry: 2026-09-23T12:34:56Z"} {
				if got := strings.Contains(overview.String(), fragment); got != tc.want {
					t.Errorf("overview detail presence of %q = %t, want %t:\n%s", fragment, got, tc.want, overview.String())
				}
			}
			detail, ok := m.selectedDetail()
			if !ok {
				t.Fatal("job detail unavailable")
			}
			for _, fragment := range []string{"Capacity: capacity", "Next retry: 2026-09-23T12:34:56Z"} {
				if got := strings.Contains(detail.body, fragment); got != tc.want {
					t.Errorf("detail presence of %q = %t, want %t:\n%s", fragment, got, tc.want, detail.body)
				}
			}
		})
	}
}
