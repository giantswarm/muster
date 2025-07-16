# Getting Started with Muster

Choose your adventure based on your primary goal:

## 🤖 I want to use Muster with my AI agent

### Easiest Setup (Recommended)
**Time**: 2 minutes | **Goal**: Single command integration

→ [AI Agent Quick Start (Standalone Mode)](ai-agent-setup.md)

**Benefits**: One command (`muster standalone`), works with Cursor/VSCode/Claude, no separate processes  
**What you get**: Access to 11 meta-tools that provide unified access to all Muster capabilities

### Advanced Setup  
**Time**: 10 minutes | **Goal**: Production-ready with separate server/agent

→ [Advanced AI Agent Integration](ai-agent-integration.md)  

**Benefits**: Visible logs, multiple MCP clients, production deployment ready

## 🏗️ I want to manage infrastructure with Muster
**Time**: 15 minutes | **Goal**: Deploy Muster for platform engineering

→ [Platform Engineering Quick Start](platform-setup.md)

**You'll learn:**
- Install Muster and understand the two-layer architecture
- Configure MCP servers for external tool integration
- Create ServiceClass templates and service instances
- Build and execute multi-step workflows using meta-tools

## 🚀 I want to explore Muster locally
**Time**: 2 minutes | **Goal**: See Muster in action with demo

→ [Local Demo](local-demo.md)

**You'll learn:**
- Two-layer architecture: agent meta-tools vs aggregator tools
- Tool discovery using `list_tools` and `filter_tools`
- Tool execution using `call_tool` patterns
- Real examples from the current configuration

## 👩‍💻 I want to contribute to Muster
**Time**: 10 minutes | **Goal**: Set up development environment

→ [Contributor Setup](../contributing/development-setup.md)

**You'll learn:**
- Development environment setup
- Run tests and scenarios
- Make your first contribution

## 📚 Need more comprehensive guides?

**For detailed integration guides**:
- [Standalone Mode (Easiest)](ai-agent-setup.md) - Single command setup
- [Advanced Separate Mode](ai-agent-integration.md) - Production-ready with logs

**After your quick start, explore:**
- [Core Concepts](../explanation/) - Architecture and design
- [How-To Guides](../how-to/) - Practical solutions
- [Reference Documentation](../reference/) - Complete specifications
- [MCP Tools Reference](../reference/mcp-tools.md) - All available tools

## Understanding Muster's Architecture

**Critical Concept**: Muster operates in two distinct layers, and understanding this is key to effective usage.

### Two-Layer Tool Architecture

**Layer 1: Agent Meta-Tools (What AI Agents Use)**
- **11 meta-tools** for accessing the aggregator: `list_tools`, `call_tool`, `describe_tool`, etc.
- **Purpose**: Bridge between AI agents and aggregator functionality
- **Usage**: AI agents connect here via `muster agent --mcp-server`

**Layer 2: Aggregator Tools (What Gets Executed)**
- **36+ core tools** organized into 5 categories: `core_service_*`, `core_workflow_*`, etc.
- **Dynamic workflow tools**: `workflow_<name>` (auto-generated from your workflow definitions)
- **External tools**: From your configured MCP servers (varies by installation)
- **Purpose**: Actual business logic and tool implementations
- **Access**: Via `call_tool` meta-tool from the agent layer

### How It Works

```bash
# What AI agents actually do:
list_tools()                                    # Discover available tools
call_tool(name="core_service_list", arguments={})  # Execute aggregator tools
filter_tools(pattern="workflow_*")             # Filter tools by pattern

# AI agents never directly call:
core_service_list()                             # Doesn't exist at agent layer
```

### Architecture Benefits

- **Unified Interface**: Single consistent way to access all tool types
- **Dynamic Discovery**: Tools are discovered at runtime, not hardcoded
- **Transparent Routing**: Meta-tools automatically route to appropriate handlers
- **External Integration**: Seamless access to MCP server tools alongside core tools

## What Makes Muster Unique

**Two-Layer Design** enables:
- **Agent Meta-Tools** (11 tools) - Discovery, execution, and resource access
- **Aggregator Core Tools** (36 tools) - Configuration, services, workflows, MCP servers
- **Dynamic Capabilities** - Auto-generated workflow tools and external MCP tools
- **Unified Access Pattern** - Consistent interface regardless of tool source

**Key Tool Categories in the Aggregator:**
- **Configuration** (5 tools) - System and aggregator management
- **Services** (9 tools) - Service instance lifecycle 
- **ServiceClasses** (7 tools) - Reusable service templates
- **MCP Servers** (6 tools) - External tool provider management
- **Workflows** (9 tools) - Multi-step process orchestration

**Plus Dynamic Capabilities**:
- Auto-generated `workflow_<name>` tools for each workflow you define
- External tools from your configured MCP servers
- Template-based argument handling and output chaining

## Next Steps
After completing your quick start:
- [Understand the Architecture](../explanation/architecture.md) - How everything fits together
- [Learn Core Concepts](../explanation/) - Background knowledge
- [Follow How-To Guides](../how-to/) - Solve specific problems
- [Browse Reference Docs](../reference/) - Technical specifications 