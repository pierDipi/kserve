# Copyright 2025 The KServe Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#    http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

import hashlib
import os
import re

import pytest
from ..common.gw_api import (
    create_or_update_gateway,
    create_or_update_route,
)
from kserve import KServeClient, constants, V1alpha1LLMInferenceService
from kubernetes import client, config
from typing import List, Optional

from .logging import logger

KSERVE_PLURAL_LLMINFERENCESERVICECONFIG = "llminferenceserviceconfigs"
KSERVE_TEST_NAMESPACE = "kserve-ci-e2e-test"

# Scheduler config constants
SCHEDULER_CONFIGMAP_NAME = "scheduler-config-e2e"
SCHEDULER_CONFIGMAP_KEY = "epp"

# PVC storage constants
PVC_MODEL_NAME = "llm-e2e-pvc-opt-125m"
PVC_INIT_JOB_NAME = "e2e-pvc-hf-cache-init"
PVC_HF_DOWNLOAD_IMAGE = os.environ.get(
    "HF_DOWNLOAD_IMAGE", "docker.io/python:3.11-slim"
)
PVC_STORAGE_CLASS_NAME = os.environ.get("PVC_STORAGE_CLASS_NAME", None)

# Vanilla Kubernetes rejects runAsNonRoot-only containers when the image does not declare a USER.
# Keep the templates OpenShift-safe and use an explicit non-root UID only in upstream CI test overrides.
UPSTREAM_K8S_NON_ROOT_SECURITY_CONTEXT = {
    "runAsNonRoot": True,
    "runAsUser": 1000,
}

UPSTREAM_K8S_VLLM_ENV_OVERRIDES = [
    {"name": "USER", "value": "nonroot"},
    {"name": "TORCHINDUCTOR_CACHE_DIR", "value": "/tmp/torchinductor-cache"},
]

LLMD_SIMULATOR_SECURITY_CONTEXT = {
    "runAsNonRoot": True,
    "runAsUser": 65532,
    "runAsGroup": 65532,
}

