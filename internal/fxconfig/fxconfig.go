// Package fxconfig decides which fx settings the desktop may write with
// `fx config set` and `fx config unset`. It does not read or write the
// settings file itself. Permission, model, provider and account keys stay
// on their own commands, and a deprecated or refused key is not treated as
// writable just because the schema still lists it.
package fxconfig

import (
	"fmt"
	"strconv"
	"strings"
)

type kind int

const (
	kindBool kind = iota
	kindInt
	kindEnum
	kindString
)

type field struct {
	kind kind
	min  int
	max  int
	has  bool
	enum []string
}

// writable is the plain preferences `fx config set` accepts. Dotted keys are
// the children fx documents, such as status_line.context.
var writable = map[string]field{
	"auto_compact_percent":     {kind: kindInt, min: 10, max: 80, has: true},
	"auto_upgrade":             {kind: kindBool},
	"cache_warming":            {kind: kindBool},
	"collapse_tool_calls":      {kind: kindBool},
	"context":                  {kind: kindBool},
	"effort":                   {kind: kindString},
	"fast_mode":                {kind: kindBool},
	"first_call_tool_choice":   {kind: kindEnum, enum: []string{"auto", "none"}},
	"max_agent_steps":          {kind: kindInt, min: 0, has: true},
	"max_tool_result_bytes":    {kind: kindInt, min: 1024, has: true},
	"prompt_history.enabled":   {kind: kindBool},
	"provider_strict":          {kind: kindBool},
	"session_titles":           {kind: kindBool},
	"slash_menu_categories":    {kind: kindBool},
	"sound.attention_required": {kind: kindBool},
	"sound.max":                {kind: kindBool},
	"sound.turn_end":           {kind: kindBool},
	"startup_scrollback":       {kind: kindBool},
	"status_line.context":      {kind: kindBool},
	"status_line.session":      {kind: kindBool},
	"status_line.workspace":    {kind: kindBool},
	"theme":                    {kind: kindString},
	"ultrafast_mode":           {kind: kindBool},
	"update_channel":           {kind: kindEnum, enum: []string{"stable", "dev"}},
	"yolo_acknowledged":        {kind: kindBool},
}

// reservedKeys are settings fx tells config set to leave alone, plus
// deprecated names and keys the schema refuses.
var reservedKeys = map[string]bool{
	"permission": true, "permission_mode": true,
	"model": true, "models": true, "provider": true, "providers": true,
	"credential_source": true, "review_model": true, "workspaces": true,
	"codex_model": true, "grok_model": true, "input_appearance": true,
	"maxxing_mode": true, "notifications": true, "sandbox": true,
	"statusLine": true, "web_fetch_worker_model": true,
	"skill_match_fuzzy": true, "fast_mode_model_bound": true,
	"$schema": true,
}

// SetArgs validates one preference and returns `config set` argv.
func SetArgs(key, value string) ([]string, error) {
	key, err := cleanKey(key)
	if err != nil {
		return nil, err
	}
	field, ok := writable[key]
	if !ok {
		return nil, fmt.Errorf("fx config cannot set %s", key)
	}
	value = strings.TrimSpace(value)
	if err := checkValue(field, value); err != nil {
		return nil, err
	}
	return []string{"config", "set", key, value}, nil
}

// UnsetArgs validates one preference and returns `config unset` argv.
func UnsetArgs(key string) ([]string, error) {
	key, err := cleanKey(key)
	if err != nil {
		return nil, err
	}
	if _, ok := writable[key]; !ok {
		return nil, fmt.Errorf("fx config cannot unset %s", key)
	}
	return []string{"config", "unset", key}, nil
}

func cleanKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" || strings.HasPrefix(key, "-") || strings.ContainsAny(key, "\x00\r\n ") {
		return "", fmt.Errorf("Enter a settings key")
	}
	if reservedKeys[key] || strings.HasPrefix(key, "permission.") || strings.HasPrefix(key, "providers.") || strings.HasPrefix(key, "models.") {
		return "", fmt.Errorf("fx config cannot change %s", key)
	}
	return key, nil
}

func checkValue(field field, value string) error {
	if value == "" || strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("Enter a value")
	}
	switch field.kind {
	case kindBool:
		if value != "true" && value != "false" {
			return fmt.Errorf("Enter true or false")
		}
	case kindInt:
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("Enter a whole number")
		}
		if field.has && (n < field.min || (field.max != 0 && n > field.max)) {
			return fmt.Errorf("Enter a value from %d to %d", field.min, field.max)
		}
	case kindEnum:
		for _, item := range field.enum {
			if value == item {
				return nil
			}
		}
		return fmt.Errorf("Enter one of %s", strings.Join(field.enum, ", "))
	}
	return nil
}
