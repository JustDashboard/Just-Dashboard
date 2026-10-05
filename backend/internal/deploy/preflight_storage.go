package deploy

import (
	"strings"

	"gopkg.in/yaml.v3"
)

func writablePersistentStorageCount(draft *Draft, configuration PlanConfiguration) int {
	storage := map[string]bool{}
	for _, mount := range configuration.Runtime.Mounts {
		if !mount.ReadOnly {
			storage[mount.Source] = true
		}
	}
	if draft.Data.Source == nil || draft.Data.Source.Kind != SourceCompose {
		return len(storage)
	}
	for _, document := range draft.Data.Source.ComposeFiles {
		var model map[string]any
		if yaml.Unmarshal([]byte(document.Content), &model) != nil {
			continue
		}
		volumes := object(model["volumes"])
		for service, raw := range object(model["services"]) {
			entries, _ := object(raw)["volumes"].([]any)
			for _, raw := range entries {
				var source, target string
				if mount := object(raw); mount != nil {
					if mount["read_only"] == true || mount["type"] == "tmpfs" {
						continue
					}
					source, _ = mount["source"].(string)
					target, _ = mount["target"].(string)
				} else if text, ok := raw.(string); ok {
					parts := strings.Split(text, ":")
					if len(parts) > 2 && strings.Contains(","+parts[2]+",", ",ro,") {
						continue
					}
					if len(parts) > 1 {
						source, target = parts[0], parts[1]
					} else {
						target = text
					}
				}
				if actual, _ := object(volumes[source])["name"].(string); actual != "" {
					source = actual
				}
				if source == "" {
					source = service + ":" + target
				}
				if target != "" {
					storage[source] = true
				}
			}
		}
	}
	return len(storage)
}
