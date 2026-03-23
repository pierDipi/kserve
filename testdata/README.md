# Test Fixtures and Test Data

This document explains the purpose and usage of test fixtures across the KServe codebase.

## Overview

Test fixtures are sample data, configurations, and expected outputs used to verify functionality in unit and integration tests. KServe uses fixtures to test:
- API serialization/deserialization
- Model inference protocols (OpenAI-compatible, gRPC, REST)
- TensorFlow model conversion and OpenAPI generation
- Controller behavior with various InferenceService configurations

## Fixture Locations

### 1. TensorFlow to OpenAPI Converter (`tools/tf2openapi/`)

**Purpose**: Test TensorFlow SavedModel to OpenAPI schema conversion

**Locations**:
- `tools/tf2openapi/cmd/testdata/` - End-to-end test fixtures
- `tools/tf2openapi/generator/testdata/` - Generator unit test fixtures

**Contents**:
- `.pb` files: TensorFlow SavedModel protobuf files
- `.golden.json` files: Expected OpenAPI schema output
- `*Req.json` files: Sample request payloads

**Usage**:
```go
// In tests, fixtures are loaded relative to the test file
data, _ := os.ReadFile("testdata/TestCensus.pb")
```

**Adding New Fixtures**:
1. Export a TensorFlow SavedModel as `.pb`
2. Create expected OpenAPI output as `.golden.json`
3. Name consistently: `Test<Name>.pb` and `Test<Name>.golden.json`
4. Reference in test: `func TestName(t *testing.T) { ... }`

### 2. Python SDK OpenAI Fixtures (`python/kserve/test/fixtures/openai/`)

**Purpose**: Test OpenAI-compatible API protocol implementation

**Contents**:
- `chat_completion*.json` - Chat completion request/response fixtures
- `completion*.json` - Text completion request/response fixtures
- `embedding*.json` - Embedding request/response fixtures
- `rerank*.json` - Re-ranking request/response fixtures
- `*_stream.txt` - Streaming response fixtures (SSE format)

**Usage**:
```python
# In Python tests
import json
from pathlib import Path

fixtures_dir = Path(__file__).parent / "fixtures" / "openai"
with open(fixtures_dir / "chat_completion.json") as f:
    expected = json.load(f)
```

**Fixture Types**:

| File Pattern | Description | Used For |
|--------------|-------------|----------|
| `*_create_params.json` | Request parameters | Testing request parsing |
| `*.json` (no suffix) | Complete responses | Testing response serialization |
| `*_chunk.json` | Single streaming chunk | Testing SSE chunk format |
| `*_stream.txt` | Full SSE stream | Testing streaming responses |

**Adding New OpenAI Fixtures**:
1. Capture real OpenAI API responses or create spec-compliant examples
2. Format as JSON with proper indentation (2 spaces)
3. Use descriptive filenames matching the test scenario
4. Document any deviations from OpenAI spec in test comments

### 3. Configuration Samples (`config/samples/`)

**Purpose**: Example InferenceService definitions for various use cases

**Location**: `config/samples/`

**Contents**:
- Basic InferenceService examples
- Multi-framework model serving examples
- Transformer and explainer configurations
- Storage configuration examples (S3, GCS, PVC)

**Usage**:
```bash
# Apply sample InferenceService
kubectl apply -f config/samples/sklearn/v1beta1/sklearn.yaml

# Use in documentation
# Examples are referenced in docs and quickstart guides
```

## Best Practices

### Fixture Naming Conventions

1. **Go testdata**: Name after the test function
   ```
   Test function: TestCensusModelConversion
   Fixture files: TestCensus.pb, TestCensus.golden.json
   ```

2. **Python fixtures**: Descriptive names matching API operation
   ```
   chat_completion_create_params.json
   chat_completion.json
   chat_completion_chunk_stream.txt
   ```

