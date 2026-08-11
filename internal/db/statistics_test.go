package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestOverallStatisticsUpsertsOneRowPerModel(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "omo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := UpsertOverallStatistics(d, []ModelStatistics{
		{Model: "alpha", AgentsStarted: 2, Active: 3 * time.Second, Idle: time.Second},
		{Model: "beta", AgentsStarted: 1, Active: time.Second},
	}); err != nil {
		t.Fatal(err)
	}
	if err := UpsertOverallStatistics(d, []ModelStatistics{
		{Model: "alpha", AgentsStarted: 4, Active: 8 * time.Second, Idle: 2 * time.Second, CEOActive: time.Second},
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := OverallStatistics(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Model != "alpha" || rows[0].AgentsStarted != 4 || rows[0].Active != 8*time.Second || rows[0].CEOActive != time.Second {
		t.Fatalf("statistics rows = %+v", rows)
	}
}