LLMINFERENCESERVICE_CONFIGS = {
    "workload-single-cpu": {
        "template": {
            "containers": [
                {
                    "name": "main",
                    "image": "public.ecr.aws/q9t5s3a7/vllm-cpu-release-repo:v0.19.0",
                    "env": [
                        {"name": "VLLM_LOGGING_LEVEL", "value": "DEBUG"},
                        {"name": "VLLM_CPU_KVCACHE_SPACE", "value": "1"},
                        {"name": "VLLM_USE_V1", "value": "0"},
                        *UPSTREAM_K8S_VLLM_ENV_OVERRIDES,
                    ],
                    "resources": {
                        "limits": {"cpu": "2", "memory": "7Gi"},
                        "requests": {"cpu": "200m", "memory": "2Gi"},
                    },
                    "securityContext": UPSTREAM_K8S_NON_ROOT_SECURITY_CONTEXT.copy(),
                }
            ]
        },
    },
    "workload-pd-cpu": {
        "template": {
            "containers": [
                {
                    "name": "main",
                    "image": "public.ecr.aws/q9t5s3a7/vllm-cpu-release-repo:v0.19.0",
                    "env": [
                        {"name": "VLLM_LOGGING_LEVEL", "value": "DEBUG"},
                        {"name": "VLLM_CPU_KVCACHE_SPACE", "value": "1"},
                        {"name": "VLLM_USE_V1", "value": "0"},
                        *UPSTREAM_K8S_VLLM_ENV_OVERRIDES,
                    ],
                    "resources": {
                        "limits": {"cpu": "2", "memory": "7Gi"},
                        "requests": {"cpu": "200m", "memory": "2Gi"},
                    },
                    "securityContext": UPSTREAM_K8S_NON_ROOT_SECURITY_CONTEXT.copy(),
                    "livenessProbe": {
                        "httpGet": {"path": "/health", "port": 8000},
                        "initialDelaySeconds": 180,
                        "periodSeconds": 30,
                        "timeoutSeconds": 30,
                        "failureThreshold": 8,
                    },
                    "readinessProbe": {
                        "httpGet": {"path": "/health", "port": 8000},
                        "initialDelaySeconds": 30,
                        "periodSeconds": 10,
                        "timeoutSeconds": 5,
                        "failureThreshold": 3,
                    },
                }
            ]
        },
        "prefill": {
            "template": {
                "containers": [
                    {
                        "name": "main",
                        "image": "public.ecr.aws/q9t5s3a7/vllm-cpu-release-repo:v0.19.0",
                        "env": [
                            {"name": "VLLM_LOGGING_LEVEL", "value": "DEBUG"},
                            {"name": "VLLM_CPU_KVCACHE_SPACE", "value": "1"},
                            {"name": "VLLM_USE_V1", "value": "0"},
                            *UPSTREAM_K8S_VLLM_ENV_OVERRIDES,
                        ],
                        "resources": {
                            "limits": {"cpu": "2", "memory": "7Gi"},
                            "requests": {"cpu": "200m", "memory": "2Gi"},
                        },
                        "livenessProbe": {
                            "httpGet": {"path": "/health", "port": 8000},
                            "initialDelaySeconds": 180,
                            "periodSeconds": 30,
                            "timeoutSeconds": 30,
                            "failureThreshold": 8,
                        },
                        "readinessProbe": {
                            "httpGet": {"path": "/health", "port": 8000},
                            "initialDelaySeconds": 30,
                            "periodSeconds": 10,
                            "timeoutSeconds": 5,
                            "failureThreshold": 3,
                        },
                        "securityContext": UPSTREAM_K8S_NON_ROOT_SECURITY_CONTEXT.copy(),
                    }
                ]
            }
        },
    },
    "model-fb-opt-125m": {
        "model": {"uri": "hf://facebook/opt-125m", "name": "facebook/opt-125m"},
    },
    "model-qwen2.5-0.5b": {
        "model": {
            "uri": "hf://Qwen/Qwen2.5-0.5B-Instruct",
            "name": "Qwen/Qwen2.5-0.5B-Instruct",
        },
    },
    "model-fb-opt-125m-pvc": {
        "model": {
            "uri": f"pvc://{PVC_MODEL_NAME}?model=facebook/opt-125m",
            "name": "facebook/opt-125m",
        },
    },
    "model-deepseek-v2-lite": {
        "model": {
            "uri": "hf://deepseek-ai/DeepSeek-V2-Lite-Chat",
            "name": "deepseek-ai/DeepSeek-V2-Lite-Chat",
        },
    },
    "model-fb-opt-125m-with-lora-hf": {
        "model": {
            "uri": "hf://facebook/opt-125m",
            "name": "facebook/opt-125m",
            "lora": {
                "adapters": [
                    {
                        "name": "lora-adapter-1",
                        "uri": "hf://edbeeching/opt-125m-lora",
                    }
                ]
            },
        },
    },
    "model-fb-opt-125m-with-multiple-lora": {
        "model": {
            "uri": "hf://facebook/opt-125m",
            "name": "facebook/opt-125m",
            "lora": {
                "adapters": [
                    {
                        "name": "lora-adapter-1",
                        "uri": "hf://edbeeching/opt-125m-lora",
                    },
                    {
                        "name": "lora-adapter-2",
                        "uri": "hf://edbeeching/opt-125m-lora",
                    },
                ],
                "maxRank": 64,
                "maxAdapters": 2,
                "maxCpuAdapters": 4,
            },
        },
    },
    "workload-dp-ep-gpu": {
        "replicas": 2,
        "parallelism": {
            "data": 1,
            "dataLocal": 8,
            "expert": True,
            "tensor": 1,
        },
        "template": {
            "containers": [
                {
                    "name": "main",
                    "env": [
                        {"name": "VLLM_LOGGING_LEVEL", "value": "DEBUG"},
                        {"name": "TRITON_LIBCUDA_PATH", "value": "/usr/lib64"},
                        {"name": "HF_HUB_DISABLE_XET", "value": "1"},
                        {"name": "VLLM_SKIP_P2P_CHECK", "value": "1"},
                        {"name": "VLLM_RANDOMIZE_DP_DUMMY_INPUTS", "value": "1"},
                        {"name": "VLLM_USE_DEEP_GEMM", "value": "0"},
                        {
                            "name": "VLLM_ALL2ALL_BACKEND",
                            "value": "deepep_high_throughput",
                        },
                        {"name": "NVIDIA_GDRCOPY", "value": "enabled"},
                        {"name": "HF_HUB_CACHE", "value": "/huggingface-cache"},
                    ],
                    "resources": {
                        "limits": {
                            "cpu": "16",
                            "memory": "512Gi",
                            "nvidia.com/gpu": "8",
                        },
                        "requests": {
                            "cpu": "8",
                            "memory": "256Gi",
                            "nvidia.com/gpu": "8",
                        },
                    },
                    "livenessProbe": {
                        "httpGet": {"path": "/health", "port": 8001, "scheme": "HTTPS"},
                        "initialDelaySeconds": 400,
                        "periodSeconds": 10,
                        "timeoutSeconds": 10,
                        "failureThreshold": 3,
                    },
                }
            ]
        },
        "worker": {
            "containers": [
                {
                    "name": "main",
                    "env": [
                        {"name": "VLLM_LOGGING_LEVEL", "value": "DEBUG"},
                        {"name": "TRITON_LIBCUDA_PATH", "value": "/usr/lib64"},
                        {"name": "HF_HUB_DISABLE_XET", "value": "1"},
                        {"name": "VLLM_SKIP_P2P_CHECK", "value": "1"},
                        {"name": "VLLM_RANDOMIZE_DP_DUMMY_INPUTS", "value": "1"},
                        {"name": "VLLM_USE_DEEP_GEMM", "value": "0"},
                        {
                            "name": "VLLM_ALL2ALL_BACKEND",
                            "value": "deepep_high_throughput",
                        },
                        {"name": "NVIDIA_GDRCOPY", "value": "enabled"},
                        {"name": "HF_HUB_CACHE", "value": "/huggingface-cache"},
                    ],
                    "resources": {
                        "limits": {
                            "cpu": "16",
                            "memory": "512Gi",
                            "nvidia.com/gpu": "8",
                        },
                        "requests": {
                            "cpu": "8",
                            "memory": "256Gi",
                            "nvidia.com/gpu": "8",
                        },
                    },
                }
            ]
        },
    },
    "workload-dp-ep-prefill-gpu": {
        "prefill": {
            "parallelism": {
                "data": 1,
                "dataLocal": 8,
                "expert": True,
                "tensor": 1,
            },
            "template": {
                "containers": [
                    {
                        "name": "main",
                        "env": [
                            {"name": "VLLM_LOGGING_LEVEL", "value": "DEBUG"},
                            {"name": "TRITON_LIBCUDA_PATH", "value": "/usr/lib64"},
                            {"name": "HF_HUB_DISABLE_XET", "value": "1"},
                            {"name": "VLLM_SKIP_P2P_CHECK", "value": "1"},
                            {"name": "VLLM_RANDOMIZE_DP_DUMMY_INPUTS", "value": "1"},
                            {"name": "VLLM_USE_DEEP_GEMM", "value": "0"},
                            {
                                "name": "VLLM_ALL2ALL_BACKEND",
                                "value": "deepep_high_throughput",
                            },
                            {"name": "NVIDIA_GDRCOPY", "value": "enabled"},
                            {"name": "HF_HUB_CACHE", "value": "/huggingface-cache"},
                        ],
                        "resources": {
                            "limits": {
                                "cpu": "16",
                                "memory": "512Gi",
                                "nvidia.com/gpu": "8",
                            },
                            "requests": {
                                "cpu": "8",
                                "memory": "256Gi",
                                "nvidia.com/gpu": "8",
                            },
                        },
                        "livenessProbe": {
                            "httpGet": {
                                "path": "/health",
                                "port": 8000,
                                "scheme": "HTTPS",
                            },
                            "initialDelaySeconds": 400,
                            "periodSeconds": 10,
                            "timeoutSeconds": 10,
                            "failureThreshold": 3,
                        },
                    }
                ]
            },
            "worker": {
                "containers": [
                    {
                        "name": "main",
                        "env": [
                            {"name": "VLLM_LOGGING_LEVEL", "value": "DEBUG"},
                            {"name": "TRITON_LIBCUDA_PATH", "value": "/usr/lib64"},
                            {"name": "HF_HUB_DISABLE_XET", "value": "1"},
                            {"name": "VLLM_SKIP_P2P_CHECK", "value": "1"},
                            {"name": "VLLM_RANDOMIZE_DP_DUMMY_INPUTS", "value": "1"},
                            {"name": "VLLM_USE_DEEP_GEMM", "value": "0"},
                            {
                                "name": "VLLM_ALL2ALL_BACKEND",
                                "value": "deepep_high_throughput",
                            },
                            {"name": "NVIDIA_GDRCOPY", "value": "enabled"},
                            {"name": "HF_HUB_CACHE", "value": "/huggingface-cache"},
                        ],
                        "resources": {
                            "limits": {
                                "cpu": "16",
                                "memory": "512Gi",
                                "nvidia.com/gpu": "8",
                            },
                            "requests": {
                                "cpu": "8",
                                "memory": "256Gi",
                                "nvidia.com/gpu": "8",
                            },
                        },
                    }
                ]
            },
        },
    },
    "router-managed": {
        "router": {"scheduler": {}, "route": {}, "gateway": {}},
    },
    "router-no-scheduler": {
        "router": {"route": {}},
    },
    # This preset simulates DP+EP that can run on CPU, the idea is to test the LWS-based deployment
    # but without the resources requirements for DP+EP (GPUs and ROCe/IB)
    "workload-simulated-dp-ep-cpu": {
        "replicas": 1,
        "parallelism": {
            "data": 2,
            "dataLocal": 1,
            "expert": True,
            "tensor": 1,
        },
        "template": {
            "containers": [
                {
                    "name": "main",
                    "image": "public.ecr.aws/q9t5s3a7/vllm-cpu-release-repo:v0.19.0",
                    "command": ["vllm", "serve", "/mnt/models"],
                    "args": [
                        "--served-model-name",
                        "{{ .Spec.Model.Name }}",
                        "--port",
                        "8000",
                    ],
                    "env": [
                        {"name": "VLLM_CPU_KVCACHE_SPACE", "value": "1"},
                        {"name": "VLLM_USE_V1", "value": "0"},
                        *UPSTREAM_K8S_VLLM_ENV_OVERRIDES,
                    ],
                    "resources": {
                        "limits": {"cpu": "2", "memory": "7Gi"},
                        "requests": {"cpu": "200m", "memory": "2Gi"},
                    },
                    "securityContext": UPSTREAM_K8S_NON_ROOT_SECURITY_CONTEXT.copy(),
                }
            ]
        },
        "worker": {
            "containers": [
                {
                    "name": "main",
                    "image": "public.ecr.aws/q9t5s3a7/vllm-cpu-release-repo:v0.19.0",
                    "command": ["vllm", "serve", "/mnt/models"],
                    "args": [
                        "--served-model-name",
                        "{{ .Spec.Model.Name }}",
                        "--port",
                        "8000",
                    ],
                    "env": [
                        {"name": "VLLM_CPU_KVCACHE_SPACE", "value": "1"},
                        {"name": "VLLM_USE_V1", "value": "0"},
                        *UPSTREAM_K8S_VLLM_ENV_OVERRIDES,
                    ],
                    "resources": {
                        "limits": {"cpu": "2", "memory": "7Gi"},
                        "requests": {"cpu": "200m", "memory": "2Gi"},
                    },
                    "securityContext": UPSTREAM_K8S_NON_ROOT_SECURITY_CONTEXT.copy(),
                }
            ]
        },
    },
    "router-custom-route-timeout": {
        "router": {
            "route": {
                "http": {
                    "spec": {
                        "rules": [
                            {
                                "timeouts": {
                                    "request": "30s",
                                    "backendRequest": "30s",
                                },
                                "matches": [
                                    {
                                        "path": {
                                            "type": "PathPrefix",
                                            "value": "/kserve-ci-e2e-test/custom-route-timeout-test/v1/completions",
                                        },
                                    },
                                ],
                                "filters": [
                                    {
                                        "type": "URLRewrite",
                                        "urlRewrite": {
                                            "path": {
                                                "replacePrefixMatch": "/v1/completions",
                                                "type": "ReplacePrefixMatch",
                                            },
                                        },
                                    },
                                ],
                                "backendRefs": [
                                    {
                                        "group": "inference.networking.k8s.io",
                                        "kind": "InferencePool",
                                        "name": "custom-route-timeout-test-inference-pool",
                                        "namespace": KSERVE_TEST_NAMESPACE,
                                        "port": 8000,
                                    }
                                ],
                            },
                            {
                                "timeouts": {
                                    "request": "30s",
                                    "backendRequest": "30s",
                                },
                                "matches": [
                                    {
                                        "path": {
                                            "type": "PathPrefix",
                                            "value": "/kserve-ci-e2e-test/custom-route-timeout-test/v1/chat/completions",
                                        },
                                    },
                                ],
                                "filters": [
                                    {
                                        "type": "URLRewrite",
                                        "urlRewrite": {
                                            "path": {
                                                "replacePrefixMatch": "/v1/chat/completions",
                                                "type": "ReplacePrefixMatch",
                                            },
                                        },
                                    },
                                ],
                                "backendRefs": [
                                    {
                                        "group": "inference.networking.k8s.io",
                                        "kind": "InferencePool",
                                        "name": "custom-route-timeout-test-inference-pool",
                                        "namespace": KSERVE_TEST_NAMESPACE,
                                        "port": 8000,
                                    }
                                ],
                            },
                            {
                                "timeouts": {
                                    "request": "30s",
                                    "backendRequest": "30s",
                                },
                                "matches": [
                                    {
                                        "path": {
                                            "type": "PathPrefix",
                                            "value": "/kserve-ci-e2e-test/custom-route-timeout-test",
                                        },
                                    },
                                ],
                                "filters": [
                                    {
                                        "type": "URLRewrite",
                                        "urlRewrite": {
                                            "path": {
                                                "replacePrefixMatch": "/",
                                                "type": "ReplacePrefixMatch",
                                            },
                                        },
                                    },
                                ],
                                "backendRefs": [
                                    {
                                        "group": "",
                                        "kind": "Service",
                                        "name": "custom-route-timeout-test-kserve-workload-svc",
                                        "namespace": KSERVE_TEST_NAMESPACE,
                                        "port": 8000,
                                    }
                                ],
                            },
                        ],
                    },
                },
            },
            "gateway": {},
        },
    },
    "router-custom-route-timeout-pd": {
        "router": {
            "route": {
                "http": {
                    "spec": {
                        "rules": [
                            {
                                "timeouts": {
                                    "request": "30s",
                                    "backendRequest": "30s",
                                },
                                "matches": [
                                    {
                                        "path": {
                                            "type": "PathPrefix",
                                            "value": "/kserve-ci-e2e-test/custom-route-timeout-pd-test/v1/completions",
                                        },
                                    },
                                ],
                                "filters": [
                                    {
                                        "type": "URLRewrite",
                                        "urlRewrite": {
                                            "path": {
                                                "replacePrefixMatch": "/v1/completions",
                                                "type": "ReplacePrefixMatch",
                                            },
                                        },
                                    },
                                ],
                                "backendRefs": [
                                    {
                                        "group": "inference.networking.k8s.io",
                                        "kind": "InferencePool",
                                        "name": "custom-route-timeout-pd-test-inference-pool",
                                        "namespace": KSERVE_TEST_NAMESPACE,
                                        "port": 8000,
                                    }
                                ],
                            },
                            {
                                "timeouts": {
                                    "request": "30s",
                                    "backendRequest": "30s",
                                },
                                "matches": [
                                    {
                                        "path": {
                                            "type": "PathPrefix",
                                            "value": "/kserve-ci-e2e-test/custom-route-timeout-pd-test/v1/chat/completions",
                                        },
                                    },
                                ],
                                "filters": [
                                    {
                                        "type": "URLRewrite",
                                        "urlRewrite": {
                                            "path": {
                                                "replacePrefixMatch": "/v1/chat/completions",
                                                "type": "ReplacePrefixMatch",
                                            },
                                        },
                                    },
                                ],
                                "backendRefs": [
                                    {
                                        "group": "inference.networking.k8s.io",
                                        "kind": "InferencePool",
                                        "name": "custom-route-timeout-pd-test-inference-pool",
                                        "namespace": KSERVE_TEST_NAMESPACE,
                                        "port": 8000,
                                    }
                                ],
                            },
                            {
                                "timeouts": {
                                    "request": "30s",
                                    "backendRequest": "30s",
                                },
                                "matches": [
                                    {
                                        "path": {
                                            "type": "PathPrefix",
                                            "value": "/kserve-ci-e2e-test/custom-route-timeout-pd-test",
                                        },
                                    },
                                ],
                                "filters": [
                                    {
                                        "type": "URLRewrite",
                                        "urlRewrite": {
                                            "path": {
                                                "replacePrefixMatch": "/",
                                                "type": "ReplacePrefixMatch",
                                            },
                                        },
                                    },
                                ],
                                "backendRefs": [
                                    {
                                        "group": "",
                                        "kind": "Service",
                                        "name": "custom-route-timeout-pd-test-kserve-workload-svc",
                                        "namespace": KSERVE_TEST_NAMESPACE,
                                        "port": 8000,
                                    }
                                ],
                            },
                        ],
                    },
                },
            },
            "gateway": {},
        },
    },
    "router-with-refs": {
        "router": {
            "route": {
                "http": {
                    "refs": [
                        {"name": "router-route-1"},
                        {"name": "router-route-2"},
                    ],
                },
            },
            "gateway": {
                "refs": [
                    {"name": "router-gateway-1", "namespace": KSERVE_TEST_NAMESPACE},
                ],
            },
        },
    },
    "router-with-refs-pd": {
        "router": {
            "route": {
                "http": {
                    "refs": [
                        {"name": "router-route-3"},
                        {"name": "router-route-4"},
                    ],
                },
            },
            "gateway": {
                "refs": [
                    {"name": "router-gateway-2", "namespace": KSERVE_TEST_NAMESPACE},
                ],
            },
        },
    },
    "scheduler-managed": {
        "router": {
            "scheduler": {},
        },
    },
    "scheduler-with-inline-config": {
        "router": {
            "scheduler": {
                "config": {
                    "inline": {
                        "apiVersion": "inference.networking.x-k8s.io/v1alpha1",
                        "kind": "EndpointPickerConfig",
                        "plugins": [
                            {"type": "single-profile-handler"},
                            {"type": "queue-scorer"},
                            {"type": "prefix-cache-scorer"},
                            {"type": "max-score-picker"},
                        ],
                        "schedulingProfiles": [
                            {
                                "name": "default",
                                "plugins": [
                                    {"pluginRef": "queue-scorer", "weight": 2},
                                    {"pluginRef": "prefix-cache-scorer", "weight": 3},
                                    {"pluginRef": "max-score-picker"},
                                ],
                            },
                        ],
                    },
                },
            },
        },
    },
    "scheduler-with-precise-prefix-cache-inline-config": {
        "router": {
            "scheduler": {
                "config": {
                    "inline": {
                        "apiVersion": "inference.networking.x-k8s.io/v1alpha1",
                        "kind": "EndpointPickerConfig",
                        "plugins": [
                            {"type": "single-profile-handler"},
                            {
                                "type": "precise-prefix-cache-scorer",
                                "parameters": {
                                    "tokenProcessorConfig": {
                                        "blockSize": 16,
                                        "hashSeed": "42",
                                    },
                                    "kvEventsConfig": {
                                        "zmqEndpoint": "tcp://*:5557",
                                    },
                                    "indexerConfig": {
                                        "tokenizersPoolConfig": {
                                            "modelName": "facebook/opt-125m",
                                        },
                                        "kvBlockIndexConfig": {
                                            "enableMetrics": True,
                                            "metricsLoggingInterval": 60000000000,
                                        },
                                    },
                                },
                            },
                            {"type": "queue-scorer"},
                            {"type": "kv-cache-utilization-scorer"},
                            {"type": "max-score-picker"},
                        ],
                        "schedulingProfiles": [
                            {
                                "name": "default",
                                "plugins": [
                                    {"pluginRef": "queue-scorer", "weight": 2},
                                    {
                                        "pluginRef": "kv-cache-utilization-scorer",
                                        "weight": 2,
                                    },
                                    {
                                        "pluginRef": "precise-prefix-cache-scorer",
                                        "weight": 3,
                                    },
                                    {"pluginRef": "max-score-picker"},
                                ],
                            },
                        ],
                    },
                },
            },
        },
    },
    "scheduler-with-configmap-ref": {
        "router": {
            "scheduler": {
                "config": {
                    "ref": {
                        "name": SCHEDULER_CONFIGMAP_NAME,
                        "key": SCHEDULER_CONFIGMAP_KEY,
                    },
                },
            },
        },
    },
    "scheduler-with-replicas": {
        "router": {
            "scheduler": {
                "replicas": 2,
            },
        },
    },
    "scheduler-with-custom-template": {
        "router": {
            "scheduler": {
                "template": {
                    "containers": [
                        {
                            "name": "main",
                            "env": [
                                {
                                    "name": "TOKENIZER_CACHE_DIR",
                                    "value": "/tmp/tokenizer-cache",
                                },
                                {
                                    "name": "HF_HOME",
                                    "value": "/tmp/tokenizer-cache",
                                },
                                {
                                    "name": "TRANSFORMERS_CACHE",
                                    "value": "/tmp/tokenizer-cache",
                                },
                                {"name": "XDG_CACHE_HOME", "value": "/tmp"},
                            ],
                            "args": [
                                "--cert-path",
                                "/var/run/kserve/tls",
                                "--pool-group",
                                "inference.networking.x-k8s.io",
                                "--pool-name",
                                "{{ ChildName .ObjectMeta.Name `-inference-pool` }}",
                                "--pool-namespace",
                                "{{ .ObjectMeta.Namespace }}",
                                "--zap-encoder",
                                "json",
                                "--grpc-port",
                                "9002",
                                "--grpc-health-port",
                                "9003",
                                "--secure-serving",
                                "--model-server-metrics-scheme",
                                "https",
                                "--kv-cache-usage-percentage-metric",
                                "vllm:kv_cache_usage_perc",
                                "--config-text",
                                (
                                    "apiVersion: inference.networking.x-k8s.io/v1alpha1\n"
                                    "kind: EndpointPickerConfig\n"
                                    "plugins:\n"
                                    "- type: single-profile-handler\n"
                                    "- type: queue-scorer\n"
                                    "- type: active-request-scorer\n"
                                    "- type: prefix-cache-scorer\n"
                                    "schedulingProfiles:\n"
                                    "- name: default\n"
                                    "  plugins:\n"
                                    "  - pluginRef: queue-scorer\n"
                                    "    weight: 2\n"
                                    "  - pluginRef: active-request-scorer\n"
                                    "    weight: 2\n"
                                    "  - pluginRef: prefix-cache-scorer\n"
                                    "    weight: 3\n"
                                ),
                            ],
                            "volumeMounts": [
                                {
                                    "name": "tokenizer-cache",
                                    "mountPath": "/tmp/tokenizer-cache",
                                },
                                {
                                    "name": "cachi2-cache",
                                    "mountPath": "/cachi2",
                                },
                            ],
                        }
                    ],
                    "volumes": [
                        {"name": "tokenizer-cache", "emptyDir": {}},
                        {"name": "cachi2-cache", "emptyDir": {}},
                    ],
                },
            },
        },
    },
    "router-with-gateway-section-name": {
        "router": {
            "gateway": {
                "refs": [
                    {
                        "name": "router-gateway-1",
                        "namespace": KSERVE_TEST_NAMESPACE,
                        "sectionName": "http",
                    },
                ],
            },
        },
    },
    "router-with-gateway-ref": {
        "router": {
            "gateway": {
                "refs": [
                    {"name": "router-gateway-1", "namespace": KSERVE_TEST_NAMESPACE},
                ],
            },
        },
    },
    "router-with-managed-route": {
        "router": {"route": {}},
    },
    "workload-llmd-simulator": {
        "replicas": 1,
        "model": {"uri": "hf://facebook/opt-125m", "name": "facebook/opt-125m"},
        "template": {
            "containers": [
                {
                    "name": "main",
                    "image": "ghcr.io/llm-d/llm-d-inference-sim:v0.8.2",
                    "command": ["/app/llm-d-inference-sim"],
                    "args": [
                        "--port",
                        "8000",
                        "--model",
                        "{{ .Spec.Model.Name }}",
                        "--mode",
                        "random",
                    ],
                    "resources": {
                        "limits": {"cpu": "1", "memory": "2Gi"},
                        "requests": {"cpu": "200m", "memory": "2Gi"},
                    },
                    "securityContext": LLMD_SIMULATOR_SECURITY_CONTEXT.copy(),
                }
            ]
        },
    },
    "workload-llmd-simulator-no-replicas": {
        "model": {"uri": "hf://facebook/opt-125m", "name": "facebook/opt-125m"},
        "template": {
            "containers": [
                {
                    "name": "main",
                    "image": "ghcr.io/llm-d/llm-d-inference-sim:v0.8.2",
                    "command": ["/app/llm-d-inference-sim"],
                    "args": [
                        "--port",
                        "8000",
                        "--model",
                        "{{ .Spec.Model.Name }}",
                        "--mode",
                        "random",
                    ],
                    "resources": {
                        "limits": {"cpu": "1", "memory": "2Gi"},
                        "requests": {"cpu": "200m", "memory": "2Gi"},
                    },
                    "securityContext": LLMD_SIMULATOR_SECURITY_CONTEXT.copy(),
                }
            ]
        },
    },
    "workload-llmd-simulator-lws": {
        "model": {"uri": "hf://facebook/opt-125m", "name": "facebook/opt-125m"},
        "parallelism": {
            "data": 2,
            "dataLocal": 1,
            "expert": True,
            "tensor": 1,
        },
        "template": {
            "containers": [
                {
                    "name": "main",
                    "image": "ghcr.io/llm-d/llm-d-inference-sim:v0.8.2",
                    "command": ["/app/llm-d-inference-sim"],
                    "args": [
                        "--port",
                        "8000",
                        "--model",
                        "{{ .Spec.Model.Name }}",
                        "--mode",
                        "random",
                    ],
                    "resources": {
                        "limits": {"cpu": "1", "memory": "2Gi"},
                        "requests": {"cpu": "200m", "memory": "2Gi"},
                    },
                    "securityContext": LLMD_SIMULATOR_SECURITY_CONTEXT.copy(),
                }
            ]
        },
        "worker": {
            "containers": [
                {
                    "name": "main",
                    "image": "ghcr.io/llm-d/llm-d-inference-sim:v0.8.2",
                    "command": ["/app/llm-d-inference-sim"],
                    "args": [
                        "--port",
                        "8000",
                        "--model",
                        "{{ .Spec.Model.Name }}",
                        "--mode",
                        "random",
                    ],
                    "resources": {
                        "limits": {"cpu": "1", "memory": "2Gi"},
                        "requests": {"cpu": "200m", "memory": "2Gi"},
                    },
                    "securityContext": LLMD_SIMULATOR_SECURITY_CONTEXT.copy(),
                }
            ]
        },
    },
    "workload-llmd-simulator-pd": {
        "model": {"uri": "hf://facebook/opt-125m", "name": "facebook/opt-125m"},
        "template": {
            "containers": [
                {
                    "name": "main",
                    "image": "ghcr.io/llm-d/llm-d-inference-sim:v0.8.2",
                    "command": ["/app/llm-d-inference-sim"],
                    "args": [
                        "--port",
                        "8000",
                        "--model",
                        "{{ .Spec.Model.Name }}",
                        "--mode",
                        "random",
                    ],
                    "resources": {
                        "limits": {"cpu": "1", "memory": "2Gi"},
                        "requests": {"cpu": "200m", "memory": "2Gi"},
                    },
                    "securityContext": LLMD_SIMULATOR_SECURITY_CONTEXT.copy(),
                }
            ]
        },
        "prefill": {
            "template": {
                "containers": [
                    {
                        "name": "main",
                        "image": "ghcr.io/llm-d/llm-d-inference-sim:v0.8.2",
                        "command": ["/app/llm-d-inference-sim"],
                        "args": [
                            "--port",
                            "8000",
                            "--model",
                            "{{ .Spec.Model.Name }}",
                            "--mode",
                            "random",
                        ],
                        "resources": {
                            "limits": {"cpu": "1", "memory": "2Gi"},
                            "requests": {"cpu": "200m", "memory": "2Gi"},
                        },
                        "securityContext": LLMD_SIMULATOR_SECURITY_CONTEXT.copy(),
                    }
                ]
            }
        },
    },
    "prometheus-scrape": {
        "annotations": {
            "prometheus.io/scrape": "true",
            "prometheus.io/port": "8000",
            "prometheus.io/path": "/metrics",
        },
    },
    "scaling-hpa": {
        "scaling": {
            "minReplicas": 1,
            "maxReplicas": 3,
            "wva": {
                "hpa": {
                    "behavior": {
                        "scaleDown": {
                            "stabilizationWindowSeconds": 10,
                            "policies": [
                                {
                                    "type": "Percent",
                                    "value": 100,
                                    "periodSeconds": 10,
                                }
                            ],
                        },
                        "scaleUp": {
                            "stabilizationWindowSeconds": 0,
                            "policies": [
                                {
                                    "type": "Percent",
                                    "value": 100,
                                    "periodSeconds": 10,
                                }
                            ],
                        },
                    }
                }
            },
        }
    },
    "scaling-keda": {
        "scaling": {
            "minReplicas": 1,
            "maxReplicas": 3,
            "wva": {
                "keda": {
                    "pollingInterval": 5,
                    "cooldownPeriod": 10,
                    "initialCooldownPeriod": 0,
                }
            },
        }
    },
    "scaling-prefill-hpa": {
        "prefill": {
            "scaling": {
                "minReplicas": 1,
                "maxReplicas": 3,
                "wva": {
                    "hpa": {
                        "behavior": {
                            "scaleDown": {
                                "stabilizationWindowSeconds": 10,
                                "policies": [
                                    {
                                        "type": "Percent",
                                        "value": 100,
                                        "periodSeconds": 10,
                                    }
                                ],
                            },
                            "scaleUp": {
                                "stabilizationWindowSeconds": 0,
                                "policies": [
                                    {
                                        "type": "Percent",
                                        "value": 100,
                                        "periodSeconds": 10,
                                    }
                                ],
                            },
                        }
                    }
                },
            }
        }
    },
    "scaling-prefill-keda": {
        "prefill": {
            "scaling": {
                "minReplicas": 1,
                "maxReplicas": 3,
                "wva": {
                    "keda": {
                        "pollingInterval": 5,
                        "cooldownPeriod": 10,
                        "initialCooldownPeriod": 0,
                    }
                },
            }
        }
    },
    "workload-llmd-simulator-kvcache": {
        "replicas": 2,
        "model": {"uri": "hf://facebook/opt-125m", "name": "facebook/opt-125m"},
        "template": {
            "containers": [
                {
                    "name": "main",
                    "image": "ghcr.io/llm-d/llm-d-inference-sim:v0.8.2",
                    "command": ["/app/llm-d-inference-sim"],
                    "args": [
                        "--port",
                        "8000",
                        "--model",
                        "{{ .Spec.Model.Name }}",
                        "--mode",
                        "random",
                        "--enable-kvcache",
                        "--block-size",
                        "16",
                        "--zmq-endpoint",
                        "tcp://{{ ChildName .ObjectMeta.Name `-epp-service` }}:5557",
                        "--hash-seed",
                        "42",
                        "--event-batch-size",
                        "1",
                    ],
                    "env": [
                        {
                            "name": "POD_IP",
                            "valueFrom": {
                                "fieldRef": {
                                    "apiVersion": "v1",
                                    "fieldPath": "status.podIP",
                                },
                            },
                        },
                    ],
                    "resources": {
                        "limits": {"cpu": "1", "memory": "2Gi"},
                        "requests": {"cpu": "20m", "memory": "20Mi"},
                    },
                    "securityContext": LLMD_SIMULATOR_SECURITY_CONTEXT.copy(),
                }
            ]
        },
    },
}


