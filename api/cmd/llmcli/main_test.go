package main

import "testing"

func TestRequireLoopbackBindAcceptsLoopbackAddresses(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8090", "localhost:8090", "LOCALHOST:8090"} {
		if err := requireLoopbackBind(addr); err != nil {
			t.Errorf("requireLoopbackBind(%q) = %v, want nil", addr, err)
		}
	}
}

// TestRequireLoopbackBindRejectsWildcardAndNonLoopback is decision 9:
// the host-fallback mode must refuse to start on any other bind address.
func TestRequireLoopbackBindRejectsWildcardAndNonLoopback(t *testing.T) {
	for _, addr := range []string{":8090", "0.0.0.0:8090", "10.0.0.5:8090", "example.com:8090"} {
		if err := requireLoopbackBind(addr); err == nil {
			t.Errorf("requireLoopbackBind(%q) = nil, want an error", addr)
		}
	}
}
