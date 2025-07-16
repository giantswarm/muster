# Muster Documentation

Welcome to Muster's docs! We've organized everything based on what you're trying to accomplish. Pick your adventure below:

## I want to...

### 🚀 Get started quickly
**New to Muster?** Choose your path based on what you're doing:

- [**Set up with my AI agent (Easiest)**](getting-started/ai-agent-setup.md) - Hook Muster up to your IDE with `muster standalone` (2 minutes)
- [**Advanced AI agent setup**](getting-started/ai-agent-integration.md) - Separate server/agent mode for production use (10 minutes)
- [**Deploy for platform work**](getting-started/platform-setup.md) - Get Muster running for infrastructure tasks (15 minutes)
- [**Try it out locally**](getting-started/local-demo.md) - Quick evaluation setup (2 minutes)

### 🛠️ Solve specific problems
**Need to get something done?** These guides show you how:

- [Create custom workflows](how-to/workflow-creation.md) - Build automated task sequences
- [Configure services](how-to/service-configuration.md) - Set up service lifecycle management
- [Troubleshoot issues](how-to/troubleshooting.md) - Fix common problems

### 📚 Look up reference information
**Need to check something specific?** Find the details here:

- [CLI command reference](reference/cli/) - Complete command documentation
- [Configuration options](reference/configuration.md) - All the settings you can tweak
- [MCP Tools documentation](reference/mcp-tools.md) - All the core mcp tools
- [CRD reference](reference/crds.md/) - Kubernetes Custom Resource definitions
- [API documentation](reference/api.md) - HTTP and MCP API specs

### 🧠 Understand how Muster works
**Want to know what's going on under the hood?** Explore the concepts:

- [System architecture](explanation/architecture.md) - How everything fits together
- [MCP aggregation](explanation/mcp-aggregation.md) - How tool aggregation works
- [Service orchestration](explanation/orchestration.md) - Workflow and service management
- [Design principles](explanation/design-principles.md) - Why we built it this way

### 🔧 Deploy and operate Muster
**Setting up Muster for real?** Here's your deployment guide:

- [Installation guide](operations/installation.md) - How to deploy Muster
- [Security setup](operations/security.md) - Lock it down properly

### 👩‍💻 Contribute to Muster
**Want to help build Muster?** Start here:

- [Development setup](contributing/development-setup.md) - Get your local dev environment ready
- [Testing framework](contributing/testing/) - Our testing approach and tools

## How this documentation works

This documentation follows the **[Diátaxis framework](https://diataxis.fr/)** - a systematic way to organize docs so you can find what you need:

- **📘 Tutorials** - Step-by-step lessons to get you started
- **📗 How-to Guides** - Solutions to specific problems you're facing
- **📙 Reference** - Technical details and specifications
- **📕 Explanation** - Background knowledge and concepts

Each type serves a different purpose, so you can jump to the right section for what you're trying to do.

## Find your path

**If you're new**: Start with [Getting Started](getting-started/) tutorials  
**If you're building**: Focus on [How-to Guides](how-to/) for specific tasks  
**If you're integrating**: Use [Reference](reference/) for detailed specs  
**If you're contributing**: Begin with [Contributing](contributing/) guidelines

**Different workflows:**
- **AI Agent setup**: getting-started → how-to → reference/api
- **Platform engineering**: getting-started → explanation → operations
- **Development**: contributing → explanation/architecture → reference
- **Troubleshooting**: how-to/troubleshooting → explanation → reference

## Need help?

### Something wrong with the docs?
- **Missing info**: [Request improvements](https://github.com/giantswarm/muster/issues/new?labels=documentation)
- **Unclear content**: [Report what's confusing](https://github.com/giantswarm/muster/issues/new?labels=documentation)
- **Broken links**: [Let us know](https://github.com/giantswarm/muster/issues/new?labels=documentation)

### Need technical support?
- **Questions**: [GitHub Discussions](https://github.com/giantswarm/muster/discussions)
- **Bug reports**: [GitHub Issues](https://github.com/giantswarm/muster/issues/new?labels=bug)
- **Feature requests**: [GitHub Issues](https://github.com/giantswarm/muster/issues/new?labels=enhancement)

## Our documentation goals

We aim to make this documentation:
- **Correct** - Technically accurate and up-to-date
- **Clear** - Easy to understand for your experience level
- **Useful** - Focused on what you actually need to do
- **Easy to navigate** - Find what you need quickly
- **Maintainable** - Stays current as Muster evolves 