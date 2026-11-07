package chrome

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"fx-desktop/internal/history"
)

// HistorySession is one saved conversation.
type HistorySession = history.Session

// HistoryState is the panel's own state plus the query controller it renders.
// The split is deliberate: Open, the search field, the delete progress and the
// list's scroll position belong to the view; the cursor walk, the dedupe sets,
// the request generation and the matches belong to history.Paging and are
// snapshotted before every worker.
type HistoryState struct {
	Open, Deleting bool
	DraftQuery     string
	ResumeProblem  string
	List           ui.ListState
	history.Paging
}

type HistoryHost struct {
	Run      func(action string)
	Resume   func(id, workspace string)
	Delete   func(id, title string)
	Clear    func(all bool)
	ActiveID string
	// Rel renders a row's UpdatedAtMS as the sidebar's relative time
	// ("just now", "5m ago"). The app injects presenter.RelTime; the
	// drawer only knows the contract. A nil Rel hides the line.
	Rel func(ms int64) string
	// Live names conversations open in any app window. Their rows still
	// resume, but cannot be deleted until the window closes.
	Live map[string]bool
	Now  time.Time
}

func historyDate(ms int64, now time.Time) string {
	if ms <= 0 {
		return "Unknown date"
	}
	t := time.UnixMilli(ms).In(now.Location())
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch {
	case !t.Before(start):
		return "Today"
	case !t.Before(start.AddDate(0, 0, -1)):
		return "Yesterday"
	case !t.Before(start.AddDate(0, 0, -7)):
		return "Previous 7 days"
	default:
		return "Older"
	}
}

type historyRow struct {
	key, group string
	index      int
}

func historyRows(entries []HistorySession, now time.Time) []historyRow {
	var rows []historyRow
	last := ""
	for i, e := range entries {
		group := historyDate(e.UpdatedAtMS, now)
		if group != last {
			rows = append(rows, historyRow{key: "group:" + group, group: group})
			last = group
		}
		rows = append(rows, historyRow{key: "session:" + e.ID, index: i})
	}
	return rows
}

func historyDeleteTip(live bool) string {
	if live {
		return "Close this conversation before deleting it"
	}
	return "Delete saved session"
}

