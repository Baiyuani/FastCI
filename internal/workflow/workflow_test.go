package workflow

import (
	"strings"
	"testing"
)

func params() Params {
	return Params{
		Host:       "ghcr.io",
		Image:      "ghcr.io/o/app",
		Dockerfile: "Dockerfile",
		Tags:       []string{"ghcr.io/o/app:latest", "ghcr.io/o/app:sha-abc1234"},
		Username:   "${{ github.actor }}",
		Password:   "${{ secrets.GITHUB_TOKEN }}",
	}
}

// TestRenderLeavesNoDelimitersBehind guards the one mistake the custom
// delimiters invite: a stray <% ... %> that reaches GitHub as literal YAML.
func TestRenderLeavesNoDelimitersBehind(t *testing.T) {
	out, err := Render(params())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "<%") || strings.Contains(out, "%>") {
		t.Errorf("the rendered workflow still holds template delimiters:\n%s", out)
	}
	if !strings.Contains(out, "${{ secrets.GITHUB_TOKEN }}") {
		t.Error("GitHub expressions must survive rendering")
	}
}

// TestRenderTagsStep is the block the build depends on: the tags have to land
// in GITHUB_OUTPUT as one line per tag, at the indentation the shell block
// needs. A slip here is a broken CI run, not a broken test run.
func TestRenderTagsStep(t *testing.T) {
	out, err := Render(params())
	if err != nil {
		t.Fatal(err)
	}
	want := `      - name: Compose image tags
        id: tags
        run: |
          set -eu
          {
            echo "tags<<FASTCI_EOF"
            echo "ghcr.io/o/app:latest"
            echo "ghcr.io/o/app:sha-abc1234"
            if [ -n "${{ inputs.tag }}" ]; then echo "ghcr.io/o/app:${{ inputs.tag }}"; fi
            echo "FASTCI_EOF"
          } >> "$GITHUB_OUTPUT"
`
	if !strings.Contains(out, want) {
		t.Errorf("the tags step is not the expected block:\n%s", out)
	}
}

func TestPath(t *testing.T) {
	if got := Path("fastci-build.yml"); got != ".github/workflows/fastci-build.yml" {
		t.Errorf("Path = %q", got)
	}
}
