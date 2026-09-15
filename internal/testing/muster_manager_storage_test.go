package testing

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/valkey-io/valkey-go"
)

func newStorageTestManager(t *testing.T) *musterInstanceManager {
	t.Helper()
	mgr, err := NewMusterInstanceManagerWithConfig(false, 39000, NewStdoutLogger(false, false), false, time.Second)
	if err != nil {
		t.Fatalf("NewMusterInstanceManagerWithConfig: %v", err)
	}
	m := mgr.(*musterInstanceManager)
	t.Cleanup(func() { _ = m.Cleanup() })
	return m
}

func pingValkey(ctx context.Context, addr string) error {
	client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{addr}, DisableCache: true})
	if err != nil {
		return err
	}
	defer client.Close()
	return client.Do(ctx, client.B().Ping().Build()).Error()
}

func TestValidateStorageConfig(t *testing.T) {
	cases := []struct {
		name    string
		cfg     *MusterPreConfiguration
		wantErr bool
	}{
		{"nil pre-configuration", nil, false},
		{"no storage block", &MusterPreConfiguration{}, false},
		{"memory", &MusterPreConfiguration{Storage: &StorageConfig{Type: "memory"}}, false},
		{"valkey", &MusterPreConfiguration{Storage: &StorageConfig{Type: "valkey"}}, false},
		{"valkey upper case", &MusterPreConfiguration{Storage: &StorageConfig{Type: "Valkey"}}, false},
		{"valkey with delay", &MusterPreConfiguration{Storage: &StorageConfig{Type: "valkey", StartDelay: time.Second}}, false},
		{"unknown type", &MusterPreConfiguration{Storage: &StorageConfig{Type: "postgres"}}, true},
		{"negative delay", &MusterPreConfiguration{Storage: &StorageConfig{Type: "valkey", StartDelay: -time.Second}}, true},
		{"delay on memory", &MusterPreConfiguration{Storage: &StorageConfig{Type: "memory", StartDelay: time.Second}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateStorageConfig(tc.cfg)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateStorageConfig(%+v) error = %v, wantErr %v", tc.cfg, err, tc.wantErr)
			}
		})
	}
}

func TestStartValkeyMemoryIsNoop(t *testing.T) {
	m := newStorageTestManager(t)
	if err := m.startValkey(context.Background(), "inst", &MusterPreConfiguration{}, m.logger); err != nil {
		t.Fatalf("startValkey on memory: %v", err)
	}
	if m.valkeyFor("inst") != nil {
		t.Fatal("memory storage must not start a valkey")
	}
	agg := map[string]interface{}{"host": "localhost"}
	m.applyStorageConfig(agg, "inst")
	if _, ok := agg["oauth"]; ok {
		t.Fatalf("memory storage must leave the config alone, got %v", agg)
	}
	if err := m.StopValkey("inst"); err == nil {
		t.Fatal("StopValkey on a memory instance must fail")
	}
	if err := m.StartValkey("inst"); err == nil {
		t.Fatal("StartValkey on a memory instance must fail")
	}
}

func TestStartValkeyImmediateRendersConfigAndAnswers(t *testing.T) {
	m := newStorageTestManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg := &MusterPreConfiguration{Storage: &StorageConfig{Type: StorageValkey}}
	if err := m.startValkey(ctx, "inst", cfg, m.logger); err != nil {
		t.Fatalf("startValkey: %v", err)
	}
	v := m.valkeyFor("inst")
	if v == nil {
		t.Fatal("no valkey registered for the instance")
	}
	if err := pingValkey(ctx, v.addr()); err != nil {
		t.Fatalf("valkey on %s does not answer: %v", v.addr(), err)
	}

	// The configuration names the store under oauth.server.storage, and a
	// server block the OAuth wiring rendered before keeps its other keys.
	agg := map[string]interface{}{
		"oauth": map[string]interface{}{
			"server": map[string]interface{}{"enabled": true, "storage": map[string]interface{}{"type": "memory"}},
		},
	}
	m.applyStorageConfig(agg, "inst")
	server := agg["oauth"].(map[string]interface{})["server"].(map[string]interface{})
	if server["enabled"] != true {
		t.Fatalf("server block lost its keys: %v", server)
	}
	storage := server["storage"].(map[string]interface{})
	if storage["type"] != StorageValkey {
		t.Fatalf("storage type = %v, want valkey", storage["type"])
	}
	if url := storage["valkey"].(map[string]interface{})["url"]; url != v.addr() {
		t.Fatalf("storage url = %v, want %s", url, v.addr())
	}

	// Outage and recovery keep the port and the data.
	client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{v.addr()}, DisableCache: true})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	defer client.Close()
	if err := client.Do(ctx, client.B().Set().Key("k").Value("v").Build()).Error(); err != nil {
		t.Fatalf("SET: %v", err)
	}
	if err := m.StopValkey("inst"); err != nil {
		t.Fatalf("StopValkey: %v", err)
	}
	if err := pingValkey(ctx, v.addr()); err == nil {
		t.Fatal("stopped valkey still answers")
	}
	if err := m.StartValkey("inst"); err != nil {
		t.Fatalf("StartValkey: %v", err)
	}
	got, err := client.Do(ctx, client.B().Get().Key("k").Build()).ToString()
	if err != nil || got != "v" {
		t.Fatalf("GET after restart = %q, %v; want v", got, err)
	}

	// Destroy releases the port for the next instance.
	m.stopValkey("inst", m.logger)
	if m.valkeyFor("inst") != nil {
		t.Fatal("valkey still registered after stopValkey")
	}
	m.portMu.Lock()
	_, reserved := m.reservedPorts[v.port]
	m.portMu.Unlock()
	if reserved {
		t.Fatalf("port %d still reserved after stopValkey", v.port)
	}
}

