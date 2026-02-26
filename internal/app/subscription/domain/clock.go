package domain

import "time"

// Clock provides an abstraction for time operations
type Clock interface {
	Now() time.Time
}

// RealClock is the production implementation of Clock
type RealClock struct{}

func (r RealClock) Now() time.Time {
	return time.Now()
}

// FixedClock is used for testing with deterministic time
type FixedClock struct {
	FixedTime time.Time
}

func (f FixedClock) Now() time.Time {
	return f.FixedTime
}
