package archtest

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// setup-android v3 defaults to the removed "tools" SDK package. Keep package
// selection explicit so an action's defaults cannot break SDK provisioning.
func TestAndroidSDKSetupDoesNotRequestLegacyTools(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var workflow struct {
			Jobs map[string]struct {
				Steps []struct {
					Uses string            `yaml:"uses"`
					With map[string]string `yaml:"with"`
				} `yaml:"steps"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(raw, &workflow); err != nil {
			t.Fatal(err)
		}
		for job, config := range workflow.Jobs {
			for _, step := range config.Steps {
				if !strings.HasPrefix(step.Uses, "android-actions/setup-android@") {
					continue
				}
				checked++
				packages, explicit := step.With["packages"]
				if !explicit {
					t.Errorf("%s job %s: select SDK packages explicitly instead of action defaults", file, job)
				}
				if slices.Contains(strings.Fields(packages), "tools") {
					t.Errorf("%s job %s: the Android SDK no longer provides the tools package", file, job)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no Android SDK setup step was checked")
	}
}
