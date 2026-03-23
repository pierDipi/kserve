# KServe Architecture

This document provides an in-repository overview of KServe's architecture. For comprehensive details, see the [official KServe documentation](https://kserve.github.io/website/docs/modelserving/).

## Overview

KServe is a Kubernetes-native model serving platform built on the operator pattern. It manages the lifecycle of machine learning models through Custom Resource Definitions (CRDs) and controllers.

```
┌─────────────────────────────────────────────────────────────┐
│                     Kubernetes Cluster                       │
│  ┌────────────────────────────────────────────────────────┐ │
│  │              KServe Control Plane (Go)                 │ │
│  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────┐  │ │
│  │  │   Manager    │  │   Webhooks   │  │  LLM ISVC   │  │ │
│  │  │  Controller  │  │ (Validation/ │  │  Controller │  │ │
│  │  │              │  │  Defaulting) │  │             │  │ │
│  │  └──────────────┘  └──────────────┘  └─────────────┘  │ │
│  └────────────────────────────────────────────────────────┘ │
│                            ↓                                 │
│  ┌────────────────────────────────────────────────────────┐ │
│  │               KServe Data Plane                        │ │
│  │  ┌──────────┐  ┌────────────┐  ┌──────────────────┐  │ │
│  │  │  Agent   │  │   Router   │  │  Model Servers   │  │ │
│  │  │ (Sidecar)│  │            │  │  (Python/Go)     │  │ │
│  │  └──────────┘  └────────────┘  └──────────────────┘  │ │
│  └────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
```

## Control Plane

The control plane manages InferenceService resources and reconciles desired state with actual cluster state.

### Components

#### 1. KServe Controller (`cmd/manager/`)
**Purpose**: Main controller that reconciles InferenceService CRDs

**Key Responsibilities**:
- Watch for InferenceService creation/updates/deletion
- Create and manage Knative Services or Kubernetes Deployments
- Configure autoscaling, networking, and routing
- Manage predictor, transformer, and explainer components
- Handle canary rollouts and traffic splitting

**Entry Point**: `cmd/manager/main.go`
**Core Logic**: `pkg/controller/v1beta1/inferenceservice/`

#### 2. LLM InferenceService Controller (`cmd/llmisvc/`)
**Purpose**: Specialized controller for Large Language Model serving

**Key Responsibilities**:
- Optimized reconciliation for LLM workloads
- GPU resource management
- Model caching and KV cache configuration
- Integration with vLLM and llm-d backends

**Entry Point**: `cmd/llmisvc/main.go`

#### 3. Local Model Controller (`cmd/localmodel/`)
**Purpose**: Manages models stored on local storage (not remote URIs)

**Key Responsibilities**:
- Handle local model storage patterns
- Node-local model management
- Integration with local storage providers

**Entry Point**: `cmd/localmodel/main.go`

#### 4. Admission Webhooks (`pkg/webhook/`)
**Purpose**: Validate and default InferenceService resources before admission

**Key Responsibilities**:
- Validate InferenceService specs
- Apply default values (runtime, resources, etc.)
- Enforce constraints and policies
- Inject sidecar configurations

**Types**:
- **Validating Webhook**: Ensures specs are valid
- **Mutating Webhook**: Applies defaults and transformations

## Data Plane

The data plane handles inference requests and model serving.

### Components

#### 1. Model Servers (Python)
**Purpose**: Framework-specific runtime servers that load models and serve predictions

**Locations**:
- `python/kserve/` - Core SDK and base server
- `python/sklearnserver/` - Scikit-learn models
- `python/xgbserver/` - XGBoost models
- `python/lgbserver/` - LightGBM models
- `python/pmmlserver/` - PMML models
- `python/paddleserver/` - PaddlePaddle models
- `python/huggingfaceserver/` - HuggingFace transformers & LLMs

**Common Features**:
- Model loading from storage (S3, GCS, PVC, HTTP)
- Inference via REST/gRPC
- OpenAPI schema generation
- Health checks and readiness probes

#### 2. Agent (`cmd/agent/`)
**Purpose**: Sidecar container that handles model lifecycle

**Key Responsibilities**:
- Download models from remote storage
- Manage model versioning
- Coordinate with storage initializer
- Health monitoring

