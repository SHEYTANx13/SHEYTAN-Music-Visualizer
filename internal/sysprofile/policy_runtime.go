package sysprofile

import "runtime"

// gomaxprocsCurrent returns the live scheduler baseline. Kept as a tiny
// indirection so tests read like the contract they assert.
func gomaxprocsCurrent() int { return runtime.GOMAXPROCS(0) }
