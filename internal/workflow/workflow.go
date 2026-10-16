// Package workflow renders the Managed workflow file FastCI puts in a Target
// repo. The template is baked into the binary and is not user-overridable.
package workflow

import (
	"strings"
	"text/template"
)

// TemplateVersion identifies the rendered shape. The marker file records it so
// a reader can tell which tool version produced a Target repo.
const TemplateVersion = 1

// Path is where the rendered workflow lives in a Target repo.
func Path(name string) string { return ".github/workflows/" + name }

// Params is everything the template interpolates. Username and Password are
// already turned into GitHub expressions by the caller (either the run's
// GITHUB_TOKEN or repo secrets).
type Params struct {
	Host       string
	Image      string
	Dockerfile string
	Tags       []string
	Username   string
	Password   string
}

// Render fills in the template. GitHub expressions like ${{ ... }} are why the
// delimiters are not the default {{ }}.
func Render(p Params) (string, error) {
	t, err := template.New("workflow").Delims("<%", "%>").Parse(templateText)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	if err := t.Execute(&out, p); err != nil {
		return "", err
	}
	return out.String(), nil
}

const templateText = `name: fastci-build

on:
  push:
    branches: [main]
  workflow_dispatch:
    inputs:
      tag:
        description: Extra image tag for this run
        required: false
        type: string

permissions:
  contents: read
  packages: write

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - name: Check out
        uses: actions/checkout@v4

      - name: Set up Buildx
        uses: docker/setup-buildx-action@v3

      - name: Log in to <% .Host %>
        uses: docker/login-action@v3
        with:
          registry: <% .Host %>
          username: <% .Username %>
          password: <% .Password %>

      - name: Compose image tags
        id: tags
        run: |
          set -eu
          {
            echo "tags<<FASTCI_EOF"
<% range .Tags %>            echo "<% . %>"
<% end %>            if [ -n "${{ inputs.tag }}" ]; then echo "<% .Image %>:${{ inputs.tag }}"; fi
            echo "FASTCI_EOF"
          } >> "$GITHUB_OUTPUT"

      - name: Build and push
        uses: docker/build-push-action@v6
        with:
          context: .
          file: <% .Dockerfile %>
          platforms: linux/amd64
          push: true
          tags: ${{ steps.tags.outputs.tags }}
`
