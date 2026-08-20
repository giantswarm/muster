package mcpserver

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
)

// fakeServiceRegistry serves a fixed set of services to negotiatedProtocolVersion.
type fakeServiceRegistry struct {
	services map[string]api.ServiceInfo
}

func (f *fakeServiceRegistry) Get(name string) (api.ServiceInfo, bool) {
	service, exists := f.services[name]
	return service, exists
}

func (f *fakeServiceRegistry) GetAll() []api.ServiceInfo { return nil }

func (f *fakeServiceRegistry) GetByType(api.ServiceType) []api.ServiceInfo { return nil }

// fakeServiceInfo reports a fixed service data map.
type fakeServiceInfo struct {
	name string
	data map[string]interface{}
}

func (f *fakeServiceInfo) GetName() string                        { return f.name }
func (f *fakeServiceInfo) GetType() api.ServiceType               { return api.TypeMCPServer }
func (f *fakeServiceInfo) GetState() api.ServiceState             { return api.StateRunning }
func (f *fakeServiceInfo) GetHealth() api.HealthStatus            { return api.HealthHealthy }
func (f *fakeServiceInfo) GetLastError() error                    { return nil }
func (f *fakeServiceInfo) GetServiceData() map[string]interface{} { return f.data }

// withServiceRegistry swaps in a registry for the duration of the test.
func withServiceRegistry(t *testing.T, registry api.ServiceRegistryHandler) {
	previous := api.GetServiceRegistry()
	api.RegisterServiceRegistry(registry)
	t.Cleanup(func() { api.RegisterServiceRegistry(previous) })
}

func TestNegotiatedProtocolVersion(t *testing.T) {
	tests := []struct {
		name     string
		registry api.ServiceRegistryHandler
		server   string
		want     string
	}{
		{
			name:     "no registry registered",
			registry: nil,
			server:   "probe",
			want:     "",
		},
		{
			name:     "server defined but not started",
			registry: &fakeServiceRegistry{services: map[string]api.ServiceInfo{}},
			server:   "probe",
			want:     "",
		},
		{
			name: "connected server reports its negotiated revision",
			registry: &fakeServiceRegistry{services: map[string]api.ServiceInfo{
				"probe": &fakeServiceInfo{name: "probe", data: map[string]interface{}{
					api.ServiceDataProtocolVersion: "2024-11-05",
				}},
			}},
			server: "probe",
			want:   "2024-11-05",
		},
		{
			name: "service data without the key",
			registry: &fakeServiceRegistry{services: map[string]api.ServiceInfo{
				"probe": &fakeServiceInfo{name: "probe", data: map[string]interface{}{
					"clientReady": false,
				}},
			}},
			server: "probe",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A nil handler is a valid registry state: GetServiceRegistry
			// returns nil until the orchestrator registers one.
			withServiceRegistry(t, tt.registry)

			require.Equal(t, tt.want, negotiatedProtocolVersion(tt.server))
		})
	}
}
