//go:build amd64 && !amd64.v2

package marsgeo

// goldenByteHashTarget: the exact-byte golden hash was pinned on amd64 at
// GOAMD64=v1 (the default). Higher levels may FMA-fuse math.Cos, so they and
// other architectures check values within 1e-6 m only.
const goldenByteHashTarget = true

const goldenTargetName = "amd64 GOAMD64=v1"
