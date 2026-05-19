package aggregator

import "testing"

const (
	testProxyBase = "http://localhost:8080"
	testProxyDial = "http://localhost:8080/mcp/kubernetes"
)

func TestProxyURLFor(t *testing.T) {
	tests := []struct {
		name, proxy, server, want string
	}{
		{name: "happy path", proxy: testProxyBase, server: "kubernetes", want: testProxyDial},
		{name: "trailing slash trimmed", proxy: testProxyBase + "/", server: "kubernetes", want: testProxyDial},
		{name: "multiple trailing slashes", proxy: testProxyBase + "///", server: "kubernetes", want: testProxyDial},
		{name: "cluster-mode service", proxy: "http://muster-agw.muster.svc.cluster.local:8080", server: "github", want: "http://muster-agw.muster.svc.cluster.local:8080/mcp/github"},
		{name: "empty proxy yields relative", proxy: "", server: "x", want: "/mcp/x"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := proxyURLFor(tc.proxy, tc.server); got != tc.want {
				t.Errorf("proxyURLFor(%q, %q) = %q, want %q", tc.proxy, tc.server, got, tc.want)
			}
		})
	}
}
