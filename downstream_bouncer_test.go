package soju

import (
	"context"
	"testing"

	"codeberg.org/emersion/soju/xirc"
)

func TestBouncerNetworksWithoutBindStaysRoot(t *testing.T) {
	srv := NewServer(nil)
	cfg := *srv.Config()
	cfg.BouncerNetworkBind = true
	srv.SetConfig(&cfg)

	dc := &downstreamConn{
		conn: &conn{srv: srv},
		caps: xirc.NewCapRegistry(),
		registration: &downstreamRegistration{
			networkName: "",
		},
	}
	dc.caps.SetEnabled("soju.im/bouncer-networks", true)

	if err := dc.loadNetwork(context.Background()); err != nil {
		t.Fatalf("loadNetwork() failed: %v", err)
	}
	if dc.network != nil {
		t.Fatalf("connection without BOUNCER BIND selected network %q", dc.network.Name)
	}
}

func TestBouncerNetworkDiscoveryDisabledForExplicitNetwork(t *testing.T) {
	srv := NewServer(nil)
	cfg := *srv.Config()
	cfg.BouncerNetworkBind = true
	srv.SetConfig(&cfg)

	dc := &downstreamConn{
		conn: &conn{srv: srv},
		caps: xirc.NewCapRegistry(),
		registration: &downstreamRegistration{
			username: "testuser/testnet@client",
		},
	}
	dc.caps.Available["soju.im/bouncer-networks"] = ""
	dc.caps.Available["soju.im/bouncer-networks-notify"] = ""
	dc.caps.SetEnabled("soju.im/bouncer-networks", true)
	dc.caps.SetEnabled("soju.im/bouncer-networks-notify", true)

	dc.updateBouncerNetworkDiscovery(context.Background())

	for _, name := range []string{"soju.im/bouncer-networks", "soju.im/bouncer-networks-notify"} {
		if dc.caps.IsAvailable(name) || dc.caps.IsEnabled(name) {
			t.Fatalf("%s remains available or enabled", name)
		}
	}
}

func TestBouncerNetworkDiscoveryUnchangedWhenDisabled(t *testing.T) {
	srv := NewServer(nil)
	dc := &downstreamConn{
		conn: &conn{srv: srv},
		caps: xirc.NewCapRegistry(),
		registration: &downstreamRegistration{
			username: "testuser/testnet@client",
		},
	}
	dc.caps.Available["soju.im/bouncer-networks"] = ""

	dc.updateBouncerNetworkDiscovery(context.Background())

	if !dc.caps.IsAvailable("soju.im/bouncer-networks") {
		t.Fatalf("soju.im/bouncer-networks unexpectedly unavailable")
	}
}

func TestBouncerNetworkDiscoveryDisabledAfterBind(t *testing.T) {
	srv := NewServer(nil)
	cfg := *srv.Config()
	cfg.BouncerNetworkBind = true
	srv.SetConfig(&cfg)

	dc := &downstreamConn{
		conn: &conn{srv: srv},
		caps: xirc.NewCapRegistry(),
		registration: &downstreamRegistration{
			authUsername: "testuser",
		},
	}
	dc.caps.Available["soju.im/bouncer-networks"] = ""
	dc.caps.Available["soju.im/bouncer-networks-notify"] = ""
	dc.caps.SetEnabled("soju.im/bouncer-networks", true)
	dc.caps.SetEnabled("soju.im/bouncer-networks-notify", true)

	dc.registration.networkID = 1
	dc.disableBouncerNetworkDiscovery(context.Background())

	for _, name := range []string{"soju.im/bouncer-networks", "soju.im/bouncer-networks-notify"} {
		if dc.caps.IsAvailable(name) || dc.caps.IsEnabled(name) {
			t.Fatalf("%s remains available or enabled after BOUNCER BIND", name)
		}
	}
}
