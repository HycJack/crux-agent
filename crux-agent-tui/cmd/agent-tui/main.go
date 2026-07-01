package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"crux-agent-tui/internal/ui"
)

func main() {
	app, err := ui.NewApp()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintln(os.Stderr, "\nMake sure you have a .env file with your API key:")
		fmt.Fprintln(os.Stderr, "  ANTHROPIC_API_KEY=sk-ant-...")
		fmt.Fprintln(os.Stderr, "  OPENAI_API_KEY=sk-...")
		fmt.Fprintln(os.Stderr, "  DEEPSEEK_API_KEY=sk-...")
		os.Exit(1)
	}

	p := tea.NewProgram(
		app,
		tea.WithAltScreen(),       // clean alternate screen buffer
		tea.WithMouseCellMotion(), // optional mouse support
	)
	app.SetProgram(p)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Runtime error: %v\n", err)
		os.Exit(1)
	}
}
