package main

import (
	"errors"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/vianel/wonder/internal/gate"
	"github.com/vianel/wonder/internal/gh"
)

type picker struct {
	issues []gh.Issue
	cursor int
	chosen map[int]bool
	done   bool
}

func (p picker) Init() tea.Cmd { return nil }

func (p picker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}
	switch k.String() {
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		if p.cursor < len(p.issues)-1 {
			p.cursor++
		}
	case " ", "x":
		n := p.issues[p.cursor].Number
		p.chosen[n] = !p.chosen[n]
	case "enter":
		if len(p.chosen) == 0 || !anyTrue(p.chosen) {
			p.chosen[p.issues[p.cursor].Number] = true
		}
		p.done = true
		return p, tea.Quit
	case "q", "esc", "ctrl+c":
		return p, tea.Quit
	}
	return p, nil
}

func anyTrue(m map[int]bool) bool {
	for _, v := range m {
		if v {
			return true
		}
	}
	return false
}

func (p picker) View() string {
	s := "🎩 Pick issues for the factory (space to select, enter to run, q to quit)\n\n"
	for i, is := range p.issues {
		cur, box := "  ", "[ ]"
		if i == p.cursor {
			cur = "› "
		}
		if p.chosen[is.Number] {
			box = "[x]"
		}
		s += fmt.Sprintf("%s%s #%-5d %s\n", cur, box, is.Number, is.Title)
	}
	return s
}

func pickCmd() *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:   "pick",
		Short: "Choose open issues from a list and run them",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if !gate.Interactive() {
				return errors.New("pick needs a terminal; use `wonder run <issue>...`")
			}
			a, err := load()
			if err != nil {
				return err
			}
			is, err := a.gh.OpenIssues()
			if err != nil {
				return err
			}
			if len(is) == 0 {
				fmt.Println("no open issues")
				return nil
			}
			m, err := tea.NewProgram(picker{issues: is, chosen: map[int]bool{}}).Run()
			if err != nil {
				return err
			}
			p := m.(picker)
			if !p.done {
				return nil
			}
			var nums []int
			for _, i := range is {
				if p.chosen[i.Number] {
					nums = append(nums, i.Number)
				}
			}
			return drive(a, nums, true, yes)
		},
	}
	c.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask the spec's questions; take the suggested answers")
	return c
}
