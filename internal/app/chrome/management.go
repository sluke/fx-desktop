package chrome

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"

	"fx-desktop/internal/management"
)

type ManagementState struct {
	// The request form is the management package's; the panel renders it and
	// the app validates the same fields.
	management.Form
	Open, Busy bool
	Page       string
	// BodyScroll is the tools body's scroll offset. The page switch resets
	// it, so a shorter page does not inherit the previous page's position.
	BodyScroll    ui.ScrollState
	Output        string
	Generation    uint64
	Details       []DetailSection
	Doctor        []DoctorCheck
	Credits       string
	Slack         SlackStatus
	SlackProblem  string
	GitHub        GitHubState
	Models        []ModelInfo
	ModelQuery    string
	ModelsProblem string
	// MCP reads are structured from fx's --json output, never guessed.
	MCPServers     []MCPServerInfo
	MCPDetail      *MCPServerDetail
	MCPProblem     string
	SessionView    *SessionDetail
	SessionProblem string
	// Allowlist edits fx's persistent local/user allow rules. The UI keeps
	// the selected scope, rule kind and tool, plus the raw rows loaded from
	// ~/.fx/settings.json.
	AllowScope   string
	AllowKind    string
	AllowTool    string
	AllowPattern string
	AllowRules   []AllowlistRule
	AllowProblem string
	Skills       []SkillInfo
	SkillQuery   string
	SkillProblem string
}

// SkillInfo is one skill found in fx's workspace or global discovery roots.
type SkillInfo struct{ Name, Description, Source, Path string }

// AllowlistRule is one raw persistent rule from ~/.fx/settings.json.
type AllowlistRule struct{ Scope, Kind, Key, Label, Pattern, Decision string }

// ModelInfo is one row of the native `fx models` list.
type ModelInfo struct{ ID, Source string }

// MCPServerInfo is one row of `fx mcp list --json`.
type MCPServerInfo struct{ Name, Source, File, Transport, Status, Error string }

// MCPToolInfo is one tool reported by `fx mcp show NAME --json`.
type MCPToolInfo struct{ Name, Description string }

// MCPServerDetail is the native view of `fx mcp show NAME --json`.
type MCPServerDetail struct {
	MCPServerInfo
	Version string
	URL     string
	Command string
	Tools   []MCPToolInfo
}

// SessionDetail is the native view of `fx session --json`.
type SessionDetail struct {
	ID         string
	Created    string
	Updated    string
	Language   string
	HistoryLen int
	Turns      []SessionTurn
}

// SessionTurn is one saved turn in a session detail.
type SessionTurn struct {
	Kind      string
	User      string
	Images    int
	Assistant string
	Tools     []string
}

// DetailFact is one label/value row in the native Details page.
type DetailFact struct{ Label, Value string }

// DetailSection groups facts for one fx inspection.
type DetailSection struct {
	Title string
	Facts []DetailFact
	Text  string
}

// DoctorCheck is one `fx doctor` result.
type DoctorCheck struct{ Name, Status, Detail string }

// SlackStatus is the native rendering of `fx slack status --json`.
type SlackStatus struct {
	Installed      bool
	AppID          string
	TeamID         string
	BotUserID      string
	Expires        string
	RefreshExpires string
}

// GitHubState is the native draft/publish form for `fx pr` and `fx issue`.
type GitHubState struct {
	Kind       string
	Context    string
	Auto       bool
	Create     bool
	Output     string
	Problem    string
	Busy       bool
	Generation uint64
}

type ManagementHost struct {
	Run           func(action string)
	PickDirectory func()
	Resume        func(id, workspace string)
	// Allowlist applies one add/remove/reset to fx's settings. key is the
	// tool category or the raw rule key; pattern is the command/url/domain.
	Allowlist func(action, scope, kind, key, pattern string)
	OpenPath  func(path string)
}

// toolPageRows is the tab strip split into two rows. Eleven pages do not
// fit one row at the sheet's fixed 560 DIP width: a single row clipped the
// last pages. Two rows keep every page visible, and keep the strip's height
// constant so switching pages cannot move the sheet. All rows share one
// column grid: each tab gets the same fixed width, so the shorter row
// leaves its trailing cells empty instead of stretching over them.
var toolPageRows = [][]string{
	{"Account", "MCP", "Folders", "Details", "Sessions", "Launch"},
	{"Models", "Rules", "Skills", "Slack", "GitHub"},
}

const (
	toolTabGapX      = 2
	toolTabPaddingX  = 10
	toolTabMinColumn = 24
)

