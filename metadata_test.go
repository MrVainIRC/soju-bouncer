package soju

import (
	"context"
	"crypto/x509"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"codeberg.org/emersion/soju/config"
	"codeberg.org/emersion/soju/database"
	"codeberg.org/emersion/soju/xirc"
	"github.com/prometheus/client_golang/prometheus"
	"gopkg.in/irc.v4"
)

func TestIsMetadataKey(t *testing.T) {
	valid := []string{
		"avatar",
		"color",
		"display-name",
		"homepage",
		"pronouns",
		"status",
		"example.org/key-name_1",
	}
	for _, key := range valid {
		if !isMetadataKey(key) {
			t.Errorf("isMetadataKey(%q) = false, want true", key)
		}
	}

	invalid := []string{
		"",
		"Avatar",
		"status message",
		"emoji/😀",
	}
	for _, key := range invalid {
		if isMetadataKey(key) {
			t.Errorf("isMetadataKey(%q) = true, want false", key)
		}
	}
}

func TestRegisteredMetadataKeys(t *testing.T) {
	for _, key := range []string{"avatar", "color", "display-name", "homepage", "pronouns", "status"} {
		if !isRegisteredMetadataKey(key) {
			t.Errorf("isRegisteredMetadataKey(%q) = false, want true", key)
		}
		if !isMetadataKey(key) {
			t.Errorf("isMetadataKey(%q) = false, want true", key)
		}
	}
}

func TestMetadataCapVariants(t *testing.T) {
	for _, capName := range metadataCapNames {
		dc := &downstreamConn{caps: xirc.NewCapRegistry()}
		dc.caps.SetEnabled(capName, true)
		dc.metadataDialect = metadataVersionForCap(capName)
		if !dc.hasMetadataCap() {
			t.Errorf("downstream metadata cap %q not detected", capName)
		}

		uc := &upstreamConn{caps: xirc.NewCapRegistry()}
		uc.caps.Available[capName] = "max-keys=1"
		uc.caps.SetEnabled(capName, true)
		if !uc.hasMetadataCap() {
			t.Errorf("upstream metadata cap %q not detected", capName)
		}
	}
}

func TestMetadataCapsAreMutuallyExclusive(t *testing.T) {
	dc := newMetadataTestDownstream(nil)
	if err := dc.handleCap(context.Background(), &irc.Message{
		Command: "CAP",
		Params:  []string{"REQ", "batch sasl draft/metadata-2 draft/metadata-3"},
	}); err != nil {
		t.Fatalf("handleCap returned error: %v", err)
	}
	if got := dc.metadataVersion(); got != metadataVersion3 {
		t.Fatalf("metadata version = %v, want metadata-3", got)
	}
	if !dc.caps.IsEnabled("sasl") || !dc.caps.IsEnabled("batch") {
		t.Fatal("independent capabilities were not enabled")
	}
	if dc.caps.IsEnabled("draft/metadata-2") {
		t.Fatal("both metadata drafts were enabled")
	}
	reply := lastMetadataTestMessage(t, dc)
	if reply.Params[1] != "ACK" {
		t.Fatalf("metadata draft selection reply = %q, want ACK", reply.Params[1])
	}
	if strings.Contains(reply.Params[2], "draft/metadata-2") {
		t.Fatalf("unchosen metadata draft included in ACK: %v", reply)
	}

	dc = newMetadataTestDownstream(nil)
	if err := dc.handleCap(context.Background(), &irc.Message{
		Command: "CAP",
		Params:  []string{"REQ", "batch draft/metadata-2"},
	}); err != nil {
		t.Fatalf("enabling metadata-2 failed: %v", err)
	}
	if err := dc.handleCap(context.Background(), &irc.Message{
		Command: "CAP",
		Params:  []string{"REQ", "draft/metadata-3"},
	}); err != nil {
		t.Fatalf("requesting conflicting metadata draft failed: %v", err)
	}
	if got := dc.metadataVersion(); got != metadataVersion2 {
		t.Fatalf("metadata version changed silently to %v", got)
	}
	if err := dc.handleCap(context.Background(), &irc.Message{
		Command: "CAP",
		Params:  []string{"REQ", "-draft/metadata-2 draft/metadata-3"},
	}); err != nil {
		t.Fatalf("switching metadata draft failed: %v", err)
	}
	if got := dc.metadataVersion(); got != metadataVersion3 {
		t.Fatalf("metadata version after explicit switch = %v, want metadata-3", got)
	}
}

func TestDownstreamAdvertisesBothMetadataDrafts(t *testing.T) {
	if _, ok := permanentDownstreamCaps["draft/metadata-2"]; !ok {
		t.Fatal("Metadata-2 is not advertised downstream")
	}
	if _, ok := permanentDownstreamCaps["draft/metadata-3"]; !ok {
		t.Fatal("Metadata-3 is not advertised downstream")
	}
}

func TestDownstreamNegotiatesSingleMetadataDraft(t *testing.T) {
	tests := []struct {
		capName string
		want    metadataVersion
	}{
		{"draft/metadata-2", metadataVersion2},
		{"draft/metadata-3", metadataVersion3},
	}
	for _, tt := range tests {
		t.Run(tt.capName, func(t *testing.T) {
			dc := newMetadataTestDownstream(nil)
			err := dc.handleCap(context.Background(), &irc.Message{
				Command: "CAP",
				Params:  []string{"REQ", "batch " + tt.capName},
			})
			if err != nil {
				t.Fatalf("handleCap returned error: %v", err)
			}
			if got := dc.metadataVersion(); got != tt.want {
				t.Fatalf("metadata version = %v, want %v", got, tt.want)
			}
			if dc.caps.IsEnabled("draft/metadata-2") == dc.caps.IsEnabled("draft/metadata-3") {
				t.Fatal("expected exactly one enabled metadata draft")
			}
		})
	}
}

func TestUpstreamMetadataCapPrefersHighestDraft(t *testing.T) {
	conn := &metadataTestIRCConn{}
	uc := &upstreamConn{
		conn: newConn(newMetadataTestServer(nil), conn, &connOptions{Logger: newDebugLogger(t)}),
		caps: xirc.NewCapRegistry(),
	}
	uc.caps.Available["draft/metadata-2"] = "before-connect,max-keys=10"
	uc.caps.Available["draft/metadata-3"] = "before-connect,max-key-bytes=64"

	uc.updateCaps(context.Background())
	msg := waitForMetadataTestMessage(t, conn, 0)
	if msg.Command != "CAP" || len(msg.Params) < 2 {
		t.Fatalf("unexpected upstream capability request: %v", msg)
	}
	requested := strings.Fields(msg.Params[1])
	if !containsString(requested, "draft/metadata-3") {
		t.Fatalf("metadata-3 was not requested: %v", requested)
	}
	if containsString(requested, "draft/metadata-2") {
		t.Fatalf("metadata-2 was requested together with metadata-3: %v", requested)
	}
}

func TestUpstreamMetadata2RequestsNotifications(t *testing.T) {
	conn := &metadataTestIRCConn{}
	uc := &upstreamConn{
		conn: newConn(newMetadataTestServer(nil), conn, &connOptions{Logger: newDebugLogger(t)}),
		caps: xirc.NewCapRegistry(),
	}
	uc.caps.Available["draft/metadata-2"] = "before-connect"
	uc.caps.Available["draft/metadata-notify-2"] = "before-connect"

	uc.updateCaps(context.Background())
	msg := waitForMetadataTestMessage(t, conn, 0)
	requested := strings.Fields(msg.Params[1])
	if !containsString(requested, "draft/metadata-2") || !containsString(requested, "draft/metadata-notify-2") {
		t.Fatalf("metadata-2 notification capability was not requested: %v", requested)
	}
}

func TestMetadataDraftWireTranslation(t *testing.T) {
	value := "available"
	metadata2 := metadataNotification(metadataVersion2, "client", "nick", "status", &value)
	metadata3 := convertMetadataMessage(metadata2, metadataVersion2, metadataVersion3, "client", true)
	if metadata3.Command != xirc.RPL_KEYVALUE ||
		len(metadata3.Params) != 5 ||
		metadata3.Params[1] != "nick" ||
		metadata3.Params[2] != "status" ||
		metadata3.Params[4] != value {
		t.Fatalf("metadata-2 to metadata-3 notification = %v", metadata3)
	}

	removed3 := metadataNotification(metadataVersion3, "client", "nick", "status", nil)
	removed2 := convertMetadataMessage(removed3, metadataVersion3, metadataVersion2, "client", true)
	if removed2.Command != "METADATA" || len(removed2.Params) != 3 ||
		removed2.Params[0] != "nick" || removed2.Params[1] != "status" {
		t.Fatalf("metadata-3 to metadata-2 removal = %v", removed2)
	}

	err2 := &irc.Message{Command: "FAIL", Params: []string{"METADATA", "KEY_INVALID", "$bad", "Invalid key"}}
	err3 := convertMetadataMessage(err2, metadataVersion2, metadataVersion3, "client", false)
	if err3.Params[1] != "INVALID_KEY" {
		t.Fatalf("metadata-3 error code = %q, want INVALID_KEY", err3.Params[1])
	}
}

