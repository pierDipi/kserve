# AI Agent Context for KServe

This document provides essential context for AI agents (and developers) working with the KServe codebase.

## Project Overview

KServe is a standardized distributed generative and predictive AI inference platform for scalable, multi-framework deployment on Kubernetes. It provides both:
- **Generative AI**: LLM serving with vLLM, llm-d, OpenAI-compatible APIs, GPU acceleration
- **Predictive AI**: Multi-framework model serving (TensorFlow, PyTorch, scikit-learn, XGBoost, ONNX)

**Repository**: https://github.com/opendatahub-io/kserve (ODH fork of https://github.com/kserve/kserve)
**Language**: Go (controllers, agents) + Python (runtime servers)
**Framework**: Kubernetes Operators, Kubebuilder, Knative

## Architecture

### High-Level Components

KServe consists of two main planes:

1. **Control Plane** (Go)
   - `cmd/manager/` - Main KServe controller
   - `cmd/llmisvc/` - LLM inference service controller
   - `cmd/localmodel/` - Local model controller
   - `pkg/controller/` - Reconciliation logic for InferenceService CRDs
   - `pkg/webhook/` - Admission webhooks for validation and defaulting

2. **Data Plane** (Python + Go)
   - `cmd/agent/` - Model agent for sidecar pattern
   - `cmd/router/` - Request routing component
   - `python/kserve/` - Python SDK and runtime
   - `python/*/server/` - Framework-specific model servers (sklearn, xgboost, huggingface, etc.)

### Directory Structure

```
kserve/
├── cmd/                    # Go binaries (manager, agent, router, etc.)
├── pkg/                    # Go packages
│   ├── apis/              # CRD definitions
│   ├── controller/        # Controller reconciliation logic
│   ├── webhook/           # Admission webhooks
│   ├── agent/             # Agent implementation
│   └── utils/             # Shared utilities
├── python/                # Python model servers and SDK
│   ├── kserve/           # Core Python SDK
│   ├── sklearnserver/    # Scikit-learn server
│   ├── xgbserver/        # XGBoost server
│   ├── huggingfaceserver/ # HuggingFace transformers server
│   └── */                # Other framework servers
├── config/               # Kubernetes manifests and kustomize configs
├── charts/               # Helm charts
├── docs/                 # Documentation (references external website)
├── hack/                 # Build scripts and utilities
├── install/              # Installation manifests
└── test/                 # E2E and integration tests

```