func TestValkeyAcceptsClientTrackingHandshake(t *testing.T) {
	m := newStorageTestManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg := &MusterPreConfiguration{Storage: &StorageConfig{Type: StorageValkey}}
	if err := m.startValkey(ctx, "inst", cfg, m.logger); err != nil {
		t.Fatalf("startValkey: %v", err)
	}
	v := m.valkeyFor("inst")
	defer m.stopValkey("inst", m.logger)

	// A client with client-side caching on (valkey-go's default, and every
	// muster release before v5.19.13) sends CLIENT TRACKING on connect.
	for _, life := range []string{"first start", "after restart"} {
		client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{v.addr()}})
		if err != nil {
			t.Fatalf("%s: caching client could not connect: %v", life, err)
		}
		if err := client.Do(ctx, client.B().Ping().Build()).Error(); err != nil {
			t.Fatalf("%s: PING: %v", life, err)
		}
		client.Close()
		if life == "first start" {
			if err := m.StopValkey("inst"); err != nil {
				t.Fatal(err)
			}
			if err := m.StartValkey("inst"); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestStartValkeyDelayedRefusesThenAnswers(t *testing.T) {
	m := newStorageTestManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg := &MusterPreConfiguration{Storage: &StorageConfig{Type: StorageValkey, StartDelay: 200 * time.Millisecond}}
	if err := m.startValkey(ctx, "inst", cfg, m.logger); err != nil {
		t.Fatalf("startValkey: %v", err)
	}
	v := m.valkeyFor("inst")
	select {
	case <-v.started:
		t.Fatal("delayed valkey started at once")
	default:
	}
	// Refused, not accepted-and-silent: a connect must fail while the store
	// is not up, the way a Valkey pod that is not scheduled yet fails.
	quick, quickCancel := context.WithTimeout(ctx, 500*time.Millisecond)
	if err := pingValkey(quick, v.addr()); err == nil {
		quickCancel()
		t.Fatal("valkey answered before its start delay")
	}
	quickCancel()
	if err := v.awaitStart(ctx); err != nil {
		t.Fatalf("delayed start: %v", err)
	}
	if err := pingValkey(ctx, v.addr()); err != nil {
		t.Fatalf("valkey does not answer after its start delay: %v", err)
	}
	m.stopValkey("inst", m.logger)
}

func TestStartValkeyBeforeDelayStartsNow(t *testing.T) {
	m := newStorageTestManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg := &MusterPreConfiguration{Storage: &StorageConfig{Type: StorageValkey, StartDelay: time.Hour}}
	if err := m.startValkey(ctx, "inst", cfg, m.logger); err != nil {
		t.Fatalf("startValkey: %v", err)
	}
	v := m.valkeyFor("inst")
	if err := m.StartValkey("inst"); err != nil {
		t.Fatalf("StartValkey before the delay: %v", err)
	}
	if err := pingValkey(ctx, v.addr()); err != nil {
		t.Fatalf("valkey does not answer after an explicit start: %v", err)
	}
	m.stopValkey("inst", m.logger)
}

func TestLogCapturePriorComesFirst(t *testing.T) {
	lc := newLogCapture()
	if _, err := lc.stdoutWriter.Write([]byte("second life out\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := lc.stderrWriter.Write([]byte("second life err\n")); err != nil {
		t.Fatal(err)
	}
	lc.close()
	lc.setPrior(&InstanceLogs{Stdout: "first life out\n", Stderr: "first life err\n"})
	logs := lc.getLogs()
	if logs.Stdout != "first life out\nsecond life out\n" {
		t.Fatalf("stdout = %q", logs.Stdout)
	}
	if logs.Stderr != "first life err\nsecond life err\n" {
		t.Fatalf("stderr = %q", logs.Stderr)
	}
	if linesContaining(strings.Split(logs.Combined, "\n"), "life") != 4 {
		t.Fatalf("combined lacks a life: %q", logs.Combined)
	}
}
