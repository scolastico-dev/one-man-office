//go:build linux

package sandbox

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/landlock-lsm/go-landlock/landlock"
)

func TestLandlockConfigForABI(t *testing.T) {
	for _, tc := range []struct {
		abi  int
		want landlock.Config
	}{
		{5, landlock.V5},
		{6, landlock.V6},
		{7, landlock.V7},
		{8, landlock.V8},
		{9, landlock.V9},
		{10, landlock.V9},
	} {
		t.Run(strconv.Itoa(tc.abi), func(t *testing.T) {
			got, err := landlockConfigForABI(tc.abi)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ABI %d config = %v, want %v", tc.abi, got, tc.want)
			}
		})
	}
	for _, abi := range []int{0, 4} {
		_, err := landlockConfigForABI(abi)
		if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "required V5") {
			t.Fatalf("ABI %d error = %v, want sandbox unsupported with required V5", abi, err)
		}
	}
}
