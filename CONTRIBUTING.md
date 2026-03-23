# Contributing to KServe

See [How to contribute](https://github.com/kserve/community#how-can-i-help-) in [KServe community repository](https://github.com/kserve/community) for general contribution guidelines.

## Table of Contents

- [Working Copy Setup](#working-copy-setup)
- [Development Environment](#development-environment)
- [Making Changes](#making-changes)
- [Running Tests](#running-tests)
- [Code Quality](#code-quality)
- [Submitting Changes](#submitting-changes)

## Working Copy Setup

To contribute, we recommend that you either [fork the
kserve/kserve](https://github.com/kserve/kserve/fork), or [fork the
opendatahub-io/kserve](https://github.com/opendatahub-io/kserve/fork)
repository. This is because ODH contributors use [GitHub
flow](https://docs.github.com/en/get-started/using-github/github-flow).

The following steps outlines our recommended remotes setup. You will have three
remotes. When contributing, please be aware of using the right base branch,
depending on if you are contributing to kserve/kserve or opendatahub-io/kserve.

1. Clone your fork of the repository:
```sh
GH_USER={your-github-user}
$ git clone git@github.com:${GH_USER}/kserve.git
```

2. Add [opendatahub-io/kserve](https://github.com/opendatahub-io/kserve/)
repository as a remote:
```sh
$ git remote add odh git@github.com:opendatahub-io/kserve.git
```

3. Add [kserve/kserve](https://github.com/kserve/kserve) repository as a remote:
```sh
$ git remote add kserve git@github.com:kserve/kserve.git
```

4. Your remotes setup would look similar to the following:
```sh
$ git remote -v
kserve  git@github.com:kserve/kserve.git (fetch)
kserve  git@github.com:kserve/kserve.git (push)
odh     git@github.com:opendatahub-io/kserve.git (fetch)
odh     git@github.com:opendatahub-io/kserve.git (push)
origin  git@github.com:${GH_USER}/kserve.git (fetch)
origin  git@github.com:${GH_USER}/kserve.git (push)
```

## Development Environment

### Prerequisites

- **Go**: Version specified in `go.mod` (Go toolchain is auto-managed)
- **Python**: 3.11 or later
- **Docker or Podman**: For building container images
- **kubectl**: For Kubernetes interactions
- **Make**: Build automation tool

### Setting Up Your Environment

```bash
# Install Go dependencies
go mod download

# Install Python dependencies (for Python servers)
cd python/kserve
pip install -e .
```

### Building the Project

```bash
# Build all components
make all

# Build specific components
make manager    # KServe controller
make agent      # Model agent
make router     # Request router

# Generate code (required after API changes)
make generate   # Generate Go code (deepcopy, clients)
make manifests  # Generate CRD YAML files
```

## Making Changes

### Branch Strategy

Create a descriptive branch for your changes:

```bash
# For features
git checkout -b feature/<short-description>

# For bug fixes
git checkout -b fix/<issue-number>-<short-description>

# For documentation
git checkout -b docs/<description>
```

### Code Changes

#### Go Code

- Follow standard Go conventions and idioms
- Run `gofmt` to format your code (done automatically by `make fmt`)
- Add unit tests for new functionality
- Update API documentation if changing public interfaces

#### Python Code

- Follow PEP 8 style guide
- Use type hints where appropriate
- Add docstrings for public functions/classes
- Add unit tests using pytest

#### API Changes

If you modify CRD types in `pkg/apis/`:

```bash
# Regenerate code and manifests
make generate manifests

# This will update:
# - DeepCopy methods
# - Client libraries
# - CRD YAML files in config/crd/
```

## Running Tests

### Go Tests

```bash
# Run all Go tests
make test

# Run tests for a specific package
go test ./pkg/controller/v1beta1/inferenceservice/...

# Run with coverage
make test  # Coverage automatically generated in coverage.out
```

### Python Tests

```bash
# Test a specific server
cd python/kserve
make test

cd python/sklearnserver
make test

# Run with coverage
pytest --cov=kserve --cov-report=html
```

### Integration Tests

Integration tests require envtest (fake Kubernetes API):

```bash
# Set up test environment
make setup-envtest

# Tests will automatically use envtest
make test
```

## Code Quality

### Pre-commit Checks

**IMPORTANT**: Before committing, always run:

```bash
make precommit
```

This will:
- Format Go code (`gofmt`)
- Format Python code (`black`)
- Lint Go code (`golangci-lint`)
- Lint Python code (`ruff`)
- Run code generators
- Tidy Go modules
- Sync dependency versions

### Individual Quality Checks

```bash
# Go
make fmt        # Format Go code
make vet        # Run go vet
make go-lint    # Run golangci-lint with auto-fix

# Python
make py-fmt     # Format with black
make py-lint    # Lint with ruff
```

### Installing Pre-commit Hooks

```bash
# Install pre-commit hooks (optional but recommended)
pre-commit install

# Hooks will run automatically on git commit
```

## Submitting Changes

### Commit Messages

Use conventional commit format:

```
<type>(<scope>): <description>

[optional body]

[optional footer]
```

**Types**:
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation changes
- `style`: Code style changes (formatting, no logic change)
- `refactor`: Code refactoring
- `test`: Adding or updating tests
- `chore`: Maintenance tasks

**Examples**:
```
feat(controller): add support for canary deployments
fix(agent): handle nil pointer in model download
docs(readme): update installation instructions
test(webhook): add validation tests for InferenceService
```

### Creating a Pull Request

1. **Ensure pre-commit checks pass**:
   ```bash
   make precommit
   make test
   ```

2. **Commit your changes**:
   ```bash
   git add <files>
   git commit -m "feat(component): description"
   ```

3. **Push to your fork**:
   ```bash
   git push origin <branch-name>
   ```

4. **Create PR on GitHub**:
   - Go to your fork on GitHub
   - Click "New Pull Request"
   - Fill out the PR template
   - Link related issues (e.g., "Fixes #123")

### PR Requirements

Your PR must:
- Pass all CI checks (tests, linting, coverage)
- Include tests for new functionality
- Update documentation if needed
- Follow the project's coding standards
- Have a clear description of changes

### Review Process

- Maintainers will review your PR
- Address review comments by pushing new commits
- Once approved, a maintainer will merge your PR

## Additional Resources

- **AGENTS.md**: AI agent context and development guide
- **ARCHITECTURE.md**: System architecture overview
- **Developer Guide**: https://kserve.github.io/website/docs/developer-guide
- **API Reference**: https://kserve.github.io/website/docs/reference/crd-api
- **Community**: https://github.com/kserve/community

## Getting Help

- **Slack**: Join the [KServe Slack](https://github.com/kserve/community/blob/main/README.md#questions-and-issues)
- **Issues**: Create an issue on GitHub for bugs or feature requests
- **Discussions**: Use GitHub Discussions for questions

## Code of Conduct

Please read and follow our [Code of Conduct](CODE_OF_CONDUCT.md).