// toolPageColumnWidth divides the sheet's inner width into one grid.
// Grow(1) cannot do this: flex distributes the leftover space on top of
// each button's intrinsic width, so a row with fewer tabs ends up with
// wider columns than the row above it.
func toolPageColumnWidth(windowWidth float32) float32 {
	columns := 0
	for _, row := range toolPageRows {
		if len(row) > columns {
			columns = len(row)
		}
	}
	panelWidth := min(float32(contentMaxWidth), windowWidth-48)
	inner := panelWidth - 2*sheetBorderWidth - 2*toolTabPaddingX
	return max(float32(toolTabMinColumn), (inner-float32(columns-1)*toolTabGapX)/float32(columns))
}

func managementDialog(c *ui.Context, state *ManagementState, h ManagementHost, p Palette) {
	width, height := c.Size()
	if state.Page == "" {
		state.Page = "Account"
	}
	tabColumnWidth := toolPageColumnWidth(width)
	sheetWithHeight(c, &state.Open, p, "Workspace tools", height-48, func() {
		ui.Column(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
			ui.Row(c).FillWidth().Padding(Space5, Space7).AlignItems(ui.Center).Shrink(0).Children(func() {
				sheetTitle(c, "Workspace tools", p.InkStrong)
				ui.Box(c).Grow(1)
				ui.Button(c, "Close").FontSize(TypeControl).BorderWidth(0).Background(p.Surface).TextColor(p.InkMuted).OnClick(func() { state.Open = false })
			})
			ui.Column(c).FillWidth().Padding(Space0, toolTabPaddingX, Space4, toolTabPaddingX).Gap(toolTabGapX).Shrink(0).Children(func() {
				for _, row := range toolPageRows {
					ui.Row(c).FillWidth().Gap(toolTabGapX).Children(func() {
						for _, page := range row {
							page := page
							button := ui.Button(c, page).Label("Tools: "+page).Font(MonoFont).FontSize(TypeLabel).Padding(Space2, Space3).Width(tabColumnWidth).Shrink(0).BorderWidth(0).Radius(RadiusChip).Disabled(state.Busy).OnClick(func() {
								state.Page = page
								state.Output = ""
								// A new page starts at its top; the old
								// page's offset would otherwise land in
								// the middle of a shorter page.
								state.BodyScroll.X, state.BodyScroll.Y = 0, 0
								if page == "Models" && len(state.Models) == 0 && h.Run != nil {
									h.Run("models")
								}
								if page == "Rules" && len(state.AllowRules) == 0 && h.Run != nil {
									h.Run("allowlist-load")
								}
								if page == "Skills" && len(state.Skills) == 0 && h.Run != nil {
									h.Run("skills-load")
								}
							})
							if state.Page == page {
								button.Background(p.Sunken).TextColor(p.Accent)
							} else {
								button.Background(p.Window).TextColor(p.InkMuted)
							}
						}
					})
				}
			})
			ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Label("Workspace tool page").TrackScroll(&state.BodyScroll).Children(func() {
				ui.Column(c).FillWidth().Padding(Space4, Space7, Space7, Space7).Gap(Space5).Children(func() {
					// statusLine is only for what fx reported or what it is
					// doing. The pages carry no standing prose: controls,
					// their labels and the badges below say what they do.
					statusLine := func(text string) { ui.Text(c, text).FontSize(TypeControl).TextColor(p.InkMuted).Wrap() }
					// badge is a short, structured fact about a setting —
					// when it takes effect — not an instruction sentence.
					badge := func(text string) {
						ui.Row(c).AlignItems(ui.Center).Padding(InlinePadY, InlinePadX).Radius(RadiusChip).Background(p.Sunken).Children(func() {
							mono(ui.Text(c, text)).FontSize(TypeBadge).TextColor(p.Accent).SingleLine()
						})
					}
					// section names a group of controls. It is the design's
					// structural caption (labelText): one size for every
					// caption on the sheet, so the page reads as one form.
					section := func(text string) {
						ui.Row(c).FillWidth().Padding(Space1, Space0, Space0, Space0).Children(func() {
							labelText(c, strings.ToUpper(text), p.InkFaint)
						})
					}
					field := func(label string, value *string) {
						ui.Column(c).FillWidth().Gap(Space2).Children(func() {
							labelText(c, label, p.InkMuted)
							ui.TextInput(c, value).Label(label).FontSize(TypeInput).FillWidth().Disabled(state.Busy)
						})
					}
					button := func(label, action string) {
						ui.Button(c, label).FontSize(TypeControl).Padding(ButtonPadY, ButtonPadX).Disabled(state.Busy || h.Run == nil).OnClick(func() {
							if h.Run != nil {
								h.Run(action)
							}
						})
					}
					check := func(label string, value *bool) {
						ui.Checkbox(c, value, label).FontSize(TypeControl).Disabled(state.Busy)
					}
					factRow := func(label, value string) {
						if value == "" {
							value = "—"
						}
						ui.Row(c).FillWidth().Gap(Space5).Children(func() {
							ui.Text(c, label).FontSize(TypeLabel).TextColor(p.InkMuted).Width(116)
							ui.Text(c, value).FontSize(TypeControl).TextColor(p.Ink).Wrap().Selectable()
						})
					}
					if state.Output != "" {
						ui.Scroll(c).FillWidth().MaxHeight(150).Label("Workspace tool output").Children(func() {
							ui.Text(c, state.Output).Font(MonoFont).FontSize(TypeLabel).TextColor(p.InkMuted).Wrap().Selectable().Padding(Space4, Space5).Background(p.Sunken)
						})
					}
					switch state.Page {
					case "Account":
						section("Provider")
						ui.RadioGroup(c, func() {
							for _, provider := range []string{"", "vercel", "codex", "grok"} {
								label := provider
								if label == "" {
									label = "Default"
								}
								// A radio list set larger than the fields
								// beside it reads as another panel.
								ui.Radio(c, &state.Provider, provider, label).FontSize(TypeControl)
							}
						})
						ui.Row(c).Gap(Space4).Children(func() { button("Sign in", "login"); button("Sign out", "logout") })
						ui.Row(c).Gap(Space4).Children(func() { button("Configure Gateway key", "setup"); button("Choose team", "teams") })
						ui.Box(c).Height(1)
						field("Default provider for new conversations", &state.ProviderDefault)
						ui.Row(c).Gap(Space4).Children(func() { button("Set default provider", "provider-set") })
						section("Credits")
						ui.Row(c).Gap(Space4).Children(func() { button("Refresh credits", "credits") })
						if state.Credits != "" {
							ui.Scroll(c).FillWidth().MaxHeight(100).Children(func() {
								ui.Text(c, state.Credits).Font(MonoFont).FontSize(TypeLabel).TextColor(p.InkMuted).Wrap().Selectable().Padding(Space4, Space5).Background(p.Sunken)
							})
						}
					case "MCP":
						badge("Applies to new sessions")
						section("Servers")
						ui.Row(c).Gap(Space4).Children(func() {
							button("List servers", "mcp-list")
							button("Check server", "mcp-show")
						})
						ui.Row(c).Gap(Space4).Children(func() {
							button("Add Slack", "mcp-add-slack")
							button("Approve all project servers", "mcp-approve-all")
						})
						section("Add server")
						field("Server name", &state.Name)
						ui.RadioGroup(c, func() {
							ui.Radio(c, &state.Transport, "stdio", "stdio").FontSize(TypeControl)
							ui.Radio(c, &state.Transport, "http", "HTTP").FontSize(TypeControl)
						})
						if state.Transport == "http" {
							field("Server URL", &state.URL)
							ui.Column(c).FillWidth().Gap(Space1).Children(func() {
								labelText(c, "HTTP headers", p.InkMuted)
								ui.TextArea(c, &state.Headers).Label("HTTP headers").FontSize(TypeInput).Placeholder("K=V, one per line").Lines(1, 3).FillWidth().Disabled(state.Busy)
							})
						} else {
							field("Server executable", &state.Command)
							ui.Column(c).FillWidth().Gap(Space1).Children(func() {
								labelText(c, "Server arguments", p.InkMuted)
								// The format belongs in the empty control,
								// not in a paragraph above it.
								ui.TextArea(c, &state.Arguments).Label("Server arguments").FontSize(TypeInput).Placeholder("one argument per line · spaces preserved").Lines(1, 3).FillWidth().Disabled(state.Busy)
							})
							ui.Column(c).FillWidth().Gap(Space1).Children(func() {
								labelText(c, "Environment", p.InkMuted)
								ui.TextArea(c, &state.Environment).Label("Environment").FontSize(TypeInput).Placeholder("K=V, one per line").Lines(1, 3).FillWidth().Disabled(state.Busy)
							})
						}
						check("Write to this project's .mcp.json", &state.MCPProject)
						ui.Row(c).Gap(Space4).Children(func() {
							button("Add server", "mcp-add")
							button("Sign in server", "mcp-login")
							button("Sign out server", "mcp-logout")
						})
						ui.Row(c).Gap(Space4).Children(func() {
							button("Remove server", "mcp-remove")
							button("Approve server", "mcp-approve")
							button("Reject server", "mcp-reject")
						})
						if state.MCPProblem != "" {
							statusLine(state.MCPProblem)
						}
						if len(state.MCPServers) > 0 {
							ui.Column(c).FillWidth().Gap(Space1).Children(func() {
								labelText(c, "Configured servers", p.InkMuted)
								ui.Scroll(c).FillWidth().MaxHeight(220).Children(func() {
									ui.Column(c).FillWidth().Gap(Space2).Children(func() {
										for _, server := range state.MCPServers {
											server := server
											ui.Column(c).FillWidth().Gap(Space1).Padding(Space3, Space4).Radius(RadiusChip).Background(p.Sunken).Children(func() {
												ui.Row(c).FillWidth().Gap(Space3).AlignItems(ui.Center).Children(func() {
													ui.Text(c, server.Name).Font(MonoFont).FontSize(TypeControl).TextColor(p.Ink).Grow(1).MinWidth(0).SingleLine()
													ui.Text(c, server.Transport).Font(MonoFont).FontSize(TypeLabel).TextColor(p.InkMuted).SingleLine()
													ui.Text(c, valueOrDash(server.Status)).Font(MonoFont).FontSize(TypeLabel).TextColor(mcpStatusColor(p, server.Status)).SingleLine()
													ui.Button(c, "Check "+server.Name).FontSize(TypeControl).Padding(InlinePadY, InlinePadX).Disabled(state.Busy || h.Run == nil).OnClick(func() {
														state.Name = server.Name
														if h.Run != nil {
															h.Run("mcp-show")
														}
													})
												})
												if server.Source != "" || server.File != "" {
													ui.Text(c, strings.TrimSpace(server.Source+" · "+server.File)).Font(MonoFont).FontSize(TypeLabel).TextColor(p.InkMuted).SingleLine()
												}
												if server.Error != "" {
													ui.Text(c, server.Error).FontSize(TypeLabel).TextColor(p.Danger).Wrap()
												}
											})
										}
									})
								})
							})
						}
						if d := state.MCPDetail; d != nil {
							ui.Column(c).FillWidth().Gap(Space2).Padding(Space4, Space5).Radius(RadiusChip).Background(p.Sunken).Children(func() {
								mono(ui.Text(c, "Checked server")).FontSize(TypeLabel).TextColor(p.Accent)
								factRow("Name", d.Name)
								factRow("Status", d.Status)
								factRow("Transport", d.Transport)
								factRow("Source", d.Source)
								factRow("File", d.File)
								factRow("Version", d.Version)
								factRow("URL", d.URL)
								factRow("Command", d.Command)
								factRow("Error", d.Error)
								factRow("Tools", fmt.Sprintf("%d", len(d.Tools)))
								for _, tool := range d.Tools {
									ui.Row(c).FillWidth().Gap(Space4).Children(func() {
										ui.Text(c, tool.Name).Font(MonoFont).FontSize(TypeLabel).TextColor(p.Ink).Width(140).SingleLine()
										ui.Text(c, tool.Description).FontSize(TypeLabel).TextColor(p.InkMuted).Wrap().Grow(1).MinWidth(0)
									})
								}
							})
						}
					case "Folders":
						badge("Applies to new sessions")
						field("Additional directory", &state.Directory)
						ui.Button(c, "Choose directory…").FontSize(TypeControl).Disabled(state.Busy || h.PickDirectory == nil).OnClick(func() {
							if h.PickDirectory != nil {
								h.PickDirectory()
							}
						})
						ui.Row(c).Gap(Space4).Children(func() {
							button("List directories", "workspace-list")
							button("Add directory", "workspace-add")
							button("Remove directory", "workspace-remove")
						})
						button("Clear additional directories", "workspace-clear")
					case "Details":
						section("Status")
						ui.RadioGroup(c, func() {
							ui.Radio(c, &state.Period, "today", "Today").FontSize(TypeControl)
							ui.Radio(c, &state.Period, "7d", "7 days").FontSize(TypeControl)
							ui.Radio(c, &state.Period, "month", "Month").FontSize(TypeControl)
						})
						ui.Row(c).Gap(Space4).Children(func() {
							button("Refresh details", "details")
							button("Run doctor", "doctor")
							button("Refresh credits", "credits")
						})
						ui.Button(c, "Open diagnostics folder").FontSize(TypeControl).Disabled(h.OpenPath == nil).OnClick(func() {
							if h.OpenPath != nil {
								if home, err := os.UserHomeDir(); err == nil {
									h.OpenPath(filepath.Join(home, ".fx", "diagnostics"))
								}
							}
						})
						for _, section := range state.Details {
							ui.Column(c).FillWidth().Gap(Space2).Padding(Space4, Space5).Radius(RadiusChip).Background(p.Sunken).Children(func() {
								mono(ui.Text(c, section.Title)).FontSize(TypeLabel).TextColor(p.Accent)
								for _, item := range section.Facts {
									factRow(item.Label, item.Value)
								}
								if section.Text != "" {
									ui.Text(c, section.Text).Font(MonoFont).FontSize(TypeLabel).TextColor(p.InkMuted).Wrap().Selectable()
								}
							})
						}
						for _, check := range state.Doctor {
							ui.Row(c).FillWidth().Gap(Space4).Children(func() {
								label := strings.ToUpper(check.Status)
								if label == "" {
									label = "CHECK"
								}
								tone := p.InkMuted
								switch check.Status {
								case "ok":
									tone = p.OK
								case "warn":
									tone = p.Warn
								case "fail":
									tone = p.Danger
								}
								ui.Text(c, label).Font(MonoFont).FontSize(TypeLabel).TextColor(tone).Width(44)
								ui.Text(c, check.Name).FontSize(TypeControl).TextColor(p.Ink).Width(100)
								ui.Text(c, check.Detail).FontSize(TypeLabel).TextColor(p.InkMuted).Wrap()
							})
						}
						ui.Box(c).Height(1)
						section("Release channel")
						ui.RadioGroup(c, func() {
							ui.Radio(c, &state.Channel, "stable", "Stable").FontSize(TypeControl)
							ui.Radio(c, &state.Channel, "dev", "Dev").FontSize(TypeControl)
						})
						button("Upgrade on this channel", "upgrade-channel")
					case "Sessions":
						section("Saved sessions")
						button("Browse history…", "sessions")
						field("Session id", &state.SessionID)
						ui.Row(c).Gap(Space4).Children(func() {
							button("Inspect session", "session-inspect")
							button("Recover corrupt session", "session-recover")
						})
						ui.Box(c).Height(1)
						section("Conversion")
						button("Convert old sessions", "sessions-convert")
						if state.SessionProblem != "" {
							statusLine(state.SessionProblem)
						}
						if d := state.SessionView; d != nil {
							ui.Column(c).FillWidth().Gap(Space2).Padding(Space4, Space5).Radius(RadiusChip).Background(p.Sunken).Children(func() {
								mono(ui.Text(c, "Session detail")).FontSize(TypeLabel).TextColor(p.Accent)
								factRow("ID", d.ID)
								factRow("Created", d.Created)
								factRow("Updated", d.Updated)
								factRow("Turns", fmt.Sprintf("%d", d.HistoryLen))
								factRow("Language", d.Language)
							})
							if len(d.Turns) > 0 {
								ui.Scroll(c).FillWidth().MaxHeight(260).Children(func() {
									ui.Column(c).FillWidth().Gap(Space2).Children(func() {
										for i, turn := range d.Turns {
											turn := turn
											ui.Column(c).FillWidth().Gap(Space1).Padding(Space3, Space4).Radius(RadiusChip).Background(p.Sunken).Children(func() {
												label := fmt.Sprintf("%d · %s", i+1, valueOrDash(turn.Kind))
												if turn.Images > 0 {
													label += fmt.Sprintf(" · %d image(s)", turn.Images)
												}
												labelText(c, label, p.InkMuted)
												if turn.User != "" {
													ui.Text(c, "You: "+turn.User).FontSize(TypeLabel).TextColor(p.Ink).Wrap()
												}
												if turn.Assistant != "" {
													ui.Text(c, turn.Assistant).FontSize(TypeLabel).TextColor(p.InkMuted).Wrap()
												}
												if len(turn.Tools) > 0 {
													ui.Text(c, "Tools: "+strings.Join(turn.Tools, ", ")).Font(MonoFont).FontSize(TypeLabel).TextColor(p.InkMuted).Wrap()
												}
											})
										}
									})
								})
							}
						}
					case "Models":
						field("Filter models", &state.ModelQuery)
						ui.Row(c).Gap(Space4).Children(func() {
							button("Refresh models", "models")
							button("Clear filter", "models-clear")
						})
						if state.ModelsProblem != "" {
							statusLine(state.ModelsProblem)
						}
						query := strings.ToLower(strings.TrimSpace(state.ModelQuery))
						shown := 0
						ui.Scroll(c).FillWidth().MaxHeight(280).Children(func() {
							ui.Column(c).FillWidth().Gap(Space1).Children(func() {
								for _, model := range state.Models {
									if query != "" && !strings.Contains(strings.ToLower(model.ID), query) && !strings.Contains(strings.ToLower(model.Source), query) {
										continue
									}
									shown++
									model := model
									ui.ButtonBase(c).FillWidth().Padding(Space2, Space3).Radius(RadiusChip).Label("Use model " + model.ID).OnClick(func() {
										state.Model = model.ID
										state.Page = "Launch"
									}).Children(func() {
										ui.Text(c, model.ID).Font(MonoFont).FontSize(TypeControl).TextColor(p.Ink).SingleLine()
										ui.Box(c).Grow(1)
										ui.Text(c, model.Source).FontSize(TypeLabel).TextColor(p.InkMuted).SingleLine()
									})
								}
								if shown == 0 {
									ui.Text(c, "No models match the filter.").FontSize(TypeControl).TextColor(p.InkMuted)
								}
							})
						})
						ui.Text(c, fmt.Sprintf("%d shown", shown)).FontSize(TypeLabel).TextColor(p.InkMuted)
					case "Launch":
						badge("Applies to new sessions")
						button("Start new conversation", "launch")
						section("Overrides")
						field("Provider override", &state.ProviderOverride)
						field("Model override", &state.Model)
						ui.Column(c).FillWidth().Gap(Space2).Children(func() {
							labelText(c, "Reasoning effort", p.InkMuted)
							ui.TextInput(c, &state.Effort).Label("Reasoning effort").Placeholder("auto · minimal · low · medium · high · xhigh").FontSize(TypeInput).FillWidth().Disabled(state.Busy)
						})
						ui.Column(c).FillWidth().Gap(Space1).Children(func() {
							labelText(c, "Additional directories", p.InkMuted)
							ui.TextArea(c, &state.AddDirs).Label("Additional directories").FontSize(TypeInput).Placeholder("one absolute path per line").Lines(1, 3).FillWidth().Disabled(state.Busy)
						})
						ui.Column(c).FillWidth().Gap(Space1).Children(func() {
							labelText(c, "Context limit overrides", p.InkMuted)
							ui.TextArea(c, &state.ContextLimits).Label("Context limit overrides").FontSize(TypeInput).Placeholder("NAME=BYTES|off, one per line").Lines(1, 3).FillWidth().Disabled(state.Busy)
						})
						ui.Column(c).FillWidth().Gap(Space1).Children(func() {
							labelText(c, "Fast mode", p.InkMuted)
							ui.RadioGroup(c, func() {
								ui.Radio(c, &state.Fast, "default", "Default").FontSize(TypeControl)
								ui.Radio(c, &state.Fast, "on", "On").FontSize(TypeControl)
								ui.Radio(c, &state.Fast, "off", "Off").FontSize(TypeControl)
							})
						})
						ui.Column(c).FillWidth().Gap(Space1).Children(func() {
							labelText(c, "Ultra mode", p.InkMuted)
							ui.RadioGroup(c, func() {
								ui.Radio(c, &state.Ultra, "default", "Default").FontSize(TypeControl)
								ui.Radio(c, &state.Ultra, "on", "On").FontSize(TypeControl)
								ui.Radio(c, &state.Ultra, "off", "Off").FontSize(TypeControl)
							})
						})
						check("Do not load additional directories", &state.NoAdditionalDirs)
						field("ACP log path", &state.LogPath)
						button("Start with these options", "launch")
					case "Rules":
						// The file is a location, not an explanation: naming it
						// beside the scope controls is enough.
						labelText(c, "~/.fx/settings.json", p.InkMuted).SingleLine()
						badge("Applies to new sessions")
						ui.RadioGroup(c, func() {
							ui.Radio(c, &state.AllowScope, "local", "This workspace").FontSize(TypeControl)
							ui.Radio(c, &state.AllowScope, "user", "All workspaces").FontSize(TypeControl)
						})
						ui.RadioGroup(c, func() {
							ui.Radio(c, &state.AllowKind, "tool", "Tool").FontSize(TypeControl)
							ui.Radio(c, &state.AllowKind, "command", "Command").FontSize(TypeControl)
							ui.Radio(c, &state.AllowKind, "url", "URL").FontSize(TypeControl)
							ui.Radio(c, &state.AllowKind, "domain", "Web fetch domain").FontSize(TypeControl)
						})
						if state.AllowKind == "tool" {
							ui.RadioGroup(c, func() {
								for _, tool := range []struct{ key, label string }{
									{"read", "Read files"}, {"write", "Write files"}, {"edit", "Edit files"},
									{"glob", "Glob files"}, {"grep", "Grep files"}, {"skill", "Skills"},
									{"install_skill", "Install skills"}, {"subagent", "Subagents"},
								} {
									ui.Radio(c, &state.AllowTool, tool.key, tool.label).FontSize(TypeControl)
								}
							})
						} else {
							field("Command, URL or domain", &state.AllowPattern)
						}
						allow := func(action string) {
							if h.Allowlist == nil {
								return
							}
							h.Allowlist(action, state.AllowScope, state.AllowKind, state.AllowTool, state.AllowPattern)
						}
						ui.Row(c).Gap(Space4).Children(func() {
							ui.Button(c, "Add rule").FontSize(TypeControl).Padding(ButtonPadY, ButtonPadX).Disabled(state.Busy || h.Allowlist == nil).OnClick(func() { allow("add") })
							ui.Button(c, "Remove selected").FontSize(TypeControl).Padding(ButtonPadY, ButtonPadX).Disabled(state.Busy || h.Allowlist == nil).OnClick(func() { allow("remove") })
						})
						ui.Row(c).Gap(Space4).Children(func() {
							ui.Button(c, "Reset this kind").FontSize(TypeControl).Padding(ButtonPadY, ButtonPadX).Disabled(state.Busy || h.Allowlist == nil).OnClick(func() { allow("reset") })
							ui.Button(c, "Reset scope").FontSize(TypeControl).Padding(ButtonPadY, ButtonPadX).Disabled(state.Busy || h.Allowlist == nil).OnClick(func() { allow("reset-all") })
						})
						if state.AllowProblem != "" {
							statusLine(state.AllowProblem)
						}
						if len(state.AllowRules) == 0 {
							statusLine("No rules")
						} else {
							ui.Scroll(c).FillWidth().MaxHeight(220).Children(func() {
								ui.Column(c).FillWidth().Gap(Space1).Children(func() {
									for _, rule := range state.AllowRules {
										rule := rule
										ui.Row(c).FillWidth().Gap(Space4).AlignItems(ui.Center).Children(func() {
											ui.Text(c, rule.Scope).Font(MonoFont).FontSize(TypeLabel).TextColor(p.InkMuted).Width(42)
											ui.Text(c, rule.Label).FontSize(TypeLabel).TextColor(p.InkMuted).Width(120)
											ui.Text(c, rule.Pattern).Font(MonoFont).FontSize(TypeLabel).TextColor(p.Ink).Wrap().Grow(1).MinWidth(0)
											ui.Button(c, "Remove").FontSize(TypeControl).Padding(InlinePadY, InlinePadX).Disabled(state.Busy || h.Allowlist == nil).OnClick(func() {
												if h.Allowlist != nil {
													h.Allowlist("remove", rule.Scope, rule.Kind, rule.Key, rule.Pattern)
												}
											})
										})
									}
								})
							})
						}
					case "Skills":
						field("Filter skills", &state.SkillQuery)
						ui.Row(c).Gap(Space4).Children(func() {
							button("Refresh skills", "skills-load")
							ui.Button(c, "Open workspace skills").FontSize(TypeControl).Disabled(state.Busy || h.OpenPath == nil || state.Project == "").OnClick(func() {
								if h.OpenPath != nil {
									h.OpenPath(filepath.Join(state.Project, ".fx", "skills"))
								}
							})
						})
						if state.SkillProblem != "" {
							statusLine(state.SkillProblem)
						}
						query := strings.ToLower(strings.TrimSpace(state.SkillQuery))
						shown := 0
						ui.Scroll(c).FillWidth().MaxHeight(280).Children(func() {
							ui.Column(c).FillWidth().Gap(Space3).Children(func() {
								for _, skill := range state.Skills {
									if query != "" && !strings.Contains(strings.ToLower(skill.Name), query) && !strings.Contains(strings.ToLower(skill.Description), query) {
										continue
									}
									shown++
									skill := skill
									ui.Column(c).FillWidth().Gap(Space1).Padding(Space3, Space4).Radius(RadiusChip).Background(p.Sunken).Children(func() {
										ui.Row(c).FillWidth().Gap(Space3).AlignItems(ui.Center).Children(func() {
											ui.Text(c, skill.Name).FontSize(TypeControl).TextColor(p.Ink).Grow(1).MinWidth(0).SingleLine()
											ui.Text(c, skill.Source).Font(MonoFont).FontSize(TypeLabel).TextColor(p.InkMuted).SingleLine()
											ui.Button(c, "Open").FontSize(TypeControl).Padding(InlinePadY, InlinePadX).Disabled(h.OpenPath == nil).OnClick(func() {
												if h.OpenPath != nil {
													h.OpenPath(skill.Path)
												}
											})
										})
										if skill.Description != "" {
											ui.Text(c, skill.Description).FontSize(TypeLabel).TextColor(p.InkMuted).Wrap()
										}
									})
								}
								if shown == 0 {
									ui.Text(c, "No skills match the filter.").FontSize(TypeControl).TextColor(p.InkMuted)
								}
							})
						})
						ui.Text(c, fmt.Sprintf("%d shown", shown)).FontSize(TypeLabel).TextColor(p.InkMuted)
					case "Slack":
						ui.Row(c).Gap(Space4).Children(func() {
							button("Refresh status", "slack-status")
							button("Install workspace bot", "slack-install")
							button("Refresh tokens", "slack-refresh")
						})
						button("Add Slack MCP server", "mcp-add-slack")
						if state.SlackProblem != "" {
							statusLine(state.SlackProblem)
						}
						ui.Column(c).FillWidth().Gap(Space2).Padding(Space4, Space5).Radius(RadiusChip).Background(p.Sunken).Children(func() {
							status := "Not installed"
							if state.Slack.Installed {
								status = "Installed"
							}
							factRow("State", status)
							factRow("App id", state.Slack.AppID)
							factRow("Team id", state.Slack.TeamID)
							factRow("Bot user id", state.Slack.BotUserID)
							factRow("Access expires", state.Slack.Expires)
							factRow("Refresh expires", state.Slack.RefreshExpires)
						})
					case "GitHub":
						ui.RadioGroup(c, func() {
							ui.Radio(c, &state.GitHub.Kind, "pr", "Pull request").FontSize(TypeControl)
							ui.Radio(c, &state.GitHub.Kind, "issue", "Issue").FontSize(TypeControl)
						})
						ui.TextArea(c, &state.GitHub.Context).Label("Context (optional)").FontSize(TypeInput).Lines(2, 5).FillWidth().Disabled(state.Busy || state.GitHub.Busy)
						ui.Column(c).FillWidth().Gap(Space1).Children(func() {
							ui.Checkbox(c, &state.GitHub.Auto, "Let fx review permissions automatically").FontSize(TypeControl).Disabled(state.Busy || state.GitHub.Busy)
							ui.Checkbox(c, &state.GitHub.Create, "Publish with GitHub").FontSize(TypeControl).Disabled(state.Busy || state.GitHub.Busy)
						})
						button("Run GitHub action", "github-run")
						if state.GitHub.Busy {
							statusLine("Running fx…")
						}
						if state.GitHub.Problem != "" {
							statusLine(state.GitHub.Problem)
						}
						if state.GitHub.Output != "" {
							ui.Scroll(c).FillWidth().MaxHeight(180).Label("GitHub action output").Children(func() {
								ui.Text(c, state.GitHub.Output).Font(MonoFont).FontSize(TypeLabel).TextColor(p.InkMuted).Wrap().Selectable().Padding(Space4, Space5).Background(p.Sunken)
							})
						}
					}
					if state.Busy {
						statusLine("Reading fx…")
					}
				})
			})
		})
	})
}

func valueOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}

// mcpStatusColor keeps color semantic: failures are danger, servers waiting
// on a connection are warn, healthy servers are OK, and unknown states stay
// muted rather than inventing a tone.
func mcpStatusColor(p Palette, status string) ui.Color {
	s := strings.ToLower(strings.TrimSpace(status))
	switch {
	case s == "ready" || s == "connected" || s == "ok" || s == "running":
		return p.OK
	case strings.Contains(s, "error") || s == "failed":
		return p.Danger
	case s == "retrying" || s == "starting" || s == "connecting":
		return p.Warn
	default:
		return p.InkMuted
	}
}
