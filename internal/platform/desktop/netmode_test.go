package desktop

import "testing"

func TestNetworkModeRoundTrip(t *testing.T) {
	l := Layout{Home: t.TempDir()}

	if got := l.NetworkMode(); got != NetLAN {
		t.Fatalf("default: want %q, got %q", NetLAN, got)
	}
	if err := l.SetNetworkMode(NetLocal); err != nil {
		t.Fatal(err)
	}
	if got := l.NetworkMode(); got != NetLocal {
		t.Fatalf("after set: want %q, got %q", NetLocal, got)
	}
	if err := l.SetNetworkMode("bogus"); err == nil {
		t.Fatal("expected error for invalid mode")
	}

	if NetLocal.BindHost() != "127.0.0.1" || NetLAN.BindHost() != "0.0.0.0" {
		t.Fatalf("BindHost mapping wrong: local=%s lan=%s", NetLocal.BindHost(), NetLAN.BindHost())
	}
}