@pytest.fixture(scope="function")
def test_case(request):
    tc = request.param
    created_configs = []

    inject_k8s_proxy()

    kserve_client = KServeClient(
        config_file=os.environ.get("KUBECONFIG", "~/.kube/config"),
        client_configuration=client.Configuration(),
    )

    # Execute before test hooks
    try:
        for func in tc.before_test:
            func()
    except Exception as before_test_error:
        raise RuntimeError(
            f"Failed to execute before test hook: {before_test_error}"
        ) from before_test_error

    try:
        # Validate base_refs defined in the test fixture exist in LLMINFERENCESERVICE_CONFIGS
        missing_refs = [
            ref for ref in tc.base_refs if ref not in LLMINFERENCESERVICE_CONFIGS
        ]
        if missing_refs:
            raise ValueError(
                f"Missing base_refs in LLMINFERENCESERVICE_CONFIGS: {missing_refs}"
            )
        if not tc.service_name:
            tc.service_name = generate_service_name(request.node.name, tc.base_refs)
        if tc.model_name == "default/model":
            tc.model_name = _get_model_name_from_configs(tc.base_refs)

        # Create unique configs for this test
        unique_base_refs = []
        for base_ref in tc.base_refs:
            unique_config_name = generate_k8s_safe_suffix(base_ref, [tc.service_name])
            unique_base_refs.append(unique_config_name)

            original_spec = LLMINFERENCESERVICE_CONFIGS[base_ref]

            unique_config_body = {
                "apiVersion": "serving.kserve.io/v1alpha1",
                "kind": "LLMInferenceServiceConfig",
                "metadata": {
                    "name": unique_config_name,
                    "namespace": KSERVE_TEST_NAMESPACE,
                },
                "spec": original_spec,
            }

            _create_or_update_llmisvc_config(
                kserve_client, unique_config_body, KSERVE_TEST_NAMESPACE
            )
            created_configs.append(unique_config_name)

        tc.llm_service = V1alpha1LLMInferenceService(
            api_version="serving.kserve.io/v1alpha1",
            kind="LLMInferenceService",
            metadata=client.V1ObjectMeta(
                name=tc.service_name, namespace=KSERVE_TEST_NAMESPACE
            ),
            spec={
                "baseRefs": [{"name": base_ref} for base_ref in unique_base_refs],
            },
        )

        yield tc

    finally:
        if os.getenv("SKIP_RESOURCE_DELETION", "False").lower() in ("true", "1", "t"):
            logger.info("Skipping resource deletion after test execution.")
            return  # noqa: B012

        for func in tc.after_test:
            try:
                func()
            except Exception as after_test_error:
                logger.warning(f"Failed to execute after test hook: {after_test_error}")


