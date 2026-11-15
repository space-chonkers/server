//go:build !amd64 || amd64.v2

package marsgeo

import "runtime"

// See golden_target_v1_test.go.
const goldenByteHashTarget = false

var goldenTargetName = func() string {
	if runtime.GOARCH == "amd64" {
		return "amd64 with GOAMD64 v2 or higher"
	}
	return runtime.GOARCH
}()
