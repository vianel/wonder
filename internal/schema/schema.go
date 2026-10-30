// Package schema extracts a JSON object from agent output and validates it
// against one of the embedded public schemas.
package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	wonder "github.com/vianel/wonder"
)

// Schema names, matching schemas/<name>.v1.json.
const (
	Spec   = "spec"
	Worker = "worker"
	Review = "review"
	Judge  = "judge"
)

var (
	mu       sync.Mutex
	compiled = map[string]*jsonschema.Schema{}
)

func load(name string) (*jsonschema.Schema, error) {
	mu.Lock()
	defer mu.Unlock()
	if s, ok := compiled[name]; ok {
		return s, nil
	}
	file := "schemas/" + name + ".v1.json"
	f, err := wonder.Assets.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	doc, err := jsonschema.UnmarshalJSON(f)
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(file, doc); err != nil {
		return nil, err
	}
	s, err := c.Compile(file)
	if err != nil {
		return nil, err
	}
	compiled[name] = s
	return s, nil
}

var fence = regexp.MustCompile("(?s)```(?:json)?\\s*\n(.*?)```")

// Extract finds the JSON object in an agent's final message: the whole text,
// else the last fenced block, else the last balanced {...}.
func Extract(text string) (string, error) {
	t := strings.TrimSpace(text)
	if json.Valid([]byte(t)) && strings.HasPrefix(t, "{") {
		return t, nil
	}
	if m := fence.FindAllStringSubmatch(t, -1); len(m) > 0 {
		c := strings.TrimSpace(m[len(m)-1][1])
		if json.Valid([]byte(c)) {
			return c, nil
		}
	}
	// Scan backwards for the last '}' and its matching '{'.
	for end := strings.LastIndex(t, "}"); end >= 0; end = strings.LastIndex(t[:end], "}") {
		for start := strings.LastIndex(t[:end], "{"); start >= 0; start = strings.LastIndex(t[:start], "{") {
			if c := t[start : end+1]; json.Valid([]byte(c)) {
				return c, nil
			}
		}
	}
	return "", errors.New("no JSON object found in output")
}

// Parse extracts the JSON object from text and validates it against schema name.
func Parse(name, text string) (json.RawMessage, error) {
	s, err := load(name)
	if err != nil {
		return nil, err
	}
	raw, err := Extract(text)
	if err != nil {
		return nil, err
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(raw))
	if err != nil {
		return nil, err
	}
	if err := s.Validate(inst); err != nil {
		return nil, fmt.Errorf("output does not match the %s schema: %v", name, err)
	}
	return json.RawMessage(raw), nil
}
