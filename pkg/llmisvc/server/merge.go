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

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func MergeModelsResponses(responses []BackendResponse) (any, int, error) {
	return mergeModels(responses, nil)
}

func MergeModelsWithPrefixes(prefixes ...string) MergeFunc {
	return func(responses []BackendResponse) (any, int, error) {
		return mergeModels(responses, prefixes)
	}
}

func mergeModels(responses []BackendResponse, prefixes []string) (any, int, error) {
	merged := ListModelsResponse{
		Object: "list",
		Data:   []json.RawMessage{},
	}

	for _, resp := range responses {
		if resp.Err != nil || resp.Status >= 400 {
			continue
		}
		var lr ListModelsResponse
		if err := json.Unmarshal(resp.Body, &lr); err != nil {
			continue
		}
		for _, raw := range lr.Data {
			if len(prefixes) > 0 && !modelIDMatchesPrefixes(raw, prefixes) {
				continue
			}
			patched, err := patchOwnedBy(raw, resp.Backend.Name+"/"+resp.Backend.Namespace)
			if err != nil {
				continue
			}
			merged.Data = append(merged.Data, patched)
		}
	}

	return merged, http.StatusOK, nil
}

func modelIDMatchesPrefixes(raw json.RawMessage, prefixes []string) bool {
	var partial struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &partial); err != nil {
		return false
	}
	for _, p := range prefixes {
		if strings.HasPrefix(partial.ID, p) {
			return true
		}
	}
	return false
}

func patchOwnedBy(raw json.RawMessage, owner string) (json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	ownerJSON, err := json.Marshal(owner)
	if err != nil {
		return nil, err
	}
	m["owned_by"] = ownerJSON
	return json.Marshal(m)
}

func MergeHealthResponses(responses []BackendResponse) (any, int, error) {
	hs := HealthStatus{
		Status:   "healthy",
		Backends: make([]BackendHealthStatus, 0, len(responses)),
	}

	allHealthy := true
	for _, resp := range responses {
		bhs := BackendHealthStatus{
			Name:      resp.Backend.Name,
			Namespace: resp.Backend.Namespace,
		}
		switch {
		case resp.Err != nil:
			bhs.Status = "unreachable"
			bhs.Error = resp.Err.Error()
			allHealthy = false
		case resp.Status >= 400:
			bhs.Status = "unhealthy"
			bhs.Error = fmt.Sprintf("status code %d", resp.Status)
			allHealthy = false
		default:
			bhs.Status = "healthy"
		}
		hs.Backends = append(hs.Backends, bhs)
	}

	if !allHealthy {
		hs.Status = "unhealthy"
		return hs, http.StatusServiceUnavailable, nil
	}

	return hs, http.StatusOK, nil
}

func MergeMetricsResponses(responses []BackendResponse) (any, int, error) {
	var sb strings.Builder
	for _, resp := range responses {
		if resp.Err != nil || resp.Status >= 400 {
			continue
		}
		lines := strings.Split(string(resp.Body), "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if strings.HasPrefix(trimmed, "#") {
				sb.WriteString(trimmed)
				sb.WriteString("\n")
				continue
			}
			labeled := addBackendLabel(trimmed, resp.Backend.Name)
			sb.WriteString(labeled)
			sb.WriteString("\n")
		}
	}
	return sb.String(), http.StatusOK, nil
}

func addBackendLabel(metricLine, backendName string) string {
	braceOpen := strings.IndexByte(metricLine, '{')
	braceClose := strings.IndexByte(metricLine, '}')
	label := fmt.Sprintf("backend=%q", backendName)

	if braceOpen >= 0 && braceClose > braceOpen {
		existing := metricLine[braceOpen+1 : braceClose]
		if existing == "" {
			return metricLine[:braceOpen+1] + label + metricLine[braceClose:]
		}
		return metricLine[:braceClose] + "," + label + metricLine[braceClose:]
	}

	spaceIdx := strings.IndexByte(metricLine, ' ')
	if spaceIdx < 0 {
		return metricLine
	}
	return metricLine[:spaceIdx] + "{" + label + "}" + metricLine[spaceIdx:]
}

func MergeLoadResponses(responses []BackendResponse) (any, int, error) {
	type backendLoad struct {
		Backend string          `json:"backend"`
		Load    json.RawMessage `json:"load"`
		Error   string          `json:"error,omitempty"`
	}
	result := struct {
		Backends []backendLoad `json:"backends"`
	}{
		Backends: make([]backendLoad, 0, len(responses)),
	}

	for _, resp := range responses {
		bl := backendLoad{
			Backend: resp.Backend.Name + "/" + resp.Backend.Namespace,
		}
		if resp.Err != nil {
			bl.Error = resp.Err.Error()
		} else if resp.Status >= 400 {
			bl.Error = fmt.Sprintf("status code %d", resp.Status)
		} else {
			bl.Load = json.RawMessage(resp.Body)
		}
		result.Backends = append(result.Backends, bl)
	}

	return result, http.StatusOK, nil
}