func historyDialog(c *ui.Context, state *HistoryState, h HistoryHost, p Palette) {
	_, height := c.Size()
	run := func(action string) {
		if h.Run != nil {
			h.Run(action)
		}
	}
	now := h.Now
	if now.IsZero() {
		now = time.Now()
	}
	rows := historyRows(state.Entries, now)
	sheet(c, &state.Open, p, "Session history", func() {
		ui.Column(c).FillWidth().Children(func() {
			ui.Row(c).FillWidth().Padding(Space5, Space7).AlignItems(ui.Center).Children(func() {
				sheetTitle(c, "Session history", p.InkStrong)
				ui.Box(c).Grow(1)
				ui.Button(c, "Close").FontSize(TypeControl).BorderWidth(0).Background(p.Surface).TextColor(p.InkMuted).OnClick(func() { state.Open = false })
			})
			ui.Column(c).FillWidth().Padding(Space0, Space7, Space5, Space7).Gap(Space4).Children(func() {
				ui.Row(c).FillWidth().Gap(Space3).Children(func() {
					ui.TextInput(c, &state.DraftQuery).Label("Search history titles").Placeholder("Search titles…").FontSize(TypeInput).Grow(1).MinWidth(0).OnSubmit(func() { run("search") })
					ui.Button(c, "Search").FontSize(TypeControl).Disabled(h.Run == nil).OnClick(func() { run("search") })
					if strings.TrimSpace(state.DraftQuery) != "" || state.Query != "" {
						ui.Button(c, "Clear").Label("Clear history search").FontSize(TypeControl).OnClick(func() { state.DraftQuery = ""; run("search") })
					}
				})
				ui.RadioGroup(c, func() {
					ui.Radio(c, &state.All, false, "This workspace").FontSize(TypeControl).OnChange(func() { run("search") })
					ui.Radio(c, &state.All, true, "All workspaces").FontSize(TypeControl).OnChange(func() { run("search") })
				}).Row().Gap(Space9).Label("History scope")
				text := fmt.Sprintf("%d sessions loaded", len(state.Entries))
				if len(state.Entries) == 1 {
					text = "1 session loaded"
				}
				if state.Query != "" {
					text = fmt.Sprintf("%d matches · %d sessions searched", len(state.Entries), state.Scanned)
					if len(state.Entries) == 1 {
						text = fmt.Sprintf("1 match · %d sessions searched", state.Scanned)
					}
				}
				if state.Busy {
					text += " · Loading…"
				}
				if state.Deleting {
					text += " · Deleting…"
				}
				mono(ui.Text(c, text)).FontSize(TypeBadge).TextColor(p.InkMuted)
				if state.Problem != "" {
					ui.Text(c, state.Problem).FontSize(TypeControl).TextColor(p.Danger).Wrap()
				}
				if state.ResumeProblem != "" {
					ui.Text(c, state.ResumeProblem).FontSize(TypeControl).TextColor(p.Danger).Wrap()
				}
			})
			ui.Box(c).Height(1).FillWidth().Background(p.Hairline)
			if len(rows) > 0 {
				ui.List(c, &state.List, len(rows)).ItemKey(func(i int) any { return rows[i].key }).
					Height(min(float32(400), max(float32(100), height-260))).FillWidth().Label("History results").Rows(func(row ui.ListRow) {
					c := row.Context
					r := rows[row.Index]
					if r.group != "" {
						ui.Text(c, r.group).FontSize(TypeControl).FontWeight(500).TextColor(p.InkMuted).Padding(Space6, Space7, Space3, Space7)
						return
					}
					e := state.Entries[r.index]
					title := e.Title
					if strings.TrimSpace(title) == "" {
						title = e.ID
					}
					live := h.Live[e.ID]
					// The sidebar marks its selected history row with a
					// rounded SurfaceHi chip inset 8px from both list edges.
					// The drawer once painted a flat, full-bleed Sunken band
					// instead, so the same list read as two different
					// controls. The outer row supplies the inset; the inner
					// one is the surface that lights up.
					ui.Row(c).FillWidth().Padding(Space0, Space4).Children(func() {
						line := ui.Row(c).FillWidth().Padding(Space0, Space3, Space0, Space3).Gap(Space1).AlignItems(ui.Center).Radius(controlRadius).Label("History row " + e.ID)
						stateSurface(line, p, e.ID == h.ActiveID, line.Hovered())
						line.Children(func() {
							button := ui.ButtonBase(c).Label("Resume history "+e.ID).Grow(1).MinWidth(0).Padding(Space4, Space0).Gap(Space1).Column().AlignItems(ui.Stretch).Disabled(h.Resume == nil || state.Deleting).OnClick(func() {
								if h.Resume != nil && !state.Deleting {
									h.Resume(e.ID, e.Workspace)
								}
							})
							button.Children(func() {
								// The row is the sidebar History section's row
								// at drawer width: clock icon, 12px title, the
								// 10px turn count at the right edge, and the
								// 10px relative time under the title.
								ui.Row(c).FillWidth().Gap(Space4).AlignItems(ui.Center).Children(func() {
									ui.Icon(c, IconClock).FontSize(IconMedium).TextColor(p.InkMuted).Shrink(0)
									ui.Text(c, title).FontSize(TypeInput).TextColor(p.Ink).Grow(1).MinWidth(0).SingleLine()
									if e.Turns > 0 {
										ui.Text(c, itoa(e.Turns)+" turns").FontSize(TypeLabel).TextColor(p.InkMuted).SingleLine().Shrink(0)
									}
									if e.ID == h.ActiveID {
										ui.Text(c, "Current").FontSize(TypeLabel).TextColor(p.InkMuted).Shrink(0)
									} else if live {
										ui.Text(c, "Open").FontSize(TypeLabel).TextColor(p.InkMuted).Shrink(0)
									}
								})
								when := ""
								if h.Rel != nil {
									when = h.Rel(e.UpdatedAtMS)
								}
								if when != "" || state.All {
									ui.Row(c).FillWidth().Gap(Space3).Padding(Space0, Space0, Space0, Space10).Children(func() {
										if when != "" {
											ui.Text(c, when).FontSize(TypeLabel).TextColor(p.InkMuted).SingleLine().Shrink(0)
										}
										if state.All && e.Workspace != "" {
											ui.Text(c, e.Workspace).FontSize(TypeLabel).TextColor(p.InkMuted).MinWidth(0).SingleLine().Tooltip(e.Workspace)
										}
									})
								}
							})
							if h.Delete != nil {
								del := ui.Button(c, "").Label("Delete saved session " + e.ID).
									Tooltip(historyDeleteTip(live)).Width(28).Height(28).Padding(Space0).Radius(RadiusChip).
									BorderWidth(0).Background(ui.Color{}).Disabled(live || state.Deleting).
									OnClick(func() {
										if h.Delete != nil && !live && !state.Deleting {
											h.Delete(e.ID, title)
										}
									})
								col := p.InkMuted
								if del.Hovered() && !live && !state.Deleting {
									col = p.Danger
									del.Background(p.Danger.Alpha(0.10))
								}
								del.Children(func() {
									ui.Icon(c, IconTrash).FontSize(IconLarge).TextColor(col)
								})
							}
						})
					})
				})
			} else {
				message := "No saved sessions."
				if state.Problem != "" {
					message = "Could not load history."
				} else if state.Busy {
					message = "Loading history…"
				} else if state.Query != "" {
					message = "No matching titles."
					if state.HasMore() {
						message = "No matches yet."
					}
				}
				ui.Text(c, message).FontSize(TypeInput).TextColor(p.InkMuted).Wrap().Padding(Space12, Space7)
			}
			ui.Row(c).FillWidth().Padding(Space5, Space7).Gap(Space4).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(p.Hairline).Children(func() {
				ui.Button(c, "Refresh").FontSize(TypeControl).OnClick(func() { run("search") }).Disabled(h.Run == nil || state.Deleting)
				if h.Clear != nil && state.Query == "" && len(state.Entries) > 0 {
					clear := ui.Button(c, "Clear all").Label("Clear all session history").
						Tooltip("Delete saved sessions in this scope").FontSize(TypeControl).
						BorderWidth(0).Background(p.Surface).TextColor(p.Danger).
						Disabled(state.Deleting).OnClick(func() {
						if h.Clear != nil && !state.Deleting {
							h.Clear(state.All)
						}
					})
					if clear.Hovered() && !state.Deleting {
						clear.Background(p.Danger.Alpha(0.10))
					}
				}
				ui.Box(c).Grow(1)
				if state.HasMore() || state.Problem != "" {
					label := "Load more"
					if state.Problem != "" {
						label = "Retry"
					}
					ui.Button(c, label).Label("Load more history").FontSize(TypeControl).Disabled(state.Busy || state.Deleting || h.Run == nil).OnClick(func() { run("more") })
				} else if state.Loaded && !state.Busy && !state.Deleting {
					ui.Text(c, "End of history").FontSize(TypeControl).TextColor(p.InkMuted)
				}
			})
		})
	})
}
