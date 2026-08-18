package retriever

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

var (
	errInvalidConfig = errors.New("invalid retriever configuration")
	errInvalidQuery  = errors.New("invalid retriever query")
)

type Config struct {
	Documents []Document `json:"documents"`
	TopK      int        `json:"topK"`
}

type Document struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type Match struct {
	ID    string  `json:"id"`
	Text  string  `json:"text"`
	Score float64 `json:"score"`
}

type Node struct{}

func Register(registrar agentnode.Registrar) error {
	return registrar.Register(Node{})
}

func (Node) Definition() agentnode.Definition {
	return agentnode.Definition{
		Type:        "extension.retriever",
		Version:     "1.0.0",
		Title:       "Retriever",
		Description: "使用本地 Jaccard 相似度检索配置文档",
		Category:    "扩展",
		ConfigSchema: agentnode.MustSchema(`{
          "$schema":"https://json-schema.org/draft/2020-12/schema",
          "type":"object",
          "properties":{
            "documents":{
              "type":"array","title":"文档","minItems":1,"maxItems":1000,
              "items":{
                "type":"object",
                "properties":{
                  "id":{"type":"string","title":"文档标识","minLength":1,"maxLength":128},
                  "text":{"type":"string","title":"文档内容","minLength":1,"maxLength":65536,"x-ui-widget":"textarea"}
                },
                "required":["id","text"],"additionalProperties":false,
                "x-ui-order":["id","text"]
              }
            },
            "topK":{"type":"integer","title":"返回数量","minimum":1,"maximum":100,"default":3}
          },
          "required":["documents","topK"],
          "additionalProperties":false,
          "x-ui-order":["documents","topK"]
        }`),
		Inputs: []agentnode.Port{{
			Key: "query", Title: "查询", Type: agentnode.DataTypeString,
			Required: true, Cardinality: agentnode.CardinalityOne,
		}},
		Outputs: []agentnode.Port{{
			Key: "matches", Title: "匹配结果", Type: agentnode.DataTypeJSON,
			Cardinality: agentnode.CardinalityOne,
		}},
	}
}

func (node Node) Resolve(config json.RawMessage) (agentnode.ResolvedPorts, error) {
	if _, err := parseConfig(config); err != nil {
		return agentnode.ResolvedPorts{}, err
	}
	definition := node.Definition()
	return agentnode.ResolvedPorts{Inputs: definition.Inputs, Outputs: definition.Outputs}, nil
}

func (Node) Execute(ctx context.Context, request agentnode.Request) (agentnode.Result, error) {
	if err := ctx.Err(); err != nil {
		return agentnode.Result{}, canceledError(err)
	}
	config, err := parseConfig(request.Config)
	if err != nil {
		return agentnode.Result{}, err
	}
	values := request.Inputs["query"]
	if len(values) != 1 {
		return agentnode.Result{}, invalidQueryError()
	}
	query, ok := values[0].(string)
	if !ok {
		return agentnode.Result{}, invalidQueryError()
	}
	queryTokens := tokenize(query)
	if len(queryTokens) == 0 {
		return agentnode.Result{}, invalidQueryError()
	}

	type scoredMatch struct {
		match Match
		raw   float64
	}
	scored := make([]scoredMatch, 0, len(config.Documents))
	for _, document := range config.Documents {
		if err := ctx.Err(); err != nil {
			return agentnode.Result{}, canceledError(err)
		}
		raw := jaccard(queryTokens, tokenize(document.Text))
		scored = append(scored, scoredMatch{
			match: Match{
				ID:    document.ID,
				Text:  document.Text,
				Score: math.Round(raw*1_000_000) / 1_000_000,
			},
			raw: raw,
		})
	}
	sort.SliceStable(scored, func(left, right int) bool {
		return scored[left].raw > scored[right].raw
	})
	limit := min(config.TopK, len(scored))
	matches := make([]Match, limit)
	for index := range limit {
		matches[index] = scored[index].match
	}
	return agentnode.Result{Outputs: map[string]any{"matches": matches}}, nil
}

func parseConfig(raw json.RawMessage) (Config, error) {
	var config Config
	if err := agentnode.DecodeConfig(raw, &config); err != nil {
		return Config{}, err
	}
	if len(config.Documents) < 1 || len(config.Documents) > 1000 || config.TopK < 1 || config.TopK > 100 {
		return Config{}, configError()
	}
	seen := make(map[string]struct{}, len(config.Documents))
	for _, document := range config.Documents {
		normalizedID := strings.TrimSpace(document.ID)
		if normalizedID == "" || utf8.RuneCountInString(document.ID) > 128 {
			return Config{}, configError()
		}
		if _, exists := seen[normalizedID]; exists {
			return Config{}, configError()
		}
		seen[normalizedID] = struct{}{}
		if strings.TrimSpace(document.Text) == "" || utf8.RuneCountInString(document.Text) > 65536 || len(tokenize(document.Text)) == 0 {
			return Config{}, configError()
		}
	}
	return config, nil
}

func tokenize(value string) map[string]struct{} {
	parts := strings.FieldsFunc(strings.ToLower(value), func(character rune) bool {
		return !unicode.IsLetter(character) && !unicode.IsNumber(character)
	})
	tokens := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		if part != "" {
			tokens[part] = struct{}{}
		}
	}
	return tokens
}

func jaccard(left, right map[string]struct{}) float64 {
	intersection := 0
	union := make(map[string]struct{}, len(left)+len(right))
	for token := range left {
		union[token] = struct{}{}
		if _, exists := right[token]; exists {
			intersection++
		}
	}
	for token := range right {
		union[token] = struct{}{}
	}
	return float64(intersection) / float64(len(union))
}

func configError() error {
	return agentnode.NewError(agentnode.ErrorKindConfig, "invalid_config", errInvalidConfig, nil)
}

func invalidQueryError() error {
	return agentnode.NewError(agentnode.ErrorKindInput, "invalid_query", errInvalidQuery, nil)
}

func canceledError(err error) error {
	return agentnode.NewError(agentnode.ErrorKindCanceled, "run_canceled", err, nil)
}

var _ agentnode.Node = Node{}
