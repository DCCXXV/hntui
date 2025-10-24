package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const MAX = 20

var (
	titleBarStyle = lipgloss.NewStyle().
			Bold(true).
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("235")).
			BorderTop(false).
			BorderLeft(false).
			BorderRight(false).
			BorderBottom(true).
			PaddingLeft(1).
			PaddingRight(1).
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("202"))

	storyStyle = lipgloss.NewStyle().
			MarginTop(1).
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("235")).
			BorderTop(false).
			BorderLeft(false).
			BorderRight(false).
			BorderBottom(true)

	selectedStyle = storyStyle.
			Bold(true)

	dataStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("248"))
)

// https://github.com/HackerNews/API/blob/master/README.md
type Story struct {
	ID          int    `json:"id"`          // The item's unique id.
	By          string `json:"by"`          // The username of the item's author.
	Time        int    `json:"time"`        // Creation date of the item, in Unix Time.
	Kids        []int  `json:"kids"`        // The ids of the item's comments, in ranked display order.
	URL         string `json:"url"`         // The URL of the story.
	Score       int    `json:"score"`       // The story's score, or the votes for a pollopt.
	Title       string `json:"title"`       // The title of the story, poll or job. HTML.
	Descendants int    `json:"descendants"` // In the case of stories or polls, the total comment count.
}

type loadedStoriesMsg struct{ loadedStories [MAX]Story }
type errMsg struct{ err error }

func fetchTopStories() tea.Msg {
	response, err := http.Get("https://hacker-news.firebaseio.com/v0/topstories.json?print=pretty")
	if err != nil {
		return errMsg{err}
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return errMsg{err}
	}

	var storyID []int
	if err := json.Unmarshal(body, &storyID); err != nil {
		return errMsg{err}
	}

	var stories [MAX]Story
	for i := range MAX {
		story, err := http.Get(fmt.Sprintf("https://hacker-news.firebaseio.com/v0/item/%d.json?print=pretty", storyID[i]))
		if err != nil {
			continue
		}
		storyBody, err := io.ReadAll(story.Body)
		story.Body.Close()
		if err != nil {
			continue
		}
		if err := json.Unmarshal(storyBody, &stories[i]); err != nil {
			continue
		}
	}
	return loadedStoriesMsg{loadedStories: stories}
}

type model struct {
	stories [MAX]Story
	cursor  int
	loading bool
	err     error
	width   int
	height  int
}

func initialModel() model {
	return model{
		loading: true,
	}
}

func (m model) Init() tea.Cmd {
	return fetchTopStories
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case loadedStoriesMsg:
		m.stories = msg.loadedStories
		m.loading = false
		return m, nil

	case errMsg:
		m.err = msg.err
		m.loading = false
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.stories)-1 {
				m.cursor++
			}
		case "r":
			m.loading = true
			return m, fetchTopStories
		case "enter":
			openURL(m.stories[m.cursor].URL)
		}
	}
	return m, nil
}

func (m model) View() string {
	if m.loading {
		return "\n  Loading Hacker News stories...\n"
	}

	if m.err != nil {
		return fmt.Sprintf("\n  Error: %v\n\n  Press 'r' to retry or 'q' to quit.\n", m.err)
	}

	var s strings.Builder
	s.WriteString(titleBarStyle.Width(m.width).Render("Hacker News "))

	visibleStart := 0
	visibleEnd := len(m.stories)

	if m.height > 0 {
		maxVisible := (m.height - 5) / 3
		if maxVisible < len(m.stories) {
			visibleStart = max(m.cursor-maxVisible/2, 0)
			visibleEnd = visibleStart + maxVisible
			if visibleEnd > len(m.stories) {
				visibleEnd = len(m.stories)
				visibleStart = max(visibleEnd-maxVisible, 0)
			}
		}
	}

	for i := visibleStart; i < visibleEnd; i++ {
		cursor := "  "
		title := m.stories[i].Title
		score := m.stories[i].Score
		comments := len(m.stories[i].Kids)

		dataStr := dataStyle.Render(fmt.Sprintf("  score: %d comments: %d", score, comments))

		if m.cursor == i {
			cursor = "> "
			storyStr := fmt.Sprintf("%s%s\n%s", cursor, title, dataStr)
			if i%2 == 0 {
				s.WriteString(selectedStyle.Width(m.width).Render(storyStr))
			} else {
				s.WriteString(selectedStyle.Width(m.width).Background(lipgloss.Color("235")).Render(storyStr))
			}
		} else {
			storyStr := fmt.Sprintf("%s%s\n%s", cursor, title, dataStr)
			if i%2 == 0 {
				s.WriteString(storyStyle.Width(m.width).Render(storyStr))
			} else {
				s.WriteString(storyStyle.Width(m.width).Background(lipgloss.Color("235")).Render(storyStr))
			}
		}

	}

	s.WriteString("\nj/k: move | r: refresh | q: quit | enter: open URL in browser")

	return s.String()
}

// https://stackoverflow.com/questions/39320371/how-start-web-server-to-open-page-in-browser-in-golang
// openURL opens the specified URL in the default browser of the user.
func openURL(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start"}
	case "darwin":
		cmd = "open"
		args = []string{url}
	default: // "linux", "freebsd", "openbsd", "netbsd"
		// Check if running under WSL
		if isWSL() {
			// Use 'cmd.exe /c start' to open the URL in the default Windows browser
			cmd = "cmd.exe"
			args = []string{"/c", "start", url}
		} else {
			// Use xdg-open on native Linux environments
			cmd = "xdg-open"
			args = []string{url}
		}
	}
	if len(args) > 1 {
		// args[0] is used for 'start' command argument, to prevent issues with URLs starting with a quote
		args = append(args[:1], append([]string{""}, args[1:]...)...)
	}
	return exec.Command(cmd, args...).Start()
}

// isWSL checks if the Go program is running inside Windows Subsystem for Linux
func isWSL() bool {
	releaseData, err := exec.Command("uname", "-r").Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(releaseData)), "microsoft")
}

func main() {
	p := tea.NewProgram(
		initialModel(),
		tea.WithAltScreen(),
	)

	if _, err := p.Run(); err != nil {
		fmt.Printf("Alas, there's been an error: %v", err)
		os.Exit(1)
	}
}
