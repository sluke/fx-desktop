// Package management owns the fx management requests: which actions exist,
// what each form value must look like, and which argv fx is given. It performs
// no I/O and knows nothing about windows or MyGo — the app supplies the form
// values and runs the command.
package management

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Form is the request form the command builders read. The management panel's
// own state embeds it, so the fields the user edits and the fields that are
// validated are the same ones, named once.
type Form struct {
	Provider, Name, Transport, Command, Arguments, URL, Directory   string
	Period, SessionID, Model, Ultra, LogPath, Channel, Effort, Fast string
	ContextLimits, AddDirs, ProviderOverride                        string
	Headers, Environment                                            string
	MCPProject, NoAdditionalDirs                                    bool
	ProviderDefault                                                 string
	Project                                                         string
}

// GitHubForm is the `fx pr` / `fx issue` request form.
type GitHubForm struct {
	Kind    string
	Context string
	Auto    bool
	Create  bool
}

func Command(action string, form Form) ([]string, bool, error) {
	value := func(s, label string) (string, error) {
		s = strings.TrimSpace(s)
		if s == "" || strings.HasPrefix(s, "-") || strings.ContainsAny(s, "\x00\r\n") {
			return "", fmt.Errorf("Enter a valid %s", label)
		}
		return s, nil
	}
	provider := strings.TrimSpace(form.Provider)
	switch action {
	case "login", "logout":
		if provider != "" && provider != "vercel" && provider != "codex" && provider != "grok" {
			return nil, false, fmt.Errorf("Choose default, vercel, codex or grok")
		}
		args := []string{action}
		if provider != "" {
			args = append(args, provider)
		}
		return args, action == "logout", nil
	case "setup", "teams":
		return []string{action}, false, nil
	case "mcp-list":
		return []string{"mcp", "list", "--json"}, false, nil
	case "mcp-show":
		name, err := value(form.Name, "server name")
		if err != nil {
			return nil, false, err
		}
		return []string{"mcp", "show", name, "--json"}, false, nil
	case "mcp-add-slack":
		return []string{"mcp", "add", "slack"}, true, nil
	case "mcp-approve-all":
		return []string{"mcp", "approve", "--all"}, true, nil
	case "mcp-add", "mcp-login", "mcp-logout", "mcp-remove", "mcp-approve", "mcp-reject":
		name, err := value(form.Name, "server name")
		if err != nil {
			return nil, false, err
		}
		switch action {
		case "mcp-add":
			args := []string{"mcp", "add"}
			if form.MCPProject {
				args = append(args, "--project")
			}
			if form.Transport == "http" {
				u, err := url.Parse(strings.TrimSpace(form.URL))
				if err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" {
					return nil, false, fmt.Errorf("Enter an http or https MCP URL")
				}
				headers, err := mcpPairs(form.Headers, "HTTP header")
				if err != nil {
					return nil, false, err
				}
				for _, header := range headers {
					args = append(args, "--header", header)
				}
				// fx 0.0.15+: `mcp add [--project] [--header K=V]... NAME URL`.
				return append(args, name, u.String()), false, nil
			}
			if form.Transport != "" && form.Transport != "stdio" {
				return nil, false, fmt.Errorf("Unsupported MCP transport")
			}
			command, err := value(form.Command, "server executable")
			if err != nil {
				return nil, false, err
			}
			env, err := mcpPairs(form.Environment, "environment variable")
			if err != nil {
				return nil, false, err
			}
			for _, item := range env {
				args = append(args, "--env", item)
			}
			// fx 0.0.15+: stdio arguments follow the `--` separator.
			args = append(args, name, "--", command)
			for _, arg := range strings.Split(strings.ReplaceAll(form.Arguments, "\r\n", "\n"), "\n") {
				if arg != "" {
					if strings.ContainsRune(arg, 0) {
						return nil, false, fmt.Errorf("Arguments cannot contain NUL")
					}
					args = append(args, arg)
				}
			}
			return args, false, nil
		case "mcp-approve":
			return []string{"mcp", "approve", name}, true, nil
		case "mcp-reject":
			return []string{"mcp", "reject", name}, true, nil
		case "mcp-login":
			return []string{"mcp", "login", name}, false, nil
		case "mcp-logout":
			return []string{"mcp", "logout", name}, true, nil
		default:
			return []string{"mcp", "remove", name}, true, nil
		}
	case "provider-set":
		name, err := value(form.ProviderDefault, "provider name")
		if err != nil {
			return nil, false, err
		}
		return []string{"provider", name}, true, nil
	case "upgrade-channel":
		channel := strings.TrimSpace(form.Channel)
		if channel != "stable" && channel != "dev" {
			return nil, false, fmt.Errorf("Choose stable or dev")
		}
		return []string{"upgrade", "--channel", channel}, true, nil
	case "models":
		return []string{"models", "--json"}, false, nil
	case "doctor":
		return []string{"doctor", "--json"}, false, nil
	case "credits":
		return []string{"credits", "--json"}, false, nil
	case "slack-status":
		return []string{"slack", "status", "--json"}, false, nil
	case "slack-install":
		return []string{"slack", "install", "--json"}, true, nil
	case "slack-refresh":
		return []string{"slack", "refresh", "--json"}, true, nil
	case "sessions-convert":
		// fx 0.0.15 removed --sessions-v2; conversion is now the default
		// store and `sessions convert` takes no global store flag.
		return []string{"sessions", "convert", "--json"}, true, nil
	case "workspace-list":
		return []string{"workspace", "list", "--json"}, false, nil
	case "workspace-clear":
		return []string{"workspace", "clear"}, true, nil
	case "workspace-add", "workspace-remove":
		dir := strings.TrimSpace(form.Directory)
		if !filepath.IsAbs(dir) || strings.ContainsRune(dir, 0) {
			return nil, false, fmt.Errorf("Choose an absolute directory path")
		}
		if action == "workspace-add" {
			if st, err := os.Stat(dir); err != nil || !st.IsDir() {
				return nil, false, fmt.Errorf("The additional directory must exist")
			}
		}
		return []string{"workspace", strings.TrimPrefix(action, "workspace-"), filepath.Clean(dir)}, action == "workspace-remove", nil
	case "session-inspect", "session-recover":
		id, err := value(form.SessionID, "session id")
		if err != nil {
			return nil, false, err
		}
		if action == "session-inspect" {
			return []string{"session", "--id", id, "--json"}, false, nil
		}
		// fx 0.0.15 removed `session migrate`; recovery is still available.
		return []string{"session", "recover", "--id", id}, true, nil
	case "launch":
		if form.NoAdditionalDirs && strings.TrimSpace(form.AddDirs) != "" {
			return nil, false, fmt.Errorf("Choose additional directories or ignore saved ones, not both")
		}
		var args []string
		globals, err := launchGlobalArgs(form)
		if err != nil {
			return nil, false, err
		}
		args = append(args, globals...)
		if strings.TrimSpace(form.ProviderOverride) != "" {
			provider, err := value(form.ProviderOverride, "provider name")
			if err != nil {
				return nil, false, err
			}
			args = append(args, "--provider", provider)
		}
		if strings.TrimSpace(form.Model) != "" {
			model, err := value(form.Model, "model id")
			if err != nil {
				return nil, false, err
			}
			args = append(args, "--model", model)
		}
		if strings.TrimSpace(form.Effort) != "" {
			effort, err := value(form.Effort, "reasoning effort")
			if err != nil {
				return nil, false, err
			}
			args = append(args, "--effort", effort)
		}
		switch form.Fast {
		case "", "default":
		case "on":
			args = append(args, "--fast")
		case "off":
			args = append(args, "--no-fast")
		default:
			return nil, false, fmt.Errorf("Choose default, on or off for fast mode")
		}
		switch form.Ultra {
		case "", "default":
		case "on":
			args = append(args, "--ultrafast")
		case "off":
			args = append(args, "--no-ultrafast")
		default:
			return nil, false, fmt.Errorf("Choose default, on or off for Ultra mode")
		}
		if form.NoAdditionalDirs {
			args = append(args, "--no-additional-dirs")
		}
		if strings.TrimSpace(form.LogPath) != "" {
			path := strings.TrimSpace(form.LogPath)
			if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
				return nil, false, fmt.Errorf("ACP log path must be absolute")
			}
			args = append(args, "--log-file", filepath.Clean(path))
		}
		return args, true, nil
	}
	return nil, false, fmt.Errorf("Unsupported operation: %s", action)
}