func TestMetadata3UsesNumericNotifications(t *testing.T) {
	dc := newMetadataTestDownstream(nil)
	dc.nick = "client"
	setMetadataTestVersion(dc, metadataVersion3)
	value := "online"

	dc.sendNetworkMetadataEvent(context.Background(), "", "client", "status", &value)
	msg := lastMetadataTestMessage(t, dc)
	if msg.Command != xirc.RPL_KEYVALUE || len(msg.Params) != 5 || msg.Params[4] != value {
		t.Fatalf("metadata-3 set notification = %v", msg)
	}

	count := dc.conn.conn.(*metadataTestIRCConn).messageCount()
	dc.sendNetworkMetadataEvent(context.Background(), "", "client", "status", nil)
	msg = waitForMetadataTestMessage(t, dc.conn.conn.(*metadataTestIRCConn), count)
	if msg.Command != xirc.RPL_KEYNOTSET || len(msg.Params) != 4 {
		t.Fatalf("metadata-3 removal notification = %v", msg)
	}
}

func TestSojuMetadataKeysAreOnlyExposedToMetadata2(t *testing.T) {
	dc, peer := newMetadataTestNetworkDownstreams(t)
	setMetadataTestVersion(dc, metadataVersion3)
	setMetadataTestVersion(peer, metadataVersion3)

	value := "1"
	dc.setMessageTargetMetadata(context.Background(), "#channel", &database.MessageTarget{}, map[string]*string{
		"soju.im/pinned": &value,
	}, "")

	msg := waitForMetadataTestMessage(t, dc.conn.conn.(*metadataTestIRCConn), 0)
	if msg.Command != "FAIL" || msg.Params[1] != "INVALID_KEY" {
		t.Fatalf("metadata-3 soju-key rejection = %v", msg)
	}
	time.Sleep(10 * time.Millisecond)
	if got := peer.conn.conn.(*metadataTestIRCConn).messageCount(); got != 0 {
		t.Fatalf("metadata-3 peer received Metadata-2 compatibility traffic: %v", peer.conn.conn.(*metadataTestIRCConn).messages())
	}
	before := dc.conn.conn.(*metadataTestIRCConn).messageCount()
	handled, err := dc.handleMetadataSub(context.Background(), &irc.Message{
		Command: "METADATA",
		Params:  []string{"*", "SUB", "soju.im/blocked"},
	})
	if err != nil || !handled {
		t.Fatalf("metadata-3 soju-key subscription handling failed: handled=%v err=%v", handled, err)
	}
	if dc.metadataSubs["soju.im/blocked"] {
		t.Fatalf("metadata-3 client subscribed to Metadata-2-only soju key")
	}
	msg = waitForMetadataTestMessage(t, dc.conn.conn.(*metadataTestIRCConn), before)
	if msg.Command != "FAIL" || msg.Params[1] != "INVALID_KEY" {
		t.Fatalf("metadata-3 soju-key subscription rejection = %v", msg)
	}

	setMetadataTestVersion(dc, metadataVersion2)
	setMetadataTestVersion(peer, metadataVersion2)
	dc.setMessageTargetMetadata(context.Background(), "#channel", &database.MessageTarget{}, map[string]*string{
		"soju.im/pinned": &value,
	}, "")
	msg = waitForMetadataTestMessage(t, peer.conn.conn.(*metadataTestIRCConn), 0)
	if msg.Command != "METADATA" || msg.Params[1] != "soju.im/pinned" {
		t.Fatalf("metadata-2 soju-key notification = %v", msg)
	}
}

func TestStandardMetadata3CommandRoutesUpstream(t *testing.T) {
	dc, _ := newMetadataTestNetworkDownstreams(t)
	setMetadataTestVersion(dc, metadataVersion3)
	ucConn := &metadataTestIRCConn{}
	uc := &upstreamConn{
		conn:        newConn(dc.srv, ucConn, &connOptions{Logger: newDebugLogger(t)}),
		network:     dc.network,
		user:        dc.user,
		caps:        xirc.NewCapRegistry(),
		pendingCmds: make(map[string][]pendingUpstreamCommand),
	}
	uc.caps.SetEnabled("draft/metadata-3", true)
	dc.network.conn = uc

	msg := &irc.Message{
		Command: "METADATA",
		Params:  []string{"*", "SET", "status", "available"},
	}
	if err := dc.handleMessageRegistered(context.Background(), msg); err != nil {
		t.Fatalf("handleMessageRegistered returned error: %v", err)
	}
	if len(uc.pendingCmds["METADATA"]) != 1 || uc.pendingCmds["METADATA"][0].msg != msg {
		t.Fatalf("standard metadata command was not queued upstream: %v", uc.pendingCmds["METADATA"])
	}
	sent := waitForMetadataTestMessage(t, ucConn, 0)
	if sent.Command != "METADATA" || len(sent.Params) < 3 || sent.Params[2] != "status" {
		t.Fatalf("unexpected upstream command: %v", sent)
	}
}

