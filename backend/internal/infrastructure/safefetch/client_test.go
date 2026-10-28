package safefetch

import (
	"net/netip"
	"testing"
)

func TestPublicIP(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1", "169.254.169.254", "10.1.2.3", "192.168.1.2", "100.100.100.100", "0.0.0.0", "fe80::1", "fc00::1", "224.0.0.1"} {
		if PublicIP(netip.MustParseAddr(s)) {
			t.Errorf("accepted %s", s)
		}
	}
	if !PublicIP(netip.MustParseAddr("1.1.1.1")) {
		t.Fatal("public rejected")
	}
}
