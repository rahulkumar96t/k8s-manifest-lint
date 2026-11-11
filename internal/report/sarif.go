package report

import (
	"io"
	"strings"

	"github.com/rahulkumar96t/k8s-manifest-lint/internal/lint"
)

const (
	sarifSchema = "https://json.schemastore.org/sarif-2.1.0.json"
	toolName    = "k8s-manifest-lint"
	toolURI     = "https://github.com/rahulkumar96t/k8s-manifest-lint"
)

// The types below cover the part of SARIF 2.1.0 that GitHub code scanning
// reads: tool.driver.rules for rule metadata and results with a physical
// location (file + line).
type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	Version        string      `json:"version,omitempty"`
	InformationURI string      `json:"informationUri"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID                   string      `json:"id"`
	Name                 string      `json:"name"`
	ShortDescription     sarifText   `json:"shortDescription"`
	FullDescription      sarifText   `json:"fullDescription"`
	Help                 sarifText   `json:"help"`
	HelpURI              string      `json:"helpUri"`
	DefaultConfiguration sarifConfig `json:"defaultConfiguration"`
	Properties           sarifProps  `json:"properties"`
}

type sarifText struct {
	Text string `json:"text"`
}

type sarifConfig struct {
	Level string `json:"level"`
}

type sarifProps struct {
	Tags []string `json:"tags"`
}

type sarifResult struct {
	RuleID    string          `json:"ruleId"`
	RuleIndex int             `json:"ruleIndex"`
	Level     string          `json:"level"`
	Message   sarifText       `json:"message"`
	Locations []sarifLocation `json:"locations"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysical  `json:"physicalLocation"`
	LogicalLocations []sarifLogical `json:"logicalLocations,omitempty"`
}

type sarifPhysical struct {
	ArtifactLocation sarifArtifact `json:"artifactLocation"`
	Region           *sarifRegion  `json:"region,omitempty"`
}

type sarifArtifact struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	StartLine   int `json:"startLine"`
	StartColumn int `json:"startColumn,omitempty"`
}

type sarifLogical struct {
	FullyQualifiedName string `json:"fullyQualifiedName"`
	Kind               string `json:"kind"`
}

func sarifLevel(s lint.Severity) string {
	switch s {
	case lint.Error:
		return "error"
	case lint.Warning:
		return "warning"
	}
	return "note"
}

// SARIF writes a SARIF 2.1.0 log with one run. Every enabled rule is listed
// in the driver so code scanning can show rule help even for clean runs.
func SARIF(w io.Writer, res *lint.Result, version string) error {
	run := sarifRun{
		Tool:    sarifTool{Driver: sarifDriver{Name: toolName, Version: version, InformationURI: toolURI, Rules: []sarifRule{}}},
		Results: []sarifResult{},
	}
	index := map[string]int{}
	for i, r := range res.Rules {
		index[r.ID] = i
		run.Tool.Driver.Rules = append(run.Tool.Driver.Rules, sarifRule{
			ID:               r.ID,
			Name:             pascal(r.ID),
			ShortDescription: sarifText{r.Summary},
			FullDescription:  sarifText{r.Rationale},
			Help: sarifText{r.Summary + " " + r.Rationale +
				" Suppress for one object with the annotation " + lint.IgnoreAnnotation + `: "` + r.ID + `".`},
			HelpURI:              toolURI + "#rules",
			DefaultConfiguration: sarifConfig{Level: sarifLevel(r.Severity)},
			Properties:           sarifProps{Tags: []string{"kubernetes", r.Category}},
		})
	}
	for _, f := range res.Findings {
		loc := sarifLocation{
			PhysicalLocation: sarifPhysical{ArtifactLocation: sarifArtifact{URI: artifactURI(f.File)}},
			LogicalLocations: []sarifLogical{{FullyQualifiedName: f.Object.String(), Kind: "resource"}},
		}
		if f.Line > 0 {
			loc.PhysicalLocation.Region = &sarifRegion{StartLine: f.Line, StartColumn: f.Column}
		}
		run.Results = append(run.Results, sarifResult{
			RuleID: f.RuleID, RuleIndex: index[f.RuleID], Level: sarifLevel(f.Severity),
			Message:   sarifText{f.Message + " (" + f.Object.String() + ")"},
			Locations: []sarifLocation{loc},
		})
	}
	return encode(w, sarifLog{Schema: sarifSchema, Version: "2.1.0", Runs: []sarifRun{run}})
}

// pascal turns "image-tag" into "ImageTag" for the SARIF rule name.
func pascal(id string) string {
	var b strings.Builder
	for _, part := range strings.Split(id, "-") {
		if part != "" {
			b.WriteString(strings.ToUpper(part[:1]) + part[1:])
		}
	}
	return b.String()
}
