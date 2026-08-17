package domain

import "encoding/json"

type Graph struct {
	SchemaVersion int    `json:"schemaVersion"`
	Nodes         []Node `json:"nodes"`
	Edges         []Edge `json:"edges"`
}

type Node struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	TypeVersion string          `json:"typeVersion"`
	Position    Position        `json:"position"`
	Config      json.RawMessage `json:"config"`
}

type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type Edge struct {
	ID         string `json:"id"`
	Source     string `json:"source"`
	SourcePort string `json:"sourcePort"`
	Target     string `json:"target"`
	TargetPort string `json:"targetPort"`
}
