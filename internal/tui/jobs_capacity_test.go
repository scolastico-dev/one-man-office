package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/queue"
)

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
