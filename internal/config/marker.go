package config

// MarkerFile is the Managed file committed into every Target repo.
const MarkerFile = ".fastci.json"

// Marker records how a Target repo was produced. It is provenance for whoever
// reads the repo, so it holds nothing machine-specific: no absolute paths, no
// credentials.
type Marker struct {
	ToolVersion     string `json:"fastci_version"`
	TemplateVersion int    `json:"template_version"`
	WorkspaceCommit string `json:"workspace_commit"`
	WorkspaceOrigin string `json:"workspace_origin,omitempty"`
	Fingerprint     string `json:"fingerprint"`
	Image           string `json:"image"`
	Dockerfile      string `json:"dockerfile"`
}