def _get_model_name_from_configs(config_names):
    """Extract the model name from model config."""
    for config_name in config_names:
        config = LLMINFERENCESERVICE_CONFIGS[config_name]
        if "model" in config and "name" in config["model"]:
            return config["model"]["name"]
    return "default/model"


_NON_DNS_CHARS = re.compile(r"[^a-z0-9]+")


def _sanitize_for_dns(s: str) -> str:
    """Replace non-DNS characters with hyphens, mirrors sanitizeForDNS in test_namespace.go."""
    return _NON_DNS_CHARS.sub("-", s.lower()).strip("-")


def generate_k8s_safe_suffix(
    base_name: str, extra_parts: Optional[List[str]] = None
) -> str:
    """Generate a Kubernetes-safe name suffix with hash."""
    raw = f"{base_name}-{'-'.join(sorted(extra_parts))}" if extra_parts else base_name
    full_name = _sanitize_for_dns(raw)

    name_hash = hashlib.sha256(full_name.encode()).hexdigest()[:8]

    # TODO: we can't use the real maximum (63), LWS and STS add additional suffixes (ie `-0`) and don't handle that case.
    max_total = 40
    sep = "-"
    max_base = max_total - len(sep) - len(name_hash)
    safe_base = full_name[:max_base].rstrip(sep)

    return f"{safe_base}{sep}{name_hash}"


