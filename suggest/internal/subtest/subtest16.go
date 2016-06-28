// +build !go1.7

package subtest

import "testing"

func RunParallel(t *testing.T, name string, test func(t *testing.T)) {
	test(t)
}