**Entry Point**: `cmd/agent/main.go`
**Core Logic**: `pkg/agent/`

#### 3. Router (`cmd/router/`)
**Purpose**: Routes requests to predictor, transformer, and explainer components

**Key Responsibilities**:
- Intelligent request routing
- Pipeline orchestration (transformer → predictor → explainer)
- Traffic management
- Protocol translation

**Entry Point**: `cmd/router/main.go`

## Data Flow

### Inference Request Flow

```
User Request
    ↓
Ingress (Istio/Knative)
    ↓
Router (if multi-component)
    ↓
┌─────────────────────┐
│  Transformer        │ (optional)
│  (preprocess)       │
└─────────────────────┘
    ↓
┌─────────────────────┐
│  Predictor          │ (required)
│  (main inference)   │
└─────────────────────┘
    ↓
┌─────────────────────┐
│  Explainer          │ (optional)
│  (explain results)  │
└─────────────────────┘
    ↓
Response to User
```

### Model Lifecycle

```
1. InferenceService Created
        ↓
2. Controller Reconciles
        ↓
3. Creates K8s Resources (Service, Deployment/Knative Service)
        ↓
4. Agent Downloads Model (from storageUri)
        ↓
5. Model Server Loads Model
        ↓
6. Health Checks Pass
        ↓
7. Service Ready for Inference
```

## Key Abstractions

### InferenceService CRD

The core API resource representing a deployed model.

**Definition**: `pkg/apis/serving/v1beta1/inferenceservice_types.go`

**Key Spec Fields**:
```yaml
apiVersion: serving.kserve.io/v1beta1
kind: InferenceService
metadata:
  name: my-model
spec:
  predictor:             # Required: main inference component
    model:
      modelFormat:
        name: sklearn    # Runtime selection
      storageUri: s3://...
      resources:
        limits:
          nvidia.com/gpu: 1
  transformer:           # Optional: preprocessing
    containers: [...]
  explainer:            # Optional: explainability
    containers: [...]
```

### Runtime Selection

KServe auto-selects runtimes based on `modelFormat.name`:
- `sklearn` → SKLearn Server
- `xgboost` → XGBoost Server
- `pytorch` → TorchServe
- `tensorflow` → TFServing
- `onnx` → ONNX Runtime
- `huggingface` → HuggingFace Server
- Custom runtimes via `containers` or `runtime` field

**Configuration**: `config/runtimes/` contains ClusterServingRuntime CRDs

## Directory Structure Details

### `/pkg` - Go Packages

```
pkg/
├── apis/                 # CRD type definitions (v1alpha1, v1beta1)
│   └── serving/
│       └── v1beta1/     # Current stable API version
├── controller/          # Controller reconciliation logic
│   └── v1beta1/
│       └── inferenceservice/  # Main ISVC reconciler
├── webhook/             # Admission webhook handlers
├── agent/               # Model agent implementation
├── credentials/         # Storage credential providers (S3, GCS, etc.)
├── constants/           # Shared constants
├── utils/               # Utility functions
└── testing/             # Test utilities and mocks
```

### `/config` - Kubernetes Manifests

```
config/
├── default/             # Default kustomize overlay
├── crd/                 # Generated CRD YAML
├── webhook/             # Webhook configurations
├── manager/             # Controller deployment
├── runtimes/            # ClusterServingRuntime definitions
└── samples/             # Example InferenceServices
```

### `/python` - Model Servers

Each server follows a consistent structure:
```
python/[server_name]/
├── [server_name]/       # Source code
├── tests/               # pytest tests
├── Dockerfile          # Container build
├── Makefile            # Build/test targets
└── setup.py            # Python package config
```

## Deployment Modes

KServe supports multiple deployment modes:

### 1. Serverless (Knative)
- **Default mode**: Uses Knative Serving
- **Features**: Scale-to-zero, request-based autoscaling, canary rollouts
- **Requirements**: Knative Serving + Istio installed

### 2. Raw Deployment
- **Alternative**: Standard Kubernetes Deployments
- **Features**: Simpler, no Knative dependency
- **Limitations**: No scale-to-zero or advanced routing