func TestClearIsNotSojuOnlyMetadata(t *testing.T) {
	msg := &irc.Message{Command: "METADATA", Params: []string{"*", "CLEAR"}}
	if metadataCommandUsesOnlySojuKeys(msg) {
		t.Fatal("CLEAR must use the normal metadata routing path")
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestHandleMetadataSubAcceptsRegisteredKeys(t *testing.T) {
	dc := newMetadataTestDownstream(nil)
	for _, key := range []string{"avatar", "color", "display-name", "homepage", "pronouns", "status"} {
		handled, err := dc.handleMetadataSub(context.Background(), &irc.Message{
			Command: "METADATA",
			Params:  []string{"*", "SUB", key},
		})
		if err != nil {
			t.Fatalf("handleMetadataSub(%q) returned error: %v", key, err)
		}
		if !handled {
			t.Fatalf("handleMetadataSub(%q) did not handle SUB", key)
		}
		if !dc.metadataSubs[key] {
			t.Fatalf("handleMetadataSub(%q) did not subscribe key", key)
		}
	}
}

func TestMetadataCapRequiresBatch(t *testing.T) {
	dc := newMetadataTestDownstream(nil)
	conn := dc.conn.conn.(*metadataTestIRCConn)
	if err := dc.handleCap(context.Background(), &irc.Message{
		Command: "CAP",
		Params:  []string{"REQ", "draft/metadata-2"},
	}); err != nil {
		t.Fatalf("handleCap returned error: %v", err)
	}
	if dc.caps.IsEnabled("draft/metadata-2") {
		t.Fatalf("draft/metadata-2 enabled without batch")
	}
	if got := lastMetadataTestMessage(t, dc).Params[1]; got != "NAK" {
		t.Fatalf("metadata without batch reply = %q, want NAK", got)
	}

	msgCount := conn.messageCount()
	if err := dc.handleCap(context.Background(), &irc.Message{
		Command: "CAP",
		Params:  []string{"REQ", "batch draft/metadata-2"},
	}); err != nil {
		t.Fatalf("handleCap returned error: %v", err)
	}
	if !dc.caps.IsEnabled("batch") || !dc.caps.IsEnabled("draft/metadata-2") {
		t.Fatalf("batch and draft/metadata-2 were not enabled together")
	}
	if got := waitForMetadataTestMessage(t, conn, msgCount).Params[1]; got != "ACK" {
		t.Fatalf("metadata with batch reply = %q, want ACK", got)
	}
}

func TestMetadataSubHandledLocallyWithNetwork(t *testing.T) {
	db := createTempSqliteDB(t)
	srv := newMetadataTestServer(db)
	userRecord := createTestUser(t, db)
	u := newUser(srv, userRecord)
	networkRecord := database.NewNetwork("irc+insecure://example.invalid")
	if err := db.StoreNetwork(context.Background(), userRecord.ID, networkRecord); err != nil {
		t.Fatalf("StoreNetwork() failed: %v", err)
	}
	net := newNetwork(u, networkRecord, nil)
	dc := newMetadataTestDownstream(db)
	dc.user = u
	dc.network = net
	u.downstreamConns = []*downstreamConn{dc}

	if err := dc.handleMessageRegistered(context.Background(), &irc.Message{
		Command: "METADATA",
		Params:  []string{"*", "SUB", "status"},
	}); err != nil {
		t.Fatalf("handleMessageRegistered returned error: %v", err)
	}
	if !dc.metadataSubs["status"] {
		t.Fatalf("status subscription was not stored locally")
	}

	msg := lastMetadataTestMessage(t, dc)
	if msg.Command != xirc.RPL_METADATASUBOK {
		t.Fatalf("reply command = %q, want %q", msg.Command, xirc.RPL_METADATASUBOK)
	}
	if len(msg.Params) != 2 || msg.Params[1] != "status" {
		t.Fatalf("reply params = %v, want status subscription acknowledgement", msg.Params)
	}
}

func TestUpstreamMetadataBatchPendingMapping(t *testing.T) {
	srv := newMetadataTestServer(nil)
	user := &user{
		User: database.User{ID: 1, Username: testUsername},
	}
	dc1 := newDownstreamConn(srv, &metadataTestIRCConn{}, 1)
	dc1.user = user
	dc1.id = 1
	dc1.nick = "one"
	dc1.caps.SetEnabled("batch", true)
	setMetadataTestVersion(dc1, metadataVersion2)
	dc2 := newDownstreamConn(srv, &metadataTestIRCConn{}, 2)
	dc2.user = user
	dc2.id = 2
	dc2.nick = "two"
	dc2.caps.SetEnabled("batch", true)
	setMetadataTestVersion(dc2, metadataVersion2)
	user.downstreamConns = []*downstreamConn{dc1, dc2}

	uc := &upstreamConn{
		conn:         newConn(srv, &metadataTestIRCConn{}, &connOptions{Logger: newDebugLogger(t)}),
		user:         user,
		network:      newNetwork(user, database.NewNetwork("irc+insecure://example.invalid"), nil),
		caps:         xirc.NewCapRegistry(),
		batches:      make(map[string]upstreamBatch),
		pendingCmds:  make(map[string][]pendingUpstreamCommand),
		serverPrefix: testServerPrefix,
		nick:         "nick",
	}
	uc.pendingCmds["METADATA"] = []pendingUpstreamCommand{
		{downstreamID: dc1.id, msg: &irc.Message{Command: "METADATA", Params: []string{"*", "LIST"}}},
		{downstreamID: dc2.id, msg: &irc.Message{Command: "METADATA", Params: []string{"*", "LIST"}}},
	}

	ctx := context.Background()
	if err := uc.handleMessage(ctx, &irc.Message{Tags: irc.Tags{}, Prefix: testServerPrefix, Command: "BATCH", Params: []string{"+m1", "metadata"}}); err != nil {
		t.Fatalf("handleMessage(+m1) failed: %v", err)
	}
	if got := uc.batches["u-m1"].metadataDownstreamID; got != dc1.id {
		t.Fatalf("metadata batch m1 downstream ID = %d, want %d", got, dc1.id)
	}
	if err := uc.handleMessage(ctx, &irc.Message{Tags: irc.Tags{"batch": "m1"}, Prefix: testServerPrefix, Command: xirc.RPL_KEYVALUE, Params: []string{"one", "*", "status", "online"}}); err != nil {
		t.Fatalf("handleMessage(m1 value) failed: %v", err)
	}
	if len(uc.pendingCmds["METADATA"]) != 2 {
		t.Fatalf("metadata command was dequeued before batch end: %v", uc.pendingCmds["METADATA"])
	}
	if err := uc.handleMessage(ctx, &irc.Message{Tags: irc.Tags{}, Prefix: testServerPrefix, Command: "BATCH", Params: []string{"-m1"}}); err != nil {
		t.Fatalf("handleMessage(-m1) failed: %v", err)
	}
	if len(uc.pendingCmds["METADATA"]) != 1 || uc.pendingCmds["METADATA"][0].downstreamID != dc2.id {
		t.Fatalf("metadata queue after m1 = %v, want only downstream %d", uc.pendingCmds["METADATA"], dc2.id)
	}
	if err := uc.handleMessage(ctx, &irc.Message{Tags: irc.Tags{}, Prefix: testServerPrefix, Command: "BATCH", Params: []string{"+m2", "metadata"}}); err != nil {
		t.Fatalf("handleMessage(+m2) failed: %v", err)
	}
	if got := uc.batches["u-m2"].metadataDownstreamID; got != dc2.id {
		t.Fatalf("metadata batch m2 downstream ID = %d, want %d", got, dc2.id)
	}
	if err := uc.handleMessage(ctx, &irc.Message{Tags: irc.Tags{"batch": "m2"}, Prefix: testServerPrefix, Command: xirc.RPL_KEYVALUE, Params: []string{"two", "*", "status", "away"}}); err != nil {
		t.Fatalf("handleMessage(m2 value) failed: %v", err)
	}
	if err := uc.handleMessage(ctx, &irc.Message{Tags: irc.Tags{}, Prefix: testServerPrefix, Command: "BATCH", Params: []string{"-m2"}}); err != nil {
		t.Fatalf("handleMessage(-m2) failed: %v", err)
	}
	if len(uc.pendingCmds["METADATA"]) != 0 {
		t.Fatalf("pending metadata commands left queued: %v", uc.pendingCmds["METADATA"])
	}
}

func TestMetadataClientSyncEnabled(t *testing.T) {
	dcA, dcB := newMetadataTestNetworkDownstreams(t)
	cfg := *dcA.srv.Config()
	cfg.MetadataClientSync = true
	dcA.srv.SetConfig(&cfg)

	value := "available"
	dcA.broadcastNetworkMetadata(context.Background(), dcA.nick, "status", &value)
	msg := lastMetadataTestMessage(t, dcB)
	if msg.Command != "METADATA" {
		t.Fatalf("broadcast command = %q, want METADATA", msg.Command)
	}
	if len(msg.Params) != 4 || msg.Params[1] != "status" || msg.Params[3] != value {
		t.Fatalf("broadcast params = %v, want status update", msg.Params)
	}
}

func TestMangoRootMetadataCompatStoresLastActiveProfile(t *testing.T) {
	dcNetwork, _ := newMetadataTestNetworkDownstreams(t)
	dcNetwork.clientName = "profileAlpha"

	cfg := *dcNetwork.srv.Config()
	cfg.MetadataRootCompat = true
	cfg.MetadataUpstreamPolicy = config.MetadataUpstreamPolicyLastActive
	dcNetwork.srv.SetConfig(&cfg)

	upstreamIRC := &metadataTestIRCConn{}
	uc := &upstreamConn{
		conn:        newConn(dcNetwork.srv, upstreamIRC, &connOptions{Logger: newDebugLogger(t)}),
		user:        dcNetwork.user,
		network:     dcNetwork.network,
		caps:        xirc.NewCapRegistry(),
		pendingCmds: make(map[string][]pendingUpstreamCommand),
	}
	uc.caps.Available["draft/metadata-2"] = ""
	uc.caps.SetEnabled("draft/metadata-2", true)
	dcNetwork.network.conn = uc

	secondRecord := database.NewNetwork("irc+insecure://second.invalid")
	if err := dcNetwork.srv.db.StoreNetwork(context.Background(), dcNetwork.user.ID, secondRecord); err != nil {
		t.Fatalf("StoreNetwork(second) failed: %v", err)
	}
	secondNetwork := newNetwork(dcNetwork.user, secondRecord, nil)
	secondUpstreamIRC := &metadataTestIRCConn{}
	secondUC := &upstreamConn{
		conn:        newConn(dcNetwork.srv, secondUpstreamIRC, &connOptions{Logger: newDebugLogger(t)}),
		user:        dcNetwork.user,
		network:     secondNetwork,
		caps:        xirc.NewCapRegistry(),
		pendingCmds: make(map[string][]pendingUpstreamCommand),
	}
	secondUC.caps.Available["draft/metadata-3"] = ""
	secondUC.caps.SetEnabled("draft/metadata-3", true)
	secondNetwork.conn = secondUC
	dcNetwork.user.networks = []*network{dcNetwork.network, secondNetwork}

	dcRoot := newDownstreamConn(dcNetwork.srv, &metadataTestIRCConn{}, 3)
	dcRoot.user = dcNetwork.user
	dcRoot.clientName = dcNetwork.clientName
	dcRoot.nick = testUsername
	setMetadataTestVersion(dcRoot, metadataVersion2)
	dcNetwork.user.downstreamConns = append(dcNetwork.user.downstreamConns, dcRoot)
	t.Cleanup(func() {
		_ = dcRoot.Close()
	})

	ctx := context.Background()
	set := &irc.Message{Command: "METADATA", Params: []string{"*", "SET", "status", "alpha"}}
	handled, err := dcRoot.mangoRootMetadataCompat(ctx, set)
	if err != nil || !handled {
		t.Fatalf("root SET = handled %v, err %v", handled, err)
	}
	assertMetadataTestCommands(t, upstreamIRC, 0, set.Params)
	assertMetadataTestCommands(t, secondUpstreamIRC, 0, set.Params)
	for _, network := range []*network{dcNetwork.network, secondNetwork} {
		profile, err := dcRoot.srv.db.ListClientNetworkMetadata(ctx, network.ID, dcRoot.clientName)
		if err != nil || len(profile) != 1 || profile[0].Key != "status" || profile[0].Value != "alpha" {
			t.Fatalf("root SET profile for %q = %#v, %v", network.GetName(), profile, err)
		}
		if network.metadataLastActiveClient != dcRoot.clientName {
			t.Fatalf("%q last active client = %q, want %q", network.GetName(), network.metadataLastActiveClient, dcRoot.clientName)
		}
	}

	clear := &irc.Message{Command: "METADATA", Params: []string{"*", "CLEAR"}}
	handled, err = dcRoot.mangoRootMetadataCompat(ctx, clear)
	if err != nil || !handled {
		t.Fatalf("root CLEAR = handled %v, err %v", handled, err)
	}
	assertMetadataTestCommands(t, upstreamIRC, 1, clear.Params)
	assertMetadataTestCommands(t, secondUpstreamIRC, 1, clear.Params)
	for _, network := range []*network{dcNetwork.network, secondNetwork} {
		profile, err := dcRoot.srv.db.ListClientNetworkMetadata(ctx, network.ID, dcRoot.clientName)
		if err != nil || len(profile) != 0 {
			t.Fatalf("root CLEAR profile for %q = %#v, %v", network.GetName(), profile, err)
		}
	}
}

func TestMetadataUpstreamPolicyNoneSkipsDisconnectedUpstream(t *testing.T) {
	dc, _ := newMetadataTestNetworkDownstreams(t)
	cfg := *dc.srv.Config()
	cfg.MetadataUpstreamPolicy = config.MetadataUpstreamPolicyNone
	dc.srv.SetConfig(&cfg)

	dc.forwardSelfMetadataUpstream(context.Background(), &irc.Message{
		Command: "METADATA",
		Params:  []string{"*", "SET", "status", "available"},
	})
}

func TestLastActiveMetadataProfiles(t *testing.T) {
	dcDesktop, dcMobile := newMetadataTestNetworkDownstreams(t)
	dcDesktop.clientName = "desktop"
	dcMobile.clientName = "mobile"
	cfg := *dcDesktop.srv.Config()
	cfg.MetadataUpstreamPolicy = config.MetadataUpstreamPolicyLastActive
	dcDesktop.srv.SetConfig(&cfg)

	upstreamIRC := &metadataTestIRCConn{}
	uc := &upstreamConn{
		conn:    newConn(dcDesktop.srv, upstreamIRC, &connOptions{Logger: newDebugLogger(t)}),
		user:    dcDesktop.user,
		network: dcDesktop.network,
		caps:    xirc.NewCapRegistry(),
	}
	uc.caps.Available["draft/metadata-2"] = ""
	uc.caps.SetEnabled("draft/metadata-2", true)
	uc.caps.Available["echo-message"] = ""
	uc.caps.SetEnabled("echo-message", true)
	dcDesktop.network.conn = uc

	ctx := context.Background()
	desktopStatus, desktopName := "desktop", "Desktop"
	mobileStatus := "mobile"
	for _, item := range []struct {
		client, key, value string
	}{
		{"desktop", "status", desktopStatus},
		{"desktop", "display-name", desktopName},
		{"mobile", "status", mobileStatus},
	} {
		if err := dcDesktop.srv.db.StoreClientNetworkMetadata(ctx, dcDesktop.network.ID, item.client, item.key, &item.value); err != nil {
			t.Fatalf("StoreClientNetworkMetadata(%s, %s) failed: %v", item.client, item.key, err)
		}
	}

	activity := &irc.Message{Command: "PRIVMSG", Params: []string{"#test", "hello"}}
	dcDesktop.handleMetadataClientActivity(ctx, activity)
	assertMetadataTestCommands(t, upstreamIRC, 0,
		[]string{"*", "SET", "display-name", desktopName},
		[]string{"*", "SET", "status", desktopStatus})

	if err := dcDesktop.srv.db.StoreNetworkMetadata(ctx, dcDesktop.network.ID, "display-name", &desktopName); err != nil {
		t.Fatal(err)
	}
	if err := dcDesktop.srv.db.StoreNetworkMetadata(ctx, dcDesktop.network.ID, "status", &desktopStatus); err != nil {
		t.Fatal(err)
	}
	dcDesktop.handleMetadataClientActivity(ctx, activity)
	assertMetadataTestCommands(t, upstreamIRC, 2)

	dcMobile.handleMetadataClientActivity(ctx, activity)
	assertMetadataTestCommands(t, upstreamIRC, 2,
		[]string{"*", "SET", "display-name"},
		[]string{"*", "SET", "status", mobileStatus})

	if err := dcDesktop.srv.db.StoreNetworkMetadata(ctx, dcDesktop.network.ID, "display-name", nil); err != nil {
		t.Fatal(err)
	}
	if err := dcDesktop.srv.db.StoreNetworkMetadata(ctx, dcDesktop.network.ID, "status", &mobileStatus); err != nil {
		t.Fatal(err)
	}
	dcDesktop.handleMetadataClientActivity(ctx, activity)
	assertMetadataTestCommands(t, upstreamIRC, 4,
		[]string{"*", "SET", "display-name", desktopName},
		[]string{"*", "SET", "status", desktopStatus})

	reconnected := newDownstreamConn(dcDesktop.srv, &metadataTestIRCConn{}, 3)
	reconnected.user = dcDesktop.user
	reconnected.network = dcDesktop.network
	reconnected.clientName = "desktop"
	reconnected.handleMetadataClientActivity(ctx, activity)
	assertMetadataTestCommands(t, upstreamIRC, 6)
	reconnected.Close()
}

func TestLastActiveMetadataIgnoresNonActivityAndOtherNetworks(t *testing.T) {
	dcA, _ := newMetadataTestNetworkDownstreams(t)
	dcA.clientName = "desktop"
	cfg := *dcA.srv.Config()
	cfg.MetadataUpstreamPolicy = config.MetadataUpstreamPolicyLastActive
	dcA.srv.SetConfig(&cfg)

	dcA.handleMetadataClientActivity(context.Background(), &irc.Message{Command: "PING", Params: []string{"token"}})
	if dcA.network.metadataLastActiveClient != "" {
		t.Fatalf("PING selected metadata client %q", dcA.network.metadataLastActiveClient)
	}

	otherRecord := database.NewNetwork("irc+insecure://other.invalid")
	otherRecord.ID = 2
	other := newNetwork(dcA.user, otherRecord, nil)
	other.metadataLastActiveClient = "mobile"
	dcA.handleMetadataClientActivity(context.Background(), &irc.Message{Command: "PRIVMSG", Params: []string{"#test", "hello"}})
	if dcA.network.metadataLastActiveClient != "desktop" || other.metadataLastActiveClient != "mobile" {
		t.Fatalf("network activity leaked: A=%q B=%q", dcA.network.metadataLastActiveClient, other.metadataLastActiveClient)
	}
}

func TestMetadataUpstreamPoliciesSelfSet(t *testing.T) {
	for _, policy := range []config.MetadataUpstreamPolicy{
		config.MetadataUpstreamPolicyAny,
		config.MetadataUpstreamPolicyNone,
		config.MetadataUpstreamPolicyLastActive,
	} {
		t.Run(string(policy), func(t *testing.T) {
			dc, _ := newMetadataTestNetworkDownstreams(t)
			dc.clientName = "desktop"
			cfg := *dc.srv.Config()
			cfg.MetadataUpstreamPolicy = policy
			dc.srv.SetConfig(&cfg)

			upstreamIRC := &metadataTestIRCConn{}
			uc := &upstreamConn{
				conn:        newConn(dc.srv, upstreamIRC, &connOptions{Logger: newDebugLogger(t)}),
				user:        dc.user,
				network:     dc.network,
				caps:        xirc.NewCapRegistry(),
				pendingCmds: make(map[string][]pendingUpstreamCommand),
			}
			uc.caps.Available["draft/metadata-2"] = ""
			uc.caps.SetEnabled("draft/metadata-2", true)
			dc.network.conn = uc

			ctx := context.Background()
			msg := &irc.Message{Command: "METADATA", Params: []string{"*", "SET", "status", "desktop"}}
			if err := dc.handleMessageRegistered(ctx, msg); err != nil {
				t.Fatalf("METADATA SET failed: %v", err)
			}

			switch policy {
			case config.MetadataUpstreamPolicyAny:
				assertMetadataTestCommands(t, upstreamIRC, 0, msg.Params)
			case config.MetadataUpstreamPolicyNone:
				assertMetadataTestCommands(t, upstreamIRC, 0)
				metadata, err := dc.srv.db.ListNetworkMetadata(ctx, dc.network.ID)
				if err != nil || len(metadata) != 1 || metadata[0].Value != "desktop" {
					t.Fatalf("none metadata = %#v, %v", metadata, err)
				}
			case config.MetadataUpstreamPolicyLastActive:
				assertMetadataTestCommands(t, upstreamIRC, 0, msg.Params)
				metadata, err := dc.srv.db.ListClientNetworkMetadata(ctx, dc.network.ID, dc.clientName)
				if err != nil || len(metadata) != 1 || metadata[0].Value != "desktop" {
					t.Fatalf("last-active client metadata = %#v, %v", metadata, err)
				}
				dc.network.metadataLastActiveClient = dc.clientName
				activeMsg := &irc.Message{Command: "METADATA", Params: []string{"*", "SET", "status", "desktop-active"}}
				if err := dc.handleMessageRegistered(ctx, activeMsg); err != nil {
					t.Fatalf("active METADATA SET failed: %v", err)
				}
				assertMetadataTestCommands(t, upstreamIRC, 0, msg.Params, activeMsg.Params)
			}
		})
	}
}

func TestLastActiveMetadataPublishEchoDeduplication(t *testing.T) {
	dcSender, dcPeer := newMetadataTestNetworkDownstreams(t)
	dcSender.clientName = "desktop"
	dcPeer.clientName = "mobile"
	cfg := *dcSender.srv.Config()
	cfg.MetadataUpstreamPolicy = config.MetadataUpstreamPolicyLastActive
	dcSender.srv.SetConfig(&cfg)

	upstreamIRC := &metadataTestIRCConn{}
	uc := &upstreamConn{
		conn:         newConn(dcSender.srv, upstreamIRC, &connOptions{Logger: newDebugLogger(t)}),
		user:         dcSender.user,
		network:      dcSender.network,
		caps:         xirc.NewCapRegistry(),
		batches:      make(map[string]upstreamBatch),
		pendingCmds:  make(map[string][]pendingUpstreamCommand),
		serverPrefix: testServerPrefix,
		nick:         "nick",
	}
	uc.caps.Available["draft/metadata-2"] = ""
	uc.caps.SetEnabled("draft/metadata-2", true)
	dcSender.network.conn = uc

	ctx := context.Background()
	value := "desktop"
	if err := dcSender.srv.db.StoreClientNetworkMetadata(ctx, dcSender.network.ID, dcSender.clientName, "status", &value); err != nil {
		t.Fatal(err)
	}
	dcSender.handleMetadataClientActivity(ctx, &irc.Message{Command: "PRIVMSG", Params: []string{"#test", "hello"}})
	assertMetadataTestCommands(t, upstreamIRC, 0, []string{"*", "SET", "status", value})

	reply := &irc.Message{
		Tags:    irc.Tags{},
		Prefix:  testServerPrefix,
		Command: xirc.RPL_KEYVALUE,
		Params:  []string{"nick", "nick", "status", "*", value},
	}
	if err := uc.handleMessage(ctx, reply); err != nil {
		t.Fatalf("handle publish reply failed: %v", err)
	}
	senderConn := dcSender.conn.conn.(*metadataTestIRCConn)
	peerConn := dcPeer.conn.conn.(*metadataTestIRCConn)

	notification := &irc.Message{
		Tags:    irc.Tags{},
		Prefix:  &irc.Prefix{Name: "nick"},
		Command: "METADATA",
		Params:  []string{"nick", "status", "*", value},
	}
	if err := uc.handleMessage(ctx, notification); err != nil {
		t.Fatalf("handle duplicate notification failed: %v", err)
	}
	runMetadataPublishFallbackEvent(t, dcSender.user)
	waitForMetadataTestMessage(t, senderConn, 0)
	waitForMetadataTestMessage(t, peerConn, 0)
	assertMetadataTestCommands(t, senderConn, 1)
	assertMetadataTestCommands(t, peerConn, 1)

	mobileValue := "mobile"
	if err := dcSender.srv.db.StoreClientNetworkMetadata(ctx, dcSender.network.ID, dcPeer.clientName, "status", &mobileValue); err != nil {
		t.Fatal(err)
	}
	dcPeer.handleMetadataClientActivity(ctx, &irc.Message{Command: "PRIVMSG", Params: []string{"#test", "mobile"}})
	assertMetadataTestCommands(t, upstreamIRC, 1, []string{"*", "SET", "status", mobileValue})
	reply = &irc.Message{
		Tags:    irc.Tags{},
		Prefix:  testServerPrefix,
		Command: xirc.RPL_KEYVALUE,
		Params:  []string{"nick", "nick", "status", "*", mobileValue},
	}
	if err := uc.handleMessage(ctx, reply); err != nil {
		t.Fatalf("handle mobile publish reply failed: %v", err)
	}
	runMetadataPublishFallbackEvent(t, dcSender.user)
	waitForMetadataTestMessage(t, senderConn, 1)
	waitForMetadataTestMessage(t, peerConn, 1)
	assertMetadataTestCommands(t, senderConn, 2)
	assertMetadataTestCommands(t, peerConn, 2)

	desktopProfile, err := dcSender.srv.db.ListClientNetworkMetadata(ctx, dcSender.network.ID, dcSender.clientName)
	if err != nil || len(desktopProfile) != 1 || desktopProfile[0].Value != value {
		t.Fatalf("desktop profile changed by published state: %#v, %v", desktopProfile, err)
	}
	mobileProfile, err := dcSender.srv.db.ListClientNetworkMetadata(ctx, dcSender.network.ID, dcPeer.clientName)
	if err != nil || len(mobileProfile) != 1 || mobileProfile[0].Value != mobileValue {
		t.Fatalf("mobile profile changed by published state: %#v, %v", mobileProfile, err)
	}
}

func TestLastActiveMetadataSwitchesOnDownstreamMessages(t *testing.T) {
	dcDesktop, dcMobile := newMetadataTestNetworkDownstreams(t)
	dcDesktop.clientName = "desktop"
	dcMobile.clientName = "mobile"
	dcDesktop.registered = true
	dcMobile.registered = true
	cfg := *dcDesktop.srv.Config()
	cfg.MetadataUpstreamPolicy = config.MetadataUpstreamPolicyLastActive
	cfg.MetadataClientSync = true
	dcDesktop.srv.SetConfig(&cfg)

	upstreamIRC := &metadataTestIRCConn{}
	uc := &upstreamConn{
		conn:         newConn(dcDesktop.srv, upstreamIRC, &connOptions{Logger: newDebugLogger(t)}),
		user:         dcDesktop.user,
		network:      dcDesktop.network,
		channels:     xirc.NewCaseMappingMap[*upstreamChannel](stdCaseMapping),
		users:        xirc.NewCaseMappingMap[*upstreamUser](stdCaseMapping),
		caps:         xirc.NewCapRegistry(),
		batches:      make(map[string]upstreamBatch),
		pendingCmds:  make(map[string][]pendingUpstreamCommand),
		serverPrefix: testServerPrefix,
		nick:         "nick",
		isupport:     make(map[string]*string),
	}
	uc.caps.Available["draft/metadata-2"] = ""
	uc.caps.SetEnabled("draft/metadata-2", true)
	dcDesktop.network.conn = uc

	ctx := context.Background()
	desktop, mobile := "desktop", "mobile"
	if err := dcDesktop.srv.db.StoreClientNetworkMetadata(ctx, dcDesktop.network.ID, dcDesktop.clientName, "status", &desktop); err != nil {
		t.Fatal(err)
	}
	if err := dcDesktop.srv.db.StoreClientNetworkMetadata(ctx, dcDesktop.network.ID, dcMobile.clientName, "status", &mobile); err != nil {
		t.Fatal(err)
	}
	if err := dcDesktop.srv.db.StoreNetworkMetadata(ctx, dcDesktop.network.ID, "status", &mobile); err != nil {
		t.Fatal(err)
	}
	dcDesktop.network.metadataLastActiveClient = dcMobile.clientName
	dcDesktop.network.metadataLastActiveUpstream = uc

	if err := dcDesktop.handleMessage(ctx, &irc.Message{Command: "PRIVMSG", Params: []string{"#test", "desktop active"}}); err != nil {
		t.Fatalf("desktop PRIVMSG failed: %v", err)
	}
	assertMetadataUpstreamMessages(t, upstreamIRC, []string{"*", "SET", "status", desktop})

	if err := dcDesktop.handleMessage(ctx, &irc.Message{Command: "NOTICE", Params: []string{"#test", "still desktop"}}); err != nil {
		t.Fatalf("desktop NOTICE failed: %v", err)
	}
	assertMetadataUpstreamMessages(t, upstreamIRC, []string{"*", "SET", "status", desktop})

	desktopReply := &irc.Message{
		Tags:    irc.Tags{},
		Prefix:  testServerPrefix,
		Command: xirc.RPL_KEYVALUE,
		Params:  []string{"nick", "nick", "status", "*", desktop},
	}
	if err := uc.handleMessage(ctx, desktopReply); err != nil {
		t.Fatalf("desktop metadata confirmation failed: %v", err)
	}
	runMetadataPublishFallbackEvent(t, dcDesktop.user)
	desktopConn := dcDesktop.conn.conn.(*metadataTestIRCConn)
	mobileConn := dcMobile.conn.conn.(*metadataTestIRCConn)
	assertMetadataDownstreamMessageCount(t, desktopConn, 1)
	assertMetadataDownstreamMessageCount(t, mobileConn, 1)

	if err := dcMobile.handleMessage(ctx, &irc.Message{Command: "PRIVMSG", Params: []string{"#test", "mobile active"}}); err != nil {
		t.Fatalf("mobile PRIVMSG failed: %v", err)
	}
	assertMetadataUpstreamMessages(t, upstreamIRC,
		[]string{"*", "SET", "status", desktop},
		[]string{"*", "SET", "status", mobile})

	mobileReply := &irc.Message{
		Tags:    irc.Tags{},
		Prefix:  testServerPrefix,
		Command: xirc.RPL_KEYVALUE,
		Params:  []string{"nick", "nick", "status", "*", mobile},
	}
	if err := uc.handleMessage(ctx, mobileReply); err != nil {
		t.Fatalf("mobile metadata confirmation failed: %v", err)
	}
	runMetadataPublishFallbackEvent(t, dcDesktop.user)
	assertMetadataDownstreamMessageCount(t, desktopConn, 2)
	assertMetadataDownstreamMessageCount(t, mobileConn, 2)
}

func TestLastActiveMetadataWithRootCompatAndBouncerBind(t *testing.T) {
	dcA, dcB := newMetadataTestNetworkDownstreams(t)
	dcA.clientName = "alpha"
	dcB.clientName = "beta"
	dcA.registered = true
	dcB.registered = true

	cfg := *dcA.srv.Config()
	cfg.MetadataUpstreamPolicy = config.MetadataUpstreamPolicyLastActive
	cfg.MetadataClientSync = true
	cfg.MetadataRootCompat = true
	cfg.BouncerNetworkBind = true
	dcA.srv.SetConfig(&cfg)

	newRoot := func(id uint64, clientName string) *downstreamConn {
		dc := newDownstreamConn(dcA.srv, &metadataTestIRCConn{}, id)
		dc.user = dcA.user
		dc.clientName = clientName
		dc.nick = testUsername
		setMetadataTestVersion(dc, metadataVersion2)
		dcA.user.downstreamConns = append(dcA.user.downstreamConns, dc)
		t.Cleanup(func() {
			_ = dc.Close()
		})
		return dc
	}
	rootA := newRoot(3, dcA.clientName)
	rootB := newRoot(4, dcB.clientName)
	unboundRoot := newRoot(5, "unbound")

	upstreamIRC := &metadataTestIRCConn{}
	uc := &upstreamConn{
		conn:         newConn(dcA.srv, upstreamIRC, &connOptions{Logger: newDebugLogger(t)}),
		user:         dcA.user,
		network:      dcA.network,
		channels:     xirc.NewCaseMappingMap[*upstreamChannel](stdCaseMapping),
		users:        xirc.NewCaseMappingMap[*upstreamUser](stdCaseMapping),
		caps:         xirc.NewCapRegistry(),
		batches:      make(map[string]upstreamBatch),
		pendingCmds:  make(map[string][]pendingUpstreamCommand),
		serverPrefix: testServerPrefix,
		nick:         "nick",
		isupport:     make(map[string]*string),
	}
	uc.caps.Available["draft/metadata-2"] = ""
	uc.caps.SetEnabled("draft/metadata-2", true)
	dcA.network.conn = uc

	ctx := context.Background()
	alphaAvatar := "https://example.test/alpha"
	betaAvatar := "https://example.test/beta"
	betaStatus := "mobile"
	for _, md := range []struct {
		client, key, value string
	}{
		{dcA.clientName, "avatar", alphaAvatar},
		{dcB.clientName, "avatar", betaAvatar},
		{dcB.clientName, "status", betaStatus},
	} {
		if err := dcA.srv.db.StoreClientNetworkMetadata(ctx, dcA.network.ID, md.client, md.key, &md.value); err != nil {
			t.Fatal(err)
		}
	}
	if err := dcA.srv.db.StoreNetworkMetadata(ctx, dcA.network.ID, "avatar", &betaAvatar); err != nil {
		t.Fatal(err)
	}
	if err := dcA.srv.db.StoreNetworkMetadata(ctx, dcA.network.ID, "status", &betaStatus); err != nil {
		t.Fatal(err)
	}
	dcA.network.metadataLastActiveClient = dcB.clientName
	dcA.network.metadataLastActiveUpstream = uc

	if err := dcA.handleMessage(ctx, &irc.Message{Command: "PRIVMSG", Params: []string{"#test", "alpha active"}}); err != nil {
		t.Fatalf("alpha activity failed: %v", err)
	}
	assertMetadataUpstreamMessages(t, upstreamIRC,
		[]string{"*", "SET", "avatar", alphaAvatar},
		[]string{"*", "SET", "status"})
	if len(uc.metadataPublishPending) != 2 {
		t.Fatalf("initial pending Metadata publishes = %#v, want avatar and status", uc.metadataPublishPending)
	}

	for i, reply := range []*irc.Message{
		{Tags: irc.Tags{}, Prefix: testServerPrefix, Command: xirc.RPL_KEYVALUE, Params: []string{"nick", "nick", "avatar", "*", alphaAvatar}},
		{Tags: irc.Tags{}, Prefix: testServerPrefix, Command: xirc.RPL_KEYNOTSET, Params: []string{"nick", "nick", "status", "Key not set"}},
	} {
		if err := uc.handleMessage(ctx, reply); err != nil {
			t.Fatalf("alpha Metadata confirmation failed: %v", err)
		}
		if got, want := len(uc.metadataPublishPending), 1-i; got != want {
			t.Fatalf("pending Metadata publishes after reply %d = %#v, want %d", i, uc.metadataPublishPending, want)
		}
	}
	runMetadataPublishFallbackEvent(t, dcA.user)
	runMetadataPublishFallbackEvent(t, dcA.user)

	for name, dc := range map[string]*downstreamConn{
		"network A": dcA,
		"network B": dcB,
		"root A":    rootA,
		"root B":    rootB,
	} {
		assertMetadataDownstreamMessageCount(t, dc.conn.conn.(*metadataTestIRCConn), 2)
		t.Logf("%s received the confirmed avatar and status removal", name)
	}
	assertMetadataDownstreamMessageCount(t, unboundRoot.conn.conn.(*metadataTestIRCConn), 0)

	if err := dcB.handleMessage(ctx, &irc.Message{Command: "PRIVMSG", Params: []string{"#test", "beta active"}}); err != nil {
		t.Fatalf("beta activity failed: %v", err)
	}
	assertMetadataUpstreamMessages(t, upstreamIRC,
		[]string{"*", "SET", "avatar", alphaAvatar},
		[]string{"*", "SET", "status"},
		[]string{"*", "SET", "avatar", betaAvatar},
		[]string{"*", "SET", "status", betaStatus})
}

func TestLastActiveMetadataLatestActivityWinsPendingPublish(t *testing.T) {
	dcAlpha, dcBeta := newMetadataTestNetworkDownstreams(t)
	dcAlpha.clientName = "alpha"
	dcBeta.clientName = "beta"
	cfg := *dcAlpha.srv.Config()
	cfg.MetadataUpstreamPolicy = config.MetadataUpstreamPolicyLastActive
	dcAlpha.srv.SetConfig(&cfg)

	upstreamIRC := &metadataTestIRCConn{}
	uc := &upstreamConn{
		conn:         newConn(dcAlpha.srv, upstreamIRC, &connOptions{Logger: newDebugLogger(t)}),
		user:         dcAlpha.user,
		network:      dcAlpha.network,
		caps:         xirc.NewCapRegistry(),
		batches:      make(map[string]upstreamBatch),
		pendingCmds:  make(map[string][]pendingUpstreamCommand),
		serverPrefix: testServerPrefix,
		nick:         "nick",
	}
	uc.caps.Available["draft/metadata-2"] = ""
	uc.caps.SetEnabled("draft/metadata-2", true)
	dcAlpha.network.conn = uc

	ctx := context.Background()
	alpha, beta := "alpha", "beta"
	if err := dcAlpha.srv.db.StoreClientNetworkMetadata(ctx, dcAlpha.network.ID, dcAlpha.clientName, "status", &alpha); err != nil {
		t.Fatal(err)
	}
	if err := dcAlpha.srv.db.StoreClientNetworkMetadata(ctx, dcAlpha.network.ID, dcBeta.clientName, "status", &beta); err != nil {
		t.Fatal(err)
	}
	if err := dcAlpha.srv.db.StoreNetworkMetadata(ctx, dcAlpha.network.ID, "status", &beta); err != nil {
		t.Fatal(err)
	}
	dcAlpha.network.metadataLastActiveClient = dcBeta.clientName
	dcAlpha.network.metadataLastActiveUpstream = uc

	dcAlpha.handleMetadataClientActivity(ctx, &irc.Message{Command: "PRIVMSG"})
	assertMetadataUpstreamMessages(t, upstreamIRC, []string{"*", "SET", "status", alpha})

	// Beta becomes active before Alpha's publish is confirmed. The confirmed
	// store still contains Beta, so no correction can be sent yet.
	dcBeta.handleMetadataClientActivity(ctx, &irc.Message{Command: "PRIVMSG"})
	assertMetadataUpstreamMessages(t, upstreamIRC, []string{"*", "SET", "status", alpha})

	if err := uc.handleMessage(ctx, &irc.Message{
		Tags:    irc.Tags{},
		Prefix:  testServerPrefix,
		Command: xirc.RPL_KEYVALUE,
		Params:  []string{"nick", "nick", "status", "*", alpha},
	}); err != nil {
		t.Fatalf("Alpha publish confirmation failed: %v", err)
	}
	assertMetadataUpstreamMessages(t, upstreamIRC,
		[]string{"*", "SET", "status", alpha},
		[]string{"*", "SET", "status", beta})
}

func TestLastActiveMetadataRestoresCompleteClientProfile(t *testing.T) {
	dcAlpha, dcBeta := newMetadataTestNetworkDownstreams(t)
	dcAlpha.clientName = "alpha"
	dcBeta.clientName = "beta"
	cfg := *dcAlpha.srv.Config()
	cfg.MetadataUpstreamPolicy = config.MetadataUpstreamPolicyLastActive
	cfg.MetadataClientSync = true
	dcAlpha.srv.SetConfig(&cfg)

	upstreamIRC := &metadataTestIRCConn{}
	uc := &upstreamConn{
		conn:         newConn(dcAlpha.srv, upstreamIRC, &connOptions{Logger: newDebugLogger(t)}),
		user:         dcAlpha.user,
		network:      dcAlpha.network,
		caps:         xirc.NewCapRegistry(),
		batches:      make(map[string]upstreamBatch),
		pendingCmds:  make(map[string][]pendingUpstreamCommand),
		serverPrefix: testServerPrefix,
		nick:         "nick",
	}
	uc.caps.Available["draft/metadata-2"] = ""
	uc.caps.SetEnabled("draft/metadata-2", true)
	dcAlpha.network.conn = uc

	ctx := context.Background()
	alphaAvatar := "https://example.test/alpha"
	betaAvatar := "https://example.test/beta"
	betaStatus := "mobile"
	if err := dcAlpha.srv.db.StoreClientNetworkMetadata(ctx, dcAlpha.network.ID, dcAlpha.clientName, "avatar", &alphaAvatar); err != nil {
		t.Fatal(err)
	}
	if err := dcAlpha.srv.db.StoreClientNetworkMetadata(ctx, dcAlpha.network.ID, dcBeta.clientName, "avatar", &betaAvatar); err != nil {
		t.Fatal(err)
	}
	if err := dcAlpha.srv.db.StoreClientNetworkMetadata(ctx, dcAlpha.network.ID, dcBeta.clientName, "status", &betaStatus); err != nil {
		t.Fatal(err)
	}
	if err := dcAlpha.srv.db.StoreNetworkMetadata(ctx, dcAlpha.network.ID, "avatar", &betaAvatar); err != nil {
		t.Fatal(err)
	}
	if err := dcAlpha.srv.db.StoreNetworkMetadata(ctx, dcAlpha.network.ID, "status", &betaStatus); err != nil {
		t.Fatal(err)
	}
	dcAlpha.network.metadataLastActiveClient = dcBeta.clientName
	dcAlpha.network.metadataLastActiveUpstream = uc

	dcAlpha.handleMetadataClientActivity(ctx, &irc.Message{Command: "PRIVMSG", Params: []string{"#test", "alpha active"}})
	assertMetadataUpstreamMessages(t, upstreamIRC,
		[]string{"*", "SET", "avatar", alphaAvatar},
		[]string{"*", "SET", "status"})
}

func assertMetadataUpstreamMessages(t *testing.T, conn *metadataTestIRCConn, wants ...[]string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		conn.lock.Lock()
		var messages []*irc.Message
		for _, msg := range conn.msgs {
			if msg.Command == "METADATA" {
				messages = append(messages, msg)
			}
		}
		conn.lock.Unlock()
		if len(messages) >= len(wants) {
			if len(messages) != len(wants) {
				t.Fatalf("upstream metadata message count = %d, want %d: %v", len(messages), len(wants), messages)
			}
			for i, want := range wants {
				if !slices.Equal(messages[i].Params, want) {
					t.Fatalf("upstream metadata message %d = %v, want %v", i, messages[i].Params, want)
				}
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("upstream metadata message count = %d, want %d: %v", len(messages), len(wants), messages)
		}
		time.Sleep(time.Millisecond)
	}
}

func assertMetadataDownstreamMessageCount(t *testing.T, conn *metadataTestIRCConn, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		conn.lock.Lock()
		var messages []*irc.Message
		for _, msg := range conn.msgs {
			if msg.Command == "METADATA" || msg.Command == xirc.RPL_KEYVALUE || msg.Command == xirc.RPL_KEYNOTSET {
				messages = append(messages, msg)
			}
		}
		conn.lock.Unlock()
		if len(messages) >= want {
			if len(messages) != want {
				t.Fatalf("downstream metadata message count = %d, want %d: %v", len(messages), want, messages)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("downstream metadata message count = %d, want %d: %v", len(messages), want, messages)
		}
		time.Sleep(time.Millisecond)
	}
}

func runMetadataPublishFallbackEvent(t *testing.T, user *user) {
	t.Helper()
	select {
	case event := <-user.events:
		fallback, ok := event.(eventMetadataPublishFallback)
		if !ok {
			t.Fatalf("unexpected user event %T", event)
		}
		fallback.uc.handleMetadataPublishFallback(context.Background(), fallback.target, fallback.key, fallback.value)
	case <-time.After(time.Second):
		t.Fatal("metadata publish fallback event not scheduled")
	}
}

func TestMetadataClientSyncDisabledAllowsAuthoritativeUpstreamNotification(t *testing.T) {
	dcSender, dcPeer := newMetadataTestNetworkDownstreams(t)
	dcSender.clientName = "desktop"
	dcPeer.clientName = "mobile"
	cfg := *dcSender.srv.Config()
	cfg.MetadataUpstreamPolicy = config.MetadataUpstreamPolicyLastActive
	cfg.MetadataClientSync = false
	dcSender.srv.SetConfig(&cfg)

	upstreamIRC := &metadataTestIRCConn{}
	uc := &upstreamConn{
		conn:         newConn(dcSender.srv, upstreamIRC, &connOptions{Logger: newDebugLogger(t)}),
		user:         dcSender.user,
		network:      dcSender.network,
		caps:         xirc.NewCapRegistry(),
		batches:      make(map[string]upstreamBatch),
		pendingCmds:  make(map[string][]pendingUpstreamCommand),
		serverPrefix: testServerPrefix,
		nick:         "nick",
	}
	uc.caps.Available["draft/metadata-2"] = ""
	uc.caps.SetEnabled("draft/metadata-2", true)
	dcSender.network.conn = uc

	ctx := context.Background()
	value := "desktop"
	if err := dcSender.srv.db.StoreClientNetworkMetadata(ctx, dcSender.network.ID, dcSender.clientName, "status", &value); err != nil {
		t.Fatal(err)
	}
	dcSender.handleMetadataClientActivity(ctx, &irc.Message{Command: "PRIVMSG", Params: []string{"#test", "hello"}})
	assertMetadataTestCommands(t, upstreamIRC, 0, []string{"*", "SET", "status", value})

	reply := &irc.Message{
		Tags:    irc.Tags{},
		Prefix:  testServerPrefix,
		Command: xirc.RPL_KEYVALUE,
		Params:  []string{"nick", "nick", "status", "*", value},
	}
	if err := uc.handleMessage(ctx, reply); err != nil {
		t.Fatalf("handle publish reply failed: %v", err)
	}
	senderConn := dcSender.conn.conn.(*metadataTestIRCConn)
	peerConn := dcPeer.conn.conn.(*metadataTestIRCConn)
	assertMetadataTestCommands(t, senderConn, 0)
	assertMetadataTestCommands(t, peerConn, 0)

	notification := &irc.Message{
		Tags:    irc.Tags{},
		Prefix:  &irc.Prefix{Name: "nick"},
		Command: "METADATA",
		Params:  []string{"nick", "status", "*", value},
	}
	if err := uc.handleMessage(ctx, notification); err != nil {
		t.Fatalf("handle authoritative notification failed: %v", err)
	}
	waitForMetadataTestMessage(t, senderConn, 0)
	waitForMetadataTestMessage(t, peerConn, 0)
	assertMetadataTestCommands(t, senderConn, 1)
	assertMetadataTestCommands(t, peerConn, 1)

	if err := dcSender.srv.db.StoreClientNetworkMetadata(ctx, dcSender.network.ID, dcSender.clientName, "status", nil); err != nil {
		t.Fatal(err)
	}
	dcSender.activateMetadataClient(ctx, true)
	assertMetadataTestCommands(t, upstreamIRC, 1, []string{"*", "SET", "status"})
	unsetReply := &irc.Message{
		Tags:    irc.Tags{},
		Prefix:  testServerPrefix,
		Command: xirc.RPL_KEYNOTSET,
		Params:  []string{"nick", "nick", "status", "Key not set"},
	}
	if err := uc.handleMessage(ctx, unsetReply); err != nil {
		t.Fatalf("handle unset reply failed: %v", err)
	}
	assertMetadataTestCommands(t, senderConn, 1)
	assertMetadataTestCommands(t, peerConn, 1)

	unsetNotification := &irc.Message{
		Tags:    irc.Tags{},
		Prefix:  &irc.Prefix{Name: "nick"},
		Command: "METADATA",
		Params:  []string{"nick", "status", "*"},
	}
	if err := uc.handleMessage(ctx, unsetNotification); err != nil {
		t.Fatalf("handle authoritative unset notification failed: %v", err)
	}
	waitForMetadataTestMessage(t, senderConn, 1)
	waitForMetadataTestMessage(t, peerConn, 1)
	assertMetadataTestCommands(t, senderConn, 2)
	assertMetadataTestCommands(t, peerConn, 2)
}

func assertMetadataTestCommands(t *testing.T, conn *metadataTestIRCConn, before int, wants ...[]string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		conn.lock.Lock()
		got := len(conn.msgs)
		conn.lock.Unlock()
		if got >= before+len(wants) || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	conn.lock.Lock()
	defer conn.lock.Unlock()
	if got := len(conn.msgs); got != before+len(wants) {
		t.Fatalf("upstream message count = %d, want %d: %v", got, before+len(wants), conn.msgs)
	}
	for i, want := range wants {
		msg := conn.msgs[before+i]
		if msg.Command != "METADATA" || !slices.Equal(msg.Params, want) {
			t.Fatalf("upstream message %d = %v, want METADATA %v", before+i, msg, want)
		}
	}
}

func newMetadataTestServer(db database.Database) *Server {
	srv := NewServer(db)
	srv.metrics.downstreamOutMessagesTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "test_downstream_out_messages_total",
		Help: "Test counter",
	})
	srv.metrics.downstreamInMessagesTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "test_downstream_in_messages_total",
		Help: "Test counter",
	})
	srv.metrics.upstreamOutMessagesTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "test_upstream_out_messages_total",
		Help: "Test counter",
	})
	srv.metrics.upstreamInMessagesTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "test_upstream_in_messages_total",
		Help: "Test counter",
	})
	return srv
}