def generate_service_name(test_name: str, base_refs: List[str]) -> str:
    base_name = test_name.split("[", 1)[0]
    base_name = base_name.replace("test_llm_inference_service", "llmisvc")
    return generate_k8s_safe_suffix(base_name, base_refs)


def generate_test_id(test_case) -> str:
    """Generate a test ID from base refs."""
    return "-".join(test_case.base_refs)


def create_router_resources(gateways, routes=None, kserve_client=None):
    """Create router resources (gateways and routes). These resources are shared and not deleted.

    The create_or_update functions are idempotent, so multiple tests creating the same
    resource will not cause errors.
    """
    if not kserve_client:
        kserve_client = KServeClient(
            config_file=os.environ.get("KUBECONFIG", "~/.kube/config")
        )

    for gateway in gateways:
        gateway_name = gateway.get("metadata", {}).get("name", "unknown")
        try:
            create_or_update_gateway(kserve_client, gateway)
            logger.info(f"✓ Created/updated Gateway {gateway_name}")
        except Exception as e:
            logger.error(f"❌ Failed to create Gateway {gateway_name}: {e}")
            raise

    for route in routes or []:
        route_name = route.get("metadata", {}).get("name", "unknown")
        try:
            create_or_update_route(kserve_client, route)
            logger.info(f"✓ Created/updated HTTPRoute {route_name}")
        except Exception as e:
            logger.error(f"❌ Failed to create HTTPRoute {route_name}: {e}")
            raise


