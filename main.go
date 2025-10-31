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
	"github.com/k3a/html2text"
	"github.com/muesli/reflow/wrap"
)

const MAX = 20

var page = 0

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

type Comment struct {
	ID   int    `json:"id"`
	By   string `json:"by"`
	Text string `json:"text"`
	Time int    `json:"time"`
}

type loadedStoriesMsg struct{ loadedStories [MAX]Story }
type loadedCommentsMsg struct{ loadedComments []Comment }
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

	var storiesID []int
	if err := json.Unmarshal(body, &storiesID); err != nil {
		return errMsg{err}
	}

	var stories [MAX]Story

	start := max(0, page*MAX)
	end := start + MAX

	if start >= len(storiesID) {
		page = len(storiesID)/MAX - 1
		start = page * MAX
		end = start + MAX
	}
	if end > len(storiesID) {
		end = len(storiesID)
	}

	for i, id := range storiesID[start:end] {
		storyResp, err := http.Get(fmt.Sprintf("https://hacker-news.firebaseio.com/v0/item/%d.json?print=pretty", id))
		if err != nil {
			continue
		}
		storyBody, err := io.ReadAll(storyResp.Body)
		storyResp.Body.Close()
		if err != nil {
			continue
		}

		var s Story
		if err := json.Unmarshal(storyBody, &s); err != nil {
			continue
		}
		stories[i] = s
	}
	return loadedStoriesMsg{loadedStories: stories}
}

func fetchComments(story Story) tea.Msg {
	var comments []Comment
	for _, commentID := range story.Kids {
		commentResp, err := http.Get(fmt.Sprintf("https://hacker-news.firebaseio.com/v0/item/%d.json?print=pretty", commentID))
		if err != nil {
			continue
		}

		commentBody, err := io.ReadAll(commentResp.Body)
		commentResp.Body.Close()
		if err != nil {
			continue
		}

		var c Comment
		if err := json.Unmarshal(commentBody, &c); err != nil {
			continue
		}
		comments = append(comments, c)
	}

	return loadedCommentsMsg{loadedComments: comments}
}

type model struct {
	stories        [MAX]Story
	comments       []Comment
	commentHeights []int
	cursor         int
	commentCursor  int
	loading        bool
	commentsView   bool
	err            error
	width          int
	height         int
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

	case loadedCommentsMsg:
		m.comments = msg.loadedComments
		m.loading = false
		m.commentCursor = 0
		m.commentHeights = make([]int, len(m.comments))
		for i := range m.comments {
			m.commentHeights[i] = m.commentHeight(i)
		}
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
			if m.commentsView {
				if m.commentCursor > 0 {
					m.commentCursor--
				}
			} else if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.commentsView {
				if m.commentCursor < len(m.comments)-1 {
					m.commentCursor++
				}
			} else if m.cursor < len(m.stories)-1 {
				m.cursor++
			}
		case "r":
			m.loading = true
			return m, fetchTopStories
		case "c":
			if !m.commentsView {
				m.commentsView = true
				m.loading = true
				return m, func() tea.Msg { return fetchComments(m.stories[m.cursor]) }
			} else {
				m.commentsView = false
			}
		case "pgup", "l":
			page++
			m.loading = true
			return m, fetchTopStories
		case "pgdown", "h":
			page--
			m.loading = true
			return m, fetchTopStories
		case "enter":
			openURL(m.stories[m.cursor].URL)
		}
	}
	return m, nil
}

func (m model) commentHeight(i int) int {
	text := html2text.HTML2Text(m.comments[i].Text)
	wrapped := wrap.String(text, m.width)
	return lipgloss.Height(wrapped)
}

func (m model) View() string {
	var s strings.Builder

	if m.err != nil {
		return fmt.Sprintf("\n  Error: %v\n\n  Press 'r' to retry or 'q' to quit.\n", m.err)
	}

	s.WriteString(titleBarStyle.Width(m.width).Render("Hacker News"))

	if m.loading {
		s.WriteString("\nLoading...\n")
	}

	if m.commentsView {
		visibleStart := 0
		visibleEnd := len(m.comments)

		if m.height > 0 {
			maxVisible := (m.height - 2) / 3
			if maxVisible > 0 && maxVisible < len(m.comments) {
				visibleStart = max(m.commentCursor-maxVisible/2, 0)
				visibleEnd = visibleStart + maxVisible
				if visibleEnd > len(m.comments) {
					visibleEnd = len(m.comments)
					visibleStart = max(visibleEnd-maxVisible, 0)
				}
			}
		}

		for i := visibleStart; i < visibleEnd && i < len(m.comments); i++ {
			cursor := "  "
			if m.commentCursor == i {
				cursor = "> "
			}
			text := html2text.HTML2Text(m.comments[i].Text)
			wrapped := wrap.String(text, m.width-2)

			var comment string
			if m.commentCursor == i {
				comment = fmt.Sprintf("%s%s\n", cursor,
					storyStyle.Width(m.width).
						Background(lipgloss.Color("235")).
						Padding(0, 1).
						Render(wrapped))
			} else {
				comment = fmt.Sprintf("%s%s\n", cursor,
					storyStyle.
						Width(m.width).
						Padding(0, 1).
						Render(wrapped))
			}
			s.WriteString(comment)
		}
		/*
			s.WriteString(
				wrap.String(
					"\nj/k move | c: back to stories | q: quit",
					m.width,
				),
			)*/

		return s.String()
	}

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

	s.WriteString(wrap.String("\nj/k: move | r: refresh | enter: view in browser | h/l: move between pages | c: comments | q: quit", m.width))

	return s.String()
}

// https://stackoverflow.com/questions/39320371/how-start-web-server-to-open-page-in-browser-in-golang
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
	default:
		cmd = "xdg-open"
		args = []string{url}
	}
	if len(args) > 1 {
		args = append(args[:1], append([]string{""}, args[1:]...)...)
	}
	return exec.Command(cmd, args...).Start()
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
