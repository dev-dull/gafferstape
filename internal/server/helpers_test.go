package server

import "github.com/dev-dull/gafferstape/internal/poller"

// fakeSnap is a SnapshotProvider that returns a pre-set Snapshot —
// shared by metrics_test.go and state_test.go.
type fakeSnap struct{ s poller.Snapshot }

func (f *fakeSnap) Snapshot() poller.Snapshot { return f.s }