func newMetadataTestDownstream(db database.Database) *downstreamConn {
	return newDownstreamConn(newMetadataTestServer(db), &metadataTestIRCConn{}, 1)
}

func newMetadataTestNetworkDownstreams(t *testing.T) (*downstreamConn, *downstreamConn) {
	t.Helper()
	db := createTempSqliteDB(t)
	srv := newMetadataTestServer(db)
	userRecord := createTestUser(t, db)
	u := newUser(srv, userRecord)
	networkRecord := database.NewNetwork("irc+insecure://example.invalid")
	if err := db.StoreNetwork(context.Background(), userRecord.ID, networkRecord); err != nil {
		t.Fatalf("StoreNetwork() failed: %v", err)
	}
	net := newNetwork(u, networkRecord, nil)
	dcA := newDownstreamConn(srv, &metadataTestIRCConn{}, 1)
	dcB := newDownstreamConn(srv, &metadataTestIRCConn{}, 2)
	for _, dc := range []*downstreamConn{dcA, dcB} {
		dc.user = u
		dc.network = net
		dc.nick = testUsername
		setMetadataTestVersion(dc, metadataVersion2)
	}
	u.downstreamConns = []*downstreamConn{dcA, dcB}
	t.Cleanup(func() {
		dcA.Close()
		dcB.Close()
	})
	return dcA, dcB
}

