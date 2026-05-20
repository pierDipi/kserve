/*
Copyright 2025 The KServe Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package llmisvc

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/kserve/kserve/pkg/constants"
)

func TestAttachPVCModelArtifact(t *testing.T) {
	tests := []struct {
		name             string
		modelUri         string
		wantPVCName      string
		wantSubPath      string
		wantModelURI     string
		wantHFHome       string
		wantHFHubOffline string
		wantMountPath    string
	}{
		{
			name:          "claim with subpath",
			modelUri:      "pvc://facebook-models/opt-125m",
			wantPVCName:   "facebook-models",
			wantSubPath:   "opt-125m",
			wantMountPath: constants.DefaultModelLocalMountPath,
		},
		{
			name:          "claim only",
			modelUri:      "pvc://myclaim",
			wantPVCName:   "myclaim",
			wantSubPath:   "",
			wantMountPath: constants.DefaultModelLocalMountPath,
		},
		{
			name:          "claim with nested subpath",
			modelUri:      "pvc://myclaim/models/v1",
			wantPVCName:   "myclaim",
			wantSubPath:   "models/v1",
			wantMountPath: constants.DefaultModelLocalMountPath,
		},
		{
			name:             "claim with model query parameter sets HF cache env vars",
			modelUri:         "pvc://facebook-models/opt-125m?model=facebook/opt-125m",
			wantPVCName:      "facebook-models",
			wantSubPath:      "opt-125m",
			wantModelURI:     "facebook/opt-125m",
			wantHFHome:       constants.DefaultModelLocalMountPath,
			wantHFHubOffline: "1",
			wantMountPath:    constants.DefaultModelLocalMountPath,
		},
		{
			name:          "claim without model query parameter does not set HF cache env vars",
			modelUri:      "pvc://myclaim/path",
			wantPVCName:   "myclaim",
			wantSubPath:   "path",
			wantModelURI:  "",
			wantMountPath: constants.DefaultModelLocalMountPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			podSpec := &corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "main"},
				},
			}

			r := &LLMISVCReconciler{}
			err := r.attachPVCModelArtifact(tt.modelUri, podSpec, "main", constants.DefaultModelLocalMountPath)
			if err != nil {
				t.Fatalf("attachPVCModelArtifact() returned unexpected error: %v", err)
			}

			// Validate PVC volume
			var pvcVolume *corev1.Volume
			for i := range podSpec.Volumes {
				if podSpec.Volumes[i].Name == constants.PvcSourceMountName {
					pvcVolume = &podSpec.Volumes[i]
					break
				}
			}
			if pvcVolume == nil {
				t.Fatal("expected PVC volume not found")
			}
			if pvcVolume.PersistentVolumeClaim == nil {
				t.Fatal("expected PersistentVolumeClaim volume source, got nil")
			}
			if got := pvcVolume.PersistentVolumeClaim.ClaimName; got != tt.wantPVCName {
				t.Errorf("PVC ClaimName = %q, want %q", got, tt.wantPVCName)
			}

			// Validate volume mount on the main container
			mainContainer := podSpec.Containers[0]
			var mount *corev1.VolumeMount
			for i := range mainContainer.VolumeMounts {
				if mainContainer.VolumeMounts[i].Name == constants.PvcSourceMountName {
					mount = &mainContainer.VolumeMounts[i]
					break
				}
			}
			if mount == nil {
				t.Fatal("expected volume mount not found on main container")
			}
			if mount.SubPath != tt.wantSubPath {
				t.Errorf("SubPath = %q, want %q", mount.SubPath, tt.wantSubPath)
			}
			if mount.MountPath != tt.wantMountPath {
				t.Errorf("MountPath = %q, want %q", mount.MountPath, tt.wantMountPath)
			}
			if !mount.ReadOnly {
				t.Error("expected ReadOnly = true")
			}

			// Validate HF cache env vars (MODEL_URI, HF_HOME, HF_HUB_OFFLINE)
			envMap := map[string]string{}
			for _, env := range mainContainer.Env {
				envMap[env.Name] = env.Value
			}
			if got := envMap["MODEL_URI"]; got != tt.wantModelURI {
				t.Errorf("MODEL_URI env var = %q, want %q", got, tt.wantModelURI)
			}
			if got := envMap["HF_HOME"]; got != tt.wantHFHome {
				t.Errorf("HF_HOME env var = %q, want %q", got, tt.wantHFHome)
			}
			if got := envMap["HF_HUB_OFFLINE"]; got != tt.wantHFHubOffline {
				t.Errorf("HF_HUB_OFFLINE env var = %q, want %q", got, tt.wantHFHubOffline)
			}
		})
	}
}