For detailed architecture, see [ARCHITECTURE.md](./ARCHITECTURE.md) and the [official docs](https://kserve.github.io/website/docs/modelserving/control_plane).

## Building the Project

### Prerequisites

- **Go**: Version specified in `go.mod` (toolchain auto-managed via GOTOOLCHAIN)
- **Python**: 3.11+ (for Python components)
- **Docker/Podman**: For building container images
- **kubectl**: For Kubernetes interaction
- **kustomize**: For manifest generation (installed via make)

### Build Commands

```bash
# Build all components
make all                    # Builds manager, agent, router with tests

# Build individual components
make manager                # Build KServe controller
make agent                  # Build model agent
make router                 # Build request router

# Generate code and manifests
make generate               # Generate Go code (deepcopy, clientsets)
make manifests              # Generate CRD manifests

# Build container images
make docker-build           # Build all Docker images
make docker-push            # Push images to registry
```

### Environment Variables

- `ENGINE`: Container engine (docker|podman, default: docker)
- `ARCH`: Architecture for podman builds (e.g., "--arch x86_64")
- `GOTAGS`: Go build tags for distribution-specific code
- `BASE_IMG`: Base Python image (default: python:3.11-slim-bookworm)

## Testing

### Running Tests

```bash
# Go tests (unit + integration)
make test                   # Run all Go tests with coverage
make test-qpext             # Test the qpext module separately

# Python tests - run from specific server directory
cd python/kserve && make test
cd python/sklearnserver && make test
cd python/huggingfaceserver && make test
# ... etc for other servers

# E2E tests (requires Kubernetes cluster)
# See .github/workflows/e2e-test.yml for setup
```

### Test Structure

- **Go tests**: `*_test.go` files alongside source code
- **Python tests**: `tests/` directories in each Python package
- **Test fixtures**: `testdata/` directories contain sample data, configs, and golden files
- **Coverage**: Go coverage tracked via `coverage.out`, Python via pytest

### Test Targets by Component

| Component | Make Target | Location |
|-----------|------------|----------|
| Go core | `make test` | `pkg/`, `cmd/` |
| Python SDK | `make test` (in `python/kserve/`) | `python/kserve/test/` |
| SKLearn server | `make test` (in `python/sklearnserver/`) | `python/sklearnserver/tests/` |
| XGBoost server | `make test` (in `python/xgbserver/`) | `python/xgbserver/tests/` |
| HuggingFace server | `make test` (in `python/huggingfaceserver/`) | `python/huggingfaceserver/tests/` |

## Code Quality & Pre-commit Checks

KServe enforces quality standards via pre-commit hooks and CI checks.

### Local Pre-commit

```bash
# Run all pre-commit checks (REQUIRED before committing)
make precommit              # Format, lint, generate, sync dependencies

# This runs:
# - go-lint (golangci-lint)
# - py-lint (ruff)
# - py-fmt (black)
# - generate (code generation)
# - manifests (CRD generation)
# - tidy (go mod tidy)
# - sync-deps (dependency version sync)
```

### Individual Quality Checks

```bash
# Go formatting and linting
make fmt                    # Run gofmt
make vet                    # Run go vet
make go-lint                # Run golangci-lint (with auto-fix)

# Python formatting and linting
make py-fmt                 # Run black formatter
make py-lint                # Run ruff linter

# Code generation
make generate               # Generate deepcopy, clientsets
make manifests              # Generate CRDs from Go types
```

### CI Validation

CI runs `make check` which ensures `make precommit` was run and all changes are committed.

## Debugging

### Logging

KServe uses structured logging:
- **Go**: `zap`, `logr`, `klog` (Kubernetes-style logging)
- **Python**: Standard Python logging with structured output

Log levels controlled via:
- Controller: `--zap-log-level` flag
- Python servers: Environment variable `LOG_LEVEL`

### Common Issues

See [docs/COMMON_ISSUES_AND_SOLUTIONS.md](docs/COMMON_ISSUES_AND_SOLUTIONS.md) for troubleshooting.

### Debug Mode

```bash
# Run controller locally (outside cluster)
make run                    # Run controller with dev config

# Enable debug logging
go run cmd/manager/main.go --zap-log-level=debug
```

## Development Workflow

### Typical Development Cycle

1. **Create feature branch**: `git checkout -b feature/your-feature`
2. **Make changes**: Edit code
3. **Generate code if needed**: `make generate manifests` (for API changes)
4. **Run tests**: `make test`
5. **Run pre-commit**: `make precommit` (REQUIRED)
6. **Commit**: Git commit with conventional commit message
7. **Push and create PR**

### Pre-commit Hook Configuration

Pre-commit hooks are configured in `.pre-commit-config.yaml` and include:
- Python linting (ruff, black)
- YAML validation
- Trailing whitespace fixes

Install hooks: `pre-commit install`

### Conventional Commits

Use conventional commit format:
```
type(scope): description

Types: feat, fix, docs, style, refactor, test, chore
Example: fix(controller): handle nil pointer in reconciliation
```

## Key Files

- `Makefile` - Primary build and task automation
- `go.mod` / `go.sum` - Go dependencies
- `kserve-deps.env` - Dependency versions (auto-generated from go.mod)
- `kserve-images.env` - Container image configurations
- `.golangci.yml` - Go linter configuration
- `ruff.toml` - Python linter configuration
- `.pre-commit-config.yaml` - Pre-commit hook configuration

## External Resources

- **Website**: https://kserve.github.io/website/
- **Developer Guide**: https://kserve.github.io/website/docs/developer-guide
- **API Reference**: https://kserve.github.io/website/docs/reference/crd-api
- **Community**: https://github.com/kserve/community

## Working with InferenceService CRDs

The core API is the InferenceService CRD defined in `pkg/apis/serving/v1beta1/`.

Common operations:
```bash
# Apply an InferenceService
kubectl apply -f config/samples/

# Watch controller logs
kubectl logs -n kserve -l control-plane=kserve-controller-manager -f

# Debug inference service
kubectl get isvc -n <namespace>
kubectl describe isvc <name> -n <namespace>
kubectl get isvc <name> -n <namespace> -oyaml
```

## Tips for AI Agents

1. **Before modifying APIs**: Run `make generate manifests` after changing CRD Go types
2. **Test fixtures**: Check `testdata/` directories for examples of expected inputs/outputs
3. **Configuration**: Most Kubernetes configs are in `config/` with kustomize overlays
4. **Python servers**: Each server in `python/` is independent with its own Makefile
5. **ODH vs upstream**: This is the OpenDataHub fork; be aware of differences from kserve/kserve upstream
6. **Large files**: Some generated files are large (e.g., install scripts ~90k lines) - these are auto-generated
7. **Multi-module**: The `qpext` directory is a separate Go module for queue proxy extensions
