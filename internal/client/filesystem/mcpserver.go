package filesystem

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/pkg/logging"
)

// GetMCPServer retrieves a specific MCPServer from filesystem.
func (f *Client) GetMCPServer(ctx context.Context, name, namespace string) (*musterv1alpha1.MCPServer, error) {
	filePath := f.getMCPServerPath(name)

	data, err := os.ReadFile(filePath) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.NewNotFound(
				schema.GroupResource{Group: "muster.giantswarm.io", Resource: "mcpservers"},
				name,
			)
		}
		return nil, fmt.Errorf("failed to read MCPServer file %s: %w", filePath, err)
	}

	var server musterv1alpha1.MCPServer
	if err := yaml.Unmarshal(data, &server); err != nil {
		return nil, fmt.Errorf("failed to unmarshal MCPServer from %s: %w", filePath, err)
	}

	if server.Name == "" {
		server.Name = name
	}
	if server.Namespace == "" {
		server.Namespace = defaultNamespace
	}

	return &server, nil
}

// ListMCPServers lists all MCPServers from filesystem.
func (f *Client) ListMCPServers(ctx context.Context, namespace string) ([]musterv1alpha1.MCPServer, error) {
	dirPath := f.getMCPServerDir()

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []musterv1alpha1.MCPServer{}, nil
		}
		return nil, fmt.Errorf("failed to read directory %s: %w", dirPath, err)
	}

	var servers []musterv1alpha1.MCPServer
	for _, entry := range entries {
		if entry.IsDir() || !isYAMLFile(entry.Name()) {
			continue
		}

		name := getNameFromFileName(entry.Name())
		server, err := f.GetMCPServer(ctx, name, namespace)
		if err != nil {
			logging.Error("fs-client", err, "Failed to load MCPServer %s", entry.Name())
			continue
		}
		servers = append(servers, *server)
	}

	return servers, nil
}

// CreateMCPServer creates a new MCPServer in filesystem.
func (f *Client) CreateMCPServer(ctx context.Context, server *musterv1alpha1.MCPServer) error {
	filePath := f.getMCPServerPath(server.Name)
	if _, err := os.Stat(filePath); err == nil {
		return errors.NewAlreadyExists(
			schema.GroupResource{Group: "muster.giantswarm.io", Resource: "mcpservers"},
			server.Name,
		)
	}

	dirPath := f.getMCPServerDir()
	if err := os.MkdirAll(dirPath, 0755); err != nil { //nolint:gosec
		return fmt.Errorf("failed to create directory %s: %w", dirPath, err)
	}

	if server.Namespace == "" {
		server.Namespace = defaultNamespace
	}

	data, err := yaml.Marshal(server)
	if err != nil {
		return fmt.Errorf("failed to marshal MCPServer %s: %w", server.Name, err)
	}

	if err := atomicWriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write MCPServer file %s: %w", filePath, err)
	}

	return nil
}

// UpdateMCPServer updates an existing MCPServer in filesystem.
func (f *Client) UpdateMCPServer(ctx context.Context, server *musterv1alpha1.MCPServer) error {
	filePath := f.getMCPServerPath(server.Name)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return errors.NewNotFound(
			schema.GroupResource{Group: "muster.giantswarm.io", Resource: "mcpservers"},
			server.Name,
		)
	}

	if server.Namespace == "" {
		server.Namespace = defaultNamespace
	}

	data, err := yaml.Marshal(server)
	if err != nil {
		return fmt.Errorf("failed to marshal MCPServer %s: %w", server.Name, err)
	}

	if err := atomicWriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write MCPServer file %s: %w", filePath, err)
	}

	return nil
}

// DeleteMCPServer deletes an MCPServer from filesystem.
func (f *Client) DeleteMCPServer(ctx context.Context, name, namespace string) error {
	filePath := f.getMCPServerPath(name)

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return errors.NewNotFound(
			schema.GroupResource{Group: "muster.giantswarm.io", Resource: "mcpservers"},
			name,
		)
	}

	if err := os.Remove(filePath); err != nil {
		return fmt.Errorf("failed to delete MCPServer file %s: %w", filePath, err)
	}

	return nil
}

// UpdateMCPServerStatus updates only the status. In filesystem mode the
// whole file is rewritten — status is embedded in the YAML.
func (f *Client) UpdateMCPServerStatus(ctx context.Context, server *musterv1alpha1.MCPServer) error {
	return f.UpdateMCPServer(ctx, server)
}

func (f *Client) getMCPServerDir() string {
	return filepath.Join(f.basePath, "mcpservers")
}

func (f *Client) getMCPServerPath(name string) string {
	return filepath.Join(f.getMCPServerDir(), name+".yaml")
}