3. **Sample configs**: Descriptive of the use case
   ```
   sklearn-iris.yaml
   pytorch-cifar10.yaml
   tensorflow-flowers.yaml
   ```

### Fixture Content Guidelines

1. **Keep fixtures minimal**: Include only data necessary for the test
2. **Use realistic data**: Fixtures should represent real-world usage
3. **Document special cases**: Add comments for non-obvious fixtures
4. **Version control**: Always commit fixtures with corresponding tests
5. **Golden file updates**: Regenerate golden files when output format changes

### Updating Fixtures

When API or protocol changes require fixture updates:

1. **Identify affected fixtures**: Search for fixtures related to changed code
2. **Regenerate programmatically when possible**:
   ```bash
   # Example: Regenerate TF2OpenAPI golden files
   cd tools/tf2openapi
   go test ./... -update-golden
   ```
3. **Manual verification**: Review changes to ensure correctness
4. **Document changes**: Note fixture updates in commit messages

## Testing with Fixtures

### Go Tests

```go
import (
    "os"
    "path/filepath"
    "testing"
)

func TestModelConversion(t *testing.T) {
    // Load input fixture
    input, err := os.ReadFile(filepath.Join("testdata", "TestModel.pb"))
    if err != nil {
        t.Fatal(err)
    }

    // Process...
    result := Convert(input)

    // Compare with golden file
    golden, _ := os.ReadFile(filepath.Join("testdata", "TestModel.golden.json"))
    if string(result) != string(golden) {
        t.Errorf("Output doesn't match golden file")
    }
}
```

### Python Tests

```python
import json
import pytest
from pathlib import Path

@pytest.fixture
def fixtures_dir():
    return Path(__file__).parent / "fixtures" / "openai"

def test_chat_completion(fixtures_dir):
    # Load fixture
    with open(fixtures_dir / "chat_completion.json") as f:
        expected = json.load(f)

    # Test logic...
    result = create_chat_completion(...)

    assert result == expected
```

## Adding New Fixture Types

When adding a new feature that requires fixtures:

1. **Create a fixture directory**: `testdata/` in the relevant component
2. **Add a README**: Document the fixture format and usage (this file or component-specific)
3. **Follow naming conventions**: Consistent with existing patterns
4. **Include in .gitignore if generated**: Only if they can be regenerated programmatically

## Related Documentation

- **CONTRIBUTING.md**: Guidelines for running tests
- **AGENTS.md**: Development and testing overview
- **Test workflows**: `.github/workflows/*-test.yml` - CI test execution

## Fixture Inventory

| Component | Location | Count | Purpose |
|-----------|----------|-------|---------|
| TF2OpenAPI | `tools/tf2openapi/*/testdata/` | ~20 files | TensorFlow model conversion |
| Python SDK | `python/kserve/test/fixtures/` | ~15 files | OpenAI protocol testing |
| Config samples | `config/samples/` | ~50 files | Example configurations |

## Common Issues

### Fixture Not Found

**Error**: `open testdata/file.json: no such file or directory`

**Solution**: Fixtures must be relative to the test file location. Ensure the test is running from the correct directory or use `filepath.Join()` with proper path resolution.

### Golden File Mismatch

**Error**: Test fails with diff output showing fixture vs. actual output

**Solutions**:
1. If output format intentionally changed: Regenerate golden files
2. If unexpected: Investigate why output differs from expected
3. Check for platform-specific differences (line endings, timestamps)

### Binary Fixtures Not Committed

**Error**: Test fails in CI but passes locally

**Solution**: Ensure binary files (`.pb`, images) are committed. Check `.gitattributes` for proper handling of binary files.

## Maintenance

Fixtures should be reviewed and updated when:
- API versions change (v1beta1 → v1beta2)
- Protocol specifications update (OpenAI API changes)
- New features require new test scenarios
- Dependencies upgrade with breaking changes

Keep fixtures lean and representative. Remove obsolete fixtures when deprecating features.