// mcpPairs validates the repeatable K=V flags fx accepts for MCP headers and
// environment variables. Blank lines are ignored; each nonblank line must
// have a nonempty key. Values stay one argv entry, exactly as typed.
func mcpPairs(raw, label string) ([]string, error) {
	var pairs []string
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.ContainsRune(line, 0) {
			return nil, fmt.Errorf("%ss cannot contain NUL", label)
		}
		key, _, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("Each %s must use K=V", strings.ToLower(label))
		}
		pairs = append(pairs, line)
	}
	return pairs, nil
}

// launchGlobalArgs parses the flags fx accepts before its subcommand. Keeping
// them out of the ACP argv is what lets `fx --context-limit ... acp` start
// instead of rejecting the option as an ACP flag.
func launchGlobalArgs(form Form) ([]string, error) {
	return GlobalArgs(form.ContextLimits, form.AddDirs, form.Project)
}

func GlobalArgs(contextLimits, addDirs, project string) ([]string, error) {
	var args []string
	for _, line := range strings.Split(strings.ReplaceAll(contextLimits, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !ok || name == "" || value == "" {
			return nil, fmt.Errorf("Context limits use NAME=BYTES or NAME=off")
		}
		for _, r := range name {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' {
				return nil, fmt.Errorf("Context limit names use lowercase letters, digits and underscores")
			}
		}
		if value != "off" {
			if value == "" {
				return nil, fmt.Errorf("Context limit values must be a byte count or off")
			}
			for _, r := range value {
				if r < '0' || r > '9' {
					return nil, fmt.Errorf("Context limit values must be a byte count or off")
				}
			}
		}
		args = append(args, "--context-limit", name+"="+value)
	}
	for _, line := range strings.Split(strings.ReplaceAll(addDirs, "\r\n", "\n"), "\n") {
		dir := strings.TrimSpace(line)
		if dir == "" {
			continue
		}
		if !filepath.IsAbs(dir) || strings.ContainsRune(dir, 0) {
			return nil, fmt.Errorf("Additional directories must be absolute paths")
		}
		dir = filepath.Clean(dir)
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return nil, fmt.Errorf("Additional directory does not exist: %s", dir)
		}
		if project != "" && filepath.Clean(project) == dir {
			return nil, fmt.Errorf("The primary workspace cannot also be an additional directory")
		}
		args = append(args, "--add-dir", dir)
	}
	return args, nil
}