def _create_or_update_llmisvc_config(kserve_client, llm_config, namespace=None):
    """Create or update an LLMInferenceServiceConfig resource."""
    version = llm_config["apiVersion"].split("/")[1]

    if namespace is None:
        namespace = llm_config.get("metadata", {}).get("namespace", "default")

    name = llm_config.get("metadata", {}).get("name")
    if not name:
        raise ValueError("LLMInferenceServiceConfig must have a name in metadata")

    logger.info(f"Checking LLMInferenceServiceConfig {name} in namespace {namespace}")

    try:
        existing_config = kserve_client.api_instance.get_namespaced_custom_object(
            constants.KSERVE_GROUP,
            version,
            namespace,
            KSERVE_PLURAL_LLMINFERENCESERVICECONFIG,
            name,
        )

        llm_config["metadata"] = existing_config["metadata"]

        outputs = kserve_client.api_instance.replace_namespaced_custom_object(
            constants.KSERVE_GROUP,
            version,
            namespace,
            KSERVE_PLURAL_LLMINFERENCESERVICECONFIG,
            name,
            llm_config,
        )
        logger.info(f"✓ Successfully updated LLMInferenceServiceConfig {name}")
        return outputs

    except client.rest.ApiException as e:
        if e.status == 404:  # Not found - create it
            logger.info(
                f"Resource not found, creating LLMInferenceServiceConfig {name}"
            )
            outputs = kserve_client.api_instance.create_namespaced_custom_object(
                constants.KSERVE_GROUP,
                version,
                namespace,
                KSERVE_PLURAL_LLMINFERENCESERVICECONFIG,
                llm_config,
            )
            logger.info(f"✓ Successfully created LLMInferenceServiceConfig {name}")
            return outputs
        else:
            raise RuntimeError(
                f"Failed to get/create LLMInferenceServiceConfig {name}: {e}"
            ) from e