### 3. ModelMesh
- **Use Case**: High-density, frequently-changing models
- **Features**: Multi-model serving, intelligent placement
- **Component**: Separate installation

Configuration via `inferenceservice.serving.kserve.io/deploymentMode` annotation.

## Build & Code Generation

### Code Generation Pipeline

```
API Changes (*.go types)
    ↓
make generate
    ↓
├── controller-gen → DeepCopy methods
├── client-gen     → Typed clients
├── informer-gen   → Informers
└── lister-gen     → Listers
    ↓
make manifests
    ↓
CRD YAML (config/crd/)
```

**Tools**: Defined in `Makefile.tools.mk`
- `controller-gen`: CRD and code generation
- `kustomize`: Manifest composition
- `setup-envtest`: Test environment

### Container Build

```bash
make docker-build        # Build all images
make docker-build-manager    # Build controller
make docker-build-agent      # Build agent
# ... etc
```

**Dockerfiles**:
- `Dockerfile` - Manager controller
- `agent.Dockerfile` - Model agent
- `router.Dockerfile` - Router
- `python/*/Dockerfile` - Model servers

## Testing Strategy

### Unit Tests
- **Location**: `*_test.go` files alongside source
- **Framework**: Go testing package
- **Run**: `make test`
- **Coverage**: Tracked in `coverage.out`

### Integration Tests
- **Location**: `pkg/controller/`, `pkg/webhook/`
- **Framework**: envtest (fake Kubernetes API)
- **Setup**: `make setup-envtest`

### E2E Tests
- **Location**: Defined in `.github/workflows/e2e-*.yml`
- **Environment**: Kind cluster with KServe installed
- **Scope**: Full InferenceService lifecycle

### Python Tests
- **Framework**: pytest
- **Coverage**: Per-server coverage
- **Run**: `cd python/[server] && make test`

## Design Decisions

### Why Operator Pattern?
- Declarative API (kubectl apply)
- Kubernetes-native lifecycle management
- Automatic reconciliation and self-healing
- Extensible via CRDs

### Why Separate Control/Data Planes?
- Scalability: Controllers scale independently from serving
- Security: Control plane can run in separate namespace
- Flexibility: Data plane can use different runtimes

### Why Multi-Component Architecture?
- Separation of concerns (transform, predict, explain)
- Reusability: Transformers can be shared across models
- Modularity: Components can be developed/tested independently

### Why Python for Model Servers?
- ML ecosystem is Python-centric
- Framework bindings (TensorFlow, PyTorch, etc.) are Python-first
- Easier integration with data science workflows

## Related Documentation

- **External Architecture Docs**: https://kserve.github.io/website/docs/modelserving/control_plane
- **API Reference**: https://kserve.github.io/website/docs/reference/crd-api
- **Developer Guide**: https://kserve.github.io/website/docs/developer-guide
- **AGENTS.md**: AI agent context and development guide (this repo)
- **CONTRIBUTING.md**: Contribution guidelines (this repo)

## Common Patterns

### Adding a New Runtime
1. Create ClusterServingRuntime YAML in `config/runtimes/`
2. Build and push container image with model server
3. Reference in InferenceService `spec.predictor.model.modelFormat.name`

### Extending the API
1. Modify types in `pkg/apis/serving/v1beta1/`
2. Run `make generate manifests`
3. Update controller reconciliation logic
4. Update webhook validation
5. Add tests

### Debugging Controller
```bash
# Local development
make install    # Install CRDs
make run        # Run controller locally

# In-cluster debugging
kubectl logs -n kserve -l control-plane=kserve-controller-manager -f
```

## Performance Considerations

- **Controller**: Lightweight, minimal memory (~300Mi default)
- **Model Servers**: Resource requirements vary by model size
- **Autoscaling**: Knative autoscaler monitors request concurrency
- **GPU**: GPU scheduling managed by Kubernetes device plugins

## Security

- **Service Accounts**: Separate accounts for controllers and data plane
- **RBAC**: Minimum required permissions defined in `config/rbac/`
- **Secrets**: Storage credentials via Kubernetes Secrets
- **Network Policies**: Can be applied to restrict traffic
- **Admission Control**: Webhooks enforce policies before admission
