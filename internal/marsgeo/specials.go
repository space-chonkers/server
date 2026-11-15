package marsgeo

// ISIS 32-bit special pixel bit patterns. HiRISE DTMs declare
// MISSING_CONSTANT = 16#FF7FFFFB# (ISIS NULL4). These patterns decode to
// finite float32 values near -3.4028e38, so a NaN/Inf check alone would let
// them through: they must be matched by raw bits.
const (
	isisSpecialLowBits  uint32 = 0xFF7FFFFA // lowest reserved pattern (ISIS VALID_MIN4 boundary), treated as invalid
	isisNull4Bits       uint32 = 0xFF7FFFFB // NULL4: the HiRISE MISSING_CONSTANT
	isisSpecialHighBits uint32 = 0xFF7FFFFF // HRS4
)

// isISISSpecialBits reports whether a raw float32 bit pattern lies in the
// ISIS special range 0xFF7FFFFA..0xFF7FFFFF.
func isISISSpecialBits(bits uint32) bool {
	return bits >= isisSpecialLowBits && bits <= isisSpecialHighBits
}