def inject_k8s_proxy():
    config.load_kube_config()
    proxy_url = os.getenv("HTTPS_PROXY", os.getenv("HTTP_PROXY", None))
    if proxy_url:
        logger.info(f"✅ Using Proxy URL: {proxy_url} for k8s client")
        client.Configuration._default.proxy = proxy_url
    else:
        logger.info("No HTTP proxy configured for k8s client")


# Scheduler config YAML used for ConfigMap ref tests
SCHEDULER_CONFIG_YAML = """apiVersion: inference.networking.x-k8s.io/v1alpha1
kind: EndpointPickerConfig
plugins:
- type: single-profile-handler
- type: queue-scorer
- type: prefix-cache-scorer
- type: max-score-picker
schedulingProfiles:
- name: default
  plugins:
  - pluginRef: queue-scorer
    weight: 2
  - pluginRef: prefix-cache-scorer
    weight: 3
  - pluginRef: max-score-picker
"""


def create_scheduler_configmap():
    """Create ConfigMap with scheduler configuration."""
    inject_k8s_proxy()
    core_v1 = client.CoreV1Api()

    configmap = client.V1ConfigMap(
        api_version="v1",
        kind="ConfigMap",
        metadata=client.V1ObjectMeta(
            name=SCHEDULER_CONFIGMAP_NAME,
            namespace=KSERVE_TEST_NAMESPACE,
        ),
        data={
            SCHEDULER_CONFIGMAP_KEY: SCHEDULER_CONFIG_YAML,
        },
    )

    try:
        core_v1.create_namespaced_config_map(
            namespace=KSERVE_TEST_NAMESPACE,
            body=configmap,
        )
        logger.info(
            f"Created ConfigMap {SCHEDULER_CONFIGMAP_NAME} in namespace {KSERVE_TEST_NAMESPACE}"
        )
    except client.rest.ApiException as e:
        if e.status == 409:  # Already exists
            core_v1.replace_namespaced_config_map(
                name=SCHEDULER_CONFIGMAP_NAME,
                namespace=KSERVE_TEST_NAMESPACE,
                body=configmap,
            )
            logger.info(
                f"Updated ConfigMap {SCHEDULER_CONFIGMAP_NAME} in namespace {KSERVE_TEST_NAMESPACE}"
            )
        else:
            raise