func setMetadataTestVersion(dc *downstreamConn, version metadataVersion) {
	dc.caps.SetEnabled("draft/metadata-2", version == metadataVersion2)
	dc.caps.SetEnabled("draft/metadata-3", version == metadataVersion3)
	dc.metadataDialect = version
}

func lastMetadataTestMessage(t *testing.T, dc *downstreamConn) *irc.Message {
	t.Helper()
	conn := dc.conn.conn.(*metadataTestIRCConn)
	return waitForMetadataTestMessage(t, conn, 0)
}

func waitForMetadataTestMessage(t *testing.T, conn *metadataTestIRCConn, after int) *irc.Message {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		conn.lock.Lock()
		n := len(conn.msgs)
		if n > after {
			msg := conn.msgs[n-1]
			conn.lock.Unlock()
			return msg
		}
		conn.lock.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("no messages sent")
		}
		time.Sleep(time.Millisecond)
	}
}

type metadataTestIRCConn struct {
	lock sync.Mutex
	msgs []*irc.Message
}

func (c *metadataTestIRCConn) ReadMessage() (*irc.Message, error) {
	return nil, nil
}

func (c *metadataTestIRCConn) WriteMessage(msg *irc.Message) error {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.msgs = append(c.msgs, msg)
	return nil
}

func (c *metadataTestIRCConn) messageCount() int {
	c.lock.Lock()
	defer c.lock.Unlock()
	return len(c.msgs)
}

func (c *metadataTestIRCConn) messages() []*irc.Message {
	c.lock.Lock()
	defer c.lock.Unlock()
	return append([]*irc.Message(nil), c.msgs...)
}

func (c *metadataTestIRCConn) Close() error {
	return nil
}

func (c *metadataTestIRCConn) SetReadDeadline(time.Time) error {
	return nil
}

func (c *metadataTestIRCConn) SetWriteDeadline(time.Time) error {
	return nil
}

func (c *metadataTestIRCConn) RemoteAddr() net.Addr {
	return metadataTestAddr("remote")
}

func (c *metadataTestIRCConn) LocalAddr() net.Addr {
	return metadataTestAddr("local")
}

func (c *metadataTestIRCConn) GetPeerCertificate() *x509.Certificate {
	return nil
}

type metadataTestAddr string

func (addr metadataTestAddr) Network() string {
	return string(addr)
}

func (addr metadataTestAddr) String() string {
	return string(addr)
}
