package main

import "testing"

func TestParseReference(t *testing.T) {
	tests := []struct {
		name, input, registry, repo, tag string
	}{
		{"docker hub short", "nginx", "registry-1.docker.io", "library/nginx", "latest"},
		{"docker hub tag", "nginx:1.27", "registry-1.docker.io", "library/nginx", "1.27"},
		{"docker hub namespace", "acme/app:stable", "registry-1.docker.io", "acme/app", "stable"},
		{"explicit docker io", "docker.io/library/alpine:3", "registry-1.docker.io", "library/alpine", "3"},
		{"ghcr", "ghcr.io/acme/app:v2", "ghcr.io", "acme/app", "v2"},
		{"port registry", "registry.local:5000/team/app:tag", "registry.local:5000", "team/app", "tag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseReference(tt.input)
			if !ok || got.registry != tt.registry || got.repo != tt.repo || got.tag != tt.tag {
				t.Fatalf("parseReference(%q) = %#v, %v; want registry=%q repo=%q tag=%q", tt.input, got, ok, tt.registry, tt.repo, tt.tag)
			}
		})
	}
}

func TestParseReferenceRejectsDigestOnly(t *testing.T) {
	if _, ok := parseReference("ghcr.io/acme/app@sha256:abcd"); ok {
		t.Fatal("digest-pinned image should not be compared as a mutable tag")
	}
}

func TestImageTag(t *testing.T) {
	cases := map[string]string{"nginx": "latest", "nginx:1.27": "1.27", "ghcr.io/acme/app:stable": "stable", "acme/app@sha256:abc": "sha256:abc"}
	for input, want := range cases {
		if got := imageTag(input); got != want {
			t.Errorf("imageTag(%q) = %q, want %q", input, got, want)
		}
	}
}