def delete_scheduler_configmap():
    """Delete ConfigMap with scheduler configuration."""
    inject_k8s_proxy()
    core_v1 = client.CoreV1Api()

    try:
        core_v1.delete_namespaced_config_map(
            name=SCHEDULER_CONFIGMAP_NAME,
            namespace=KSERVE_TEST_NAMESPACE,
        )
        logger.info(
            f"Deleted ConfigMap {SCHEDULER_CONFIGMAP_NAME} from namespace {KSERVE_TEST_NAMESPACE}"
        )
    except client.rest.ApiException as e:
        if e.status != 404:  # Ignore not found
            raise


def create_pvc_with_hf_cache():
    """Create a PVC and a Job that populates it with a HuggingFace Hub cache.

    The Job runs ``huggingface-cli download`` so the PVC ends up with the
    standard HF cache directory layout (models--org--name/blobs/snapshots/refs/).
    This is required for testing the ``pvc://<name>?model=<id>`` feature, where
    vLLM resolves the model from HF_HOME on the PVC.

    This resource is shared across tests and is NOT deleted in after_test hooks
    (same pattern as create_router_resources). Multiple tests calling this
    function concurrently are safe: PVC and Job creation are idempotent, and
    concurrent callers simply wait for the same Job to complete.
    """
    inject_k8s_proxy()
    batch_v1 = client.BatchV1Api()

    # If the download Job already succeeded, the PVC is ready.
    if _is_pvc_init_job_completed(batch_v1):
        logger.info(f"Job {PVC_INIT_JOB_NAME} already succeeded, PVC is ready")
        return

    _create_pvc(client.CoreV1Api())
    _create_pvc_init_job(batch_v1)
    _wait_for_pvc_init_job(batch_v1)


def _is_pvc_init_job_completed(batch_v1):
    """Return True if the PVC init Job has already succeeded."""
    try:
        job_status = batch_v1.read_namespaced_job_status(
            name=PVC_INIT_JOB_NAME, namespace=KSERVE_TEST_NAMESPACE
        )
        return job_status.status.succeeded and job_status.status.succeeded >= 1
    except client.rest.ApiException as e:
        if e.status == 404:
            return False
        raise


def _create_pvc(core_v1):
    pvc = client.V1PersistentVolumeClaim(
        api_version="v1",
        kind="PersistentVolumeClaim",
        metadata=client.V1ObjectMeta(
            name=PVC_MODEL_NAME,
            namespace=KSERVE_TEST_NAMESPACE,
        ),
        # RWO is safe for parallel tests on single-node clusters (KinD, Minikube)
        # because multiple pods on the same node can mount the same RWO PVC.
        # Multi-node clusters would need RWX, but local-path provisioners
        # (KinD, Minikube) only support RWO.
        spec=client.V1PersistentVolumeClaimSpec(
            access_modes=["ReadWriteOnce"],
            storage_class_name=PVC_STORAGE_CLASS_NAME,
            resources=client.V1VolumeResourceRequirements(
                requests={"storage": "5Gi"},
            ),
        ),
    )

    try:
        core_v1.create_namespaced_persistent_volume_claim(
            namespace=KSERVE_TEST_NAMESPACE, body=pvc
        )
        logger.info(f"Created PVC {PVC_MODEL_NAME}")
    except client.rest.ApiException as e:
        if e.status == 409:
            logger.info(f"PVC {PVC_MODEL_NAME} already exists")
        else:
            raise


def _create_pvc_init_job(batch_v1):
    job = client.V1Job(
        api_version="batch/v1",
        kind="Job",
        metadata=client.V1ObjectMeta(
            name=PVC_INIT_JOB_NAME,
            namespace=KSERVE_TEST_NAMESPACE,
        ),
        spec=client.V1JobSpec(
            completions=1,
            parallelism=1,
            backoff_limit=2,
            template=client.V1PodTemplateSpec(
                spec=client.V1PodSpec(
                    restart_policy="Never",
                    volumes=[
                        client.V1Volume(
                            name="model-cache",
                            persistent_volume_claim=client.V1PersistentVolumeClaimVolumeSource(
                                claim_name=PVC_MODEL_NAME,
                                read_only=False,
                            ),
                        ),
                    ],
                    containers=[
                        client.V1Container(
                            name="download",
                            image=PVC_HF_DOWNLOAD_IMAGE,
                            command=["sh", "-c"],
                            # Download facebook/opt-125m into the HF cache
                            # layout under /mnt/models/hub so vLLM can
                            # resolve it via HF_HOME=/mnt/models.
                            args=[
                                "pip install --quiet huggingface_hub && "
                                "huggingface-cli download facebook/opt-125m "
                                "--cache-dir /mnt/models/hub"
                            ],
                            volume_mounts=[
                                client.V1VolumeMount(
                                    name="model-cache",
                                    mount_path="/mnt/models",
                                ),
                            ],
                            resources=client.V1ResourceRequirements(
                                requests={"cpu": "500m", "memory": "1Gi"},
                                limits={"cpu": "1", "memory": "2Gi"},
                            ),
                        ),
                    ],
                ),
            ),
        ),
    )

    try:
        batch_v1.create_namespaced_job(namespace=KSERVE_TEST_NAMESPACE, body=job)
        logger.info(f"Created Job {PVC_INIT_JOB_NAME}")
    except client.rest.ApiException as e:
        if e.status == 409:
            logger.info(f"Job {PVC_INIT_JOB_NAME} already exists")
        else:
            raise


def _wait_for_pvc_init_job(batch_v1, timeout=600):
    import time

    start = time.time()
    while time.time() - start < timeout:
        job_status = batch_v1.read_namespaced_job_status(
            name=PVC_INIT_JOB_NAME, namespace=KSERVE_TEST_NAMESPACE
        )
        if job_status.status.succeeded and job_status.status.succeeded >= 1:
            logger.info(f"Job {PVC_INIT_JOB_NAME} completed successfully")
            return
        if job_status.status.failed and job_status.status.failed >= 2:
            raise RuntimeError(
                f"Job {PVC_INIT_JOB_NAME} failed after "
                f"{job_status.status.failed} attempts"
            )
        time.sleep(10)

    raise TimeoutError(f"Job {PVC_INIT_JOB_NAME} did not complete within {timeout}s")