// githubCommand builds `fx pr` or `fx issue`. The context is a single argv
// entry, never a shell string. `--create` is the external publishing step, so
// the UI asks for confirmation before it runs.
func GitHub(form GitHubForm) ([]string, bool, error) {
	kind := strings.TrimSpace(form.Kind)
	if kind != "pr" && kind != "issue" {
		return nil, false, fmt.Errorf("Choose a pull request or an issue")
	}
	args := []string{kind}
	if form.Auto {
		args = append(args, "--auto")
	}
	if form.Create {
		args = append(args, "--create")
	}
	context := strings.TrimSpace(form.Context)
	if strings.ContainsRune(context, 0) {
		return nil, false, fmt.Errorf("Context cannot contain NUL")
	}
	if context != "" {
		// Keep a context that begins with a dash from becoming an fx flag.
		if strings.HasPrefix(context, "-") {
			args = append(args, "--")
		}
		args = append(args, context)
	}
	return args, form.Create, nil
}

func JobTimeout(action string) time.Duration {
	switch action {
	case "upgrade-channel":
		return 10 * time.Minute
	case "slack-install", "slack-refresh", "mcp-add-slack":
		return 5 * time.Minute
	case "sessions-convert", "session-recover":
		return 2 * time.Minute
	default:
		return 60 * time.Second
	}
}
