package gorapide

// RapideTriv is Stanford Rapide's predefined trivial type. The type has
// exactly one value, denoted by the source literal Unit. It is deliberately
// distinct from nil, Boolean, Root, and an empty structural interface.
//
// The representation has no exported state, so every RapideTriv value has the
// same denotation and can be copied without introducing host identity.
type RapideTriv struct{}

// RapideUnit returns the sole value of RapideTriv.
func RapideUnit() RapideTriv { return RapideTriv{} }

func (RapideTriv) String() string { return "Unit" }
