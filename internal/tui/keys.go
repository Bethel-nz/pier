package tui

import "github.com/charmbracelet/bubbles/key"

type keyMap struct {
	Up         key.Binding
	Down       key.Binding
	SelectUp   key.Binding
	SelectDown key.Binding
	Plan       key.Binding
	Share      key.Binding
	Copy       key.Binding
	Open       key.Binding
	Refresh    key.Binding
	Help       key.Binding
	Quit       key.Binding
	Confirm    key.Binding
	Cancel     key.Binding
	Add        key.Binding
	Pause      key.Binding
	NextPane   key.Binding
	LeftPane   key.Binding
	RightPane  key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		Up:         key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "up")),
		Down:       key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "down")),
		SelectUp:   key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/↓", "select")),
		SelectDown: key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↑/↓", "select")),
		Plan:       key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "plan")),
		Share:      key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "share/unshare")),
		Copy:       key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "copy")),
		Open:       key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open")),
		Refresh:    key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Help:       key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:       key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Confirm:    key.NewBinding(key.WithKeys("enter", "y"), key.WithHelp("enter", "confirm")),
		Cancel:     key.NewBinding(key.WithKeys("esc", "n"), key.WithHelp("esc", "cancel")),
		Add:        key.NewBinding(key.WithKeys("a", "+"), key.WithHelp("a", "add")),
		Pause:      key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "pause/resume")),
		NextPane:   key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next pane")),
		LeftPane:   key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("h/l", "switch pane")),
		RightPane:  key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("h/l", "switch pane")),
	}
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.NextPane, k.SelectUp, k.Up, k.Down, k.Plan, k.Share, k.Pause, k.Copy, k.Open, k.Add, k.Help, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{k.ShortHelp()}
}
