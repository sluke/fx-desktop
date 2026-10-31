package chrome

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type HistorySession struct {
	ID, Title, Workspace string
	UpdatedAtMS          int64
}

// HistoryState is UI-owned. Paging metadata is snapshotted before each worker.
type HistoryState struct {
	Open, Busy, Loaded, Deleting, All, Exhausted      bool
	DraftQuery, Query, Cursor, Problem, ResumeProblem string
	Entries, Pending                                  []HistorySession
	Seen, Cursors                                     map[string]bool
	Scanned                                           int
	Generation                                        uint64
	List                                              ui.ListState
}

func (s *HistoryState) HasMore() bool { return s.Loaded && (!s.Exhausted || len(s.Pending) > 0) }

type HistoryHost struct {
	Run      func(action string)
	Resume   func(id, workspace string)
	Delete   func(id, title string)
	Clear    func(all bool)
	ActiveID string
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
			ui.Row(c).FillWidth().Padding(10, 14).AlignItems(ui.Center).Children(func() {
				sheetTitle(c, "Session history", p.InkStrong)
				ui.Box(c).Grow(1)
				ui.Button(c, "Close").FontSize(11).BorderWidth(0).Background(p.Surface).TextColor(p.InkMuted).OnClick(func() { state.Open = false })
			})
			ui.Column(c).FillWidth().Padding(0, 14, 10, 14).Gap(8).Children(func() {
				ui.Row(c).FillWidth().Gap(6).Children(func() {
					ui.TextInput(c, &state.DraftQuery).Label("Search history titles").Placeholder("Search titles…").FontSize(12).Grow(1).MinWidth(0).OnSubmit(func() { run("search") })
					ui.Button(c, "Search").FontSize(11).Disabled(h.Run == nil).OnClick(func() { run("search") })
					if strings.TrimSpace(state.DraftQuery) != "" || state.Query != "" {
						ui.Button(c, "Clear").Label("Clear history search").FontSize(11).OnClick(func() { state.DraftQuery = ""; run("search") })
					}
				})
				ui.RadioGroup(c, func() {
					ui.Radio(c, &state.All, false, "This workspace").FontSize(11).OnChange(func() { run("search") })
					ui.Radio(c, &state.All, true, "All workspaces").FontSize(11).OnChange(func() { run("search") })
				}).Row().Gap(18).Label("History scope")
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
				mono(ui.Text(c, text)).FontSize(9).TextColor(p.InkMuted)
				if state.Problem != "" {
					ui.Text(c, state.Problem).FontSize(11).TextColor(p.Danger).Wrap()
				}
				if state.ResumeProblem != "" {
					ui.Text(c, state.ResumeProblem).FontSize(11).TextColor(p.Danger).Wrap()
				}
			})
			ui.Box(c).Height(1).FillWidth().Background(p.Hairline)
			if len(rows) > 0 {
				ui.List(c, &state.List, len(rows)).ItemKey(func(i int) any { return rows[i].key }).
					Height(min(float32(400), max(float32(100), height-260))).FillWidth().Label("History results").Rows(func(row ui.ListRow) {
					c := row.Context
					r := rows[row.Index]
					if r.group != "" {
						ui.Text(c, r.group).FontSize(11).FontWeight(500).TextColor(p.InkMuted).Padding(12, 14, 6, 14)
						return
					}
					e := state.Entries[r.index]
					title := e.Title
					if strings.TrimSpace(title) == "" {
						title = e.ID
					}
					live := h.Live[e.ID]
					line := ui.Row(c).FillWidth().Padding(0, 14, 0, 6).Gap(2).AlignItems(ui.Center)
					if e.ID == h.ActiveID {
						line.Background(p.Sunken)
					} else if line.Hovered() {
						line.Background(p.SurfaceHi)
					}
					line.Children(func() {
						button := ui.ButtonBase(c).Label("Resume history "+e.ID).Grow(1).MinWidth(0).Padding(9, 0).Gap(3).Column().AlignItems(ui.Stretch).Disabled(h.Resume == nil || state.Deleting).OnClick(func() {
							if h.Resume != nil && !state.Deleting {
								h.Resume(e.ID, e.Workspace)
							}
						})
						button.Children(func() {
							ui.Row(c).FillWidth().Gap(8).Children(func() {
								ui.Text(c, title).FontSize(12).TextColor(p.Ink).Grow(1).MinWidth(0).SingleLine()
								if e.ID == h.ActiveID {
									ui.Text(c, "Current").FontSize(10).TextColor(p.InkMuted).Shrink(0)
								} else if live {
									ui.Text(c, "Open").FontSize(10).TextColor(p.InkMuted).Shrink(0)
								}
							})
							if state.All {
								ui.Text(c, e.Workspace).FontSize(10).TextColor(p.InkMuted).FillWidth().SingleLine().Tooltip(e.Workspace)
							}
						})
						if h.Delete != nil {
							del := ui.Button(c, "").Label("Delete saved session " + e.ID).
								Tooltip(historyDeleteTip(live)).Width(28).Height(28).Padding(0).Radius(4).
								BorderWidth(0).Background(ui.Color{}).Disabled(live || state.Deleting).
								OnClick(func() {
									if h.Delete != nil && !live && !state.Deleting {
										h.Delete(e.ID, title)
									}
								})
							col := p.InkMuted
							if del.Hovered() && !live && !state.Deleting {
								col = p.Danger
								del.Background(ui.RGBA(p.Danger.R, p.Danger.G, p.Danger.B, 0.10))
							}
							del.Children(func() {
								ui.Icon(c, IconTrash).FontSize(14).TextColor(col)
							})
						}
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
						message = "No matches yet. Load more to continue searching."
					}
				}
				ui.Text(c, message).FontSize(12).TextColor(p.InkMuted).Wrap().Padding(24, 14)
			}
			ui.Row(c).FillWidth().Padding(10, 14).Gap(8).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(p.Hairline).Children(func() {
				ui.Button(c, "Refresh").FontSize(11).OnClick(func() { run("search") }).Disabled(h.Run == nil || state.Deleting)
				if h.Clear != nil && state.Query == "" && len(state.Entries) > 0 {
					clear := ui.Button(c, "Clear all").Label("Clear all session history").
						Tooltip("Delete saved sessions in this scope").FontSize(11).
						BorderWidth(0).Background(p.Surface).TextColor(p.Danger).
						Disabled(state.Deleting).OnClick(func() {
						if h.Clear != nil && !state.Deleting {
							h.Clear(state.All)
						}
					})
					if clear.Hovered() && !state.Deleting {
						clear.Background(ui.RGBA(p.Danger.R, p.Danger.G, p.Danger.B, 0.10))
					}
				}
				ui.Box(c).Grow(1)
				if state.HasMore() || state.Problem != "" {
					label := "Load more"
					if state.Problem != "" {
						label = "Retry"
					}
					ui.Button(c, label).Label("Load more history").FontSize(11).Disabled(state.Busy || state.Deleting || h.Run == nil).OnClick(func() { run("more") })
				} else if state.Loaded && !state.Busy && !state.Deleting {
					ui.Text(c, "End of history").FontSize(11).TextColor(p.InkMuted)
				}
			})
		})
	})
}
