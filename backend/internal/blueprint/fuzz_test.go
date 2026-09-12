package blueprint

import (
	"encoding/json"
	"strings"
	"testing"
)

// A blueprint document arrives as bytes. Anything Parse accepts is something a
// reviewer's rules must already hold for, because the catalogue loader parses
// with exactly this function.
func FuzzParseBlueprint(f *testing.F) {
	entries, err := Catalog()
	if err != nil {
		f.Fatal(err)
	}
	for _, entry := range entries {
		encoded, marshalErr := json.Marshal(entry)
		if marshalErr != nil {
			f.Fatal(marshalErr)
		}
		f.Add(string(encoded))
	}
	f.Add(`{}`)
	f.Add(`{"id":"x","version":"1.0.0","unknown":true}`)
	f.Add(`{"id":"../escape","version":"1.0.0"}`)

	f.Fuzz(func(t *testing.T, document string) {
		parsed, parseErr := Parse([]byte(document))
		if parseErr != nil {
			return
		}
		// Anything that parsed also validated, so every supply-chain rule holds.
		if err := Validate(parsed); err != nil {
			t.Fatalf("Parse accepted a blueprint Validate rejects: %v", err)
		}
		if !identifierRE.MatchString(parsed.ID) {
			t.Fatalf("accepted id %q", parsed.ID)
		}
		for _, volume := range parsed.Volumes {
			if !strings.HasPrefix(volume.Target, "/") || strings.Contains(volume.Target, "..") {
				t.Fatalf("accepted volume target %q", volume.Target)
			}
		}
		for _, list := range [][]Operation{
			parsed.Operations.Install, parsed.Operations.Release,
			parsed.Operations.Startup, parsed.Operations.Stop,
		} {
			for _, operation := range list {
				switch operation.Kind {
				case OperationSetVariable, OperationWriteFile, OperationConsole,
					OperationStopSignal, OperationWaitForLog, OperationFetchArtifact:
				default:
					t.Fatalf("accepted operation kind %q", operation.Kind)
				}
				if operation.Kind == OperationFetchArtifact && !strings.HasPrefix(operation.URL, "https://") {
					t.Fatalf("accepted a fetch from %q", operation.URL)
				}
			}
		}
		if parsed.Profile == ProfileDatabase {
			for _, port := range parsed.Ports {
				if port.Exposure != "internal" {
					t.Fatalf("accepted a database exposing %q", port.Exposure)
				}
			}
		}
	})
}

// Rendering must stay pure and secret-free whatever is handed to it. A value
// that reaches a plan unvalidated is a value that reaches a container.
func FuzzRenderMinecraftInputs(f *testing.F) {
	for _, seed := range []string{
		"true", "false", "VANILLA", "PAPER", "1.21.4", "LATEST", "2048",
		"$(id)", "`id`", "a\nb", "a\x00b", strings.Repeat("x", 5000), "",
	} {
		f.Add(seed, seed)
	}
	f.Fuzz(func(t *testing.T, version, name string) {
		definition, err := Get("minecraft-java")
		if err != nil {
			t.Fatal(err)
		}
		inputs := map[string]string{"eula": "true", "version": version, "server-name": name}
		plan, renderErr := Render(definition, inputs)
		if renderErr != nil {
			return
		}
		for _, variable := range plan.Variables {
			if strings.ContainsAny(variable.Value, "\x00\r\n") {
				t.Fatalf("%s carries a control character: %q", variable.Name, variable.Value)
			}
			if variable.Generated && variable.Value != "" {
				t.Fatalf("rendered a value for generated secret %s", variable.Name)
			}
		}
		// Determinism holds for accepted input too, not only for the fixtures.
		again, againErr := Render(definition, inputs)
		if againErr != nil || again.Digest != plan.Digest {
			t.Fatalf("rendering twice gave %q then %q (%v)", plan.Digest, again.Digest, againErr)
		}
	})
}
