# Development Setup

Get your local development environment ready for contributing to Muster.

## Prerequisites

### Required Software
- **Go 1.21+**: [Download from golang.org](https://golang.org/downloads/)
- **Git**: For version control
- **Make**: For build automation
- **Docker** (optional): For containerized testing

### Recommended Tools
- **IDE**: VSCode with Go extension, or GoLand
- **golangci-lint**: For code quality checks
- **goimports**: For import formatting
- **gopls**: Go language server

## Initial Setup

### 1. Fork and Clone

```bash
# Fork the repository on GitHub, then clone your fork
git clone https://github.com/YOUR_USERNAME/muster.git
cd muster

# Add upstream remote
git remote add upstream https://github.com/giantswarm/muster.git
```

### 2. Install Dependencies

```bash
# Download Go modules
go mod download

# Install development tools
go install golang.org/x/tools/cmd/goimports@latest
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
```

### 3. Verify Setup

```bash
# Build the project
make build

# Run tests
make test

# Check code quality
make lint
```

## Development Workflow

### Before Making Changes

```bash
# Update your local main branch
git checkout main
git pull upstream main

# Create a feature branch
git checkout -b feature/your-feature-name
```

### Code Quality Standards

**CRITICAL: Run these before every commit:**

```bash
# Format imports and code
goimports -w .
go fmt ./...

# Run tests
make test

# Update binary for testing
go install
```

### Testing Requirements

**Minimum 80% test coverage** is required for all new code.

```bash
# Run tests with coverage
make test-coverage

# Run specific test
go test ./internal/api -v

# Run test scenarios
muster test --parallel 20

# Run specific scenario
muster test --scenario mcpserver-crud --verbose --debug
```

### Test Scenario Development

Test scenarios are in `internal/testing/scenarios/`. They use BDD format:

```yaml
# Example scenario structure
scenario: feature-test
description: "Test new feature functionality"
steps:
  - description: "Setup initial state"
    command: "muster create serviceclass test-class"
    expect_success: true
    
  - description: "Test the feature"
    command: "muster start service test-service test-class"
    expect_success: true
    expect_output_contains: "Service started successfully"
```

**Important Testing Rules:**
- **NEVER use timers/sleep/wait** in tests or scenarios
- **Always treat scenarios as the defined user behavior**
- **Fix code, not scenarios** when tests fail
- Use dependency injection for time-dependent code

## Architecture Guidelines

### Service Locator Pattern

**CRITICAL: All inter-package communication MUST go through the central API layer.**

#### Adding New Functionality

**1. Define Interface in API:**
```go
// internal/api/handlers.go
type MyServiceHandler interface {
    DoSomething(ctx context.Context) error
}
```

**2. Implement Adapter:**
```go
// internal/myservice/api_adapter.go
type Adapter struct {
    logic *ServiceLogic
}

func (a *Adapter) DoSomething(ctx context.Context) error {
    return a.logic.performAction(ctx)
}

func (a *Adapter) Register() {
    api.RegisterMyService(a)
}
```

**3. Consume via API:**
```go
// in another package
import "github.com/giantswarm/muster/internal/api"

func useService(ctx context.Context) {
    handler := api.GetMyService()
    if handler == nil {
        return // handle gracefully
    }
    handler.DoSomething(ctx)
}
```

### Anti-Patterns to Avoid

- **NEVER import workflow, mcpserver, serviceclass, service packages directly**
- **NEVER use time.Sleep or timers to fix race conditions**
- **NEVER change schema.json manually** (generated via `muster test --generate-schema`)

## Code Style

### Go Standards

```go
// ✅ Good: Proper error wrapping
if err != nil {
    return fmt.Errorf("failed to create service: %w", err)
}

// ✅ Good: Exported function with documentation
// CreateService creates a new service instance from the given service class.
// It validates the parameters and initializes the service with default settings.
func CreateService(name, className string) (*Service, error) {
    // implementation
}

// ❌ Bad: No error context
if err != nil {
    return err
}

// ❌ Bad: No documentation for exported function
func CreateService(name, className string) (*Service, error) {
    // implementation
}
```

### File Organization

- **Keep files under 400 lines**
- **One primary concept per file**
- **Use descriptive file names**
- **Group related functionality**

```
internal/mypackage/
├── doc.go              # Package documentation
├── types.go            # Type definitions
├── api_adapter.go      # API service locator adapter
├── logic.go            # Core business logic
├── logic_test.go       # Unit tests
└── validation.go       # Input validation
```

### Documentation Requirements

**Every package MUST have a doc.go file:**
```go
// Package mypackage provides functionality for managing custom resources.
//
// This package implements the service locator pattern by providing an adapter
// that registers handlers with the central API layer. It handles resource
// lifecycle management including creation, validation, and cleanup.
package mypackage
```

**Every exported function MUST have GoDoc comments:**
```go
// CreateResource creates a new resource with the specified configuration.
// It validates the input parameters and returns an error if validation fails.
// The resource is automatically registered with the central registry.
func CreateResource(config *ResourceConfig) (*Resource, error) {
    // implementation
}
```

## IDE Configuration

### VSCode Settings

Create `.vscode/settings.json`:
```json
{
    "go.lintTool": "golangci-lint",
    "go.lintOnSave": "package",
    "go.formatTool": "goimports",
    "go.useLanguageServer": true,
    "[go]": {
        "editor.formatOnSave": true,
        "editor.codeActionsOnSave": {
            "source.organizeImports": true
        }
    }
}
```

### Recommended Extensions

- **Go**: Official Go extension
- **Go Test Explorer**: Test runner integration
- **golangci-lint**: Linting integration
- **Git Graph**: Visual git history

## Debugging

### Local Development

```bash
# Run with debug logging
muster serve --log-level debug

# Enable pprof for profiling
muster serve --enable-pprof --pprof-port 6060

# Run agent in REPL mode for testing
muster agent --repl --endpoint http://localhost:8080
```

### Using Debugger

```bash
# Build with debug symbols
go build -gcflags="all=-N -l" .

# Run with dlv (Delve debugger)
dlv exec ./muster -- serve --log-level debug
```

### Debugging Tests

```bash
# Run specific test with verbose output
go test -v ./internal/api -run TestSpecificFunction

# Run test with debugger
dlv test ./internal/api -- -test.run TestSpecificFunction
```

## Contributing Workflow

### 1. Make Your Changes

Follow the architectural guidelines and maintain test coverage.

### 2. Test Thoroughly

```bash
# Run all tests
make test

# Run scenarios
muster test --parallel 20

# Update binary
go install

# Manual testing
muster agent --repl
```

### 3. Format and Lint

```bash
# REQUIRED before every commit
goimports -w .
go fmt ./...
make lint
```

### 4. Commit Changes

```bash
# Stage changes
git add .

# Commit with descriptive message
git commit -m "feat: add new workflow validation feature

- Implement workflow parameter validation
- Add comprehensive test scenarios
- Update documentation
- Closes #123"
```

### 5. Push and Create PR

```bash
# Push to your fork
git push origin feature/your-feature-name

# Create pull request on GitHub
# Include: description, testing done, breaking changes
```

## Common Development Tasks

### Adding a New CLI Command

1. **Create command file** in `cmd/`
2. **Add to root command** in `cmd/root.go`
3. **Implement handler** following service locator pattern
4. **Add tests** and scenarios
5. **Update documentation**

### Adding a New MCP Tool

1. **Define tool** in appropriate service package
2. **Register with aggregator** via API adapter
3. **Add integration tests**
4. **Update tool documentation**

### Adding a New Resource Type

1. **Define CRD** in `pkg/apis/muster/v1alpha1/`
2. **Implement handlers** following service locator pattern
3. **Add CLI commands** for CRUD operations
4. **Create test scenarios**
5. **Update schema** via `muster test --generate-schema`

## Finishing Up

### Before Submitting PR

**Run the complete checklist:**

```bash
# Format code
goimports -w .
go fmt ./...

# Run tests
make test

# Update binary
go install

# Run scenarios
muster test --parallel 20

# Check for any failures
echo "All checks passed! Ready to submit PR."
```

### Getting Help

- **Architecture questions**: Read [Architecture Decision Records](../explanation/decisions/)
- **Testing help**: See [Testing Documentation](testing/)
- **Code style**: Follow existing patterns in similar packages
- **Stuck?**: Ask in [GitHub Discussions](https://github.com/giantswarm/muster/discussions)

## Next Steps

After setup:
1. [Read the architecture documentation](../explanation/architecture.md)
2. [Understand the testing framework](testing/)
3. [Pick a good first issue](https://github.com/giantswarm/muster/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22)

## Troubleshooting Development Issues

### Build Failures

```bash
# Clean module cache
go clean -modcache
go mod download

# Reset to clean state
git clean -fdx
go mod tidy
```

### Test Failures

```bash
# Run tests with more details
go test -v -race ./...

# Check for race conditions
go test -race ./internal/...

# Debug specific scenario
muster test --scenario problem-scenario --verbose --debug
```

### Import Issues

```bash
# Fix import formatting
goimports -w .

# Check for circular dependencies
go mod graph | grep cycle
```

Remember: **When in doubt, follow existing patterns in the codebase and ask for help!** 