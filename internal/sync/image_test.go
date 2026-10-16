package sync

import (
	"slices"
	"testing"
)

func TestImageRefs(t *testing.T) {
	for _, tc := range []struct {
		name string
		tags []string
		want []string
	}{
		{
			name: "no configured tags means latest",
			want: []string{"ghcr.io/o/app:latest", "ghcr.io/o/app:sha-1a2b3c4"},
		},
		{
			// The user's tags replace latest; the sha tag is always appended,
			// because it is what names the Workspace commit (ADR-0004).
			name: "configured tags replace latest",
			tags: []string{"v1", "stable"},
			want: []string{"ghcr.io/o/app:v1", "ghcr.io/o/app:stable", "ghcr.io/o/app:sha-1a2b3c4"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ImageRefs("ghcr.io/o/app", tc.tags, "1a2b3c4")
			if !slices.Equal(got, tc.want) {
				t.Errorf("ImageRefs = %v, want %v", got, tc.want)
			}
		})
	}
}
