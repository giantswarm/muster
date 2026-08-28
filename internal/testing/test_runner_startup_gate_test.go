package testing

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// gateProbeManager is a MusterInstanceManager fake that records how many
// scenarios are inside their startup phase (CreateInstance through
// WaitForReady) at once.
type gateProbeManager struct {
	mu        sync.Mutex
	inStartup int
	highWater int
}

func (m *gateProbeManager) enterStartup() {
	m.mu.Lock()
	m.inStartup++
	if m.inStartup > m.highWater {
		m.highWater = m.inStartup
	}
	m.mu.Unlock()
}

func (m *gateProbeManager) leaveStartup() {
	m.mu.Lock()
	m.inStartup--
	m.mu.Unlock()
}

func (m *gateProbeManager) CreateInstance(ctx context.Context, scenarioName string, config *MusterPreConfiguration, logger TestLogger) (*MusterInstance, error) {
	m.enterStartup()
	time.Sleep(5 * time.Millisecond)
	return &MusterInstance{
		ID: "fake-" + scenarioName,
		// A closed port: the post-readiness client connect fails immediately,
		// which is irrelevant to the concurrency being measured here.
		Endpoint: "http://127.0.0.1:9/mcp",
	}, nil
}

func (m *gateProbeManager) WaitForReady(ctx context.Context, instance *MusterInstance, logger TestLogger) error {
	time.Sleep(5 * time.Millisecond)
	m.leaveStartup()
	return nil
}

func (m *gateProbeManager) DestroyInstance(ctx context.Context, instance *MusterInstance, logger TestLogger) error {
	return nil
}

func (m *gateProbeManager) InstanceExitStatus(instance *MusterInstance) (bool, error) {
	return false, nil
}

// TestStartupParallelGate verifies that at high --parallel the number of
// concurrently *starting* instances is bounded by the startup gate, while an
// explicitly disabled gate lets the full worker pool start at once. The t=0
// startup herd is the mechanism behind the giantswarm/muster#1101 CI flakes.
func TestStartupParallelGate(t *testing.T) {
	scenarios := make([]TestScenario, 60)
	for i := range scenarios {
		scenarios[i] = TestScenario{
			Name:     "gate-probe-" + string(rune('a'+i%26)) + string(rune('0'+i/26)),
			Category: CategoryBehavioral,
			Concept:  ConceptWorkflow,
		}
	}

	run := func(startupParallel int) *gateProbeManager {
		manager := &gateProbeManager{}
		logger := NewSilentLogger(false, false)
		runner := NewTestRunnerWithLogger(
			NewMCPTestClientWithLogger(false, logger),
			NewTestScenarioLoaderWithLogger(false, logger),
			NewStructuredReporter(false, false, ""),
			manager,
			false,
			logger,
		)
		_, err := runner.Run(context.Background(), TestConfiguration{
			Parallel:        50,
			StartupParallel: startupParallel,
		}, scenarios)
		require.NoError(t, err)
		return manager
	}

	gated := run(0) // 0 selects defaultStartupParallel
	require.LessOrEqual(t, gated.highWater, defaultStartupParallel,
		"startup concurrency must be bounded by the gate")
	require.GreaterOrEqual(t, gated.highWater, 2,
		"startups must still overlap, the gate is not a serializer")

	ungated := run(-1)
	require.Greater(t, ungated.highWater, defaultStartupParallel,
		"disabling the gate must allow the full worker pool into startup")
}
